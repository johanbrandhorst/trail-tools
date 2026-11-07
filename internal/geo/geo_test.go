package geo_test

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

func TestPolyline(t *testing.T) {
	t.Parallel()
	// The example from the Google polyline algorithm documentation.
	const encoded = "_p~iF~ps|U_ulLnnqC_mqNvxq`@"
	points, err := geo.DecodePolyline(encoded)
	if err != nil {
		t.Fatalf("DecodePolyline: %v", err)
	}
	want := []geo.Point{{Lat: 38.5, Lng: -120.2}, {Lat: 40.7, Lng: -120.95}, {Lat: 43.252, Lng: -126.453}}
	if len(points) != len(want) {
		t.Fatalf("got %d points, want %d", len(points), len(want))
	}
	for i := range want {
		if math.Abs(points[i].Lat-want[i].Lat) > 1e-9 || math.Abs(points[i].Lng-want[i].Lng) > 1e-9 {
			t.Errorf("point %d: got %v, want %v", i, points[i], want[i])
		}
	}
	if got := geo.EncodePolyline(points); got != encoded {
		t.Errorf("EncodePolyline: got %q, want %q", got, encoded)
	}
	if _, err := geo.DecodePolyline("_p~iF~ps|U_"); err == nil {
		t.Error("expected error for truncated polyline")
	}
}

func TestDistance(t *testing.T) {
	t.Parallel()
	// One degree of latitude is roughly 111.2km.
	d := geo.Distance(geo.Point{Lat: 0, Lng: 0}, geo.Point{Lat: 1, Lng: 0})
	if math.Abs(d-111195) > 10 {
		t.Errorf("got %v, want ~111195", d)
	}
}

func TestGPXRoundTrip(t *testing.T) {
	t.Parallel()
	points := []geo.Point{
		{Lat: 46.1, Lng: 7.1, Ele: 1000},
		{Lat: 46.11, Lng: 7.11, Ele: 1100.5},
		{Lat: 46.12, Lng: 7.12, Ele: math.NaN()},
	}
	var buf bytes.Buffer
	if err := geo.WriteGPX(&buf, "Loop <1>", points); err != nil {
		t.Fatalf("WriteGPX: %v", err)
	}
	track, err := geo.ParseGPX(&buf)
	if err != nil {
		t.Fatalf("ParseGPX: %v", err)
	}
	if track.Name != "Loop <1>" {
		t.Errorf("got name %q", track.Name)
	}
	if len(track.Points) != 3 {
		t.Fatalf("got %d points", len(track.Points))
	}
	if track.Points[1].Ele != 1100.5 || !math.IsNaN(track.Points[2].Ele) {
		t.Errorf("unexpected elevations %v", track.Points)
	}
}

func TestParseGPXRoute(t *testing.T) {
	t.Parallel()
	const gpx = `<?xml version="1.0"?>
<gpx version="1.1"><rte><name>Course</name>
<rtept lat="1" lon="2"><ele>5</ele></rtept>
<rtept lat="1.001" lon="2.001"><ele>6</ele></rtept>
</rte></gpx>`
	track, err := geo.ParseGPX(strings.NewReader(gpx))
	if err != nil {
		t.Fatalf("ParseGPX: %v", err)
	}
	if track.Name != "Course" || len(track.Points) != 2 {
		t.Errorf("unexpected track %+v", track)
	}
	if _, err := geo.ParseGPX(strings.NewReader("<gpx></gpx>")); err == nil {
		t.Error("expected error for empty GPX")
	}
}

func TestElevationGainLoss(t *testing.T) {
	t.Parallel()
	// Small noise is ignored, real climbs are counted.
	elevations := []float64{100, 101, 100, 102, 110, 120, 119, 121, 100, math.NaN(), 90}
	gain, loss := geo.ElevationGainLoss(elevations)
	if gain != 20 || loss != 30 {
		t.Errorf("got gain %v loss %v, want 20 and 30", gain, loss)
	}
}

func TestMaxGrades(t *testing.T) {
	t.Parallel()
	// 1km climbing at 10%, then 1km descending at 20%.
	var dists, elevations []float64
	for d := 0.0; d <= 1000; d += 50 {
		dists = append(dists, d)
		elevations = append(elevations, d*0.1)
	}
	for d := 1050.0; d <= 2000; d += 50 {
		dists = append(dists, d)
		elevations = append(elevations, 100-(d-1000)*0.2)
	}
	climb, descent := geo.MaxGrades(dists, elevations)
	if math.Abs(climb-10) > 0.01 || math.Abs(descent-20) > 0.01 {
		t.Errorf("got climb %v descent %v, want 10 and 20", climb, descent)
	}
}

func TestClassifyTerrain(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		tags string
		want geo.Terrain
	}{
		{"highway=path surface=ground", geo.TerrainTrail},
		{"highway=path", geo.TerrainTrail},
		{"highway=path sac_scale=demanding_mountain_hiking", geo.TerrainTechnical},
		{"highway=track tracktype=grade2", geo.TerrainGravel},
		{"highway=track tracktype=grade4", geo.TerrainTrail},
		{"highway=residential", geo.TerrainPaved},
		{"highway=footway surface=gravel", geo.TerrainGravel},
		{"highway=path surface=scree", geo.TerrainTechnical},
		{"", geo.TerrainUnknown},
	} {
		if got := geo.ClassifyTerrain(geo.ParseTags(tt.tags)); got != tt.want {
			t.Errorf("%q: got %q, want %q", tt.tags, got, tt.want)
		}
	}
}

func TestComputeStats(t *testing.T) {
	t.Parallel()
	points := []geo.Point{
		{Lat: 0, Lng: 0, Ele: 100},
		{Lat: 0.001, Lng: 0, Ele: 110},
		{Lat: 0.002, Lng: 0, Ele: 105},
	}
	stats := geo.ComputeStats(points, []geo.Terrain{geo.TerrainTrail, geo.TerrainPaved, geo.TerrainUnknown})
	if math.Abs(stats.DistanceMeters-222.4) > 0.5 {
		t.Errorf("distance %v", stats.DistanceMeters)
	}
	if stats.ElevationGainMeters != 10 || stats.ElevationLossMeters != 5 {
		t.Errorf("gain %v loss %v", stats.ElevationGainMeters, stats.ElevationLossMeters)
	}
	if stats.MinElevationMeters != 100 || stats.MaxElevationMeters != 110 {
		t.Errorf("min %v max %v", stats.MinElevationMeters, stats.MaxElevationMeters)
	}
	if math.Abs(stats.TerrainMeters[geo.TerrainTrail]-111.2) > 0.5 || math.Abs(stats.TerrainMeters[geo.TerrainPaved]-111.2) > 0.5 {
		t.Errorf("terrain %v", stats.TerrainMeters)
	}
}

func TestDownsample(t *testing.T) {
	t.Parallel()
	idxs := geo.Downsample(10, 4)
	if len(idxs) != 4 || idxs[0] != 0 || idxs[3] != 9 {
		t.Errorf("got %v", idxs)
	}
	if got := geo.Downsample(3, 10); len(got) != 3 {
		t.Errorf("got %v", got)
	}
}
