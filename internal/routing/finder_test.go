package routing_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/bs-tools/trailrunn.ing/internal/routing"
	"github.com/bs-tools/trailrunn.ing/internal/store/storetest"
)

func TestFinder(t *testing.T) {
	t.Parallel()
	db := storetest.New(t)
	ctx := context.Background()
	ways, nodes, bounds := testGraph()
	// A footway from the end of the spur to the east trail splits the east
	// trail in two edges at C.
	nodes = &routing.Nodes{
		IDs:  append(slices.Clone(nodes.IDs), 9),
		Lats: append(slices.Clone(nodes.Lats), 0.005),
		Lngs: append(slices.Clone(nodes.Lngs), 0.003),
	}
	ways = append(ways, routing.Way{ID: 15, Nodes: []int64{6, 9, 3}, Highway: "footway", Name: "Link"})
	elevations := make([]float64, len(nodes.IDs))
	// Overlapping regions both have the trails and points of interest.
	for _, region := range []string{"test", "overlap"} {
		edges := routing.BuildEdges(ways, nodes, elevations, bounds)
		if _, err := routing.SaveRegion(ctx, db, region, bounds, edges, testFeatures(), nil); err != nil {
			t.Fatalf("SaveRegion: %v", err)
		}
	}
	finder := routing.NewFinder(db)

	// The trails near the spur are returned whole, once: the spur, the link
	// and the east trail, but not the road or the west trail.
	trails, err := finder.FindTrails(ctx, geo.Point{Lat: 0.005, Lng: 0.002}, 300, 10)
	if err != nil {
		t.Fatalf("FindTrails: %v", err)
	}
	byID := map[int64]routing.Trail{}
	for _, trail := range trails {
		if _, ok := byID[trail.WayID]; ok {
			t.Errorf("way %d was returned twice", trail.WayID)
		}
		byID[trail.WayID] = trail
	}
	if len(trails) != 3 || byID[11].WayID == 0 || byID[13].WayID == 0 || byID[15].WayID == 0 {
		t.Fatalf("got trails %+v, want ways 11, 13 and 15", trails)
	}
	east := byID[11]
	if east.Name != "East Trail" || east.Highway != "path" || east.Surface != "dirt" || east.Terrain != geo.TerrainTrail {
		t.Errorf("got east trail %+v", east)
	}
	wantPoints := []geo.Point{{Lat: 0, Lng: 0}, {Lat: 0.005, Lng: 0.004}, {Lat: 0.01, Lng: 0}}
	if !slices.EqualFunc(east.Points, wantPoints, func(a, b geo.Point) bool { return a.Lat == b.Lat && a.Lng == b.Lng }) {
		t.Errorf("got east trail points %v, want %v", east.Points, wantPoints)
	}
	// The link is straight, so its middle point is simplified away.
	if link := byID[15]; link.Name != "Link" || len(link.Points) != 2 {
		t.Errorf("got link %+v", link)
	}
	// The limit keeps the nearest trails: the spur and the link, which both
	// start at the center, but not the east trail.
	trails, err = finder.FindTrails(ctx, geo.Point{Lat: 0.005, Lng: 0.002}, 300, 2)
	if err != nil {
		t.Fatalf("FindTrails: %v", err)
	}
	if len(trails) != 2 || trails[0].WayID != 13 || trails[1].WayID != 15 {
		t.Errorf("got trails %+v, want ways 13 and 15", trails)
	}

	// Points of interest of the given kinds are returned once.
	features, err := finder.FindFeatures(ctx, geo.Point{Lat: 0.005, Lng: 0.005}, 1000, []geo.FeatureKind{geo.FeaturePeak, geo.FeatureParking})
	if err != nil {
		t.Fatalf("FindFeatures: %v", err)
	}
	if len(features) != 2 {
		t.Fatalf("got features %+v, want the peak and the car park", features)
	}
	slices.SortFunc(features, func(a, b routing.Feature) int { return int(a.Point.Lat*1e6 - b.Point.Lat*1e6) })
	parking, peak := features[0], features[1]
	if peak.ID != 20 || peak.Way || peak.Kind != geo.FeaturePeak || peak.Name != "East Peak" ||
		peak.Point.Lat != 0.005 || peak.Point.Lng != 0.006 || peak.Point.Ele != 1234 {
		t.Errorf("got peak %+v", peak)
	}
	if parking.ID != 20 || !parking.Way || parking.Kind != geo.FeatureParking || !sameElevation(parking.Point.Ele, math.NaN()) {
		t.Errorf("got car park %+v", parking)
	}
	// The radius limits the search.
	features, err = finder.FindFeatures(ctx, geo.Point{Lat: 0.005, Lng: 0.005}, 300, []geo.FeatureKind{geo.FeaturePeak, geo.FeatureParking})
	if err != nil || len(features) != 1 || features[0].Kind != geo.FeaturePeak {
		t.Errorf("FindFeatures: got %+v, %v, want the peak", features, err)
	}

	// Nothing is found in an imported area without trails or points of
	// interest of the kinds.
	if trails, err := finder.FindTrails(ctx, geo.Point{Lat: 0.019, Lng: 0.019}, 100, 10); err != nil || len(trails) != 0 {
		t.Errorf("FindTrails: got %+v, %v, want none", trails, err)
	}
	if features, err := finder.FindFeatures(ctx, geo.Point{Lat: 0.005, Lng: 0.005}, 1000, []geo.FeatureKind{geo.FeatureWater}); err != nil || len(features) != 0 {
		t.Errorf("FindFeatures: got %+v, %v, want none", features, err)
	}
	// Areas outside the imported regions have no map data.
	outside := geo.Point{Lat: -30, Lng: -30}
	if _, err := finder.FindTrails(ctx, outside, 1000, 10); !errors.Is(err, routing.ErrNoMapData) {
		t.Errorf("FindTrails: got %v, want ErrNoMapData", err)
	}
	if _, err := finder.FindFeatures(ctx, outside, 1000, []geo.FeatureKind{geo.FeaturePeak}); !errors.Is(err, routing.ErrNoMapData) {
		t.Errorf("FindFeatures: got %v, want ErrNoMapData", err)
	}
}

