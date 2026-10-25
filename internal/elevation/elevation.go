// Package elevation looks up ground elevations from a digital elevation model
// (DEM): public SRTM derived tiles, downloaded and cached as needed, so that
// lookups don't depend on a rate limited API.
package elevation

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/blob"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

// DefaultURL is the base URL of the elevation tiles: SRTM derived 1x1 degree
// tiles in the HGT format from the AWS Terrain Tiles open dataset.
const DefaultURL = "https://s3.amazonaws.com/elevation-tiles-prod/skadi"

// DefaultCacheDir is the default directory elevation tiles are cached in.
var DefaultCacheDir = filepath.Join(os.TempDir(), "trail-tools-dem")

// maxTileBytes is the size of the largest tiles, of 1 arc second samples
// (3601 a side), uncompressed.
const maxTileBytes = 3601 * 3601 * 2

// maxDownloadBytes limits the size of downloaded tiles, which are gzipped and
// so smaller than maxTileBytes.
const maxDownloadBytes = 32 << 20

// maxCachedTiles is the number of decoded tiles kept in memory. A tile is up
// to 26 MB, and most routes cover one or two.
const maxCachedTiles = 3

// demVoid marks missing data in HGT tiles.
const demVoid = -32768

// maxPointsTiles is the most tiles Points loads for one lookup. Each takes a
// second or two to download and up to 50 MB to decode, so a lookup of points
// spread around the world would keep the server busy for minutes. Routes and
// tracks span a few tiles, which are about 110 km a side.
const maxPointsTiles = 16

// tileDownloadTimeout bounds downloading a tile, which takes a second or
// two, so a stalled download doesn't hold up the loading of other tiles.
const tileDownloadTimeout = 30 * time.Second

// ErrInvalidCoordinates is returned for points that aren't on the Earth's
// surface, with latitudes outside -90 to 90 or longitudes outside -180 to
// 180, or that aren't numbers.
var ErrInvalidCoordinates = errors.New("latitudes must be between -90 and 90 and longitudes between -180 and 180")

// ErrTooManyTiles is returned by Points for points spread over more than
// maxPointsTiles tiles.
var ErrTooManyTiles = fmt.Errorf("the points are spread over more than %d areas of elevation data, which are about 110 km a side; look up the elevations of points closer together",
	maxPointsTiles)

// DEM looks up ground elevations from HGT tiles, downloading them to a cache
// directory, if it has one, as needed, and keeping the most recently used
// ones decoded in memory. It is safe for concurrent use.
type DEM struct {
	baseURL  string
	cacheDir string
	client   *http.Client
	// loadSlot is held while loading a tile. Decoding one takes up to 50 MB,
	// so tiles are loaded one at a time, while lookups of loaded tiles go on.
	loadSlot chan struct{}

	// mu guards the fields below. It isn't held while loading tiles.
	mu sync.Mutex
	// The tiles being loaded, so concurrent lookups don't download or decode
	// the same tile twice.
	loading map[tileCoord]*tileLoad
	// An optional durable cache of the tiles, e.g. a Cloud Storage bucket,
	// for when the cache directory doesn't outlive the process.
	store  blob.Store
	report *errreport.Reporter
	// The most recently used tiles, most recent first. A nil tile records
	// that there is no tile, e.g. over the sea.
	tiles []cachedTile
	// Areas looked up without loading tiles.
	patches []*Patch
}

// tileCoord is the south west corner of a tile.
type tileCoord struct{ lat, lng int }

type cachedTile struct {
	tileCoord
	tile *hgtTile
}

// tileLoad is a tile being loaded. done is closed once tile or err is set.
type tileLoad struct {
	done chan struct{}
	tile *hgtTile
	err  error
}

