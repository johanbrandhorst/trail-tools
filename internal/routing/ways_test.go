package routing_test

import (
	"bytes"
	"math"
	"slices"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routing"
)

func TestOSMRoundTrip(t *testing.T) {
	ways, nodes, _ := testGraph()
	features := testFeatures()
	var buf bytes.Buffer
	if err := routing.WriteOSM(&buf, &routing.OSM{Ways: ways, Nodes: nodes, Features: features}); err != nil {
		t.Fatalf("WriteOSM: %v", err)
	}
	written := bytes.Clone(buf.Bytes())
	got, err := routing.ReadOSM(&buf)
	if err != nil {
		t.Fatalf("ReadOSM: %v", err)
	}
	if !slices.Equal(got.Nodes.IDs, nodes.IDs) {
		t.Errorf("got node IDs %v, want %v", got.Nodes.IDs, nodes.IDs)
	}
	for i := range nodes.IDs {
		if math.Abs(got.Nodes.Lats[i]-nodes.Lats[i]) > 1e-6 || math.Abs(got.Nodes.Lngs[i]-nodes.Lngs[i]) > 1e-6 {
			t.Errorf("node %d at %v, %v, want %v, %v", nodes.IDs[i], got.Nodes.Lats[i], got.Nodes.Lngs[i], nodes.Lats[i], nodes.Lngs[i])
		}
	}
	byID := map[int64]routing.Way{}
	for _, w := range got.Ways {
		byID[w.ID] = w
	}
	if len(got.Ways) != len(ways) {
		t.Fatalf("got %d ways, want %d", len(got.Ways), len(ways))
	}
	for _, want := range ways {
		got := byID[want.ID]
		if got.Highway != want.Highway || got.SacScale != want.SacScale || got.Terrain != want.Terrain ||
			got.Name != want.Name || got.Surface != want.Surface || !slices.Equal(got.Nodes, want.Nodes) {
			t.Errorf("got way %+v, want %+v", got, want)
		}
	}
	if len(got.Features) != len(features) {
		t.Fatalf("got %d features, want %d", len(got.Features), len(features))
	}
	for _, want := range features {
		i := slices.IndexFunc(got.Features, func(f routing.Feature) bool { return f.ID == want.ID && f.Way == want.Way })
		if i < 0 {
			t.Errorf("feature %+v is missing", want)
			continue
		}
		f := got.Features[i]
		if f.Kind != want.Kind || f.Name != want.Name || math.Abs(f.Point.Lat-want.Point.Lat) > 1e-6 ||
			math.Abs(f.Point.Lng-want.Point.Lng) > 1e-6 || !sameElevation(f.Point.Ele, want.Point.Ele) {
			t.Errorf("got feature %+v, want %+v", f, want)
		}
	}
	// Garbage and truncated files fail.
	for _, data := range [][]byte{nil, []byte("not gzip"), written[:len(written)/2]} {
		if _, err := routing.ReadOSM(bytes.NewReader(data)); err == nil {
			t.Errorf("ReadOSM of %d bytes succeeded", len(data))
		}
	}
}

// sameElevation reports whether elevations are equal, or both unknown.
func sameElevation(a, b float64) bool {
	return a == b || math.IsNaN(a) && math.IsNaN(b)
}

// testFeatures are points of interest around testGraph: a peak with an
// elevation east of the middle, a car park way at the start and a spring far
// away. The peak and the car park have the same ID.
func testFeatures() []routing.Feature {
	return []routing.Feature{
		{ID: 20, Kind: geo.FeaturePeak, Name: "East Peak", Point: geo.Point{Lat: 0.005, Lng: 0.006, Ele: 1234}},
		{ID: 20, Way: true, Kind: geo.FeatureParking, Point: geo.Point{Lat: 0.0001, Lng: 0, Ele: math.NaN()}},
		{ID: 21, Kind: geo.FeatureWater, Name: "Far Spring", Point: geo.Point{Lat: 10, Lng: 10, Ele: math.NaN()}},
	}
}
