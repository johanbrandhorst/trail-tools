package route

import (
	"math"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

// line returns n points north from (46, 7), spacing meters apart.
func line(n int, spacing float64) []geo.Point {
	points := make([]geo.Point, n)
	for i := range points {
		points[i] = geo.Point{Lat: 46 + float64(i)*spacing/metersPerDegreeLat, Lng: 7, Ele: 1000}
	}
	return points
}

func TestResampleBoundsPoints(t *testing.T) {
	t.Parallel()
	// Short gaps are filled in.
	if got := resample(line(2, 1000), snapSpacingMeters); len(got) < 40 {
		t.Errorf("got %d points for a 1 km gap, want it filled in every %d m", len(got), snapSpacingMeters)
	}
	// Long gaps, like around the world, aren't.
	if got := resample([]geo.Point{{}, {Lng: 180}, {}}, snapSpacingMeters); len(got) != 3 {
		t.Errorf("got %d points for a track around the world, want 3", len(got))
	}
	// Resampling stops once there are too many points.
	if got := resample(line(100_000, snapSpacingMeters), snapSpacingMeters); len(got) > maxSnapPoints+2 {
		t.Errorf("got %d points for a 2,000 km track, want at most %d", len(got), maxSnapPoints+2)
	}
}

func TestCheckTrack(t *testing.T) {
	t.Parallel()
	var hops []geo.Point
	for i := range 50 {
		hops = append(hops, geo.Point{Lng: float64(i%2) * 180})
	}
	for name, tt := range map[string]struct {
		points  []geo.Point
		wantErr bool
	}{
		"ultra recorded every second": {points: line(100_000, 2)},
		"too many points":             {points: line(maxTrackPoints+1, 0.1), wantErr: true},
		"around the world":            {points: hops, wantErr: true},
	} {
		err := checkTrack(tt.points)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: checkTrack returned %v, want error %v", name, err, tt.wantErr)
		}
		if err != nil && connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: got code %v, want InvalidArgument", name, connect.CodeOf(err))
		}
	}
}

func TestLegsFromTrackSimplifies(t *testing.T) {
	t.Parallel()
	// A straight track recorded every meter is simplified to the ends of
	// its legs, and of the chunks they're simplified in.
	_, legs := legsFromTrack(line(10_001, 1))
	n := 0
	for _, leg := range legs {
		n += len(leg.Points)
	}
	if n > 3*len(legs) {
		t.Errorf("got %d points in %d legs, want at most 3 in each", n, len(legs))
	}
	if err := checkRoutePoints(legs); err != nil {
		t.Errorf("checkRoutePoints: %v", err)
	}

	// A track that zigzags by more than the tolerance at every point keeps
	// them all, too many to store.
	zigzag := line(maxRouteTrackPoints+1000, 1)
	for i := range zigzag {
		zigzag[i].Lng += float64(i%2) * 5 / (metersPerDegreeLat * math.Cos(46*math.Pi/180))
	}
	_, legs = legsFromTrack(zigzag)
	if err := checkRoutePoints(legs); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("checkRoutePoints of a zigzag of %d points returned %v, want InvalidArgument", len(zigzag), err)
	}
}
