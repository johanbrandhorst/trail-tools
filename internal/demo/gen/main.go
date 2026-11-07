// Command gen writes the demo data: the trails and points of interest of
// Squamish from the Geofabrik extract covering it, and their elevations from
// the elevation tiles.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"

	"github.com/johanbrandhorst/trail-tools/internal/demo"
	"github.com/johanbrandhorst/trail-tools/internal/elevation"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routing"
	"github.com/johanbrandhorst/trail-tools/internal/telemetry"
)

func main() {
	ctx := context.Background()
	b := demo.Bounds
	client := telemetry.NewClient(0)
	data, err := readExtract(ctx, client, b)
	if err != nil {
		log.Fatal(err)
	}
	data = clip(data, b)
	if err := write("squamish-osm.bin.gz", func(w io.Writer) error { return routing.WriteOSM(w, data) }); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Wrote %d ways with %d nodes and %d points of interest\n", len(data.Ways), len(data.Nodes.IDs), len(data.Features))
	patch, err := elevation.NewDEM(elevation.DefaultURL, elevation.DefaultCacheDir, client).Patch(ctx, b)
	if err != nil {
		log.Fatal(err)
	}
	if err := write("squamish-elevation.bin.gz", func(w io.Writer) error { _, err := patch.WriteTo(w); return err }); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Wrote the elevations")
}

// readExtract downloads the Geofabrik extract covering the bounds, and reads
// the foot accessible ways and points of interest within them, as an import
// does.
func readExtract(ctx context.Context, client *http.Client, b geo.Bounds) (*routing.OSM, error) {
	extracts, err := routing.NewExtracts(routing.DefaultGeofabrikIndexURL, client).Find(ctx, b)
	if err != nil {
		return nil, err
	}
	if len(extracts) != 1 {
		return nil, fmt.Errorf("got extracts %+v, want one", extracts)
	}
	fmt.Printf("Downloading %s\n", extracts[0].URL)
	path, err := download(ctx, client, extracts[0].URL)
	if err != nil {
		return nil, err
	}
	defer os.Remove(path)
	return routing.ReadPBF(ctx, path, b)
}

func download(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download %s: status %d", url, resp.StatusCode)
	}
	f, err := os.CreateTemp("", "demo-*.osm.pbf")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}

// clip returns the pieces of the ways within the bounds, so that the
// elevations of the bounds cover all their nodes. The pieces of a way keep
// its ID.
func clip(data *routing.OSM, b geo.Bounds) *routing.OSM {
	nodes := data.Nodes
	inside := func(id int64) bool {
		i, ok := slices.BinarySearch(nodes.IDs, id)
		return ok && b.Contains(geo.Point{Lat: nodes.Lats[i], Lng: nodes.Lngs[i]})
	}
	var ways []routing.Way
	used := map[int64]bool{}
	for _, w := range data.Ways {
		var piece []int64
		flush := func() {
			if len(piece) >= 2 {
				clipped := w
				clipped.Nodes = piece
				ways = append(ways, clipped)
				for _, id := range piece {
					used[id] = true
				}
			}
			piece = nil
		}
		for _, id := range w.Nodes {
			if inside(id) {
				piece = append(piece, id)
			} else {
				flush()
			}
		}
		flush()
	}
	clipped := &routing.Nodes{}
	for i, id := range nodes.IDs {
		if used[id] {
			clipped.IDs = append(clipped.IDs, id)
			clipped.Lats = append(clipped.Lats, nodes.Lats[i])
			clipped.Lngs = append(clipped.Lngs, nodes.Lngs[i])
		}
	}
	return &routing.OSM{Ways: ways, Nodes: clipped, Features: data.Features}
}

func write(name string, fn func(io.Writer) error) error {
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
