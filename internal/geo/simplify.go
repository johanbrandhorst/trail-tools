package geo

import "math"

// Clip returns the parts of a track inside bounds, each with at least two
// points. Lines crossing the edges of the bounds are cut at the edges, so
// parts of sparse tracks, such as simplified ones, aren't lost between points
// outside the bounds.
func Clip(points []Point, b Bounds) [][]Point {
	var parts [][]Point
	var part []Point
	for i := 1; i < len(points); i++ {
		from, to := points[i-1], points[i]
		t0, t1, ok := clipSegment(from, to, b)
		if !ok {
			continue
		}
		// A part ends where a line leaves the bounds, so a line continuing
		// one starts where the last one ended.
		if part == nil {
			part = []Point{Interpolate(from, to, t0)}
		}
		if t1 < 1 {
			parts = append(parts, append(part, Interpolate(from, to, t1)))
			part = nil
			continue
		}
		part = append(part, to)
	}
	if part != nil {
		parts = append(parts, part)
	}
	return parts
}

// clipSegment returns the fractions of the way from a to b where the line
// between them enters and leaves bounds, and whether it's inside the bounds
// at all (the Liang-Barsky algorithm).
func clipSegment(a, b Point, bounds Bounds) (t0, t1 float64, ok bool) {
	t0, t1 = 0, 1
	dLng, dLat := b.Lng-a.Lng, b.Lat-a.Lat
	// For each edge, the change towards it and the distance to it.
	for _, edge := range [4][2]float64{
		{-dLng, a.Lng - bounds.MinLng},
		{dLng, bounds.MaxLng - a.Lng},
		{-dLat, a.Lat - bounds.MinLat},
		{dLat, bounds.MaxLat - a.Lat},
	} {
		change, distance := edge[0], edge[1]
		switch {
		case change == 0:
			// Parallel to the edge, and outside it.
			if distance < 0 {
				return 0, 0, false
			}
		case change < 0:
			// Entering the bounds across the edge.
			t0 = max(t0, distance/change)
		default:
			// Leaving the bounds across the edge.
			t1 = min(t1, distance/change)
		}
		if t0 > t1 {
			return 0, 0, false
		}
	}
	return t0, t1, true
}

// simplifyChunkPoints is the most points Simplify simplifies at once. The
// Douglas-Peucker algorithm takes time quadratic in the points for some
// tracks, such as spirals, and recurses as deep as there are points, so
// longer tracks are simplified in chunks, which keeps a few more points at
// their ends.
const simplifyChunkPoints = 1000

// Simplify removes points of a track that are within tolerance meters of the
// line through their neighbors (the Douglas-Peucker algorithm).
func Simplify(points []Point, tolerance float64) []Point {
	return simplify(points, tolerance, SegmentDistance)
}

// SimplifyTrack is like Simplify, but keeps points whose elevation is more
// than eleTolerance meters from the line through their neighbors, so that
// hills along straight lines aren't flattened. Unknown elevations are
// ignored.
func SimplifyTrack(points []Point, tolerance, eleTolerance float64) []Point {
	return simplify(points, tolerance, func(p, a, b Point) float64 {
		d := SegmentDistance(p, a, b)
		if math.IsNaN(p.Ele) || math.IsNaN(a.Ele) || math.IsNaN(b.Ele) {
			return d
		}
		f := segmentFraction(p, a, b)
		// Scaled, so that it exceeds tolerance where the elevation exceeds
		// eleTolerance.
		ele := math.Abs(p.Ele-(a.Ele+(b.Ele-a.Ele)*f)) * tolerance / eleTolerance
		return math.Max(d, ele)
	})
}

// simplify removes points of a track whose deviation from the line through
// their neighbors is at most tolerance, in chunks of simplifyChunkPoints.
func simplify(points []Point, tolerance float64, deviation func(p, a, b Point) float64) []Point {
	if len(points) <= simplifyChunkPoints {
		return douglasPeucker(points, tolerance, deviation)
	}
	var result []Point
	// Consecutive chunks share their end points.
	for start := 0; start < len(points)-1; start += simplifyChunkPoints - 1 {
		chunk := douglasPeucker(points[start:min(start+simplifyChunkPoints, len(points))], tolerance, deviation)
		if len(result) > 0 {
			chunk = chunk[1:]
		}
		result = append(result, chunk...)
	}
	return result
}

// douglasPeucker simplifies a track with the Douglas-Peucker algorithm.
func douglasPeucker(points []Point, tolerance float64, deviation func(p, a, b Point) float64) []Point {
	if len(points) < 3 {
		return points
	}
	worst, worstIndex := 0.0, 0
	last := len(points) - 1
	for i := 1; i < last; i++ {
		if d := deviation(points[i], points[0], points[last]); d > worst {
			worst, worstIndex = d, i
		}
	}
	if worst <= tolerance {
		return []Point{points[0], points[last]}
	}
	first := douglasPeucker(points[:worstIndex+1], tolerance, deviation)
	// Capped, so appending to it doesn't overwrite the points.
	first = first[: len(first)-1 : len(first)-1]
	return append(first, douglasPeucker(points[worstIndex:], tolerance, deviation)...)
}

// SegmentDistance returns the distance in meters from p to the segment from a
// to b, in a flat projection around p, which is accurate over the short
// distances it is used for.
func SegmentDistance(p, a, b Point) float64 {
	ax, ay, dx, dy := projectSegment(p, a, b)
	f := segmentFraction(p, a, b)
	return math.Hypot(ax+f*dx, ay+f*dy)
}

// segmentFraction returns the fraction of the way from a to b of the point
// on the segment nearest to p.
func segmentFraction(p, a, b Point) float64 {
	ax, ay, dx, dy := projectSegment(p, a, b)
	if length := dx*dx + dy*dy; length > 0 {
		return math.Max(0, math.Min(1, -(ax*dx+ay*dy)/length))
	}
	return 0
}

// projectSegment returns a, and the vector from a to b, in meters on a flat
// projection around p.
func projectSegment(p, a, b Point) (ax, ay, dx, dy float64) {
	scale := math.Cos(p.Lat * math.Pi / 180)
	ax, ay = (a.Lng-p.Lng)*scale*metersPerDegreeLat, (a.Lat-p.Lat)*metersPerDegreeLat
	bx, by := (b.Lng-p.Lng)*scale*metersPerDegreeLat, (b.Lat-p.Lat)*metersPerDegreeLat
	return ax, ay, bx - ax, by - ay
}
