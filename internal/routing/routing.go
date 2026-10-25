// Package routing routes paths for running on OpenStreetMap data with
// pgRouting, and imports the data into the routing graph.
package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// Profile controls which paths are preferred.
type Profile int

const (
	// ProfileTrail prefers trails and unpaved paths and avoids busy roads and
	// climbing.
	ProfileTrail Profile = iota
	// ProfileShortest takes the shortest path.
	ProfileShortest
)

// ErrNotRoutable is wrapped by errors for points that can't be routed
// between, such as points far from any mapped path.
var ErrNotRoutable = errors.New("not routable")

const (
	// maxSnapMeters is the furthest a point can be from a mapped path.
	maxSnapMeters = 500
	// minSearchMarginMeters is the smallest margin around the points within
	// which paths are searched.
	minSearchMarginMeters = 2000
	// searchAttempts is the number of times the search area is grown when no
	// path is found.
	searchAttempts = 2
	// preferMeters is how close an edge must be to a preferred path to count
	// as on it.
	preferMeters = 25
	// preferCostFactor scales the cost of edges on preferred paths.
	preferCostFactor = 0.5
	// metersPerDegreeLat is the approximate length of a degree of latitude.
	metersPerDegreeLat = 111320.0
)

// trailCost is the cost of traveling an edge forward with the trail profile:
// its length, scaled by how pleasant it is to run on, plus the effort of
// climbing, a climbed meter costing like 8 meters on the flat.
const trailCost = `length_meters * (case highway
	when 'path' then 1.0 when 'footway' then 1.0 when 'bridleway' then 1.05 when 'track' then 1.05
	when 'steps' then 1.3 when 'pedestrian' then 1.2 when 'living_street' then 1.3
	when 'cycleway' then 1.4 when 'service' then 1.4 when 'unclassified' then 1.4
	when 'residential' then 1.5 when 'road' then 1.5
	when 'tertiary' then 2.0 when 'tertiary_link' then 2.0
	when 'secondary' then 3.0 when 'secondary_link' then 3.0
	when 'primary' then 5.0 when 'primary_link' then 5.0
	else 1.5 end)
* (case when terrain = 'paved' and highway in ('path', 'footway', 'track', 'bridleway') then 1.2 else 1.0 end)
* (case sac_scale when 'demanding_mountain_hiking' then 1.3 when 'alpine_hiking' then 1.8
	when 'demanding_alpine_hiking' then 3.0 when 'difficult_alpine_hiking' then 5.0 else 1.0 end)`

// costs returns the SQL expressions for the cost of traveling edges forward
// and backward with a profile.
func costs(profile Profile, prefer bool) (cost, reverseCost string) {
	if profile == ProfileShortest {
		cost, reverseCost = "length_meters", "length_meters"
	} else {
		cost = "(" + trailCost + " + 8 * climb_meters + 2 * descent_meters)"
		reverseCost = "(" + trailCost + " + 8 * descent_meters + 2 * climb_meters)"
	}
	if prefer {
		// An edge is on a preferred path if its middle and quarter points are
		// near one.
		factor := fmt.Sprintf(`(case when exists (select 1 from preferred_paths p
			where ST_DWithin(ST_LineInterpolatePoint(routing_edges.geom, 0.25), p.geom, p.tolerance)
			and ST_DWithin(ST_LineInterpolatePoint(routing_edges.geom, 0.5), p.geom, p.tolerance)
			and ST_DWithin(ST_LineInterpolatePoint(routing_edges.geom, 0.75), p.geom, p.tolerance)
		) then %g else 1 end)`, preferCostFactor)
		cost, reverseCost = cost+" * "+factor, reverseCost+" * "+factor
	}
	return cost, reverseCost
}

// Request is a request for a path between two points.
type Request struct {
	From, To geo.Point
	Profile  Profile
	// Paths to prefer, such as the user's previous activities. Edges along
	// them cost less, so routes follow them where they reasonably can.
	Prefer [][]geo.Point
}

// Path is a routed path.
type Path struct {
	// The points of the path, with elevations. It starts and ends where the
	// requested points were snapped onto the map.
	Points []geo.Point
	// The terrain of each segment: Terrains[i] is the terrain between
	// Points[i] and Points[i+1]. It has the same length as Points.
	Terrains []geo.Terrain
}

