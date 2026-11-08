package routing_test

import (
	"math"
	"slices"
	"testing"

	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/bs-tools/trailrunn.ing/internal/routing"
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

func TestReadForests(t *testing.T) {
	t.Parallel()
	// A wood drawn as a closed way inside the bounds, and one outside. A
	// forest relation's outline is split into two ways, one of them reversed,
	// with a clearing as an inner way. Another relation is missing a way, so
	// its outline doesn't close.
	node := func(id int64, lat, lng float64) *osmpbf.Node { return &osmpbf.Node{ID: id, Lat: lat, Lon: lng} }
	outer := func(ids ...int64) []osmpbf.Member {
		var members []osmpbf.Member
		for _, id := range ids {
			members = append(members, osmpbf.Member{ID: id, Type: osmpbf.WayType, Role: "outer"})
		}
		return members
	}
	elements := []any{
		node(1, 0.1, 0.1), node(2, 0.1, 0.2), node(3, 0.2, 0.2),
		node(4, 2.1, 2.1), node(5, 2.1, 2.2), node(6, 2.2, 2.2),
		node(11, 0.5, 0.5), node(12, 0.5, 0.7), node(13, 0.7, 0.7), node(14, 0.7, 0.5),
		node(15, 0.55, 0.55), node(16, 0.55, 0.6), node(17, 0.6, 0.6),
		node(21, 0.8, 0.8), node(22, 0.8, 0.9), node(23, 0.9, 0.9),
		&osmpbf.Way{ID: 40, NodeIDs: []int64{1, 2, 3, 1}, Tags: map[string]string{"natural": "wood"}},
		&osmpbf.Way{ID: 41, NodeIDs: []int64{4, 5, 6, 4}, Tags: map[string]string{"landuse": "forest"}},
		&osmpbf.Way{ID: 42, NodeIDs: []int64{11, 12, 13}},
		&osmpbf.Way{ID: 43, NodeIDs: []int64{11, 14, 13}},
		&osmpbf.Way{ID: 44, NodeIDs: []int64{15, 16, 17, 15}},
		&osmpbf.Way{ID: 45, NodeIDs: []int64{21, 22, 23}},
		&osmpbf.Relation{
			ID: 50, Tags: map[string]string{"type": "multipolygon", "landuse": "forest"},
			Members: append(outer(42, 43), osmpbf.Member{ID: 44, Type: osmpbf.WayType, Role: "inner"}),
		},
		&osmpbf.Relation{ID: 51, Tags: map[string]string{"type": "multipolygon", "natural": "wood"}, Members: outer(45, 46)},
	}
	data, err := routing.ReadElements(geo.Bounds{MinLat: 0, MinLng: 0, MaxLat: 1, MaxLng: 1}, elements)
	if err != nil {
		t.Fatalf("ReadElements: %v", err)
	}
	if len(data.Forests) != 2 {
		t.Fatalf("got forests %+v, want the wood inside the bounds and the relation", data.Forests)
	}
	if f := data.Forests[0]; f.ID != 40 || f.Relation || len(f.Ring) != 4 {
		t.Errorf("got forest %+v, want the wood", f)
	}
	f := data.Forests[1]
	want := []geo.Point{{Lat: 0.5, Lng: 0.5}, {Lat: 0.5, Lng: 0.7}, {Lat: 0.7, Lng: 0.7}, {Lat: 0.7, Lng: 0.5}, {Lat: 0.5, Lng: 0.5}}
	if f.ID != 50 || !f.Relation || !slices.Equal(f.Ring, want) {
		t.Errorf("got forest %+v, want the relation's outline %v", f, want)
	}
	// Forests aren't ways to run on.
	if len(data.Ways) != 0 {
		t.Errorf("got ways %+v, want none", data.Ways)
	}
}
