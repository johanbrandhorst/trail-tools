package demo_test

import (
	"context"
	"io"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/demo"
	"github.com/johanbrandhorst/trail-tools/internal/elevation"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routing"
	"github.com/johanbrandhorst/trail-tools/internal/store/storetest"
)

// chief is the first peak of the Stawamus Chief, about 600 m above the sea.
var chief = geo.Point{Lat: 49.6836, Lng: -123.1384}

func TestData(t *testing.T) {
	patch, err := demo.Elevation()
	if err != nil {
		t.Fatalf("Elevation: %v", err)
	}
	if b := patch.Bounds(); b.MinLat > demo.Bounds.MinLat || b.MaxLat < demo.Bounds.MaxLat ||
		b.MinLng > demo.Bounds.MinLng || b.MaxLng < demo.Bounds.MaxLng {
		t.Errorf("the elevations cover %+v, want all of %+v", b, demo.Bounds)
	}
	data, err := demo.OSM()
	if err != nil {
		t.Fatalf("OSM: %v", err)
	}
	if len(data.Ways) < 1000 || len(data.Nodes.IDs) < 10_000 || len(data.Features) < 100 {
		t.Errorf("got %d ways, %d nodes and %d points of interest, want the trails of Squamish",
			len(data.Ways), len(data.Nodes.IDs), len(data.Features))
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storetest.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// No elevation tiles are downloaded for Squamish.
	dem := elevation.NewDEM("http://invalid.test", t.TempDir(), &http.Client{})
	if err := demo.Load(ctx, log, db, dem); err != nil {
		t.Fatalf("Load: %v", err)
	}
	elevations, err := dem.Points(ctx, []geo.Point{chief})
	if err != nil {
		t.Fatalf("Points: %v", err)
	}
	if math.Abs(elevations[0]-600) > 150 {
		t.Errorf("the Chief is at %.0f m, want about 600 m", elevations[0])
	}
	// The trails can be routed on: up the Chief from its trailhead.
	router := routing.NewRouter(db)
	path, err := router.Route(ctx, routing.Request{From: geo.Point{Lat: 49.6806, Lng: -123.1483}, To: chief})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if last := path.Points[len(path.Points)-1]; geo.Distance(last, chief) > 300 || last.Ele < 400 {
		t.Errorf("the route ends at %+v, want the summit", last)
	}
	// Named trails and peaks are found around the Chief.
	finder := routing.NewFinder(db)
	trails, err := finder.FindTrails(ctx, chief, 1000, 100)
	if err != nil {
		t.Fatalf("FindTrails: %v", err)
	}
	if !slices.ContainsFunc(trails, func(t routing.Trail) bool { return t.Name != "" }) {
		t.Errorf("got %d trails, want named trails", len(trails))
	}
	peaks, err := finder.FindFeatures(ctx, chief, 1000, []geo.FeatureKind{geo.FeaturePeak})
	if err != nil {
		t.Fatalf("FindFeatures: %v", err)
	}
	if !slices.ContainsFunc(peaks, func(f routing.Feature) bool { return f.Point.Ele > 400 }) {
		t.Errorf("got peaks %+v, want the Chief's", peaks)
	}
	// Loading again keeps the region.
	if err := demo.Load(ctx, log, db, dem); err != nil {
		t.Fatalf("Load again: %v", err)
	}
	regions, err := router.Regions(ctx)
	if err != nil || len(regions) != 1 {
		t.Errorf("Regions = %v, %v, want the one region", regions, err)
	}
}
