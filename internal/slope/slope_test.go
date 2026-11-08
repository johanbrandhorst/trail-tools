package slope_test

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"testing"

	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/bs-tools/trailrunn.ing/internal/slope"
)

// plane returns the elevations of the grid points of a tile on a slope of
// degrees rising to the east, flat west of westOf.
func plane(points []geo.Point, degrees, westOf float64) []float64 {
	elevations := make([]float64, len(points))
	for i, p := range points {
		if p.Lng < westOf {
			continue
		}
		// Meters east of westOf along the parallel.
		east := geo.Distance(geo.Point{Lat: p.Lat, Lng: westOf}, p)
		elevations[i] = east * math.Tan(degrees*math.Pi/180)
	}
	return elevations
}

func TestRender(t *testing.T) {
	t.Parallel()
	// A tile at zoom 14 in the Selkirks.
	z, x, y := 14, 2593, 5578
	points := slope.GridPoints(z, x, y)
	if len(points) != 258*258 {
		t.Fatalf("got %d grid points", len(points))
	}
	middle := (points[0].Lng + points[len(points)-1].Lng) / 2
	for name, tc := range map[string]struct {
		degrees float64
		// The color index of the east of the tile; the west is flat.
		want uint8
	}{
		"gentle": {degrees: 20, want: 0},
		"27-30":  {degrees: 28, want: 1},
		"30-35":  {degrees: 32, want: 2},
		"35-40":  {degrees: 37, want: 3},
		"40-45":  {degrees: 42, want: 4},
		"cliff":  {degrees: 60, want: 5},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data, err := slope.Render(z, x, y, plane(points, tc.degrees, middle))
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			paletted, ok := img.(*image.Paletted)
			if !ok || img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
				t.Fatalf("got a %T of %v, want a paletted 256x256 image", img, img.Bounds())
			}
			if got := paletted.ColorIndexAt(10, 128); got != 0 {
				t.Errorf("the flat west has color %d, want clear", got)
			}
			if got := paletted.ColorIndexAt(240, 128); got != tc.want {
				t.Errorf("a %v degree slope has color %d, want %d", tc.degrees, got, tc.want)
			}
		})
	}

	if _, err := slope.Render(z, x, y, make([]float64, 10)); err == nil {
		t.Error("Render of too few elevations succeeded")
	}
}

func TestValidTile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		z, x, y int
		want    bool
	}{
		{12, 0, 0, true},
		{15, 32767, 32767, true},
		{11, 0, 0, false},
		{16, 0, 0, false},
		{12, 4096, 0, false},
		{12, 0, -1, false},
	} {
		if got := slope.ValidTile(tc.z, tc.x, tc.y); got != tc.want {
			t.Errorf("ValidTile(%d, %d, %d) = %v, want %v", tc.z, tc.x, tc.y, got, tc.want)
		}
	}
}
