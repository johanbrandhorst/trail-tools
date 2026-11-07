package routing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/johanbrandhorst/trail-tools/internal/elevation"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// ImportConfig describes an OpenStreetMap import.
type ImportConfig struct {
	// The name of the region. Importing a region again replaces it.
	Region string
	// The OpenStreetMap PBF extracts: file paths or http(s) URLs, such as
	// Geofabrik extracts. Neighboring extracts can be imported together,
	// e.g. for an area across a border.
	Sources []string
	// Only ways with a node inside the bounds are imported. The whole extract
	// is imported if the bounds are zero.
	Bounds geo.Bounds
	// The elevation model.
	DEM *elevation.DEM
	// Downloads the extracts given by URL.
	HTTPClient *http.Client
}

// Import loads the foot accessible ways and points of interest of
// OpenStreetMap extracts into the routing graph, and returns the number of
// edges imported.
func Import(ctx context.Context, log *slog.Logger, db *store.DB, conf ImportConfig) (int, error) {
	if len(conf.Sources) == 0 {
		return 0, errors.New("no OpenStreetMap extract to import")
	}
	data := &OSM{Nodes: &Nodes{}}
	// Extracts are read one at a time, and only what's inside the bounds is
	// kept, so memory grows with the area rather than the extracts.
	for _, source := range conf.Sources {
		more, err := readSource(ctx, log, conf.HTTPClient, source, conf.Bounds)
		if err != nil {
			return 0, err
		}
		log.InfoContext(ctx, "Read the OpenStreetMap extract", slog.String("source", source), slog.Int("ways", len(more.Ways)),
			slog.Int("nodes", len(more.Nodes.IDs)), slog.Int("points_of_interest", len(more.Features)))
		data = MergeExtracts(data, more)
	}
	count, err := ImportOSM(ctx, log, db, conf.Region, conf.Bounds, data, conf.DEM)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// readSource reads an OpenStreetMap extract from a file or URL with ReadPBF.
// A downloaded extract is removed once read.
func readSource(ctx context.Context, log *slog.Logger, client *http.Client, source string, bounds geo.Bounds) (*OSM, error) {
	path := source
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		log.InfoContext(ctx, "Downloading the OpenStreetMap extract", slog.String("url", source))
		downloaded, err := download(ctx, client, source)
		if err != nil {
			return nil, err
		}
		defer os.Remove(downloaded)
		path = downloaded
	}
	return ReadPBF(ctx, path, bounds)
}

// download saves a URL to a temporary file and returns its path.
func download(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download %s: status %d", url, resp.StatusCode)
	}
	f, err := os.CreateTemp("", "osm-*.pbf")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", fmt.Errorf("failed to download %s: %w", url, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