// Router routes paths on the routing graph.
type Router struct {
	db *store.DB
}

// NewRouter creates a Router.
func NewRouter(db *store.DB) *Router {
	return &Router{db: db}
}

// Regions returns the bounds of the imported regions, the areas paths can be
// routed in.
func (r *Router) Regions(ctx context.Context) ([]geo.Bounds, error) {
	rows, err := r.db.Query(ctx, `select ST_YMin(bounds), ST_XMin(bounds), ST_YMax(bounds), ST_XMax(bounds) from routing_regions`)
	if err != nil {
		return nil, fmt.Errorf("failed to list the imported regions: %w", err)
	}
	regions, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (geo.Bounds, error) {
		var b geo.Bounds
		err := row.Scan(&b.MinLat, &b.MinLng, &b.MaxLat, &b.MaxLng)
		return b, err
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list the imported regions: %w", err)
	}
	return regions, nil
}

// edge is an edge of the routing graph with its geometry.
type edge struct {
	id, source, target int64
	points             []geo.Point
	// The distance along the edge to each point, in meters.
	dists   []float64
	terrain geo.Terrain
}

// position is a point on an edge, as the distance along it in meters.
type position struct {
	edge *edge
	dist float64
	// The costs of traveling the whole edge forward and backward.
	cost, reverseCost float64
}

// scanEdge scans an edge from a row's first columns, its id, source, target,
// ST_AsGeoJSON(geom), elevations and terrain, followed by any extra columns.
func scanEdge(row pgx.Row, extra ...any) (*edge, error) {
	var e edge
	var geojson string
	var elevations []float32
	var terrain string
	dest := append([]any{&e.id, &e.source, &e.target, &geojson, &elevations, &terrain}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	var line struct {
		Coordinates [][2]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal([]byte(geojson), &line); err != nil {
		return nil, fmt.Errorf("failed to decode edge %d: %w", e.id, err)
	}
	for i, c := range line.Coordinates {
		p := geo.Point{Lng: c[0], Lat: c[1]}
		if i < len(elevations) {
			p.Ele = float64(elevations[i])
		}
		e.points = append(e.points, p)
	}
	e.dists = geo.CumulativeDistances(e.points)
	e.terrain = geo.Terrain(terrain)
	return &e, nil
}

// length returns the length of the edge in meters.
func (e *edge) length() float64 {
	return e.dists[len(e.dists)-1]
}

// Route computes a path between two points.
func (r *Router) Route(ctx context.Context, req Request) (*Path, error) {
	// The preferred paths are stored in a temporary table, which the search
	// needs a transaction for. Without them, it runs without one, saving the
	// round trips of starting and ending it.
	var db store.Querier = r.db
	prefer := len(req.Prefer) > 0
	if prefer {
		tx, err := r.db.Begin(ctx)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx)
		if err := savePreferred(ctx, tx, req.Prefer); err != nil {
			return nil, err
		}
		db = tx
	}
	cost, reverseCost := costs(req.Profile, prefer)
	ends, err := snap(ctx, db, []geo.Point{req.From, req.To}, cost, reverseCost)
	if err != nil {
		return nil, err
	}
	from, to := ends[0], ends[1]
	margin := math.Max(minSearchMarginMeters, geo.Distance(req.From, req.To)/2)
	for range searchAttempts {
		path, err := search(ctx, db, from, to, cost, reverseCost, margin)
		if err != nil || path != nil {
			return path, err
		}
		margin *= 4
	}
	return nil, fmt.Errorf("%w: no path was found between the points", ErrNotRoutable)
}

// snap returns the positions on the nearest edges to points, with the costs
// of the edges, in a single round trip.
func snap(ctx context.Context, db store.Querier, points []geo.Point, cost, reverseCost string) ([]position, error) {
	lngs, lats := make([]float64, len(points)), make([]float64, len(points))
	for i, p := range points {
		lngs[i], lats[i] = p.Lng, p.Lat
	}
	rows, err := db.Query(ctx, `select e.id, e.source, e.target, ST_AsGeoJSON(e.geom), e.elevations, e.terrain,
		e.distance, e.cost, e.reverse_cost, pt.n
	from unnest($1::double precision[], $2::double precision[]) with ordinality as pt(lng, lat, n)
	cross join lateral (
		select id, source, target, geom, elevations, terrain,
			ST_Distance(geom::geography, ST_SetSRID(ST_MakePoint(pt.lng, pt.lat), 4326)::geography) as distance,
			`+cost+` as cost, `+reverseCost+` as reverse_cost
		from routing_edges
		order by geom <-> ST_SetSRID(ST_MakePoint(pt.lng, pt.lat), 4326)
		limit 1
	) e`, lngs, lats)
	if err != nil {
		return nil, fmt.Errorf("failed to find the nearest paths: %w", err)
	}
	defer rows.Close()
	positions := make([]position, len(points))
	distances := make([]float64, len(points))
	found := make([]bool, len(points))
	for rows.Next() {
		var distance, forward, backward float64
		var n int64
		e, err := scanEdge(rows, &distance, &forward, &backward, &n)
		if err != nil {
			return nil, fmt.Errorf("failed to find the nearest paths: %w", err)
		}
		// Ordinality counts from 1.
		i := n - 1
		positions[i] = position{edge: e, dist: project(e, points[i]), cost: forward, reverseCost: backward}
		distances[i], found[i] = distance, true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to find the nearest paths: %w", err)
	}
	for i, p := range points {
		if !found[i] {
			return nil, fmt.Errorf("%w: no map data has been imported; run import-osm for the area", ErrNotRoutable)
		}
		if distances[i] <= maxSnapMeters {
			continue
		}
		var covered bool
		if err := db.QueryRow(ctx, `select exists (select 1 from routing_regions
			where ST_Contains(bounds, ST_SetSRID(ST_MakePoint($1, $2), 4326)))`, p.Lng, p.Lat).Scan(&covered); err != nil {
			return nil, fmt.Errorf("failed to check the imported regions: %w", err)
		}
		if !covered {
			return nil, fmt.Errorf("%w: no map data has been imported for the area around %.5f, %.5f; run import-osm for it", ErrNotRoutable, p.Lat, p.Lng)
		}
		return nil, fmt.Errorf("%w: there is no mapped path within %d meters of %.5f, %.5f", ErrNotRoutable, maxSnapMeters, p.Lat, p.Lng)
	}
	return positions, nil
}

// project returns the distance along an edge to the point nearest p.
func project(e *edge, p geo.Point) float64 {
	best, bestDist := math.Inf(1), 0.0
	scale := math.Cos(p.Lat * math.Pi / 180)
	for i := 1; i < len(e.points); i++ {
		a, b := e.points[i-1], e.points[i]
		ax, ay := (a.Lng-p.Lng)*scale, a.Lat-p.Lat
		bx, by := (b.Lng-p.Lng)*scale, b.Lat-p.Lat
		dx, dy := bx-ax, by-ay
		f := 0.0
		if l := dx*dx + dy*dy; l > 0 {
			f = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/l))
		}
		x, y := ax+f*dx, ay+f*dy
		if d := x*x + y*y; d < best {
			best = d
			bestDist = e.dists[i-1] + f*(e.dists[i]-e.dists[i-1])
		}
	}
	return bestDist
}

