package routing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/elevation"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// ImportState is the state of an import.
type ImportState string

const (
	ImportQueued  ImportState = "queued"
	ImportRunning ImportState = "running"
	ImportDone    ImportState = "done"
	ImportFailed  ImportState = "failed"
)

const (
	// importTimeout limits how long an import runs. A job running imports,
	// with import-osm -import-id, should be stopped after the same time.
	importTimeout = time.Hour
	// staleRunningImport is how long after starting a running import is
	// taken to have stopped without recording its result, such as when its
	// process ran out of memory or was stopped by its job's timeout.
	staleRunningImport = importTimeout + 15*time.Minute
	// staleQueuedImport is how long after being queued an import is taken
	// to have never started, such as when its job couldn't start.
	staleQueuedImport = 5 * time.Minute
)

// ErrActiveImport is returned when creating an import while another is
// queued or running.
var ErrActiveImport = errors.New("an import is already queued or running")

// ImportRecord is an import of an area of OpenStreetMap data into the routing
// graph.
type ImportRecord struct {
	ID         pgtype.UUID
	Region     string
	Bounds     geo.Bounds
	Extracts   []Extract
	State      ImportState
	Message    string
	EdgeCount  int
	CreateTime time.Time
	FinishTime pgtype.Timestamptz
}

// Region is an imported area of the routing graph.
type Region struct {
	Name      string
	Bounds    geo.Bounds
	EdgeCount int
	// The space the region takes, estimated from its share of the edges,
	// counting the points of interest and the indexes.
	SizeBytes  int64
	ImportTime time.Time
}

const importColumns = `id, region, south, west, north, east, extract_names, extract_urls,
	state::text, message, edge_count, create_time, finish_time`

func scanImport(row pgx.Row) (*ImportRecord, error) {
	var r ImportRecord
	var names, urls []string
	var state string
	if err := row.Scan(&r.ID, &r.Region, &r.Bounds.MinLat, &r.Bounds.MinLng, &r.Bounds.MaxLat, &r.Bounds.MaxLng,
		&names, &urls, &state, &r.Message, &r.EdgeCount, &r.CreateTime, &r.FinishTime); err != nil {
		return nil, err
	}
	if len(names) != len(urls) {
		return nil, fmt.Errorf("the import has %d extract names but %d URLs", len(names), len(urls))
	}
	for i := range names {
		r.Extracts = append(r.Extracts, Extract{Name: names[i], URL: urls[i]})
	}
	r.State = ImportState(state)
	return &r, nil
}

// CreateImport records a new import, queued to run.
func CreateImport(ctx context.Context, db *store.DB, region string, bounds geo.Bounds, extracts []Extract, createdBy pgtype.UUID) (*ImportRecord, error) {
	names, urls := make([]string, len(extracts)), make([]string, len(extracts))
	for i, x := range extracts {
		names[i], urls[i] = x.Name, x.URL
	}
	r, err := scanImport(db.QueryRow(ctx, `insert into routing_imports (
		region, south, west, north, east, extract_names, extract_urls, created_by
	) values ($1, $2, $3, $4, $5, $6, $7, $8)
	returning `+importColumns,
		region, bounds.MinLat, bounds.MinLng, bounds.MaxLat, bounds.MaxLng, names, urls, createdBy))
	if err != nil {
		if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "routing_imports_active" {
			return nil, ErrActiveImport
		}
		return nil, fmt.Errorf("failed to create the import: %w", err)
	}
	return r, nil
}

// GetImport gets an import.
func GetImport(ctx context.Context, db *store.DB, id pgtype.UUID) (*ImportRecord, error) {
	r, err := scanImport(db.QueryRow(ctx, `select `+importColumns+` from routing_imports where id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get the import: %w", err)
	}
	return r, nil
}

// ListImports lists the most recent imports, newest first. Call
// FailStaleImports first so imports that stopped show as failed.
func ListImports(ctx context.Context, db *store.DB, limit int) ([]*ImportRecord, error) {
	rows, err := db.Query(ctx, `select `+importColumns+` from routing_imports order by create_time desc limit $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list imports: %w", err)
	}
	defer rows.Close()
	var imports []*ImportRecord
	for rows.Next() {
		r, err := scanImport(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to list imports: %w", err)
		}
		imports = append(imports, r)
	}
	return imports, rows.Err()
}

// HasActiveImport reports whether an import is queued or running. Call
// FailStaleImports first, or an import that stopped counts as running.
func HasActiveImport(ctx context.Context, db *store.DB) (bool, error) {
	var active bool
	if err := db.QueryRow(ctx, `select exists(select 1 from routing_imports where state in ('queued', 'running'))`).Scan(&active); err != nil {
		return false, fmt.Errorf("failed to check for imports: %w", err)
	}
	return active, nil
}

// FailStaleImports marks imports as failed that stopped without recording
// their result, so they don't hold up later imports, and returns them. Their
// process died without reporting why, e.g. out of memory or at its job's
// timeout, so the caller should report them.
func FailStaleImports(ctx context.Context, db *store.DB) ([]*ImportRecord, error) {
	rows, err := db.Query(ctx, `update routing_imports set state = 'failed', finish_time = current_timestamp,
		message = case state when 'queued' then 'the import didn''t start' else 'the import stopped without finishing' end
		where (state = 'queued' and create_time < current_timestamp - $1::interval)
			or (state = 'running' and start_time < current_timestamp - $2::interval)
		returning `+importColumns,
		staleQueuedImport, staleRunningImport)
	if err != nil {
		return nil, fmt.Errorf("failed to update stale imports: %w", err)
	}
	stale, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*ImportRecord, error) {
		return scanImport(row)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update stale imports: %w", err)
	}
	return stale, nil
}

