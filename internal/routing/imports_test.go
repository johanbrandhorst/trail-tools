package routing_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/elevation"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routing"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/store/storetest"
)

// createImport creates an import reading an extract that doesn't exist, so
// running it fails without a download.
func createImport(t *testing.T, db *store.DB) *routing.ImportRecord {
	t.Helper()
	extracts := []routing.Extract{{Name: "Testland", URL: filepath.Join(t.TempDir(), "missing.osm.pbf")}}
	bounds := geo.Bounds{MinLat: 46, MinLng: 7, MaxLat: 46.5, MaxLng: 7.5}
	r, err := routing.CreateImport(context.Background(), db, "test", bounds, extracts, pgtype.UUID{})
	if err != nil {
		t.Fatalf("CreateImport: %v", err)
	}
	return r
}

func wantImport(t *testing.T, db *store.DB, id pgtype.UUID, state routing.ImportState, message string) {
	t.Helper()
	r, err := routing.GetImport(context.Background(), db, id)
	if err != nil {
		t.Fatalf("GetImport: %v", err)
	}
	if r.State != state || (message != "" && r.Message != message) {
		t.Errorf("got import %s %q, want %s %q", r.State, r.Message, state, message)
	}
}

func TestRunImport(t *testing.T) {
	t.Parallel()
	db := storetest.New(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dem := elevation.NewDEM("http://localhost:0", t.TempDir(), &http.Client{})

	// Only one import is queued or running at a time.
	r := createImport(t, db)
	if _, err := routing.CreateImport(ctx, db, "other", r.Bounds, r.Extracts, pgtype.UUID{}); !errors.Is(err, routing.ErrActiveImport) {
		t.Errorf("CreateImport: got %v, want ErrActiveImport", err)
	}

	// A failed import records why, and isn't run again.
	if err := routing.RunImport(ctx, log, errreport.NewLocal(log), nil, db, dem, r.ID); err == nil {
		t.Fatal("RunImport: want an error for a missing extract")
	}
	failed, err := routing.GetImport(ctx, db, r.ID)
	if err != nil || failed.State != routing.ImportFailed || failed.Message == "" || !failed.FinishTime.Valid {
		t.Fatalf("GetImport: %+v %v", failed, err)
	}
	if err := routing.RunImport(ctx, log, errreport.NewLocal(log), nil, db, dem, r.ID); err == nil {
		t.Error("RunImport: want an error for an import that isn't queued")
	}
	wantImport(t, db, r.ID, routing.ImportFailed, failed.Message)

	// A finished import isn't marked as failed.
	r = createImport(t, db)
	if _, err := db.Exec(ctx, "update routing_imports set state = 'done' where id = $1", r.ID); err != nil {
		t.Fatal(err)
	}
	if err := routing.RunImport(ctx, log, errreport.NewLocal(log), nil, db, dem, r.ID); err == nil {
		t.Error("RunImport: want an error for an import that isn't queued")
	}
	if err := routing.FailImport(ctx, db, r.ID, "too late"); err != nil {
		t.Fatal(err)
	}
	wantImport(t, db, r.ID, routing.ImportDone, "")

	// An import still waiting to run when the server stops is marked as
	// failed.
	r = createImport(t, db)
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	runner := routing.NewLocalRunner(stopped, log, errreport.NewLocal(log), nil, db, dem)
	if err := runner.Run(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	runner.Wait()
	wantImport(t, db, r.ID, routing.ImportFailed, "")
}

func TestStaleImports(t *testing.T) {
	t.Parallel()
	db := storetest.New(t)
	ctx := context.Background()

	// An import queued for a few minutes never started.
	queued := createImport(t, db)
	if _, err := db.Exec(ctx, "update routing_imports set create_time = create_time - interval '10 minutes' where id = $1", queued.ID); err != nil {
		t.Fatal(err)
	}
	stale, err := routing.FailStaleImports(ctx, db)
	if err != nil || len(stale) != 1 || stale[0].ID != queued.ID {
		t.Fatalf("FailStaleImports: got %v %v, want the queued import", stale, err)
	}
	if active, err := routing.HasActiveImport(ctx, db); err != nil || active {
		t.Errorf("HasActiveImport: got %v %v, want false", active, err)
	}
	wantImport(t, db, queued.ID, routing.ImportFailed, "the import didn't start")

	// An import running for an hour stopped without recording its result.
	running := createImport(t, db)
	if _, err := db.Exec(ctx, "update routing_imports set state = 'running', start_time = current_timestamp - interval '30 minutes' where id = $1", running.ID); err != nil {
		t.Fatal(err)
	}
	if stale, err := routing.FailStaleImports(ctx, db); err != nil || len(stale) != 0 {
		t.Errorf("FailStaleImports: got %v %v, want none", stale, err)
	}
	if active, err := routing.HasActiveImport(ctx, db); err != nil || !active {
		t.Errorf("HasActiveImport: got %v %v, want true", active, err)
	}
	if _, err := db.Exec(ctx, "update routing_imports set start_time = current_timestamp - interval '2 hours' where id = $1", running.ID); err != nil {
		t.Fatal(err)
	}
	stale, err = routing.FailStaleImports(ctx, db)
	if err != nil || len(stale) != 1 || stale[0].ID != running.ID || stale[0].Message != "the import stopped without finishing" {
		t.Fatalf("FailStaleImports: got %v %v, want the running import", stale, err)
	}
	imports, err := routing.ListImports(ctx, db, 10)
	if err != nil || len(imports) != 2 {
		t.Fatalf("ListImports: %v %v", imports, err)
	}
	if imports[0].State != routing.ImportFailed || imports[0].Message != "the import stopped without finishing" || !imports[0].FinishTime.Valid {
		t.Errorf("got import %+v, want it failed", imports[0])
	}
}
