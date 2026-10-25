// Store is the package that manages the database connection and migrations.
// It is used by other packages that need access to the database.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	iofs "io/fs"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	migrate "github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source"
	migrateiofs "github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jackc/pgx/v5/tracelog"
	slogadapter "github.com/mcosta74/pgx-slog"
)

// querySpanName names a query's span after its sqlc name, e.g. "GetRoute"
// for a query starting with "-- name: GetRoute :one", or else its first
// words.
func querySpanName(stmt string) string {
	stmt = strings.TrimSpace(stmt)
	if name, ok := strings.CutPrefix(stmt, "-- name: "); ok {
		name, _, _ = strings.Cut(name, " ")
		return name
	}
	first, _, _ := strings.Cut(stmt, "\n")
	if len(first) > maxSpanNameLength {
		first = first[:maxSpanNameLength] + "…"
	}
	return first
}

// maxSpanNameLength is the length span names of queries without a name are
// cut to.
const maxSpanNameLength = 60

//go:embed migrations/*.sql
var fs embed.FS

type DB struct {
	*pgxpool.Pool
}

// maxConns is the most connections a server opens to the database, rather
// than pgx's default of 4 on a single CPU, so requests like routing, which
// mostly wait for round trips, can run many queries at once. Neon's smallest
// compute accepts 97 connections. During a deploy the old and new instances
// run side by side, and the import job needs a few more, so each instance
// stays below half of them. A pool_max_conns parameter in the database URL
// overrides it.
const maxConns = 45

// New creates a new Directory, connecting it to the postgres server on
// the URL provided.
func New(ctx context.Context, logger *slog.Logger, pgURL *url.URL) (*DB, error) {
	poolConf, err := pgxpool.ParseConfig((*pgURL).String())
	if err != nil {
		return nil, fmt.Errorf("failed to parse postgres URI: %w", err)
	}
	// Send each query in a single round trip, with its parameters as text for
	// the database to type. By default, pgx first prepares each query on each
	// connection, which takes another round trip, and connections rarely
	// outlive a Cloud Run instance. []byte parameters are sent as bytea, so
	// sqlc.yaml maps JSON columns to json.RawMessage.
	poolConf.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	if !pgURL.Query().Has("pool_max_conns") {
		poolConf.MaxConns = maxConns
	}
	poolConf.ConnConfig.Tracer = multitracer.New(
		&tracelog.TraceLog{
			Logger:   &logLevelMapper{wrapped: slogadapter.NewLogger(logger)},
			LogLevel: tracelog.LogLevelTrace,
		},
		// Queries are traced with the global trace provider, named by sqlc's
		// query names.
		otelpgx.NewTracer(otelpgx.WithSpanNameFunc(querySpanName)),
	)

	// Validate and/or run migrations
	err = validateSchema(ctx, logger, *poolConf.ConnConfig, pgURL.Scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to validate schema: %w", err)
	}

	// Automatically register our enum types on each new connection
	enumTypes, err := getEnumTypes(ctx, poolConf.ConnConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get enum types: %w", err)
	}
	poolConf.AfterConnect = registerEnums(enumTypes)

	pool, err := pgxpool.NewWithConfig(ctx, poolConf)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres: %w", err)
	}

	return &DB{Pool: pool}, nil
}

// Timeouts of migrations. Migrations run while the previous revision
// serves, so a migration waiting for a lock its queries hold would also hold
// up every query after it; it fails instead, and is retried.
const (
	// migrationLockTimeout bounds waiting for a lock, of a table or of the
	// migrations, which another instance holds while migrating.
	migrationLockTimeout = "10s"
	// migrationStatementTimeout bounds each migration. Cloud Run gives the
	// server 4 minutes to start listening.
	migrationStatementTimeout = "2min"
	// migrationAttempts is how many times migrating is tried when waiting for
	// a lock times out.
	migrationAttempts = 5
	// migrationRetryDelay is the wait between attempts.
	migrationRetryDelay = 5 * time.Second
)

// validateSchema migrates the Postgres schema to the current version,
// retrying when a lock is held for too long.
func validateSchema(ctx context.Context, logger *slog.Logger, conf pgx.ConnConfig, scheme string) error {
	conf.RuntimeParams = maps.Clone(conf.RuntimeParams)
	if conf.RuntimeParams == nil {
		conf.RuntimeParams = map[string]string{}
	}
	conf.RuntimeParams["lock_timeout"] = migrationLockTimeout
	conf.RuntimeParams["statement_timeout"] = migrationStatementTimeout
	for attempt := 1; ; attempt++ {
		err := migrateSchema(ctx, logger, fs, conf, scheme)
		if err == nil || !isLockTimeout(err) || attempt == migrationAttempts {
			return err
		}
		logger.WarnContext(ctx, "Timed out waiting for a lock while migrating, retrying", slog.String("error", err.Error()))
		select {
		case <-time.After(migrationRetryDelay):
		case <-ctx.Done():
			return err
		}
	}
}