// savePreferred stores the preferred paths in a temporary table for the
// transaction.
func savePreferred(ctx context.Context, tx pgx.Tx, paths [][]geo.Point) error {
	if _, err := tx.Exec(ctx, `create temporary table preferred_paths (
		geom geometry(LineString, 4326), tolerance double precision
	) on commit drop`); err != nil {
		return fmt.Errorf("failed to create the preferred paths table: %w", err)
	}
	batch := &pgx.Batch{}
	for _, path := range paths {
		if len(path) < 2 {
			continue
		}
		// The tolerance in degrees, measured along a parallel, which is the
		// shorter direction, so it is at least preferMeters in any direction.
		tolerance := preferMeters / (metersPerDegreeLat * math.Max(0.01, math.Cos(path[0].Lat*math.Pi/180)))
		batch.Queue(`insert into preferred_paths values (ST_GeomFromText($1, 4326), $2)`, lineWKT(path), tolerance)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("failed to save the preferred paths: %w", err)
	}
	if _, err := tx.Exec(ctx, `create index on preferred_paths using gist (geom)`); err != nil {
		return fmt.Errorf("failed to index the preferred paths: %w", err)
	}
	return nil
}

// candidate is a way to start or end a path at a vertex, with the cost of
// traveling between the vertex and the snapped point.
type candidate struct {
	vertex int64
	// The distance along the snapped point's edge to the vertex in meters: 0
	// at its source and its length at its target. The vertex alone can't say
	// which end of the edge is meant, as both ends of a loop, an edge from a
	// vertex back to itself, are the same vertex.
	dist float64
	cost float64
}

