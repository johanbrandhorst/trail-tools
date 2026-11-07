package routing

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/bs-tools/trailrunn.ing/internal/geo"
)

const (
	// familiarCellMeters is the size of the grid cells tracks are marked on
	// to match them to edges.
	familiarCellMeters = 12.5
	// familiarReachCells is how many cells around a track's cell count as
	// near it, so edges within about preferMeters of a track are matched.
	familiarReachCells = 2
	// familiarSampleMeters is how often a track's segments are sampled when
	// marking its cells: under half a cell, so no cell along it is missed.
	familiarSampleMeters = 6
	// maxFamiliarCells bounds the grid, about 30 km across at
	// familiarCellMeters, as the cells' marks take 4 bytes each.
	maxFamiliarCells = 6_000_000
)

// FamiliarWay is a way the user has run along, with how often.
type FamiliarWay struct {
	Trail
	// The number of distinct tracks that ran along the way's most run edge.
	ActivityCount int
	// The length of the way's edges that at least one track ran along, in
	// meters.
	RunLengthMeters float64
	// The length of the way in the bounds, in meters.
	LengthMeters float64
}

// familiarEdge is an edge of a way in the bounds.
type familiarEdge struct {
	way     int
	length  float64
	points  []geo.Point
	samples [3]int32 // The cells of its middle and quarter points.
	count   int
	// The last track that ran it, plus one, so a track counts once.
	lastTrack int
}

// FamiliarWays returns the ways in the bounds that the tracks run along,
// the most run first. A track runs an edge if it passes within about
// preferMeters of the edge's middle and quarter points, as for preferred
// paths when routing; the distance is measured on a grid of
// familiarCellMeters cells, so it's 20 to 35 meters. Ways without an
// OpenStreetMap ID, which the import joined, are left out. It returns
// ErrNoMapData if the bounds are outside every imported region.
//
// The tracks are matched here rather than in the database, which measured
// every edge near a track against every other track near it: with hundreds
// of tracks along the same trails, that took longer than the statement
// timeout.
func (f *Finder) FamiliarWays(ctx context.Context, bounds geo.Bounds, tracks [][]geo.Point, limit int) ([]FamiliarWay, error) {
	g := newFamiliarGrid(bounds)
	if g.cols*g.rows > maxFamiliarCells {
		return nil, fmt.Errorf("the area is too large to find familiar trails in")
	}
	ways, edges, err := f.familiarEdges(ctx, bounds, g)
	if err != nil {
		return nil, err
	}
	if len(ways) == 0 {
		return nil, f.checkBoundsImported(ctx, bounds)
	}
	// The edges by the cell of their middle.
	byCell := map[int32][]int{}
	for i, e := range edges {
		if e.samples[0] >= 0 {
			byCell[e.samples[0]] = append(byCell[e.samples[0]], i)
		}
	}
	// marks holds the track, plus one, that last marked each cell, so the
	// marks needn't be cleared between tracks.
	marks := make([]int32, g.cols*g.rows)
	var cells []int32
	for t, track := range tracks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		stamp := int32(t + 1)
		cells = cells[:0]
		mark := func(p geo.Point) {
			col, row := g.cellOf(p)
			for r := row - familiarReachCells; r <= row+familiarReachCells; r++ {
				for c := col - familiarReachCells; c <= col+familiarReachCells; c++ {
					if c < 0 || r < 0 || c >= g.cols || r >= g.rows {
						continue
					}
					if i := int32(r*g.cols + c); marks[i] != stamp {
						marks[i] = stamp
						cells = append(cells, i)
					}
				}
			}
		}
		for i, p := range track {
			if i == 0 {
				mark(p)
				continue
			}
			prev := track[i-1]
			steps := int(math.Ceil(geo.Distance(prev, p) / familiarSampleMeters))
			for s := 1; s <= steps; s++ {
				mark(geo.Interpolate(prev, p, float64(s)/float64(steps)))
			}
		}
		for _, cell := range cells {
			for _, i := range byCell[cell] {
				e := &edges[i]
				if e.lastTrack == t+1 || e.samples[1] < 0 || e.samples[2] < 0 ||
					marks[e.samples[1]] != stamp || marks[e.samples[2]] != stamp {
					continue
				}
				e.lastTrack = t + 1
				e.count++
			}
		}
	}
	for _, e := range edges {
		w := &ways[e.way]
		w.LengthMeters += e.length
		if e.count == 0 {
			continue
		}
		w.ActivityCount = max(w.ActivityCount, e.count)
		w.RunLengthMeters += e.length
		w.Points = append(w.Points, e.points...)
	}
	var run []FamiliarWay
	for _, w := range ways {
		if w.ActivityCount > 0 {
			run = append(run, w)
		}
	}
	sort.SliceStable(run, func(i, j int) bool {
		if run[i].ActivityCount != run[j].ActivityCount {
			return run[i].ActivityCount > run[j].ActivityCount
		}
		return run[i].RunLengthMeters > run[j].RunLengthMeters
	})
	return run[:min(limit, len(run))], nil
}

