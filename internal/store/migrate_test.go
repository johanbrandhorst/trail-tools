package store_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"testing/fstest"

	"github.com/golang-migrate/migrate/v4/database"
	"github.com/jackc/pgx/v5"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/store/storetest"
)

func TestMigrateRecovers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storetest.New(t)
	// The migrations run in a schema of their own, with its own
	// schema_migrations table.
	if _, err := db.Exec(ctx, "create schema migrate_test"); err != nil {
		t.Fatal(err)
	}
	conf := *db.Config().ConnConfig.Copy()
	conf.RuntimeParams["search_path"] = "migrate_test"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	one := fstest.MapFS{"migrations/1_a.up.sql": {Data: []byte("create table a (n int);")}}
	two := fstest.MapFS{
		"migrations/1_a.up.sql": one["migrations/1_a.up.sql"],
		"migrations/2_b.up.sql": {Data: []byte("create table b (n int);")},
	}
	setVersion := func(version int, dirty bool) {
		t.Helper()
		if _, err := db.Exec(ctx, "update migrate_test.schema_migrations set version = $1, dirty = $2", version, dirty); err != nil {
			t.Fatal(err)
		}
	}
	wantVersion := func(version int, dirty bool) {
		t.Helper()
		var gotVersion int
		var gotDirty bool
		if err := db.QueryRow(ctx, "select version, dirty from migrate_test.schema_migrations").Scan(&gotVersion, &gotDirty); err != nil {
			t.Fatal(err)
		}
		if gotVersion != version || gotDirty != dirty {
			t.Errorf("got version %d, dirty %v, want %d, %v", gotVersion, gotDirty, version, dirty)
		}
	}

	// Migrate to the first version.
	if err := store.MigrateSchema(ctx, logger, one, conf, "postgres"); err != nil {
		t.Fatalf("MigrateSchema: %v", err)
	}
	wantVersion(1, false)

	// The second migration failed and was rolled back, leaving its version
	// dirty. It's retried.
	setVersion(2, true)
	if err := store.MigrateSchema(ctx, logger, two, conf, "postgres"); err != nil {
		t.Fatalf("MigrateSchema after a failed migration: %v", err)
	}
	wantVersion(2, false)
	if _, err := db.Exec(ctx, "select from migrate_test.b"); err != nil {
		t.Errorf("the retried migration didn't run: %v", err)
	}

	// A revision without the second migration, rolled back to, runs without
	// migrating, also when a newer migration failed.
	if err := store.MigrateSchema(ctx, logger, one, conf, "postgres"); err != nil {
		t.Fatalf("MigrateSchema of an older revision: %v", err)
	}
	wantVersion(2, false)
	setVersion(3, true)
	if err := store.MigrateSchema(ctx, logger, two, conf, "postgres"); err != nil {
		t.Fatalf("MigrateSchema after a newer migration failed: %v", err)
	}
	wantVersion(3, true)
}

func TestLockTimeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storetest.New(t)
	holder, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	if _, err := holder.Exec(ctx, "select pg_advisory_lock(42)"); err != nil {
		t.Fatal(err)
	}
	defer holder.Exec(ctx, "select pg_advisory_unlock(42)")

	// Waiting for an advisory lock, like golang-migrate's, is bounded by the
	// lock timeout.
	conf := db.Config().ConnConfig.Copy()
	conf.RuntimeParams["lock_timeout"] = "100ms"
	waiter, err := pgx.ConnectConfig(ctx, conf)
	if err != nil {
		t.Fatal(err)
	}
	defer waiter.Close(ctx)
	_, err = waiter.Exec(ctx, "select pg_advisory_lock(42)")
	if err == nil {
		t.Fatal("got the lock another connection holds")
	}
	if !store.IsLockTimeout(&database.Error{OrigErr: err}) || !store.IsLockTimeout(database.Error{OrigErr: err}) {
		t.Errorf("IsLockTimeout(%v) = false, want true", err)
	}
}
