package routing_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routing"
)

// geofabrikIndex has a continent with countries in it, and an extract
// without a PBF download. Switzerland's outline leaves out a corner in the
// east, where Liechtenstein is, though its bounding box covers it. Italy's
// outline has a hole, which only the continent covers. Austria borders
// Switzerland and Liechtenstein in the east.
const geofabrikIndex = `{"type": "FeatureCollection", "features": [
	{"properties": {"id": "europe", "name": "Europe", "urls": {"pbf": "https://example.com/europe.osm.pbf"}},
	 "geometry": {"type": "MultiPolygon", "coordinates": [[[[0, 35], [20, 35], [20, 55], [0, 55], [0, 35]]]]}},
	{"properties": {"id": "switzerland", "name": "Switzerland", "urls": {"pbf": "https://example.com/switzerland.osm.pbf"}},
	 "geometry": {"type": "MultiPolygon", "coordinates": [[[[5.9, 45.8], [10.5, 45.8], [10.5, 47.0], [9.47, 47.0], [9.47, 47.8], [5.9, 47.8], [5.9, 45.8]]]]}},
	{"properties": {"id": "liechtenstein", "name": "Liechtenstein", "urls": {"pbf": "https://example.com/liechtenstein.osm.pbf"}},
	 "geometry": {"type": "Polygon", "coordinates": [[[9.47, 47.0], [9.64, 47.0], [9.64, 47.28], [9.47, 47.28], [9.47, 47.0]]]}},
	{"properties": {"id": "austria", "name": "Austria", "urls": {"pbf": "https://example.com/austria.osm.pbf"}},
	 "geometry": {"type": "MultiPolygon", "coordinates": [[[[9.64, 47.0], [17.2, 47.0], [17.2, 49.0], [9.64, 49.0], [9.64, 47.0]]]]}},
	{"properties": {"id": "italy", "name": "Italy", "urls": {"pbf": "https://example.com/italy.osm.pbf"}},
	 "geometry": {"type": "MultiPolygon", "coordinates": [[
		[[6.6, 36.6], [18.5, 36.6], [18.5, 45.8], [6.6, 45.8], [6.6, 36.6]],
		[[12.4, 43.89], [12.52, 43.89], [12.52, 43.99], [12.4, 43.99], [12.4, 43.89]]
	 ]]}},
	{"properties": {"id": "planet", "name": "Planet", "urls": {}},
	 "geometry": {"type": "Polygon", "coordinates": [[[-180, -90], [180, -90], [180, 90], [-180, 90], [-180, -90]]]}}
]}`

func TestExtractsFind(t *testing.T) {
	t.Parallel()
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(geofabrikIndex))
	}))
	t.Cleanup(srv.Close)
	extracts := routing.NewExtracts(srv.URL, srv.Client())
	ctx := context.Background()

	tests := []struct {
		name   string
		bounds geo.Bounds
		want   []string
	}{
		{name: "inside a country", bounds: geo.Bounds{MinLat: 46.5, MinLng: 7, MaxLat: 47, MaxLng: 8}, want: []string{"Switzerland"}},
		{name: "inside a bounding box but not the outline", bounds: geo.Bounds{MinLat: 47.1, MinLng: 9.5, MaxLat: 47.2, MaxLng: 9.6}, want: []string{"Liechtenstein"}},
		{name: "across a border", bounds: geo.Bounds{MinLat: 47.05, MinLng: 9.3, MaxLat: 47.25, MaxLng: 9.6}, want: []string{"Switzerland", "Liechtenstein"}},
		{name: "most of the area first", bounds: geo.Bounds{MinLat: 45.5, MinLng: 8, MaxLat: 46, MaxLng: 9}, want: []string{"Italy", "Switzerland"}},
		{name: "a small country in a large area", bounds: geo.Bounds{MinLat: 44.5, MinLng: 6.6, MaxLat: 47.25, MaxLng: 9.6}, want: []string{"Switzerland", "Italy", "Liechtenstein"}},
		{name: "in a hole", bounds: geo.Bounds{MinLat: 43.9, MinLng: 12.42, MaxLat: 43.98, MaxLng: 12.5}, want: []string{"Europe"}},
		{name: "partly at sea", bounds: geo.Bounds{MinLat: 47, MinLng: -0.5, MaxLat: 47.5, MaxLng: 0.5}, want: []string{"Europe"}},
	}
	for _, tt := range tests {
		got, err := extracts.Find(ctx, tt.bounds)
		var names []string
		for _, x := range got {
			names = append(names, x.Name)
		}
		if err != nil || !slices.Equal(names, tt.want) {
			t.Errorf("%s: got %v, %v, want %v", tt.name, names, err, tt.want)
		}
	}
	got, err := extracts.Find(ctx, geo.Bounds{MinLat: 47.1, MinLng: 9.5, MaxLat: 47.2, MaxLng: 9.6})
	if err != nil || len(got) != 1 || got[0].URL != "https://example.com/liechtenstein.osm.pbf" {
		t.Errorf("got %v, %v, want Liechtenstein's URL", got, err)
	}
	if _, err := extracts.Find(ctx, geo.Bounds{MinLat: 30, MinLng: -30, MaxLat: 31, MaxLng: -29}); !errors.Is(err, routing.ErrNoExtract) {
		t.Errorf("got %v for an area at sea, want ErrNoExtract", err)
	}
	// Italy, Switzerland, Liechtenstein, Austria and the rest of Europe.
	if _, err := extracts.Find(ctx, geo.Bounds{MinLat: 45.5, MinLng: 9, MaxLat: 47.5, MaxLng: 10.5}); !errors.Is(err, routing.ErrTooManyExtracts) {
		t.Errorf("got %v for an area across five extracts, want ErrTooManyExtracts", err)
	}
	if requests != 1 {
		t.Errorf("the index was fetched %d times, want once", requests)
	}
}
