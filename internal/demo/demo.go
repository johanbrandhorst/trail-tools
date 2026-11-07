// Package demo embeds map data of Squamish, BC, where the fake Strava's
// athlete lives: its trails, roads and points of interest from
// OpenStreetMap, and its elevations. A local server loads them, so routes,
// snapping, finding trails and the fake Strava's activities work around
// Squamish without importing or downloading anything.
//
// Regenerate the data with go generate, which downloads the Geofabrik
// extract covering Squamish and elevation tiles.
package demo

//go:generate go run ./gen

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"log/slog"

	"github.com/johanbrandhorst/trail-tools/internal/elevation"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routing"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// Bounds is the area of the data: Squamish, from Murrin Park in the south
// to Garibaldi Lake in the north, and from Howe Sound to Diamond Head.
var Bounds = geo.Bounds{MinLat: 49.62, MinLng: -123.26, MaxLat: 49.98, MaxLng: -122.98}

// Region is the name of the routing region of the trails.
const Region = "squamish-demo"

// The files are written by go generate.
var (
	//go:embed squamish-osm.bin.gz
	osm []byte
	//go:embed squamish-elevation.bin.gz
	elevations []byte
)

// Elevation returns the elevations of Squamish.
func Elevation() (*elevation.Patch, error) {
	return elevation.ReadPatch(bytes.NewReader(elevations))
}

// OSM returns the foot accessible ways of Squamish, with their nodes, and
// its points of interest.
func OSM() (*routing.OSM, error) {
	return routing.ReadOSM(bytes.NewReader(osm))
}

// Load adds the elevations of Squamish to the DEM, and imports its trails
// and points of interest into the routing graph, unless an imported region
// covers Squamish already.
func Load(ctx context.Context, log *slog.Logger, db *store.DB, dem *elevation.DEM) error {
	patch, err := Elevation()
	if err != nil {
		return fmt.Errorf("failed to read the elevations: %w", err)
	}
	dem.AddPatch(patch)
	regions, err := routing.NewRouter(db).Regions(ctx)
	if err != nil {
		return fmt.Errorf("failed to get the routing regions: %w", err)
	}
	center := geo.Point{Lat: (Bounds.MinLat + Bounds.MaxLat) / 2, Lng: (Bounds.MinLng + Bounds.MaxLng) / 2}
	for _, b := range regions {
		if b.Contains(center) {
			return nil
		}
	}
	data, err := OSM()
	if err != nil {
		return fmt.Errorf("failed to read the trails: %w", err)
	}
	log.InfoContext(ctx, "Importing the trails of Squamish for the fake Strava")
	if _, err := routing.ImportOSM(ctx, log, db, Region, Bounds, data, dem); err != nil {
		return fmt.Errorf("failed to import the trails: %w", err)
	}
	return nil
}
