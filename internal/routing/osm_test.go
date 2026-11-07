package routing_test

import (
	"math"
	"slices"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routing"
	"github.com/qedus/osmpbf"
)

func TestReadOSM(t *testing.T) {
	t.Parallel()
	// A path, a residential road and a motorway meet at node 2, inside the
	// bounds. A car park outline has its center inside the bounds, and
	// another outside. A peak node is inside, a spring outside.
	elements := []any{
		&osmpbf.Node{ID: 1, Lat: 0.5, Lon: 0.5},
		&osmpbf.Node{ID: 2, Lat: 0.5, Lon: 0.9},
		&osmpbf.Node{ID: 3, Lat: 0.5, Lon: 1.5},
		&osmpbf.Node{ID: 4, Lat: 0.1, Lon: 0.1},
		&osmpbf.Node{ID: 5, Lat: 0.3, Lon: 0.3},
		&osmpbf.Node{ID: 6, Lat: 0.9, Lon: 0.8},
		&osmpbf.Node{ID: 7, Lat: 0.9, Lon: 1.6},
		&osmpbf.Node{ID: 8, Lat: 0.2, Lon: 0.2, Tags: map[string]string{"natural": "peak", "name": "Top", "ele": "1234 m"}},
		&osmpbf.Node{ID: 9, Lat: 0.2, Lon: 1.2, Tags: map[string]string{"natural": "spring"}},
		&osmpbf.Node{ID: 10, Lat: 0.3, Lon: 0.4, Tags: map[string]string{"amenity": "bench"}},
		&osmpbf.Way{ID: 20, NodeIDs: []int64{1, 2}, Tags: map[string]string{"highway": "path", "name": "Ridge Trail", "surface": "dirt", "sac_scale": "mountain_hiking"}},
		&osmpbf.Way{ID: 21, NodeIDs: []int64{2, 3}, Tags: map[string]string{"highway": "residential"}},
		&osmpbf.Way{ID: 22, NodeIDs: []int64{2, 3}, Tags: map[string]string{"highway": "motorway"}},
		&osmpbf.Way{ID: 23, NodeIDs: []int64{4, 5, 4}, Tags: map[string]string{"amenity": "parking", "name": "Lot"}},
		&osmpbf.Way{ID: 24, NodeIDs: []int64{6, 7, 6}, Tags: map[string]string{"amenity": "parking"}},
		&osmpbf.Relation{ID: 30, Tags: map[string]string{"amenity": "parking"}},
	}
	bounds := geo.Bounds{MinLat: 0, MinLng: 0, MaxLat: 1, MaxLng: 1}
	data, err := routing.ReadElements(bounds, elements)
	if err != nil {
		t.Fatalf("ReadElements: %v", err)
	}
	var ids []int64
	for _, w := range data.Ways {
		ids = append(ids, w.ID)
	}
	if !slices.Equal(ids, []int64{20, 21}) {
		t.Errorf("got ways %v, want the path and the road", ids)
	}
	if w := data.Ways[0]; w.Name != "Ridge Trail" || w.Surface != "dirt" || w.SacScale != "mountain_hiking" || w.Terrain != geo.TerrainTechnical {
		t.Errorf("got path %+v", w)
	}
	// Only the nodes of the ways are kept, including those outside.
	if !slices.Equal(data.Nodes.IDs, []int64{1, 2, 3}) || data.Nodes.Lngs[2] != 1.5 {
		t.Errorf("got nodes %+v, want those of the ways", data.Nodes)
	}
	want := []routing.Feature{
		{ID: 8, Kind: geo.FeaturePeak, Name: "Top", Point: geo.Point{Lat: 0.2, Lng: 0.2, Ele: 1234}},
		{ID: 23, Way: true, Kind: geo.FeatureParking, Name: "Lot", Point: geo.Point{Lat: 0.2, Lng: 0.2, Ele: math.NaN()}},
	}
	if len(data.Features) != len(want) {
		t.Fatalf("got features %+v, want %+v", data.Features, want)
	}
	for i, f := range data.Features {
		w := want[i]
		if f.ID != w.ID || f.Way != w.Way || f.Kind != w.Kind || f.Name != w.Name ||
			math.Abs(f.Point.Lat-w.Point.Lat) > 1e-9 || math.Abs(f.Point.Lng-w.Point.Lng) > 1e-9 || !sameElevation(f.Point.Ele, w.Point.Ele) {
			t.Errorf("got feature %+v, want %+v", f, w)
		}
	}

	// Without bounds, everything is read.
	data, err = routing.ReadElements(geo.Bounds{}, elements)
	if err != nil {
		t.Fatalf("ReadElements: %v", err)
	}
	if len(data.Ways) != 2 || len(data.Features) != 4 {
		t.Errorf("got %d ways and %d features, want 2 and 4", len(data.Ways), len(data.Features))
	}
}
