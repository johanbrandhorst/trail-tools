// Package slope renders map tiles that shade terrain by its slope angle,
// the steepness that decides where avalanches can start: most start on
// slopes of 30 to 45 degrees. Ski tourers read them to choose terrain, with
// the avalanche forecast saying which angles are dangerous.
package slope

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"

	"github.com/bs-tools/trailrunn.ing/internal/geo"
)

const (
	// TileSize is the width and height of a tile in pixels.
	TileSize = 256
	// MinZoom is the lowest zoom tiles are rendered at. Below it, a pixel
	// spans more than twice the elevation model's 30 m samples, which
	// flattens the slopes, and a screen of tiles spans many of the model's
	// tiles, which take a second or two each to load.
	MinZoom = 12
	// MaxZoom is the highest zoom tiles are rendered at, where a pixel is
	// about 5 m. Higher zooms add no detail to the 30 m samples, so maps
	// enlarge these tiles instead.
	MaxZoom = 15
	// gridSize is the width of the grid of elevations a tile is rendered
	// from: its pixels and a border of one, for the slopes at its edges.
	gridSize = TileSize + 2
	// earthCircumferenceMeters is the circumference of the Earth at the
	// equator, as in the Web Mercator projection.
	earthCircumferenceMeters = 40075016.686
)

// Band is a range of slope angles shaded in one color.
type Band struct {
	// The lowest angle of the band, in degrees. The band ends where the
	// next starts.
	MinDegrees float64
	Color      color.NRGBA
}

// Bands are the shaded slope angles, as on avalanche terrain maps: 27 to 30
// degrees, where avalanches are possible, then every 5 degrees up to 45,
// above which slopes are too steep to hold much snow. Gentler slopes are
// left clear.
var Bands = []Band{
	{MinDegrees: 27, Color: color.NRGBA{R: 0xf2, G: 0xe2, B: 0x05, A: 0xb0}},
	{MinDegrees: 30, Color: color.NRGBA{R: 0xf5, G: 0x9e, B: 0x0b, A: 0xc0}},
	{MinDegrees: 35, Color: color.NRGBA{R: 0xdc, G: 0x26, B: 0x26, A: 0xc8}},
	{MinDegrees: 40, Color: color.NRGBA{R: 0xa2, G: 0x1c, B: 0xaf, A: 0xd0}},
	{MinDegrees: 45, Color: color.NRGBA{R: 0x1e, G: 0x1b, B: 0x4b, A: 0xd0}},
}

// ValidTile reports whether z/x/y is a tile that's rendered: within the
// zoom levels, and on the map.
func ValidTile(z, x, y int) bool {
	n := 1 << z
	return z >= MinZoom && z <= MaxZoom && x >= 0 && x < n && y >= 0 && y < n
}

// pixelPoint returns the location of the center of a pixel of a tile, in
// pixels from the tile's north west corner, which can be outside the tile.
func pixelPoint(z, x, y int, px, py float64) geo.Point {
	worldSize := float64(TileSize) * math.Exp2(float64(z))
	gx := float64(x*TileSize) + px + 0.5
	gy := float64(y*TileSize) + py + 0.5
	lng := gx/worldSize*360 - 180
	lat := math.Atan(math.Sinh(math.Pi*(1-2*gy/worldSize))) * 180 / math.Pi
	return geo.Point{Lat: lat, Lng: lng}
}

// GridPoints returns the points to look up the elevations of to render a
// tile: the centers of its pixels and of a border of one pixel around it, by
// row from the north west.
func GridPoints(z, x, y int) []geo.Point {
	points := make([]geo.Point, 0, gridSize*gridSize)
	for row := range gridSize {
		for col := range gridSize {
			points = append(points, pixelPoint(z, x, y, float64(col-1), float64(row-1)))
		}
	}
	return points
}

// Render renders the tile z/x/y as a PNG from the elevations of its
// GridPoints, in meters.
func Render(z, x, y int, elevations []float64) ([]byte, error) {
	if len(elevations) != gridSize*gridSize {
		return nil, fmt.Errorf("got %d elevations, want %d", len(elevations), gridSize*gridSize)
	}
	palette := color.Palette{color.NRGBA{}}
	for _, b := range Bands {
		palette = append(palette, b.Color)
	}
	img := image.NewPaletted(image.Rect(0, 0, TileSize, TileSize), palette)
	at := func(row, col int) float64 { return elevations[row*gridSize+col] }
	for py := range TileSize {
		// Pixels are about square on the ground in the Web Mercator
		// projection, so one size per row does for both directions.
		lat := pixelPoint(z, x, y, 0, float64(py)).Lat
		pixelMeters := earthCircumferenceMeters * math.Cos(lat*math.Pi/180) / (TileSize * math.Exp2(float64(z)))
		for px := range TileSize {
			// Horn's method, the gradient of the 3x3 neighborhood weighted
			// to its middle row and column, as GIS slope tools use.
			r, c := py+1, px+1
			a, b, cc := at(r-1, c-1), at(r-1, c), at(r-1, c+1)
			d, f := at(r, c-1), at(r, c+1)
			g, h, i := at(r+1, c-1), at(r+1, c), at(r+1, c+1)
			dzdx := ((cc + 2*f + i) - (a + 2*d + g)) / (8 * pixelMeters)
			dzdy := ((g + 2*h + i) - (a + 2*b + cc)) / (8 * pixelMeters)
			degrees := math.Atan(math.Hypot(dzdx, dzdy)) * 180 / math.Pi
			img.SetColorIndex(px, py, uint8(band(degrees)))
		}
	}
	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("failed to encode tile: %w", err)
	}
	return buf.Bytes(), nil
}

// band returns the index in the palette of a slope angle: 0 for clear, or 1
// plus the index of its band. NaN, from missing elevations, is clear.
func band(degrees float64) int {
	index := 0
	for i, b := range Bands {
		if degrees >= b.MinDegrees {
			index = i + 1
		}
	}
	return index
}
