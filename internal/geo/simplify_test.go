package geo_test

import (
	"math"
	"slices"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

func TestClip(t *testing.T) {
	t.Parallel()
	bounds := geo.Bounds{MinLat: 0, MinLng: 0, MaxLat: 1, MaxLng: 1}
	for _, tt := range []struct {
		name   string
		points []geo.Point
		want   [][]geo.Point
	}{
		{
			name:   "inside",
			points: []geo.Point{{Lat: 0.2, Lng: 0.2}, {Lat: 0.5, Lng: 0.5}, {Lat: 0.8, Lng: 0.2}},
			want:   [][]geo.Point{{{Lat: 0.2, Lng: 0.2}, {Lat: 0.5, Lng: 0.5}, {Lat: 0.8, Lng: 0.2}}},
		},
		{
			name:   "crossing without points inside",
			points: []geo.Point{{Lat: 0.5, Lng: -1}, {Lat: 0.5, Lng: 2}},
			want:   [][]geo.Point{{{Lat: 0.5, Lng: 0}, {Lat: 0.5, Lng: 1}}},
		},
		{
			name:   "leaving and coming back",
			points: []geo.Point{{Lat: 0.5, Lng: 0.5}, {Lat: 1.5, Lng: 0.5}, {Lat: 0.5, Lng: 0.75}, {Lat: 0.25, Lng: 0.75}},
			want: [][]geo.Point{
				{{Lat: 0.5, Lng: 0.5}, {Lat: 1, Lng: 0.5}},
				{{Lat: 1, Lng: 0.625}, {Lat: 0.5, Lng: 0.75}, {Lat: 0.25, Lng: 0.75}},
			},
		},
		{
			name:   "passing by",
			points: []geo.Point{{Lat: -1, Lng: 0.5}, {Lat: 0.5, Lng: -1}, {Lat: 2, Lng: -1}},
		},
		{
			name:   "one point",
			points: []geo.Point{{Lat: 0.5, Lng: 0.5}},
		},
	} {
		got := geo.Clip(tt.points, bounds)
		if !slices.EqualFunc(got, tt.want, func(a, b []geo.Point) bool {
			return slices.EqualFunc(a, b, func(p, q geo.Point) bool {
				return math.Abs(p.Lat-q.Lat) < 1e-12 && math.Abs(p.Lng-q.Lng) < 1e-12
			})
		}) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestSimplify(t *testing.T) {
	t.Parallel()
	// Two straight lines to a point 30 meters east of the ends, with less
	// than a meter of noise.
	var points []geo.Point
	for i := range 101 {
		east := 30*(1-math.Abs(float64(i-50))/50) + 0.3*float64(i%2)
		points = append(points, geo.Point{Lat: float64(i) * 10 / 111320, Lng: east / 111320})
	}
	original := slices.Clone(points)
	got := geo.Simplify(points, 2)
	if len(got) != 3 || got[0] != points[0] || got[1] != points[50] || got[2] != points[100] {
		t.Errorf("got %v, want the ends and the bump", got)
	}
	if !slices.Equal(points, original) {
		t.Error("the points were changed")
	}
}

func TestSimplifyLongTrack(t *testing.T) {
	t.Parallel()
	// A spiral, which the Douglas-Peucker algorithm takes quadratic time
	// for, of 100,000 points a meter apart.
	var spiral []geo.Point
	for i := range 100_000 {
		r := 10 + float64(i)/100
		a := float64(i) / r
		spiral = append(spiral, geo.Point{Lat: 46 + r*math.Sin(a)/111320, Lng: 7 + r*math.Cos(a)/111320/0.6947})
	}
	got := geo.Simplify(spiral, 1)
	if got[0] != spiral[0] || got[len(got)-1] != spiral[len(spiral)-1] {
		t.Error("the ends weren't kept")
	}
	if len(got) >= len(spiral)/2 {
		t.Errorf("got %d points, want fewer than half of %d", len(got), len(spiral))
	}
	// Every point is within the tolerance of the simplified track.
	for i, p := range spiral {
		if i%997 != 0 {
			continue
		}
		nearest := math.Inf(1)
		for k := 1; k < len(got); k++ {
			nearest = math.Min(nearest, geo.SegmentDistance(p, got[k-1], got[k]))
		}
		if nearest > 1.01 {
			t.Errorf("point %d is %.2f m from the simplified track, want at most 1 m", i, nearest)
		}
	}
}

func TestSimplifyTrack(t *testing.T) {
	t.Parallel()
	// A straight line 2 km north over a 50 m hill in the middle, with a
	// point every 10 m.
	var points []geo.Point
	for i := range 201 {
		ele := 1000 + 50*math.Max(0, 1-math.Abs(float64(i-100))/20)
		points = append(points, geo.Point{Lat: 46 + float64(i)*10/111320, Lng: 7, Ele: ele})
	}
	if got := geo.Simplify(points, 1); len(got) != 2 {
		t.Errorf("Simplify kept %d points, want the ends", len(got))
	}
	// The hill is kept: its foot on either side and its top.
	got := geo.SimplifyTrack(points, 1, 2)
	if len(got) != 5 || got[2] != points[100] {
		t.Errorf("SimplifyTrack kept %v, want the ends, the hill's feet and its top", got)
	}
	// Unknown elevations are ignored.
	for i := range points {
		points[i].Ele = math.NaN()
	}
	if got := geo.SimplifyTrack(points, 1, 2); len(got) != 2 {
		t.Errorf("SimplifyTrack without elevations kept %d points, want the ends", len(got))
	}
}