// search finds the cheapest path between two positions, within margin
// meters of them. It returns nil if there is none in the area.
func search(ctx context.Context, db store.Querier, from, to position, cost, reverseCost string, margin float64) (*Path, error) {
	fromFraction := from.dist / math.Max(from.edge.length(), 1e-9)
	toFraction := to.dist / math.Max(to.edge.length(), 1e-9)
	starts := []candidate{
		{from.edge.source, 0, fromFraction * from.reverseCost},
		{from.edge.target, from.edge.length(), (1 - fromFraction) * from.cost},
	}
	ends := []candidate{
		{to.edge.source, 0, toFraction * to.cost},
		{to.edge.target, to.edge.length(), (1 - toFraction) * to.reverseCost},
	}

	bounds, _ := geo.BoundsOf([]geo.Point{from.edge.points[0], to.edge.points[0]})
	bounds = padBounds(bounds, margin)
	edgesSQL := fmt.Sprintf(`select id, source, target, %s as cost, %s as reverse_cost
		from routing_edges where geom && ST_MakeEnvelope(%f, %f, %f, %f, 4326)`,
		cost, reverseCost, bounds.MinLng, bounds.MinLat, bounds.MaxLng, bounds.MaxLat)
	// The database searches from both start vertices to both end vertices,
	// picks the cheapest of the paths including the costs of reaching the
	// vertices, and returns its edges with their geometry, in a single round
	// trip. It returns which candidates the path starts and ends with, by
	// their position counted from 1, rather than their vertices, which both
	// candidates on a loop share. The vertices are passed to pgRouting once
	// each, as the candidates of a loop would otherwise ask it for the same
	// path twice, and its edges would be returned twice.
	startVertices, startCosts := candidateColumns(starts)
	endVertices, endCosts := candidateColumns(ends)
	rows, err := db.Query(ctx, `with paths as (
		select start_vid, end_vid, path_seq, node, edge, agg_cost
		from pgr_dijkstra($1, array(select distinct unnest($2::bigint[])), array(select distinct unnest($3::bigint[])), directed => true)
	), best as (
		select paths.start_vid, paths.end_vid, s.n as start_n, e.n as end_n, max(paths.agg_cost) + s.cost + e.cost as total
		from paths
		join unnest($2::bigint[], $4::double precision[]) with ordinality as s(vertex, cost, n) on s.vertex = paths.start_vid
		join unnest($3::bigint[], $5::double precision[]) with ordinality as e(vertex, cost, n) on e.vertex = paths.end_vid
		group by paths.start_vid, paths.end_vid, s.n, e.n, s.cost, e.cost
		order by total, s.n, e.n
		limit 1
	)
	select r.id, r.source, r.target, ST_AsGeoJSON(r.geom), r.elevations, r.terrain,
		best.start_n, best.end_n, best.total, paths.node
	from best
	join paths on paths.start_vid = best.start_vid and paths.end_vid = best.end_vid
	join routing_edges r on r.id = paths.edge
	order by paths.path_seq`,
		edgesSQL, startVertices, endVertices, startCosts, endCosts)
	if err != nil {
		return nil, fmt.Errorf("failed to search for a path: %w", err)
	}
	// The rows hold a connection of the pool until they're closed.
	defer rows.Close()
	type step struct {
		node int64
		edge *edge
	}
	var steps []step
	best := math.Inf(1)
	// The positions of the best path's start and end candidates, counted
	// from 1 as the query returns them.
	var startN, endN int64
	for rows.Next() {
		var s step
		var err error
		s.edge, err = scanEdge(rows, &startN, &endN, &best, &s.node)
		if err != nil {
			return nil, fmt.Errorf("failed to search for a path: %w", err)
		}
		steps = append(steps, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to search for a path: %w", err)
	}

	var bestStart, bestEnd candidate
	if len(steps) > 0 {
		bestStart, bestEnd = starts[startN-1], ends[endN-1]
	}
	// pgRouting returns no path from a vertex to itself, so paths through a
	// vertex both edges share are costed here. This includes a path around
	// the end of a loop both points are on, leaving it through one end and
	// coming back through the other.
	for _, s := range starts {
		for _, e := range ends {
			if s.vertex == e.vertex && s.cost+e.cost < best {
				best, bestStart, bestEnd, steps = s.cost+e.cost, s, e, nil
			}
		}
	}
	direct := false
	if from.edge.id == to.edge.id {
		// Both points are on the same edge, so the path may stay on it.
		f := math.Abs(toFraction - fromFraction)
		c := f * from.cost
		if toFraction < fromFraction {
			c = f * from.reverseCost
		}
		if c <= best {
			best, direct = c, true
		}
	}
	if math.IsInf(best, 1) {
		return nil, nil
	}

	b := &pathBuilder{}
	if direct {
		b.add(from.edge.piece(from.dist, to.dist), from.edge.terrain)
		return b.path(), nil
	}
	// The path from the snapped point to the first vertex.
	b.add(from.edge.piece(from.dist, bestStart.dist), from.edge.terrain)
	for _, s := range steps {
		if s.node == s.edge.source {
			b.add(s.edge.piece(0, s.edge.length()), s.edge.terrain)
		} else {
			b.add(s.edge.piece(s.edge.length(), 0), s.edge.terrain)
		}
	}
	// The path from the last vertex to the snapped point.
	b.add(to.edge.piece(bestEnd.dist, to.dist), to.edge.terrain)
	return b.path(), nil
}

// candidateColumns returns the vertices and costs of candidates, as columns
// for a query.
func candidateColumns(candidates []candidate) (vertices []int64, costs []float64) {
	for _, c := range candidates {
		vertices = append(vertices, c.vertex)
		costs = append(costs, c.cost)
	}
	return vertices, costs
}

// piece returns the points of the edge between two distances along it, in
// the direction from the first to the second, with interpolated ends.
func (e *edge) piece(from, to float64) []geo.Point {
	reverse := from > to
	lo, hi := math.Min(from, to), math.Max(from, to)
	points := []geo.Point{e.at(lo)}
	for i, d := range e.dists {
		if d > lo && d < hi {
			points = append(points, e.points[i])
		}
	}
	points = append(points, e.at(hi))
	if reverse {
		for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
			points[i], points[j] = points[j], points[i]
		}
	}
	return points
}

