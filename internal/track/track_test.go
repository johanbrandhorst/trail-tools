package track

import (
	"math"
	"testing"

	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

// testTrack is a track heading north from the equator, with a point every
// 100 seconds and about 111 meters, and a pause of 300 seconds at the third
// point.
func testTrack() *Track {
	return New(activitiesv1.ActivityStreams_builder{
		TimeSeconds:    []int32{0, 100, 200, 500, 600},
		DistanceMeters: []float64{0, 111, 222, 222, 333},
		Lat:            []float64{0, 0.001, 0.002, 0.002, 0.003},
		Lng:            []float64{0, 0, 0, 0, 0},
		AltitudeMeters: []float64{100, 110, 120, 120, 130},
	}.Build())
}

func TestPositionAt(t *testing.T) {
	tr := testTrack()
	tests := []struct {
		name    string
		elapsed float64
		wantOK  bool
		wantLat float64
		dist    float64
	}{
		{name: "start", elapsed: 0, wantOK: true, wantLat: 0, dist: 0},
		{name: "between points", elapsed: 150, wantOK: true, wantLat: 0.0015, dist: 166.5},
		{name: "during a pause", elapsed: 350, wantOK: true, wantLat: 0.002, dist: 222},
		{name: "end", elapsed: 600, wantOK: true, wantLat: 0.003, dist: 333},
		{name: "shortly before the start", elapsed: -300, wantOK: true, wantLat: 0, dist: 0},
		{name: "shortly after the end", elapsed: 1000, wantOK: true, wantLat: 0.003, dist: 333},
		{name: "long before the start", elapsed: -3600, wantOK: false},
		{name: "long after the end", elapsed: 4000, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, dist, ok := tr.PositionAt(tt.elapsed)
			if ok != tt.wantOK {
				t.Fatalf("PositionAt(%v) ok = %v, want %v", tt.elapsed, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if math.Abs(p.Lat-tt.wantLat) > 1e-9 || p.Lng != 0 {
				t.Errorf("PositionAt(%v) = %v, want latitude %v", tt.elapsed, p, tt.wantLat)
			}
			if math.Abs(dist-tt.dist) > 1e-6 {
				t.Errorf("PositionAt(%v) distance = %v, want %v", tt.elapsed, dist, tt.dist)
			}
		})
	}

	// Tracks without times, e.g. from a summary polyline, can't place
	// photos by time.
	noTimes := FromPolyline(geo.EncodePolyline([]geo.Point{{Lat: 0, Lng: 0}, {Lat: 0.01, Lng: 0}}))
	if _, _, ok := New(noTimes).PositionAt(0); ok {
		t.Error("PositionAt on a track without times succeeded")
	}
	var missing *Track
	if _, _, ok := missing.PositionAt(0); ok {
		t.Error("PositionAt on a missing track succeeded")
	}
}

func TestNearestDistance(t *testing.T) {
	tr := testTrack()
	dist, ok := tr.NearestDistance(geo.Point{Lat: 0.0029, Lng: 0.0001})
	if !ok || dist != 333 {
		t.Errorf("NearestDistance = %v, %v, want 333, true", dist, ok)
	}
	var missing *Track
	if _, ok := missing.NearestDistance(geo.Point{}); ok {
		t.Error("NearestDistance on a missing track succeeded")
	}
}

func TestTrackFromStreams(t *testing.T) {
	var streams activitiesv1.ActivityStreams
	for i := range 5000 {
		streams.SetTimeSeconds(append(streams.GetTimeSeconds(), int32(i)))
		streams.SetDistanceMeters(append(streams.GetDistanceMeters(), float64(i)))
		streams.SetLat(append(streams.GetLat(), float64(i)*1e-5))
		streams.SetLng(append(streams.GetLng(), 0))
		streams.SetHeartrateBpm(append(streams.GetHeartrateBpm(), 150))
	}
	got := FromStreams(&streams)
	if n := len(got.GetLat()); n > MaxPoints || n < MaxPoints/2 {
		t.Errorf("track has %d points, want at most %d", n, MaxPoints)
	}
	if len(got.GetTimeSeconds()) != len(got.GetLat()) || len(got.GetDistanceMeters()) != len(got.GetLat()) {
		t.Error("track lost its times or distances")
	}
	if len(got.GetHeartrateBpm()) != 0 || len(got.GetAltitudeMeters()) != 0 {
		t.Error("track has streams it shouldn't")
	}
	if FromStreams(&activitiesv1.ActivityStreams{}) != nil {
		t.Error("track from empty streams is not nil")
	}
}

func TestRoutePoints(t *testing.T) {
	points := New(activitiesv1.ActivityStreams_builder{
		Lat: []float64{0, 0.001},
		Lng: []float64{0, 0},
	}.Build()).RoutePoints()
	if len(points) != 2 || points[1].GetElevationMeters() != 0 || math.Abs(points[1].GetDistanceMeters()-111.2) > 0.1 {
		t.Errorf("unexpected route points %v", points)
	}
}
