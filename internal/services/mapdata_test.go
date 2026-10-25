package services_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	routingv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routing/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routing"
	mapdatasvc "github.com/johanbrandhorst/trail-tools/internal/services/mapdata"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// fakeExtracts has two extracts south of 60 degrees, Testland and
// Otherland east of 9 degrees, and too many to import from north of 50
// degrees. Extracts south of 46 degrees take a moment to find.
type fakeExtracts struct{}

func (fakeExtracts) Find(_ context.Context, bounds geo.Bounds) ([]routing.Extract, error) {
	switch {
	case bounds.MaxLat > 60:
		return nil, routing.ErrNoExtract
	case bounds.MaxLat > 50:
		return nil, routing.ErrTooManyExtracts
	case bounds.MaxLat < 46:
		time.Sleep(100 * time.Millisecond)
	}
	extracts := []routing.Extract{{Name: "Testland", URL: "https://example.com/testland.osm.pbf"}}
	if bounds.MaxLng > 9 {
		extracts = append(extracts, routing.Extract{Name: "Otherland", URL: "https://example.com/otherland.osm.pbf"})
	}
	return extracts, nil
}

// fakeImportRunner records the imports it's asked to run.
type fakeImportRunner struct {
	mu  sync.Mutex
	ids []pgtype.UUID
}

func (f *fakeImportRunner) Run(_ context.Context, id pgtype.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids = append(f.ids, id)
	return nil
}

func area(south, west, north, east float64) *routingv1.Area {
	return routingv1.Area_builder{
		SouthWest: geov1.LatLng_builder{Lat: south, Lng: west}.Build(),
		NorthEast: geov1.LatLng_builder{Lat: north, Lng: east}.Build(),
	}.Build()
}