// NewDEM creates a DEM that downloads tiles from baseURL into cacheDir with
// client. If cacheDir is empty, tiles aren't kept on disk, only the most
// recently used ones in memory.
func NewDEM(baseURL, cacheDir string, client *http.Client) *DEM {
	return &DEM{
		baseURL:  baseURL,
		cacheDir: cacheDir,
		client:   client,
		loadSlot: make(chan struct{}, 1),
		loading:  map[tileCoord]*tileLoad{},
	}
}

// SetTileStore makes the DEM look for tiles missing from the cache directory,
// or all tiles if it has none, in store before downloading them, and add the
// tiles it downloads to it. Failing to read or write the store is reported,
// but the tiles are downloaded instead.
func (d *DEM) SetTileStore(store blob.Store, report *errreport.Reporter) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.store, d.report = store, report
}

// cachedTile returns the tile with the south west corner lat, lng, loading it
// if it isn't in memory.
func (d *DEM) cachedTile(ctx context.Context, lat, lng int) (*hgtTile, error) {
	c := tileCoord{lat, lng}
	for {
		d.mu.Lock()
		if t, ok := d.recentTile(c); ok {
			d.mu.Unlock()
			return t, nil
		}
		if load, ok := d.loading[c]; ok {
			// Wait for the lookup loading the tile.
			d.mu.Unlock()
			select {
			case <-load.done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if errors.Is(load.err, context.Canceled) || errors.Is(load.err, context.DeadlineExceeded) {
				// The lookup loading it gave up, so try again.
				continue
			}
			return load.tile, load.err
		}
		load := &tileLoad{done: make(chan struct{})}
		d.loading[c] = load
		store, report := d.store, d.report
		d.mu.Unlock()

		load.tile, load.err = d.loadTile(ctx, c, store, report)
		d.mu.Lock()
		delete(d.loading, c)
		if load.err == nil {
			d.addTile(c, load.tile)
		}
		d.mu.Unlock()
		close(load.done)
		return load.tile, load.err
	}
}

// recentTile returns a tile kept in memory, moving it to the front. It must
// be called with mu held.
func (d *DEM) recentTile(c tileCoord) (*hgtTile, bool) {
	for i, cached := range d.tiles {
		if cached.tileCoord == c {
			copy(d.tiles[1:i+1], d.tiles[:i])
			d.tiles[0] = cached
			return cached.tile, true
		}
	}
	return nil, false
}

// addTile keeps a tile in memory, in place of the least recently used one
// if there are maxCachedTiles. It must be called with mu held.
func (d *DEM) addTile(c tileCoord, t *hgtTile) {
	if len(d.tiles) < maxCachedTiles {
		d.tiles = append(d.tiles, cachedTile{})
	}
	copy(d.tiles[1:], d.tiles)
	d.tiles[0] = cachedTile{tileCoord: c, tile: t}
}

// loadTile loads a tile when no other tile is being loaded.
func (d *DEM) loadTile(ctx context.Context, c tileCoord, store blob.Store, report *errreport.Reporter) (*hgtTile, error) {
	select {
	case d.loadSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-d.loadSlot }()
	return d.tile(ctx, c.lat, c.lng, store, report)
}

// hgtTile is a 1x1 degree tile of elevations in meters, stored row by row
// from the north edge, with size samples on each side.
type hgtTile struct {
	lat, lng int
	size     int
	samples  []int16
}

// tileName returns the name of the tile whose south west corner is lat, lng,
// e.g. N46E007.
func tileName(lat, lng int) string {
	ns, ew := 'N', 'E'
	if lat < 0 {
		ns = 'S'
	}
	if lng < 0 {
		ew = 'W'
	}
	return fmt.Sprintf("%c%02d%c%03d", ns, abs(lat), ew, abs(lng))
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// tile loads the tile with the south west corner lat, lng. It returns nil if
// there is no tile, e.g. over the sea.
func (d *DEM) tile(ctx context.Context, lat, lng int, store blob.Store, report *errreport.Reporter) (*hgtTile, error) {
	name := tileName(lat, lng)
	data, err := d.tileData(ctx, name, store, report)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		// No data records that the tile doesn't exist.
		return nil, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to read tile %s: %w", name, err)
	}
	raw, err := io.ReadAll(io.LimitReader(zr, maxTileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read tile %s: %w", name, err)
	}
	size := int(math.Round(math.Sqrt(float64(len(raw) / 2))))
	if size < 2 || size*size*2 != len(raw) || len(raw) > maxTileBytes {
		return nil, fmt.Errorf("tile %s has an unexpected size of %d bytes", name, len(raw))
	}
	t := &hgtTile{lat: lat, lng: lng, size: size, samples: make([]int16, size*size)}
	for i := range t.samples {
		t.samples[i] = int16(binary.BigEndian.Uint16(raw[2*i:]))
	}
	return t, nil
}

// tileData returns the gzipped tile from the cache directory, if the DEM
// has one, or else fetches it and adds it to the cache directory. No data
// records that the tile doesn't exist.
func (d *DEM) tileData(ctx context.Context, name string, store blob.Store, report *errreport.Reporter) ([]byte, error) {
	if d.cacheDir == "" {
		return d.fetch(ctx, name, store, report)
	}
	path := filepath.Join(d.cacheDir, name+".hgt.gz")
	data, err := os.ReadFile(path)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return data, err
	}
	data, err = d.fetch(ctx, name, store, report)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(d.cacheDir, 0o755); err != nil {
		return nil, err
	}
	if err := writeFile(path, data); err != nil {
		return nil, err
	}
	return data, nil
}

// tileKey returns the key of a tile in the tile store, following the tile
// server's paths, e.g. skadi/N46/N46E007.hgt.gz.
func tileKey(name string) string {
	return "skadi/" + name[:3] + "/" + name + ".hgt.gz"
}

// fetch returns the gzipped tile from the tile store, if there is one and it
// has it, or else downloads it and adds it to the tile store. No data, or an
// empty object, records that the tile doesn't exist.
func (d *DEM) fetch(ctx context.Context, name string, store blob.Store, report *errreport.Reporter) ([]byte, error) {
	key := tileKey(name)
	if store != nil {
		data, err := store.Get(ctx, key)
		if err == nil {
			return data, nil
		}
		if !errors.Is(err, blob.ErrNotFound) && ctx.Err() == nil {
			report.Report(ctx, "Failed to read an elevation tile from the tile store, downloading it", err,
				slog.String("tile", name))
		}
	}
	data, err := d.download(ctx, name)
	if err != nil {
		return nil, err
	}
	if store != nil {
		if err := store.Put(ctx, key, data, "application/gzip"); err != nil && ctx.Err() == nil {
			report.Report(ctx, "Failed to add an elevation tile to the tile store", err,
				slog.String("tile", name))
		}
	}
	return data, nil
}

// download downloads the gzipped tile. It returns no data if there is no
// tile, e.g. over the sea.
func (d *DEM) download(ctx context.Context, name string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, tileDownloadTimeout)
	defer cancel()
	url := fmt.Sprintf("%s/%s/%s.hgt.gz", d.baseURL, name[:3], name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download elevation tile %s: %w", name, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusForbidden:
		// There is no tile, e.g. over the sea.
		return nil, nil
	default:
		return nil, fmt.Errorf("failed to download elevation tile %s: status %d", name, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to download elevation tile %s: %w", name, err)
	}
	if len(data) > maxDownloadBytes {
		return nil, fmt.Errorf("elevation tile %s is larger than %d bytes", name, maxDownloadBytes)
	}
	return data, nil
}

// writeFile writes data to a temporary file and renames it to path, so
// readers never see a partial file.
func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// elevation returns the elevation at a point in the tile by bilinear
// interpolation, or NaN if there is no data.
func (t *hgtTile) elevation(lat, lng float64) float64 {
	// Rows go south from the north edge, columns east from the west edge.
	y := (float64(t.lat+1) - lat) * float64(t.size-1)
	x := (lng - float64(t.lng)) * float64(t.size-1)
	row := min(max(int(y), 0), t.size-2)
	col := min(max(int(x), 0), t.size-2)
	fy, fx := y-float64(row), x-float64(col)
	corners := [4]int16{
		t.samples[row*t.size+col],
		t.samples[row*t.size+col+1],
		t.samples[(row+1)*t.size+col],
		t.samples[(row+1)*t.size+col+1],
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
		return math.NaN()
	}
	return sum / total
}

// Elevations returns the elevation of each point, from the patches or else
// the tiles, loading one tile at a time to limit memory use. Elevations are
// NaN where there is no data. It loads as many tiles as the points need, for
// imports of map data; lookups for requests use Points.
func (d *DEM) Elevations(ctx context.Context, lats, lngs []float64) ([]float64, error) {
	return d.elevations(ctx, lats, lngs, 0)
}

// elevations is Elevations, loading at most maxTiles tiles, or any number
// if it's 0.
func (d *DEM) elevations(ctx context.Context, lats, lngs []float64, maxTiles int) ([]float64, error) {
	if len(lats) != len(lngs) {
		return nil, fmt.Errorf("got %d latitudes and %d longitudes", len(lats), len(lngs))
	}
	for i := range lats {
		// NaN fails both comparisons.
		if !(lats[i] >= -90 && lats[i] <= 90 && lngs[i] >= -180 && lngs[i] <= 180) {
			return nil, ErrInvalidCoordinates
		}
	}
	elevations := make([]float64, len(lats))
	var rest []int
	for i := range lats {
		e, ok := d.patchElevation(lats[i], lngs[i])
		if !ok {
			rest = append(rest, i)
		}
		elevations[i] = e
	}
	if len(rest) == 0 {
		return elevations, nil
	}
	restLats := make([]float64, len(rest))
	restLngs := make([]float64, len(rest))
	for j, i := range rest {
		restLats[j], restLngs[j] = lats[i], lngs[i]
	}
	fromTiles, err := d.tileElevations(ctx, restLats, restLngs, maxTiles)
	if err != nil {
		return nil, err
	}
	for j, i := range rest {
		elevations[i] = fromTiles[j]
	}
	return elevations, nil
}

// tileElevations returns the elevation of each point from the tiles, of
// which it loads at most maxTiles, or any number if it's 0.
func (d *DEM) tileElevations(ctx context.Context, lats, lngs []float64, maxTiles int) ([]float64, error) {
	byTile := map[tileCoord][]int{}
	for i := range lats {
		// The north and east edges of the Earth are in the tiles south and
		// west of them, as there are no tiles beyond.
		k := tileCoord{min(int(math.Floor(lats[i])), 89), min(int(math.Floor(lngs[i])), 179)}
		byTile[k] = append(byTile[k], i)
	}
	if maxTiles > 0 && len(byTile) > maxTiles {
		return nil, ErrTooManyTiles
	}
	elevations := make([]float64, len(lats))
	for k, idxs := range byTile {
		t, err := d.cachedTile(ctx, k.lat, k.lng)
		if err != nil {
			return nil, err
		}
		for _, i := range idxs {
			elevations[i] = math.NaN()
			if t != nil {
				elevations[i] = t.elevation(lats[i], lngs[i])
			}
		}
	}
	return elevations, nil
}

// Points returns the elevation of each point, loading at most
// maxPointsTiles tiles. Points without data, e.g. over the sea, are at sea
// level.
func (d *DEM) Points(ctx context.Context, points []geo.Point) ([]float64, error) {
	lats := make([]float64, len(points))
	lngs := make([]float64, len(points))
	for i, p := range points {
		lats[i], lngs[i] = p.Lat, p.Lng
	}
	elevations, err := d.elevations(ctx, lats, lngs, maxPointsTiles)
	if err != nil {
		return nil, err
	}
	for i, e := range elevations {
		if math.IsNaN(e) {
			elevations[i] = 0
		}
	}
	return elevations, nil
}