// migrateSchema applies the migrations in migrations/ in fsys.
//
// golang-migrate marks the version dirty before running a migration and
// clean after, and refuses to run with a dirty version, until someone sets
// it by hand. Each migration file runs as a single query, so Postgres runs
// it in one transaction (and migrations must keep it that way, see
// TestMigrationsAreAtomic): a failed migration left nothing behind, and is
// retried. golang-migrate also refuses to run if the database's version
// isn't among the migrations, as when a revision is rolled back to after a
// newer one migrated. As migrations keep the previous revision working, it
// runs without migrating instead.
func migrateSchema(ctx context.Context, logger *slog.Logger, fsys iofs.FS, conf pgx.ConnConfig, scheme string) error {
	sourceInstance, err := migrateiofs.New(fsys, "migrations")
	if err != nil {
		return err
	}
	defer sourceInstance.Close()
	db := stdlib.OpenDB(conf)
	defer db.Close()
	var driverInstance database.Driver
	switch scheme {
	case "postgres", "postgresql":
		driverInstance, err = pgxmigrate.WithInstance(db, new(pgxmigrate.Config))
	default:
		return fmt.Errorf("unknown scheme: %q", scheme)
	}
	if err != nil {
		return err
	}
	upToDate, err := prepareMigration(ctx, logger, sourceInstance, driverInstance)
	if err != nil || upToDate {
		return err
	}
	m, err := migrate.NewWithInstance("iofs", sourceInstance, scheme, driverInstance)
	if err != nil {
		return err
	}
	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		return err
	}
	return nil
}

// prepareMigration clears the dirty version a failed migration left, and
// reports whether the database is already at or past the last migration.
func prepareMigration(ctx context.Context, logger *slog.Logger, src source.Driver, drv database.Driver) (upToDate bool, err error) {
	// Hold the migrations' lock, so a migration another instance is running
	// isn't taken for a failed one.
	if err := drv.Lock(); err != nil {
		return false, err
	}
	defer func() {
		if unlockErr := drv.Unlock(); unlockErr != nil && err == nil {
			err = unlockErr
		}
	}()
	version, dirty, err := drv.Version()
	if err != nil {
		return false, err
	}
	last, err := lastVersion(src)
	if err != nil {
		return false, err
	}
	if version > last {
		// A dirty version newer than the migrations is cleared by the next
		// revision that knows the migration.
		logger.WarnContext(ctx, "The database was migrated by a newer revision, running without migrating",
			slog.Int("version", version), slog.Bool("dirty", dirty), slog.Int("last_migration", last))
		return true, nil
	}
	if !dirty {
		return version == last, nil
	}
	prev := database.NilVersion
	if p, err := src.Prev(uint(version)); err == nil {
		prev = int(p)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	logger.WarnContext(ctx, "A migration failed earlier and was rolled back, retrying it",
		slog.Int("version", version), slog.Int("previous_version", prev))
	return false, drv.SetVersion(prev, false)
}

// lastVersion returns the version of the last migration in src.
func lastVersion(src source.Driver) (int, error) {
	v, err := src.First()
	if err != nil {
		return 0, err
	}
	for {
		next, err := src.Next(v)
		if errors.Is(err, os.ErrNotExist) {
			return int(v), nil
		}
		if err != nil {
			return 0, err
		}
		v = next
	}
}

// isLockTimeout reports whether err is from waiting for a lock for longer
// than the lock timeout.
func isLockTimeout(err error) bool {
	if errors.Is(err, migrate.ErrLockTimeout) {
		return true
	}
	// golang-migrate's database errors don't unwrap.
	var dbErr database.Error
	if errors.As(err, &dbErr) {
		err = dbErr.OrigErr
	}
	var dbErrPtr *database.Error
	if errors.As(err, &dbErrPtr) {
		err = dbErrPtr.OrigErr
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.LockNotAvailable
}

// getEnumTypes returns a list of all enum types in the database.
func getEnumTypes(ctx context.Context, conf *pgx.ConnConfig) ([]string, error) {
	conn, err := pgx.ConnectConfig(ctx, conf)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres: %w", err)
	}
	defer conn.Close(ctx)
	const queryEnums = `
   select t.typname as name 
     from pg_type t 
left join pg_catalog.pg_namespace n on n.oid = t.typnamespace 
    where (t.typrelid = 0 or (select c.relkind = 'c' from pg_catalog.pg_class c where c.oid = t.typrelid)) 
      and not exists(select 1 from pg_catalog.pg_type el where el.oid = t.typelem and el.typarray = t.oid)
      and n.nspname not in ('pg_catalog', 'information_schema')
      -- Only enums: extensions such as PostGIS add other types pgx can't load.
      and t.typtype = 'e';
`

	rows, err := conn.Query(ctx, queryEnums)
	if err != nil {
		return nil, fmt.Errorf("failed to query enums in postgres: %w", err)
	}

	var enums []string
	for rows.Next() {
		var enum string
		if err := rows.Scan(&enum); err != nil {
			return nil, fmt.Errorf("failed to scan enum: %w", err)
		}
		enums = append(enums, enum)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan enums in postgres: %w", err)
	}
	return enums, nil
}

// registerEnums returns a function that registers all enums in the database on each new connection.
func registerEnums(enums []string) func(ctx context.Context, conn *pgx.Conn) error {
	names := make([]string, 0, 2*len(enums))
	for _, enum := range enums {
		names = append(names, enum, "_"+enum)
	}
	return func(ctx context.Context, conn *pgx.Conn) error {
		// Register all enums and their arrays, loaded with a single query
		// rather than several per type, as each new connection waits for it.
		types, err := conn.LoadTypes(ctx, names)
		if err != nil {
			return err
		}
		conn.TypeMap().RegisterTypes(types)
		return nil
	}
}
