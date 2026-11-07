package elevation

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

// patchMagic starts patch files.
const patchMagic = "TTDEMPATCH1"

// Patch is a rectangle of elevations cut out of the tiles, such as one
// embedded in the application for an area used when running locally. A DEM
// with a patch looks up points in it without loading tiles.
type Patch struct {
	// The north west corner, and the distance between samples, in degrees.
	north, west, step float64
	rows, cols        int
	// The samples row by row from the north edge, demVoid where there's no
	// data.
	samples []int16
}

// Bounds returns the area of the patch.
func (p *Patch) Bounds() geo.Bounds {
	return geo.Bounds{
		MinLat: p.north - float64(p.rows-1)*p.step,
		MinLng: p.west,
		MaxLat: p.north,
		MaxLng: p.west + float64(p.cols-1)*p.step,
	}
}

// elevation returns the elevation at a point by bilinear interpolation, and
// whether the point is in the patch. It's NaN if there is no data.
func (p *Patch) elevation(lat, lng float64) (float64, bool) {
	y := (p.north - lat) / p.step
	x := (lng - p.west) / p.step
	if y < 0 || x < 0 || y > float64(p.rows-1) || x > float64(p.cols-1) {
		return 0, false
	}
	row := min(int(y), p.rows-2)
	col := min(int(x), p.cols-2)
	fy, fx := y-float64(row), x-float64(col)
	corners := [4]int16{
		p.samples[row*p.cols+col],
		p.samples[row*p.cols+col+1],
		p.samples[(row+1)*p.cols+col],
		p.samples[(row+1)*p.cols+col+1],
	}
	weights := [4]float64{(1 - fx) * (1 - fy), fx * (1 - fy), (1 - fx) * fy, fx * fy}
	var sum, total float64
	for i, v := range corners {
		if v == demVoid {
			continue
		}
		sum += float64(v) * weights[i]
		total += weights[i]
	}
	if total == 0 {
		return math.NaN(), true
	}
	return sum / total, true
}

// AddPatch makes the DEM look up points in the patch from it, instead of
// from the tiles.
func (d *DEM) AddPatch(p *Patch) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.patches = append(d.patches, p)
}

// patchElevation returns the elevation of a point from the DEM's patches,
// and whether a patch covers it.
func (d *DEM) patchElevation(lat, lng float64) (float64, bool) {
	d.mu.Lock()
	patches := d.patches
	d.mu.Unlock()
	for _, p := range patches {
		if e, ok := p.elevation(lat, lng); ok {
			return e, true
		}
	}
	return 0, false
}

// Patch cuts the samples within the bounds out of the tiles, at their full
// resolution.
func (d *DEM) Patch(ctx context.Context, bounds geo.Bounds) (*Patch, error) {
	if !(bounds.MaxLat > bounds.MinLat && bounds.MaxLng > bounds.MinLng) {
		return nil, errors.New("the bounds are empty")
	}
	t, err := d.cachedTile(ctx, int(math.Floor(bounds.MinLat)), int(math.Floor(bounds.MinLng)))
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, errors.New("there is no elevation data for the bounds")
	}
	// Samples are aligned with the tiles' grid.
	step := 1 / float64(t.size-1)
	north, south := math.Ceil(bounds.MaxLat/step)*step, math.Floor(bounds.MinLat/step)*step
	west, east := math.Floor(bounds.MinLng/step)*step, math.Ceil(bounds.MaxLng/step)*step
	rows := int(math.Round((north-south)/step)) + 1
	cols := int(math.Round((east-west)/step)) + 1
	p := &Patch{north: north, west: west, step: step, rows: rows, cols: cols, samples: make([]int16, rows*cols)}
	lats := make([]float64, 0, rows*cols)
	lngs := make([]float64, 0, rows*cols)
	for row := range rows {
		for col := range cols {
			lats = append(lats, north-float64(row)*step)
			lngs = append(lngs, west+float64(col)*step)
		}
	}
	elevations, err := d.tileElevations(ctx, lats, lngs, 0)
	if err != nil {
		return nil, err
	}
	for i, e := range elevations {
		p.samples[i] = demVoid
		if !math.IsNaN(e) {
			p.samples[i] = int16(math.Round(e))
		}
	}
	return p, nil
}

// WriteTo writes the patch gzipped, with each sample stored as the
// difference to the one before it, which compresses well.
func (p *Patch) WriteTo(w io.Writer) (int64, error) {
	cw := &countingWriter{w: w}
	zw, err := gzip.NewWriterLevel(cw, gzip.BestCompression)
	if err != nil {
		return 0, err
	}
	bw := bufio.NewWriter(zw)
	header := []any{[]byte(patchMagic), p.north, p.west, p.step, uint32(p.rows), uint32(p.cols)}
	for _, v := range header {
		if err := binary.Write(bw, binary.BigEndian, v); err != nil {
			return cw.n, err
		}
	}
	var buf [binary.MaxVarintLen64]byte
	previous := int64(0)
	for _, s := range p.samples {
		n := binary.PutVarint(buf[:], int64(s)-previous)
		if _, err := bw.Write(buf[:n]); err != nil {
			return cw.n, err
		}
		previous = int64(s)
	}
	if err := bw.Flush(); err != nil {
		return cw.n, err
	}
	err = zw.Close()
	return cw.n, err
}

// ReadPatch reads a patch written by WriteTo.
func ReadPatch(r io.Reader) (*Patch, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read the patch: %w", err)
	}
	br := bufio.NewReader(zr)
	magic := make([]byte, len(patchMagic))
	if _, err := io.ReadFull(br, magic); err != nil || string(magic) != patchMagic {
		return nil, errors.New("not an elevation patch")
	}
	p := &Patch{}
	var rows, cols uint32
	for _, v := range []any{&p.north, &p.west, &p.step, &rows, &cols} {
		if err := binary.Read(br, binary.BigEndian, v); err != nil {
			return nil, fmt.Errorf("failed to read the patch: %w", err)
		}
	}
	p.rows, p.cols = int(rows), int(cols)
	if p.rows < 2 || p.cols < 2 || p.rows*p.cols > 1<<26 || !(p.step > 0) {
		return nil, errors.New("the patch has an invalid size")
	}
	p.samples = make([]int16, p.rows*p.cols)
	previous := int64(0)
	for i := range p.samples {
		delta, err := binary.ReadVarint(br)
		if err != nil {
			return nil, fmt.Errorf("failed to read the patch: %w", err)
		}
		previous += delta
		p.samples[i] = int16(previous)
	}
	return p, nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n += int64(n)
	return n, err
}
