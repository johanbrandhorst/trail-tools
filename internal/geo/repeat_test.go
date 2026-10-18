package geo

import (
	"math"
	"testing"
)

// track returns a track through points given in meters east and north of a
// point near Squamish, with a point every 5 m.
func track(corners ...[2]float64) []Point {
	const lat0, lng0 = 49.7, -123.15
	toPoint := func(c [2]float64) Point {
		return Point{
			Lat: lat0 + c[1]/metersPerDegreeLat,
			Lng: lng0 + c[0]/(metersPerDegreeLat*math.Cos(lat0*math.Pi/180)),
		}
	}
	var points []Point
	for i := 1; i < len(corners); i++ {
		a, b := corners[i-1], corners[i]
		n := int(math.Hypot(b[0]-a[0], b[1]-a[1]) / 5)
		for k := range n {
			f := float64(k) / float64(n)
			points = append(points, toPoint([2]float64{a[0] + f*(b[0]-a[0]), a[1] + f*(b[1]-a[1])}))
		}
	}
	return append(points, toPoint(corners[len(corners)-1]))
}

func TestRepeatedDistance(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		points   []Point
		min, max float64
	}{
		{
			// The return repeats the way out, except for the turnaround.
			name:   "out and back",
			points: track([2]float64{0, 0}, [2]float64{0, 5000}, [2]float64{0, 0}),
			min:    4700, max: 5000,
		},
		{
			name:   "loop",
			points: track([2]float64{0, 0}, [2]float64{1000, 0}, [2]float64{1000, 1000}, [2]float64{0, 1000}, [2]float64{0, 0}),
			min:    0, max: 0,
		},
		{
			// A 1 km stem to a 4 km loop and back: the stem's return repeats.
			name: "lollipop",
			points: track([2]float64{0, 0}, [2]float64{0, 1000}, [2]float64{1000, 1000}, [2]float64{1000, 2000},
				[2]float64{0, 2000}, [2]float64{0, 1000}, [2]float64{0, 0}),
			min: 900, max: 1100,
		},
		{
			// Switchbacks 20 m apart don't repeat each other.
			name: "switchbacks",
			points: track([2]float64{0, 0}, [2]float64{200, 0}, [2]float64{200, 20}, [2]float64{0, 20},
				[2]float64{0, 40}, [2]float64{200, 40}, [2]float64{200, 60}, [2]float64{0, 60}),
			min: 0, max: 0,
		},
		{
			name:   "empty",
			points: nil,
			min:    0, max: 0,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := RepeatedDistance(tt.points); got < tt.min || got > tt.max {
				t.Errorf("RepeatedDistance = %.0f m, want %.0f to %.0f m", got, tt.min, tt.max)
			}
		})
	}
}

func TestRepeatedDistanceBoundsSamples(t *testing.T) {
	t.Parallel()
	// A tiny track around the world, back and forth: its lines aren't
	// sampled.
	var hops []Point
	for i := range 50 {
		hops = append(hops, Point{Lng: float64(i%2) * 180})
	}
	if n := len(resample(hops, repeatSpacing)); n != len(hops) {
		t.Errorf("got %d samples of a track around the world, want one per point", n)
	}
	if got := RepeatedDistance(hops); got != 0 {
		t.Errorf("RepeatedDistance of a track around the world = %.0f m, want 0", got)
	}

	// A 10,000 km track along the equator, with a point every 4 km, is
	// sampled further apart than every 10 m.
	var long []Point
	for i := range 2501 {
		long = append(long, Point{Lng: float64(i) * 4000 / metersPerDegreeLat})
	}
	spacing := math.Max(repeatSpacing, sampledDistance(long)/maxRepeatSamples)
	if n := len(resample(long, spacing)); n > maxRepeatSamples+1 {
		t.Errorf("got %d samples of a 10,000 km track, want at most %d", n, maxRepeatSamples+1)
	}
	if got := RepeatedDistance(long); got != 0 {
		t.Errorf("RepeatedDistance of a 10,000 km straight track = %.0f m, want 0", got)
	}
}