func TestFamiliarWays(t *testing.T) {
	t.Parallel()
	db := storetest.New(t)
	ctx := context.Background()
	ways, nodes, bounds := testGraph()
	edges := routing.BuildEdges(ways, nodes, make([]float64, len(nodes.IDs)), bounds)
	if _, err := routing.SaveRegion(ctx, db, "test", bounds, edges, nil, nil); err != nil {
		t.Fatalf("SaveRegion: %v", err)
	}
	finder := routing.NewFinder(db)
	// Two runs along the east trail, a few meters off it as GPS is, and
	// one along the west trail.
	east := []geo.Point{{Lat: 0, Lng: 0.0001}, {Lat: 0.005, Lng: 0.0041}, {Lat: 0.01, Lng: 0.0001}}
	west := []geo.Point{{Lat: 0, Lng: 0}, {Lat: 0.005, Lng: -0.0045}, {Lat: 0.01, Lng: 0}}
	got, err := finder.FamiliarWays(ctx, bounds, [][]geo.Point{east, west, east}, 10)
	if err != nil {
		t.Fatalf("FamiliarWays: %v", err)
	}
	if len(got) != 2 || got[0].WayID != 11 || got[0].ActivityCount != 2 || got[1].WayID != 12 || got[1].ActivityCount != 1 {
		t.Fatalf("got %+v, want the east trail run twice, then the west trail once", got)
	}
	if got[0].Name != "East Trail" || got[0].RunLengthMeters < 1000 || math.Abs(got[0].RunLengthMeters-got[0].LengthMeters) > 1 {
		t.Errorf("got east trail %+v, want all of it run", got[0])
	}
	// The road between them, and the spur, weren't run.
	if got, err := finder.FamiliarWays(ctx, bounds, nil, 10); err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v without tracks, want none", got, err)
	}
	// Areas without map data are reported.
	far := geo.Bounds{MinLat: 50, MinLng: 50, MaxLat: 50.01, MaxLng: 50.01}
	if _, err := finder.FamiliarWays(ctx, far, [][]geo.Point{{{Lat: 50, Lng: 50}, {Lat: 50.01, Lng: 50.01}}}, 10); !errors.Is(err, routing.ErrNoMapData) {
		t.Errorf("got %v outside the imported regions, want ErrNoMapData", err)
	}
}
