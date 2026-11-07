package routing

import (
	"context"
	"fmt"
	"iter"
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// The routing queries use PostGIS and pgRouting functions that sqlc doesn't
// know about, so they are written with pgx directly.

// SaveRegion replaces the edges and points of interest of a region in the
// routing graph. The edges are streamed into the database, so they don't all
// need to be in memory.
func SaveRegion(ctx context.Context, db *store.DB, region string, bounds geo.Bounds, edges iter.Seq[Edge], features []Feature) (int, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var regionID int16
	if err := tx.QueryRow(ctx, `insert into routing_regions (name, bounds, edge_count)
	values ($1, ST_MakeEnvelope($2, $3, $4, $5, 4326), 0)
	on conflict (name) do update set bounds = excluded.bounds, import_time = current_timestamp
	returning id`, region, bounds.MinLng, bounds.MinLat, bounds.MaxLng, bounds.MaxLat).Scan(&regionID); err != nil {
		return 0, fmt.Errorf("failed to save the region: %w", err)
	}
	if _, err := tx.Exec(ctx, `delete from routing_edges where region_id = $1`, regionID); err != nil {
		return 0, fmt.Errorf("failed to delete the region's edges: %w", err)
	}
	if _, err := tx.Exec(ctx, `delete from points_of_interest where region_id = $1`, regionID); err != nil {
		return 0, fmt.Errorf("failed to delete the region's points of interest: %w", err)
	}
	// Geometries are copied as text into a staging table, as pgx can't encode
	// PostGIS types.
	if _, err := tx.Exec(ctx, `create temporary table staged_edges (
		source bigint, target bigint, wkt text, elevations smallint[], length_meters real,
		climb_meters real, descent_meters real, highway text, sac_scale text, terrain text,
		way_id bigint, name text, surface text
	) on commit drop`); err != nil {
		return 0, fmt.Errorf("failed to create the staging table: %w", err)
	}
	next, stop := iter.Pull(edges)
	defer stop()
	source := &edgeSource{next: next}
	columns := []string{"source", "target", "wkt", "elevations", "length_meters", "climb_meters", "descent_meters", "highway", "sac_scale", "terrain", "way_id", "name", "surface"}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"staged_edges"}, columns, source); err != nil {
		return 0, fmt.Errorf("failed to copy edges: %w", err)
	}
	if _, err := tx.Exec(ctx, `insert into routing_edges (
		region_id, source, target, geom, elevations, length_meters, climb_meters,
		descent_meters, highway, sac_scale, terrain, way_id, name, surface
	) select $1, source, target, ST_GeomFromText(wkt, 4326), elevations, length_meters,
		climb_meters, descent_meters, highway, sac_scale, terrain, way_id, name, surface
	from staged_edges`, regionID); err != nil {
		return 0, fmt.Errorf("failed to insert edges: %w", err)
	}
	if _, err := tx.Exec(ctx, `create temporary table staged_points_of_interest (
		osm_id bigint, is_way boolean, kind text, name text, lat double precision,
		lng double precision, elevation_meters real
	) on commit drop`); err != nil {
		return 0, fmt.Errorf("failed to create the staging table: %w", err)
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"staged_points_of_interest"},
		[]string{"osm_id", "is_way", "kind", "name", "lat", "lng", "elevation_meters"},
		pgx.CopyFromSlice(len(features), func(i int) ([]any, error) {
			f := features[i]
			var ele *float32
			if !math.IsNaN(f.Point.Ele) {
				v := float32(f.Point.Ele)
				ele = &v
			}
			return []any{f.ID, f.Way, string(f.Kind), f.Name, f.Point.Lat, f.Point.Lng, ele}, nil
		})); err != nil {
		return 0, fmt.Errorf("failed to copy points of interest: %w", err)
	}
	if _, err := tx.Exec(ctx, `insert into points_of_interest (
		region_id, osm_id, is_way, kind, name, location, elevation_meters
	) select $1, osm_id, is_way, kind, name, ST_SetSRID(ST_MakePoint(lng, lat), 4326), elevation_meters
	from staged_points_of_interest`, regionID); err != nil {
		return 0, fmt.Errorf("failed to insert points of interest: %w", err)
	}
	if _, err := tx.Exec(ctx, `update routing_regions set edge_count = $2 where id = $1`, regionID, source.count); err != nil {
		return 0, fmt.Errorf("failed to save the region: %w", err)
	}
	return source.count, tx.Commit(ctx)
}

// edgeSource is a pgx.CopyFromSource of edges.
type edgeSource struct {
	next  func() (Edge, bool)
	row   []any
	count int
}

func (s *edgeSource) Next() bool {
	e, ok := s.next()
	if !ok {
		return false
	}
	elevations := filledElevations(e.Points)
	climb, descent := climbDescent(elevations)
	rounded := make([]int16, len(elevations))
	for i, v := range elevations {
		rounded[i] = int16(math.Round(float64(v)))
	}
	s.row = []any{
		e.Source, e.Target, lineWKT(e.Points), rounded, float32(pathLength(e.Points)),
		climb, descent, e.Highway, e.SacScale, string(e.Terrain), e.WayID, e.Name, e.Surface,
	}
	s.count++
	return true
}

func (s *edgeSource) Values() ([]any, error) { return s.row, nil }

func (s *edgeSource) Err() error { return nil }

// lineWKT returns the well-known text of a line through the points.
func lineWKT(points []geo.Point) string {
	var b strings.Builder
	b.WriteString("LINESTRING(")
	for i, p := range points {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(p.Lng, 'f', 7, 64))
		b.WriteByte(' ')
		b.WriteString(strconv.FormatFloat(p.Lat, 'f', 7, 64))
	}
	b.WriteByte(')')
	return b.String()
}

// filledElevations returns the elevations of the points, filling unknown
// ones from the nearest known elevation before them, or after them at the
// start. They are 0 if none are known.
func filledElevations(points []geo.Point) []float32 {
	elevations := make([]float32, len(points))
	last := math.NaN()
	for i, p := range points {
		if !math.IsNaN(p.Ele) {
			last = p.Ele
		}
		elevations[i] = float32(last)
	}
	first := float32(0)
	for _, e := range elevations {
		if !math.IsNaN(float64(e)) {
			first = e
			break
		}
	}
	for i, e := range elevations {
		if !math.IsNaN(float64(e)) {
			break
		}
		elevations[i] = first
	}
	return elevations
}

// climbDescent returns the total elevation gained and lost along a path.
func climbDescent(elevations []float32) (climb, descent float32) {
	for i := 1; i < len(elevations); i++ {
		if d := elevations[i] - elevations[i-1]; d > 0 {
			climb += d
		} else {
			descent -= d
		}
	}
	return climb, descent
}

func pathLength(points []geo.Point) float64 {
	var length float64
	for i := 1; i < len(points); i++ {
		length += geo.Distance(points[i-1], points[i])
	}
	return length
}