// at returns the point at a distance along the edge.
func (e *edge) at(dist float64) geo.Point {
	for i := 1; i < len(e.points); i++ {
		if dist <= e.dists[i] {
			span := e.dists[i] - e.dists[i-1]
			if span <= 0 {
				return e.points[i]
			}
			return geo.Interpolate(e.points[i-1], e.points[i], (dist-e.dists[i-1])/span)
		}
	}
	return e.points[len(e.points)-1]
}

// pathBuilder joins pieces of edges into a path.
type pathBuilder struct {
	points   []geo.Point
	terrains []geo.Terrain
}

func (b *pathBuilder) add(points []geo.Point, terrain geo.Terrain) {
	for _, p := range points {
		if n := len(b.points); n > 0 && b.points[n-1].Lat == p.Lat && b.points[n-1].Lng == p.Lng {
			// The segment after a joining point belongs to the new piece.
			b.terrains[n-1] = terrain
			continue
		}
		b.points = append(b.points, p)
		b.terrains = append(b.terrains, terrain)
	}
}

func (b *pathBuilder) path() *Path {
	points, terrains := b.points, b.terrains
	if len(points) == 1 {
		// The points snapped to the same place.
		points = append(points, points[0])
		terrains = append(terrains, terrains[0])
	}
	terrains[len(terrains)-1] = geo.TerrainUnknown
	return &Path{Points: points, Terrains: terrains}
}

// padBounds grows bounds by about padding meters on each side.
func padBounds(b geo.Bounds, padding float64) geo.Bounds {
	midLat := (b.MinLat + b.MaxLat) / 2
	padLat := padding / metersPerDegreeLat
	padLng := padding / (metersPerDegreeLat * math.Max(0.01, math.Cos(midLat*math.Pi/180)))
	return geo.Bounds{
		MinLat: math.Max(-90, b.MinLat-padLat),
		MinLng: math.Max(-180, b.MinLng-padLng),
		MaxLat: math.Min(90, b.MaxLat+padLat),
		MaxLng: math.Min(180, b.MaxLng+padLng),
	}
}
