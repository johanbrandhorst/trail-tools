package routing

import (
	"context"
	"fmt"
	"math"

	"github.com/bs-tools/trailrunn.ing/internal/geo"
)

const (
	// forestSampleMeters is how far apart the points of a path are that are
	// looked up on the map for ForestDistances: a runner covers it in well
	// under a minute, and finer sampling would cost more lookups without
	// changing a warning.
	forestSampleMeters = 100
	// maxForestSamples bounds the lookups of one ForestDistances call, so a
	// long ultra route, or a month of workouts, still takes one quick query.
	// Paths sample more coarsely when they would need more.
	maxForestSamples = 2000
	// forestSnapMeters is how far a point can be from an edge to take its
	// forest, about the error of a GPS track or a hand drawn route.
	forestSnapMeters = 30
)

// ForestDistances returns how many meters of each path are in forest, from
// the edges of the imported map data nearest to points along it. Points
// further than forestSnapMeters from an edge, like those of straight legs
// across open ground or outside the imported regions, count as not in
// forest, as do edges of regions imported before forests were.
func (f *Finder) ForestDistances(ctx context.Context, paths [][]geo.Point) ([]float64, error) {
	total := 0.0
	for _, p := range paths {
		total += pathLength(p)
	}
	spacing := math.Max(forestSampleMeters, total/maxForestSamples)
	var indexes []int32
	var lats, lngs []float64
	for i, p := range paths {
		for _, s := range samplePath(p, spacing) {
			indexes = append(indexes, int32(i))
			lats, lngs = append(lats, s.Lat), append(lngs, s.Lng)
		}
	}
	distances := make([]float64, len(paths))
	if len(indexes) == 0 {
		return distances, nil
	}
	dy := forestSnapMeters / metersPerDegreeLat
	// The nearest edge within the box around each point, by the index's
	// distance in degrees, which is near enough at this scale.
	rows, err := f.db.Query(ctx, `select s.path, count(*)
	from unnest($1::int[], $2::float8[], $3::float8[]) as s (path, lat, lng),
		ST_SetSRID(ST_MakePoint(s.lng, s.lat), 4326) as point (geom)
	where (select e.forest from routing_edges e
		where e.geom && ST_Expand(point.geom, $4 / greatest(0.01, cos(radians(s.lat))), $4)
		order by e.geom <-> point.geom limit 1)
	group by s.path`, indexes, lats, lngs, dy)
	if err != nil {
		return nil, fmt.Errorf("failed to look up forests: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var path int32
		var count int64
		if err := rows.Scan(&path, &count); err != nil {
			return nil, fmt.Errorf("failed to look up forests: %w", err)
		}
		distances[path] = math.Min(float64(count)*spacing, pathLength(paths[path]))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to look up forests: %w", err)
	}
	return distances, nil
}

// samplePath returns points spacing meters apart along a path, each
// standing for the spacing meters around it, from half the spacing in.
func samplePath(points []geo.Point, spacing float64) []geo.Point {
	var samples []geo.Point
	next := spacing / 2
	along := 0.0
	for i := 1; i < len(points); i++ {
		d := geo.Distance(points[i-1], points[i])
		for d > 0 && next <= along+d {
			samples = append(samples, geo.Interpolate(points[i-1], points[i], (next-along)/d))
			next += spacing
		}
		along += d
	}
	return samples
}

// ForestShare returns the share of the length of the edges within radius
// meters of center that is in forest, from 0 to 1: how likely a run from
// there is to be in forest when its route isn't known. It is 0 if there are
// no edges there.
func (f *Finder) ForestShare(ctx context.Context, center geo.Point, radius float64) (float64, error) {
	var forest, all float64
	if err := f.db.QueryRow(ctx, `select coalesce(sum(length_meters) filter (where forest), 0), coalesce(sum(length_meters), 0)
	from routing_edges
	where `+nearSQL("geom"), searchArea(center, radius)...).Scan(&forest, &all); err != nil {
		return 0, fmt.Errorf("failed to look up forests: %w", err)
	}
	if all == 0 {
		return 0, nil
	}
	return forest / all, nil
}
