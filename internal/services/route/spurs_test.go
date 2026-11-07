package route

import (
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

// east returns the point meters east of p.
func east(p geo.Point, meters float64) geo.Point {
	p.Lng += meters / geo.Distance(geo.Point{Lat: p.Lat, Lng: 0}, geo.Point{Lat: p.Lat, Lng: 1})
	return p
}

func TestRemoveSpurs(t *testing.T) {
	t.Parallel()
	// A kilometer north, with a detour 100 m east and back halfway.
	north := line(51, 20)
	var track []geo.Point
	track = append(track, north[:26]...)
	for _, m := range []float64{20, 40, 60, 80, 100, 80, 60, 40, 20} {
		track = append(track, east(north[25], m))
	}
	track = append(track, north[25:]...)
	got, removed := removeSpurs(track)
	if removed != 1 {
		t.Errorf("removed %d out-and-backs, want 1", removed)
	}
	// The point the detour came back to may be kept twice.
	if len(got) < len(north) || len(got) > len(north)+1 {
		t.Errorf("got %d points, want the %d of the straight line", len(got), len(north))
	}
	for _, p := range got {
		if p.Lng != north[0].Lng {
			t.Fatalf("point %v is off the straight line", p)
		}
	}
	if got[len(got)-1] != north[len(north)-1] {
		t.Errorf("the track ends at %v, want %v", got[len(got)-1], north[len(north)-1])
	}

	// A switchback comes back near, but higher up, and a lap of a running
	// track is longer than an out-and-back; both are kept.
	climb := append([]geo.Point{}, north[:26]...)
	for i, m := range []float64{20, 40, 60, 80, 100, 80, 60, 40, 20, 0} {
		p := east(north[25], m)
		p.Ele += float64(i+1) * 3
		climb = append(climb, p)
	}
	if got, removed := removeSpurs(climb); removed != 0 || len(got) != len(climb) {
		t.Errorf("removed %d out-and-backs and %d points from a switchback", removed, len(climb)-len(got))
	}
	// The lap goes round east of the line, 40 m from it, coming back to
	// where it left it after 480 m.
	lap := append([]geo.Point{}, north[:11]...)
	corner := north[10]
	var steps [][2]float64
	for _, side := range []struct {
		east, north float64
		n           int
	}{{20, 0, 6}, {0, 20, 6}, {-20, 0, 4}, {0, -20, 6}, {-20, 0, 2}} {
		for range side.n {
			steps = append(steps, [2]float64{side.east, side.north})
		}
	}
	for _, step := range steps {
		corner = east(corner, step[0])
		corner.Lat += step[1] / metersPerDegreeLat
		lap = append(lap, corner)
	}
	lap = append(lap, north[11:]...)
	if _, removed := removeSpurs(lap); removed != 0 {
		t.Errorf("removed %d out-and-backs from a lap longer than %d m", removed, maxSpurMeters)
	}
}