// familiarEdges returns the ways with edges in the bounds, and their edges
// in order, with the cells of their middle and quarter points. Overlapping
// regions can both have a way, so each way's edges are taken from one.
func (f *Finder) familiarEdges(ctx context.Context, bounds geo.Bounds, g familiarGrid) ([]FamiliarWay, []familiarEdge, error) {
	rows, err := f.db.Query(ctx, `select way_id, region_id, ST_AsEncodedPolyline(geom), name, highway, surface, sac_scale, terrain, length_meters
	from routing_edges
	where way_id is not null and geom && ST_MakeEnvelope($1, $2, $3, $4, 4326)
	order by way_id, region_id, id`, bounds.MinLng, bounds.MinLat, bounds.MaxLng, bounds.MaxLat)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find the trails: %w", err)
	}
	defer rows.Close()
	var ways []FamiliarWay
	var edges []familiarEdge
	var region int64
	for rows.Next() {
		var w FamiliarWay
		var regionID int64
		var polyline, terrain string
		var length float64
		if err := rows.Scan(&w.WayID, &regionID, &polyline, &w.Name, &w.Highway, &w.Surface, &w.SacScale, &terrain, &length); err != nil {
			return nil, nil, fmt.Errorf("failed to find the trails: %w", err)
		}
		if n := len(ways); n == 0 || ways[n-1].WayID != w.WayID {
			w.Terrain = geo.Terrain(terrain)
			ways = append(ways, w)
			region = regionID
		} else if regionID != region {
			continue
		}
		points, err := geo.DecodePolyline(polyline)
		if err != nil || len(points) < 2 {
			continue
		}
		e := familiarEdge{way: len(ways) - 1, length: length, points: points}
		for i, fraction := range []float64{0.5, 0.25, 0.75} {
			e.samples[i] = g.index(pointAlong(points, fraction))
		}
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("failed to find the trails: %w", err)
	}
	return ways, edges, nil
}

// pointAlong returns the point a fraction of the way along a line.
func pointAlong(points []geo.Point, fraction float64) geo.Point {
	dists := geo.CumulativeDistances(points)
	target := fraction * dists[len(dists)-1]
	for i := 1; i < len(points); i++ {
		if dists[i] >= target {
			span := dists[i] - dists[i-1]
			if span == 0 {
				return points[i]
			}
			return geo.Interpolate(points[i-1], points[i], (target-dists[i-1])/span)
		}
	}
	return points[len(points)-1]
}

// familiarGrid is a grid of familiarCellMeters cells over bounds.
type familiarGrid struct {
	bounds           geo.Bounds
	cellLat, cellLng float64
	cols, rows       int
}

func newFamiliarGrid(bounds geo.Bounds) familiarGrid {
	midLat := (bounds.MinLat + bounds.MaxLat) / 2
	cellLat := familiarCellMeters / metersPerDegreeLat
	cellLng := familiarCellMeters / (metersPerDegreeLat * math.Max(0.01, math.Cos(midLat*math.Pi/180)))
	return familiarGrid{
		bounds:  bounds,
		cellLat: cellLat,
		cellLng: cellLng,
		cols:    int(math.Ceil((bounds.MaxLng-bounds.MinLng)/cellLng)) + 1,
		rows:    int(math.Ceil((bounds.MaxLat-bounds.MinLat)/cellLat)) + 1,
	}
}

// cellOf returns the column and row of the cell of a point, which may be
// outside the grid.
func (g familiarGrid) cellOf(p geo.Point) (col, row int) {
	return int(math.Floor((p.Lng - g.bounds.MinLng) / g.cellLng)), int(math.Floor((p.Lat - g.bounds.MinLat) / g.cellLat))
}

// index returns the index of the cell of a point, or -1 outside the grid.
func (g familiarGrid) index(p geo.Point) int32 {
	col, row := g.cellOf(p)
	if col < 0 || row < 0 || col >= g.cols || row >= g.rows {
		return -1
	}
	return int32(row*g.cols + col)
}

// checkBoundsImported returns ErrNoMapData if the middle of the bounds is
// outside every imported region, and nil otherwise.
func (f *Finder) checkBoundsImported(ctx context.Context, bounds geo.Bounds) error {
	center := geo.Point{Lat: (bounds.MinLat + bounds.MaxLat) / 2, Lng: (bounds.MinLng + bounds.MaxLng) / 2}
	return f.checkImported(ctx, center, 0)
}
