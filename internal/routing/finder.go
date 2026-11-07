package routing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// ErrNoMapData is returned when finding trails or points of interest in an
// area outside every imported region.
var ErrNoMapData = errors.New("no map data has been imported for the area")

// TrailHighways are the highway types of the ways FindTrails finds.
var TrailHighways = []string{"path", "footway", "track", "bridleway", "steps", "cycleway"}

// trailHighwaysSQL is the SQL list of TrailHighways. It matches the
// predicate of the routing_edges_trail_way index, so it must stay a literal.
var trailHighwaysSQL = "('" + strings.Join(TrailHighways, "', '") + "')"

// Trail is a section of an OpenStreetMap way that can be run on.
type Trail struct {
	// The OpenStreetMap way ID.
	WayID                            int64
	Name, Highway, Surface, SacScale string
	Terrain                          geo.Terrain
	// The points of the trail, without elevations.
	Points []geo.Point
}

// Finder finds trails and points of interest in the imported regions.
type Finder struct {
	db *store.DB
}

// NewFinder creates a Finder.
func NewFinder(db *store.DB) *Finder {
	return &Finder{db: db}
}

// searchArea returns the SQL arguments for a search within radius meters of
// center: its longitude and latitude, the radius, and the half width and
// height of a bounding box around it in degrees, to use the spatial
// indexes.
func searchArea(center geo.Point, radius float64) []any {
	dy := radius / metersPerDegreeLat
	dx := dy / math.Max(0.01, math.Cos(center.Lat*math.Pi/180))
	return []any{center.Lng, center.Lat, radius, dx, dy}
}

// nearSQL is true for a geometry within the search area of searchArea's
// arguments.
func nearSQL(column string) string {
	return column + ` && ST_Expand(ST_SetSRID(ST_MakePoint($1, $2), 4326), $4, $5)
	and ST_DWithin(` + column + `::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3)`
}

// FindTrails returns the up to limit trails nearest center with a section
// within radius meters of it, whole, nearest first. A way that's only partly
// imported is returned as one trail for each part. It returns ErrNoMapData if
// the area is outside every imported region.
func (f *Finder) FindTrails(ctx context.Context, center geo.Point, radius float64, limit int) ([]Trail, error) {
	// Ways are found by their edges near the center, and returned with all
	// their edges, in order. Overlapping regions can both have a way, so
	// it's taken from one of them. The edges are read as encoded polylines,
	// which are accurate to about a meter and much cheaper to decode than
	// GeoJSON.
	rows, err := f.db.Query(ctx, `with near as (
		select way_id, min(region_id) as region_id,
			min(ST_Distance(geom::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, false)) as distance
		from routing_edges
		where way_id is not null and highway in `+trailHighwaysSQL+` and `+nearSQL("geom")+`
		group by way_id
		order by distance, way_id
		limit $6
	)
	select e.way_id, e.source, e.target, ST_AsEncodedPolyline(e.geom), e.name, e.highway, e.surface, e.sac_scale, e.terrain
	from routing_edges e join near n on e.way_id = n.way_id and e.region_id = n.region_id
	where e.highway in `+trailHighwaysSQL+`
	order by n.distance, e.way_id, e.id`, append(searchArea(center, radius), limit)...)
	if err != nil {
		return nil, fmt.Errorf("failed to find trails: %w", err)
	}
	var trails []Trail
	// The target of the last edge of the last trail.
	var lastTarget int64
	for rows.Next() {
		var t Trail
		var source, target int64
		var polyline, terrain string
		if err := rows.Scan(&t.WayID, &source, &target, &polyline, &t.Name, &t.Highway, &t.Surface, &t.SacScale, &terrain); err != nil {
			return nil, fmt.Errorf("failed to find trails: %w", err)
		}
		points, err := geo.DecodePolyline(polyline)
		if err != nil {
			return nil, fmt.Errorf("failed to decode a section of way %d: %w", t.WayID, err)
		}
		// The edges of a way were imported in order, each starting where the
		// one before ends.
		if n := len(trails); n > 0 && trails[n-1].WayID == t.WayID && lastTarget == source {
			trails[n-1].Points = append(trails[n-1].Points, points[1:]...)
		} else {
			t.Terrain = geo.Terrain(terrain)
			t.Points = points
			trails = append(trails, t)
		}
		lastTarget = target
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to find trails: %w", err)
	}
	if len(trails) == 0 {
		return nil, f.checkImported(ctx, center, radius)
	}
	return trails, nil
}

// FindFeatures returns the points of interest of the given kinds within
// radius meters of center. It returns ErrNoMapData if the area is outside
// every imported region.
func (f *Finder) FindFeatures(ctx context.Context, center geo.Point, radius float64, kinds []geo.FeatureKind) ([]Feature, error) {
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = string(k)
	}
	rows, err := f.db.Query(ctx, `select distinct on (osm_id, is_way)
		osm_id, is_way, kind, name, ST_Y(location), ST_X(location), coalesce(elevation_meters, 'NaN')
	from points_of_interest
	where kind = any($6) and `+nearSQL("location"), append(searchArea(center, radius), names)...)
	if err != nil {
		return nil, fmt.Errorf("failed to find points of interest: %w", err)
	}
	features, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Feature, error) {
		var f Feature
		var kind string
		err := row.Scan(&f.ID, &f.Way, &kind, &f.Name, &f.Point.Lat, &f.Point.Lng, &f.Point.Ele)
		f.Kind = geo.FeatureKind(kind)
		return f, err
	})
	if err != nil {
		return nil, fmt.Errorf("failed to find points of interest: %w", err)
	}
	if len(features) == 0 {
		return nil, f.checkImported(ctx, center, radius)
	}
	return features, nil
}

// checkImported returns ErrNoMapData if no imported region is within radius
// meters of center.
func (f *Finder) checkImported(ctx context.Context, center geo.Point, radius float64) error {
	var imported bool
	if err := f.db.QueryRow(ctx, `select exists (select 1 from routing_regions
		where ST_DWithin(bounds::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3))`,
		center.Lng, center.Lat, radius).Scan(&imported); err != nil {
		return fmt.Errorf("failed to check the imported regions: %w", err)
	}
	if !imported {
		return ErrNoMapData
	}
	return nil
}
