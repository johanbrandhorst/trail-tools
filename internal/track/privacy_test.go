package track

import (
	"math"
	"slices"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

func TestPublicPoints(t *testing.T) {
	t.Parallel()
	// A track heading north, a point about every 111 meters.
	var points []geo.Point
	for i := range 11 {
		points = append(points, geo.Point{Lat: float64(i) * 0.001})
	}
	tr := &Track{Points: points, Dists: geo.CumulativeDistances(points)}
	for name, tt := range map[string]struct {
		zones      []Zone
		wantLats   []float64
		wantBreaks []int32
	}{
		"no zones": {nil, []float64{0, 0.001, 0.002, 0.003, 0.004, 0.005, 0.006, 0.007, 0.008, 0.009, 0.01}, nil},
		// Hiding the start, around the first two points.
		"at the start": {[]Zone{{Center: points[0], RadiusMeters: 150}}, []float64{0.002, 0.003, 0.004, 0.005, 0.006, 0.007, 0.008, 0.009, 0.01}, nil},
		// Hiding the middle splits the track in two.
		"in the middle": {[]Zone{{Center: points[5], RadiusMeters: 150}}, []float64{0, 0.001, 0.002, 0.003, 0.007, 0.008, 0.009, 0.01}, []int32{4}},
		"everywhere":    {[]Zone{{Center: points[5], RadiusMeters: 5000}}, nil, nil},
	} {
		got, breaks := tr.PublicPoints(tt.zones)
		var lats []float64
		for _, p := range got {
			lats = append(lats, float64(int(p.GetLat()*1000+0.5))/1000)
		}
		if !slices.Equal(lats, tt.wantLats) || !slices.Equal(breaks, tt.wantBreaks) {
			t.Errorf("%s: got %v with breaks %v, want %v with breaks %v", name, lats, breaks, tt.wantLats, tt.wantBreaks)
		}
		for i, p := range got {
			if i > 0 && p.GetDistanceMeters() <= got[i-1].GetDistanceMeters() {
				t.Errorf("%s: distances aren't along the whole track: %v", name, got)
			}
		}
	}
}

func TestHiddenZone(t *testing.T) {
	t.Parallel()
	center := geo.Point{Lat: 47.37, Lng: 8.54}
	const radius = 500
	var bearings []float64
	for i := range 200 {
		seed := [16]byte{byte(i), byte(i >> 8)}
		z := hiddenZone(center, radius, seed)
		// It always covers the zone, centered a quarter to half the radius
		// away.
		offset := geo.Distance(z.Center, center)
		if offset < radius/4-1 || offset > radius/2+1 || offset+radius > z.RadiusMeters+1 {
			t.Fatalf("seed %d: got a circle of %v m centered %v m away, want it to cover the zone from %v to %v m away", i, z.RadiusMeters, offset, radius/4, radius/2)
		}
		// It's the same every time.
		if again := hiddenZone(center, radius, seed); again != z {
			t.Fatalf("seed %d: got %v, then %v", i, z, again)
		}
		bearings = append(bearings, math.Atan2(z.Center.Lng-center.Lng, z.Center.Lat-center.Lat))
	}
	// The directions differ between seeds, all around the zone.
	slices.Sort(bearings)
	for i := 1; i < len(bearings); i++ {
		if gap := bearings[i] - bearings[i-1]; gap > math.Pi/4 {
			t.Errorf("no circle is centered in a direction between %.2f and %.2f radians", bearings[i-1], bearings[i])
		}
	}
}