func TestMapData(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// Only administrators can manage map data.
	_, err := e.mapDataAPI.GetMapData(ctx, routingv1.GetMapDataRequest_builder{}.Build())
	wantCode(t, err, connect.CodePermissionDenied)
	if _, err := e.db.Exec(ctx, "update users set is_admin = true where id = $1", e.userID); err != nil {
		t.Fatal(err)
	}

	found, err := e.mapDataAPI.FindExtracts(ctx, routingv1.FindExtractsRequest_builder{Area: area(46.6, 7.9, 47.5, 9.2)}.Build())
	if err != nil || !slices.Equal(found.GetExtractNames(), []string{"Testland", "Otherland"}) {
		t.Fatalf("FindExtracts: %v %v", found, err)
	}
	_, err = e.mapDataAPI.FindExtracts(ctx, routingv1.FindExtractsRequest_builder{Area: area(61, 7, 62, 8)}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)
	_, err = e.mapDataAPI.FindExtracts(ctx, routingv1.FindExtractsRequest_builder{Area: area(51, 7, 52, 8)}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)
	_, err = e.mapDataAPI.FindExtracts(ctx, routingv1.FindExtractsRequest_builder{Area: area(40, 0, 50, 10)}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = e.mapDataAPI.StartImport(ctx, routingv1.StartImportRequest_builder{Region: "Central Switzerland", Area: area(46.6, 7.9, 47.5, 9.2)}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	// An import is queued with the extracts to read and handed to the
	// runner, and only one runs at a time.
	started, err := e.mapDataAPI.StartImport(ctx, routingv1.StartImportRequest_builder{Region: "central-switzerland", Area: area(46.6, 7.9, 47.5, 9.2)}.Build())
	if err != nil {
		t.Fatalf("StartImport: %v", err)
	}
	imported := started.GetImport()
	if imported.GetState() != routingv1.ImportState_IMPORT_STATE_QUEUED || !slices.Equal(imported.GetExtractNames(), []string{"Testland", "Otherland"}) {
		t.Errorf("unexpected import %v", imported)
	}
	id, _ := store.StringToUUID(imported.GetId())
	record, err := routing.GetImport(ctx, e.db, id)
	if err != nil || len(record.Extracts) != 2 || record.Extracts[1].URL != "https://example.com/otherland.osm.pbf" {
		t.Errorf("GetImport: %+v %v", record, err)
	}
	if len(e.imports.ids) != 1 || store.UUIDToString(e.imports.ids[0]) != imported.GetId() {
		t.Errorf("the runner got %v, want %s", e.imports.ids, imported.GetId())
	}
	_, err = e.mapDataAPI.StartImport(ctx, routingv1.StartImportRequest_builder{Region: "other", Area: area(46, 7, 46.5, 7.5)}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)

	// Finish the import with a small graph, as running it needs a download.
	bounds := geo.Bounds{MinLat: 46.6, MinLng: 7.9, MaxLat: 47.5, MaxLng: 9.2}
	edges := slices.Values([]routing.Edge{
		{Source: 1, Target: 2, Points: []geo.Point{{Lat: 47, Lng: 8, Ele: 500}, {Lat: 47.01, Lng: 8, Ele: 520}}, Highway: "path", Terrain: geo.TerrainTrail},
		{Source: 2, Target: 3, Points: []geo.Point{{Lat: 47.01, Lng: 8, Ele: 520}, {Lat: 47.02, Lng: 8, Ele: 510}}, Highway: "track", Terrain: geo.TerrainGravel},
	})
	if _, err := routing.SaveRegion(ctx, e.db, "central-switzerland", bounds, edges, nil); err != nil {
		t.Fatal(err)
	}
	if err := routing.FailImport(ctx, e.db, id, "the download failed"); err != nil {
		t.Fatal(err)
	}

	data, err := e.mapDataAPI.GetMapData(ctx, routingv1.GetMapDataRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("GetMapData: %v", err)
	}
	regions := data.GetRegions()
	if len(regions) != 1 || regions[0].GetName() != "central-switzerland" || regions[0].GetEdgeCount() != 2 || regions[0].GetSizeBytes() <= 0 {
		t.Errorf("unexpected regions %v", regions)
	}
	if regions[0].GetArea().GetNorthEast().GetLat() != 47.5 {
		t.Errorf("unexpected region area %v", regions[0].GetArea())
	}
	imports := data.GetImports()
	if len(imports) != 1 || imports[0].GetState() != routingv1.ImportState_IMPORT_STATE_FAILED || imports[0].GetMessage() != "the download failed" ||
		!imports[0].HasFinishTime() || len(imports[0].GetExtractNames()) != 2 {
		t.Errorf("unexpected imports %v", imports)
	}
	if data.GetDatabaseSizeBytes() <= 0 {
		t.Errorf("unexpected database size %d", data.GetDatabaseSizeBytes())
	}

	// Deleting a region frees its part of the graph.
	if _, err := e.mapDataAPI.DeleteRegion(ctx, routingv1.DeleteRegionRequest_builder{Name: "central-switzerland"}.Build()); err != nil {
		t.Fatalf("DeleteRegion: %v", err)
	}
	_, err = e.mapDataAPI.DeleteRegion(ctx, routingv1.DeleteRegionRequest_builder{Name: "central-switzerland"}.Build())
	wantCode(t, err, connect.CodeNotFound)

	// Of imports started at once, one starts, even when they all check for
	// an active import before any is created.
	var wg sync.WaitGroup
	results := make([]error, 5)
	for i := range results {
		wg.Go(func() {
			_, results[i] = e.mapDataAPI.StartImport(ctx, routingv1.StartImportRequest_builder{Region: "other", Area: area(45, 7, 45.5, 7.5)}.Build())
		})
	}
	wg.Wait()
	startedCount := 0
	for _, err := range results {
		if err == nil {
			startedCount++
		} else {
			wantCode(t, err, connect.CodeFailedPrecondition)
		}
	}
	if startedCount != 1 {
		t.Errorf("%d of %d imports started, want 1", startedCount, len(results))
	}

	// An import that never started doesn't hold up later imports. Its
	// queued time is moved back, so it's listed last.
	if _, err := e.db.Exec(ctx, "update routing_imports set create_time = create_time - interval '10 minutes' where state = 'queued'"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mapDataAPI.StartImport(ctx, routingv1.StartImportRequest_builder{Region: "other", Area: area(46, 7, 46.5, 7.5)}.Build()); err != nil {
		t.Fatalf("StartImport: %v", err)
	}
	data, err = e.mapDataAPI.GetMapData(ctx, routingv1.GetMapDataRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("GetMapData: %v", err)
	}
	imports = data.GetImports()
	if len(imports) != 3 || imports[0].GetState() != routingv1.ImportState_IMPORT_STATE_QUEUED ||
		imports[2].GetState() != routingv1.ImportState_IMPORT_STATE_FAILED || imports[2].GetMessage() != "the import didn't start" {
		t.Errorf("unexpected imports %v", imports)
	}
}

func TestRefreshMapData(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// The local reporter logs the errors it's given.
	var reported bytes.Buffer
	reportLog := slog.New(slog.NewTextHandler(&reported, nil))
	runner := &fakeImportRunner{}
	handler := mapdatasvc.NewService(log, errreport.NewLocal(reportLog), e.db, fakeExtracts{}, runner).RefreshHandler()
	refresh := func() int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, mapdatasvc.RefreshPath, nil))
		return rec.Code
	}
	edges := func() func(func(routing.Edge) bool) {
		return slices.Values([]routing.Edge{
			{Source: 1, Target: 2, Points: []geo.Point{{Lat: 47, Lng: 8, Ele: 500}, {Lat: 47.01, Lng: 8, Ele: 520}}, Highway: "path", Terrain: geo.TerrainTrail},
		})
	}

	// A region imported from extracts and one loaded otherwise, like the
	// demo region, both imported 40 days ago.
	bounds := geo.Bounds{MinLat: 46.6, MinLng: 7.9, MaxLat: 47.5, MaxLng: 9.2}
	for _, name := range []string{"central-switzerland", "demo"} {
		if _, err := routing.SaveRegion(ctx, e.db, name, bounds, edges(), nil); err != nil {
			t.Fatal(err)
		}
	}
	first, err := routing.CreateImport(ctx, e.db, "central-switzerland", bounds, nil, e.userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(ctx, "update routing_imports set state = 'done' where id = $1", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(ctx, "update routing_regions set import_time = now() - interval '40 days'"); err != nil {
		t.Fatal(err)
	}

	// Only the scheduler's POST refreshes.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, mapdatasvc.RefreshPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: got status %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}

	// The imported region is imported again, with the extracts of its area.
	if code := refresh(); code != http.StatusAccepted {
		t.Fatalf("refresh: got status %d, want %d", code, http.StatusAccepted)
	}
	if len(runner.ids) != 1 {
		t.Fatalf("the runner got %v, want one import", runner.ids)
	}
	record, err := routing.GetImport(ctx, e.db, runner.ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if record.Region != "central-switzerland" || record.State != routing.ImportQueued || len(record.Extracts) != 2 {
		t.Errorf("unexpected import %+v", record)
	}

	// Nothing else starts while it runs.
	if code := refresh(); code != http.StatusNoContent || len(runner.ids) != 1 {
		t.Errorf("refresh during an import: got status %d and imports %v, want %d and one import", code, runner.ids, http.StatusNoContent)
	}

	// Once it's done, the region isn't due, and the demo region never is.
	if _, err := e.db.Exec(ctx, "update routing_imports set state = 'done' where id = $1", record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := routing.SaveRegion(ctx, e.db, "central-switzerland", bounds, edges(), nil); err != nil {
		t.Fatal(err)
	}
	if code := refresh(); code != http.StatusNoContent || len(runner.ids) != 1 {
		t.Errorf("refresh after the import: got status %d and imports %v, want %d and one import", code, runner.ids, http.StatusNoContent)
	}

	// An import whose process died without recording its result, e.g. out
	// of memory, is reported once it's taken to have stopped.
	died, err := routing.CreateImport(ctx, e.db, "central-switzerland", bounds, nil, e.userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(ctx, "update routing_imports set state = 'running', start_time = now() - interval '2 hours' where id = $1", died.ID); err != nil {
		t.Fatal(err)
	}
	if code := refresh(); code != http.StatusNoContent {
		t.Errorf("refresh after an import died: got status %d, want %d", code, http.StatusNoContent)
	}
	if got := reported.String(); !strings.Contains(got, "stopped without recording its result") || !strings.Contains(got, "central-switzerland") {
		t.Errorf("got reports %q, want the import that stopped", got)
	}
}