// FailImport marks an import as failed, unless it has finished.
func FailImport(ctx context.Context, db *store.DB, id pgtype.UUID, message string) error {
	if _, err := db.Exec(ctx, `update routing_imports set state = 'failed', message = $2,
		finish_time = current_timestamp where id = $1 and state in ('queued', 'running')`, id, message); err != nil {
		return fmt.Errorf("failed to update the import: %w", err)
	}
	return nil
}

// RunImport runs a queued import, recording its progress. It is called by
// the server for imports in the background, and by the import-osm command
// for imports run as a job.
func RunImport(ctx context.Context, log *slog.Logger, report *errreport.Reporter, client *http.Client, db *store.DB, dem *elevation.DEM, id pgtype.UUID) error {
	// Start the import only if it's still queued, so it isn't run twice or
	// run after it was taken to have never started.
	r, err := scanImport(db.QueryRow(ctx, `update routing_imports set state = 'running', message = '',
		start_time = current_timestamp where id = $1 and state = 'queued'
		returning `+importColumns, id))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return errors.New("the import isn't queued to run")
	case err != nil:
		err = fmt.Errorf("failed to start the import: %w", err)
	default:
		err = runImport(ctx, log, client, db, dem, r)
	}
	if err != nil {
		// Record the failure even if ctx was cancelled.
		ctx := context.WithoutCancel(ctx)
		if err := FailImport(ctx, db, id, err.Error()); err != nil {
			report.Report(ctx, "Failed to record the failed import", err)
		}
	}
	return err
}

// runImport runs a started import and records its result.
func runImport(ctx context.Context, log *slog.Logger, client *http.Client, db *store.DB, dem *elevation.DEM, r *ImportRecord) error {
	sources := make([]string, len(r.Extracts))
	for i, x := range r.Extracts {
		sources[i] = x.URL
	}
	importCtx, cancel := context.WithTimeout(ctx, importTimeout)
	defer cancel()
	count, err := Import(importCtx, log, db, ImportConfig{
		Region:     r.Region,
		Sources:    sources,
		Bounds:     r.Bounds,
		DEM:        dem,
		HTTPClient: client,
	})
	if err != nil {
		return err
	}
	// Record the result even if ctx was cancelled, unless the import was
	// taken to have stopped.
	tag, err := db.Exec(context.WithoutCancel(ctx), `update routing_imports set state = 'done', edge_count = $2,
		finish_time = current_timestamp where id = $1 and state = 'running'`, r.ID, count)
	if err != nil {
		return fmt.Errorf("failed to update the import: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errors.New("the import finished after it was taken to have stopped")
	}
	return nil
}

// ListRegions lists the imported regions, by name.
func ListRegions(ctx context.Context, db *store.DB) ([]Region, error) {
	rows, err := db.Query(ctx, `select name, ST_YMin(bounds), ST_XMin(bounds), ST_YMax(bounds), ST_XMax(bounds),
		edge_count, import_time from routing_regions order by name`)
	if err != nil {
		return nil, fmt.Errorf("failed to list regions: %w", err)
	}
	defer rows.Close()
	var regions []Region
	for rows.Next() {
		var r Region
		if err := rows.Scan(&r.Name, &r.Bounds.MinLat, &r.Bounds.MinLng, &r.Bounds.MaxLat, &r.Bounds.MaxLng, &r.EdgeCount, &r.ImportTime); err != nil {
			return nil, fmt.Errorf("failed to list regions: %w", err)
		}
		regions = append(regions, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to list regions: %w", err)
	}
	var size int64
	if err := db.QueryRow(ctx, `select pg_total_relation_size('routing_edges') + pg_total_relation_size('points_of_interest')`).Scan(&size); err != nil {
		return nil, fmt.Errorf("failed to get the routing graph's size: %w", err)
	}
	total := 0
	for _, r := range regions {
		total += r.EdgeCount
	}
	if total > 0 {
		for i := range regions {
			regions[i].SizeBytes = size * int64(regions[i].EdgeCount) / int64(total)
		}
	}
	return regions, nil
}

// ImportedRegions returns the names of the regions imported from
// OpenStreetMap extracts, as opposed to loaded otherwise, like the demo
// region of a local server.
func ImportedRegions(ctx context.Context, db *store.DB) (map[string]bool, error) {
	rows, err := db.Query(ctx, `select distinct region from routing_imports where state = 'done'`)
	if err != nil {
		return nil, fmt.Errorf("failed to list imported regions: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("failed to list imported regions: %w", err)
	}
	result := make(map[string]bool, len(names))
	for _, name := range names {
		result[name] = true
	}
	return result, nil
}

// DeleteRegion deletes a region, its edges and its points of interest from
// the routing graph.
func DeleteRegion(ctx context.Context, db *store.DB, name string) error {
	tag, err := db.Exec(ctx, `delete from routing_regions where name = $1`, name)
	if err != nil {
		return fmt.Errorf("failed to delete the region: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// DatabaseSize returns the size of the database in bytes.
func DatabaseSize(ctx context.Context, db *store.DB) (int64, error) {
	var size int64
	if err := db.QueryRow(ctx, `select pg_database_size(current_database())`).Scan(&size); err != nil {
		return 0, fmt.Errorf("failed to get the database size: %w", err)
	}
	return size, nil
}
