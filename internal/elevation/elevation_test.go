package elevation_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/blob"
	"github.com/johanbrandhorst/trail-tools/internal/elevation"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

func writeTile(t *testing.T, dir, name string, size int, sample func(row, col int) int16) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), gzipTile(t, size, sample), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gzipTile returns a gzipped HGT tile of size samples a side.
func gzipTile(t *testing.T, size int, sample func(row, col int) int16) []byte {
	t.Helper()
	var raw bytes.Buffer
	for row := range size {
		for col := range size {
			if err := binary.Write(&raw, binary.BigEndian, sample(row, col)); err != nil {
				t.Fatal(err)
			}
		}
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	if _, err := w.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return gz.Bytes()
}

func TestDEM(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A tile rising 100m per sample to the east, from 0 at the west edge,
	// with a void in the north east corner. Rows go south from the north.
	writeTile(t, dir, "N46E007.hgt.gz", 3, func(row, col int) int16 {
		if row == 0 && col == 2 {
			return -32768
		}
		return int16(col * 100)
	})
	// An empty file records a missing tile, e.g. over the sea.
	if err := os.WriteFile(filepath.Join(dir, "N00E000.hgt.gz"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// The URL is never used, as the tiles are cached.
	dem := elevation.NewDEM("http://invalid.test", dir, &http.Client{})
	elevations, err := dem.Elevations(context.Background(), []float64{46.5, 46.25, 0.5}, []float64{7.25, 7.75, 0.5})
	if err != nil {
		t.Fatalf("Elevations: %v", err)
	}
	if math.Abs(elevations[0]-50) > 0.01 {
		t.Errorf("got %v, want 50 a quarter of the way east", elevations[0])
	}
	if math.Abs(elevations[1]-150) > 0.01 {
		t.Errorf("got %v, want 150 three quarters of the way east", elevations[1])
	}
	if !math.IsNaN(elevations[2]) {
		t.Errorf("got %v for a missing tile, want NaN", elevations[2])
	}
}

func TestPatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A tile of 11 samples a side, 0.1 degrees apart, rising 10 m per
	// sample to the east and 1 m per sample to the south.
	writeTile(t, dir, "N46E007.hgt.gz", 11, func(row, col int) int16 {
		return int16(1000 + col*10 + row)
	})
	ctx := context.Background()
	dem := elevation.NewDEM("http://invalid.test", dir, &http.Client{})
	patch, err := dem.Patch(ctx, geo.Bounds{MinLat: 46.35, MinLng: 7.25, MaxLat: 46.62, MaxLng: 7.5})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	// The patch is aligned with the samples, covering the bounds.
	if b := patch.Bounds(); math.Abs(b.MinLat-46.3) > 1e-9 || math.Abs(b.MaxLat-46.7) > 1e-9 ||
		math.Abs(b.MinLng-7.2) > 1e-9 || math.Abs(b.MaxLng-7.5) > 1e-9 {
		t.Errorf("got patch bounds %+v", b)
	}
	var buf bytes.Buffer
	if _, err := patch.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	read, err := elevation.ReadPatch(&buf)
	if err != nil {
		t.Fatalf("ReadPatch: %v", err)
	}
	if _, err := elevation.ReadPatch(bytes.NewReader([]byte("not a patch"))); err == nil {
		t.Error("ReadPatch of garbage succeeded")
	}

	// With the patch, points in it are looked up without the tile, and
	// others from the tiles.
	empty := t.TempDir()
	withPatch := elevation.NewDEM("http://invalid.test", empty, &http.Client{})
	withPatch.AddPatch(read)
	got, err := withPatch.Elevations(ctx, []float64{46.45, 46.6}, []float64{7.35, 7.3})
	if err != nil {
		t.Fatalf("Elevations: %v", err)
	}
	want, err := dem.Elevations(ctx, []float64{46.45, 46.6}, []float64{7.35, 7.3})
	if err != nil {
		t.Fatal(err)
	}
	for i := range got {
		if math.Abs(got[i]-want[i]) > 1e-6 {
			t.Errorf("point %d: got %v from the patch, want %v", i, got[i], want[i])
		}
	}
	// Outside the patch, the tile is downloaded, which fails here.
	if _, err := withPatch.Elevations(ctx, []float64{46.9}, []float64{7.9}); err == nil {
		t.Error("Elevations outside the patch didn't use the tiles")
	}
}

// serveTiles serves the tiles by path, e.g. /N46/N46E007.hgt.gz, and 404 for
// others. It returns the server's URL and a function returning the number of
// requests for a path.
func serveTiles(t *testing.T, tiles map[string][]byte) (string, func(path string) int) {
	t.Helper()
	var mu sync.Mutex
	requests := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		tile, ok := tiles[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(tile)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return requests[path]
	}
}

// failingStore fails every operation, like an unreachable bucket.
type failingStore struct{}

func (failingStore) Put(context.Context, string, []byte, string) error {
	return errors.New("bucket unavailable")
}

func (failingStore) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("bucket unavailable")
}

func (failingStore) Delete(context.Context, string) error {
	return errors.New("bucket unavailable")
}

func TestDEMTileStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	// A tile rising 100m per sample to the east. N00E000 doesn't exist.
	tile := gzipTile(t, 3, func(row, col int) int16 { return int16(col * 100) })
	const tilePath, missingPath = "/N46/N46E007.hgt.gz", "/N00/N00E000.hgt.gz"
	url, requests := serveTiles(t, map[string][]byte{tilePath: tile})
	store, err := blob.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkElevations := func(t *testing.T, dem *elevation.DEM) {
		t.Helper()
		elevations, err := dem.Elevations(ctx, []float64{46.5, 0.5}, []float64{7.25, 0.5})
		if err != nil {
			t.Fatalf("Elevations: %v", err)
		}
		if math.Abs(elevations[0]-50) > 0.01 {
			t.Errorf("got %v, want 50 a quarter of the way east", elevations[0])
		}
		if !math.IsNaN(elevations[1]) {
			t.Errorf("got %v for a missing tile, want NaN", elevations[1])
		}
	}
	checkRequests := func(t *testing.T, want int) {
		t.Helper()
		for _, path := range []string{tilePath, missingPath} {
			if got := requests(path); got != want {
				t.Errorf("got %d requests for %s, want %d", got, path, want)
			}
		}
	}

	// Downloaded tiles are added to the store, and missing tiles recorded
	// as empty objects.
	dem := elevation.NewDEM(url, t.TempDir(), &http.Client{})
	dem.SetTileStore(store, errreport.NewLocal(log))
	checkElevations(t, dem)
	checkRequests(t, 1)
	stored, err := store.Get(ctx, "skadi/N46/N46E007.hgt.gz")
	if err != nil {
		t.Fatalf("the downloaded tile isn't in the store: %v", err)
	}
	if !bytes.Equal(stored, tile) {
		t.Error("the stored tile differs from the downloaded one")
	}
	missing, err := store.Get(ctx, "skadi/N00/N00E000.hgt.gz")
	if err != nil {
		t.Fatalf("the missing tile isn't recorded in the store: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("got %d bytes recording a missing tile, want none", len(missing))
	}

	// A DEM with an empty cache directory, like after a restart, gets the
	// tiles from the store without downloading them.
	restarted := elevation.NewDEM(url, t.TempDir(), &http.Client{})
	restarted.SetTileStore(store, errreport.NewLocal(log))
	checkElevations(t, restarted)
	checkRequests(t, 1)

	// A DEM without a cache directory gets the tiles from the store too.
	noDisk := elevation.NewDEM(url, "", &http.Client{})
	noDisk.SetTileStore(store, errreport.NewLocal(log))
	checkElevations(t, noDisk)
	checkRequests(t, 1)

	// When the store fails, the tiles are downloaded.
	failing := elevation.NewDEM(url, t.TempDir(), &http.Client{})
	failing.SetTileStore(failingStore{}, errreport.NewLocal(log))
	checkElevations(t, failing)
	checkRequests(t, 2)
}

func TestDEMLimitsDownloads(t *testing.T) {
	t.Parallel()
	// A tile server sending more data than any tile has.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 1<<20)
		for range 40 {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	dem := elevation.NewDEM(srv.URL, "", srv.Client())
	_, err := dem.Elevations(context.Background(), []float64{46.5}, []float64{7.5})
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("got error %v, want the download to be too large", err)
	}
}

func TestDEMValidatesPoints(t *testing.T) {
	t.Parallel()
	// The URL is never used, as the points are rejected first.
	dem := elevation.NewDEM("http://invalid.test", "", &http.Client{})
	ctx := context.Background()
	for name, p := range map[string]geo.Point{
		"latitude too large":  {Lat: 91, Lng: 7},
		"longitude too small": {Lat: 46, Lng: -181},
		"not a number":        {Lat: math.NaN(), Lng: 7},
		"infinite":            {Lat: 46, Lng: math.Inf(1)},
	} {
		if _, err := dem.Points(ctx, []geo.Point{p}); !errors.Is(err, elevation.ErrInvalidCoordinates) {
			t.Errorf("%s: got %v, want %v", name, err, elevation.ErrInvalidCoordinates)
		}
	}
	// Points spread around the world need too many tiles.
	var spread []geo.Point
	for lat := range 20 {
		spread = append(spread, geo.Point{Lat: float64(lat * 4), Lng: 0})
	}
	if _, err := dem.Points(ctx, spread); !errors.Is(err, elevation.ErrTooManyTiles) {
		t.Errorf("points in 20 tiles: got %v, want %v", err, elevation.ErrTooManyTiles)
	}
}

func TestDEMLoadsTilesOnce(t *testing.T) {
	t.Parallel()
	tile := gzipTile(t, 3, func(row, col int) int16 { return int16(col * 100) })
	url, requests := serveTiles(t, map[string][]byte{"/N46/N46E007.hgt.gz": tile})
	dem := elevation.NewDEM(url, "", &http.Client{})
	// Concurrent lookups in the same tile download it once.
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, err := dem.Elevations(context.Background(), []float64{46.5}, []float64{7.5}); err != nil {
				t.Errorf("Elevations: %v", err)
			}
		})
	}
	wg.Wait()
	if got := requests("/N46/N46E007.hgt.gz"); got != 1 {
		t.Errorf("downloaded the tile %d times, want once", got)
	}
}

func TestDEMLookupsDontWaitForDownloads(t *testing.T) {
	t.Parallel()
	tile := gzipTile(t, 3, func(row, col int) int16 { return int16(col * 100) })
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/N10/N10E010.hgt.gz" {
			// A slow download.
			<-release
		}
		_, _ = w.Write(tile)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	dem := elevation.NewDEM(srv.URL, "", srv.Client())
	ctx := context.Background()
	if _, err := dem.Elevations(ctx, []float64{46.5}, []float64{7.5}); err != nil {
		t.Fatalf("Elevations: %v", err)
	}
	slow := make(chan error, 1)
	go func() {
		_, err := dem.Elevations(ctx, []float64{10.5}, []float64{10.5})
		slow <- err
	}()
	// A lookup in the loaded tile doesn't wait for the slow download.
	done := make(chan error, 1)
	go func() {
		_, err := dem.Elevations(ctx, []float64{46.25}, []float64{7.25})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Elevations: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("a lookup in a loaded tile waited for another tile's download")
	}
	close(release)
	if err := <-slow; err != nil {
		t.Errorf("Elevations with the slow download: %v", err)
	}
}
