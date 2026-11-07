package fakestrava

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routing"
)

// Router routes paths along mapped trails and roads, such as the routing
// graph of imported OpenStreetMap data.
type Router interface {
	// Regions returns the areas paths can be routed in.
	Regions(ctx context.Context) ([]geo.Bounds, error)
	// Route returns the points of a path between two points, with
	// elevations. With trails, the path prefers trails; otherwise it's the
	// shortest.
	Route(ctx context.Context, from, to geo.Point, trails bool) ([]geo.Point, error)
}

// NewRouter returns a Router that routes on the routing graph.
func NewRouter(r *routing.Router) Router {
	return graphRouter{r}
}

type graphRouter struct{ r *routing.Router }

func (g graphRouter) Regions(ctx context.Context) ([]geo.Bounds, error) {
	return g.r.Regions(ctx)
}

func (g graphRouter) Route(ctx context.Context, from, to geo.Point, trails bool) ([]geo.Point, error) {
	profile := routing.ProfileShortest
	if trails {
		profile = routing.ProfileTrail
	}
	path, err := g.r.Route(ctx, routing.Request{From: from, To: to, Profile: profile})
	if err != nil {
		return nil, err
	}
	return path.Points, nil
}

const (
	// pointSpacing is about the distance between the points of a track, in
	// meters.
	pointSpacing = 10
	// smoothingMeters is the distance over which the elevations of routed
	// tracks are averaged, to remove the noise of elevation models.
	smoothingMeters = 100
	// trackCacheVersion changes when routed tracks change, so that cached
	// tracks aren't used anymore.
	trackCacheVersion = 3
	// minRoutedMeters is the shortest routed track.
	minRoutedMeters = 2000
)

// tracer makes the tracks of outings: along mapped paths if the trailhead
// is in a region the router covers, or else made up.
type tracer struct {
	// Nil without routing.
	router  Router
	regions []geo.Bounds
	// Where routed tracks are cached, as routing them takes a while. Empty
	// to not cache them.
	cacheDir string
}

// track returns the track of an outing of about the distance in meters,
// with elevations and points about pointSpacing apart, and whether it follows
// mapped paths.
func (t *tracer) track(ctx context.Context, o outing, meters float64, r *rand.Rand) ([]geo.Point, bool) {
	if t != nil && t.covers(o.place.trailhead) {
		if points := t.routed(ctx, o, meters); points != nil {
			return points, true
		}
	}
	return madeUp(o, meters, r), false
}

// covers returns whether the router covers a point.
func (t *tracer) covers(p geo.Point) bool {
	if t.router == nil {
		return false
	}
	for _, b := range t.regions {
		if b.Contains(p) {
			return true
		}
	}
	return false
}

// routed returns a loop from the trailhead of an outing along mapped paths,
// from the cache if it was routed before, or nil if none was found.
func (t *tracer) routed(ctx context.Context, o outing, meters float64) []geo.Point {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d|%s|%s|%.0f|%v", trackCacheVersion, o.place.name, o.sport.sportType, meters, o.sport.trails)
	cacheFile := ""
	if t.cacheDir != "" {
		cacheFile = filepath.Join(t.cacheDir, fmt.Sprintf("%d-%x.json", o.id, h.Sum64()))
		if points := readTrack(cacheFile); points != nil {
			return points
		}
	}
	// The waypoints depend on the outing only, so the track is the same
	// every time.
	r := rand.New(rand.NewPCG(uint64(o.id), 7))
	path, err := routeTrack(ctx, t.router, o.place.trailhead, meters, o.sport.trails, r)
	if err != nil {
		return nil
	}
	points := smoothElevations(densify(path))
	if cacheFile != "" {
		writeTrack(cacheFile, points)
	}
	return points
}

// routeTrack routes a track of about the distance from start and back: a
// loop if one comes close to the distance, or else out and back along a
// path, as on mountain trails. Where the paths don't allow the distance, the
// track is shorter or longer, as long as it's at least minRoutedMeters.
func routeTrack(ctx context.Context, router Router, start geo.Point, meters float64, trails bool, r *rand.Rand) ([]geo.Point, error) {
	loop, loopErr := routeLoop(ctx, router, start, meters, trails, r)
	if loopErr == nil && lengthOff(loop, meters) < math.Log(1.5) {
		return loop, nil
	}
	best, err := routeOutAndBack(ctx, router, start, meters, trails, r)
	switch {
	case err != nil && loopErr != nil:
		return nil, err
	case err != nil || loopErr == nil && lengthOff(loop, meters) < lengthOff(best, meters):
		best = loop
	}
	if length := geo.CumulativeDistances(best)[len(best)-1]; length < minRoutedMeters {
		return nil, fmt.Errorf("the paths only allow a track of %.0f m", length)
	}
	return best, nil
}

// lengthOff returns how far the length of a path is from the distance in
// meters, as the absolute log of their ratio.
func lengthOff(points []geo.Point, meters float64) float64 {
	return math.Abs(math.Log(geo.CumulativeDistances(points)[len(points)-1] / meters))
}

// routeOutAndBack routes out from start along a path, in the direction
// where it gets closest to half the distance, and back the same way.
func routeOutAndBack(ctx context.Context, router Router, start geo.Point, meters float64, trails bool, r *rand.Rand) ([]geo.Point, error) {
	half := meters / 2
	heading := r.Float64() * 2 * math.Pi
	var best []geo.Point
	bestOff := math.Inf(1)
	var lastErr error
	// Targets far from any path can't be routed to, so try many directions,
	// and closer ones too.
	var targets []geo.Point
	for _, reach := range []float64{1, 0.6} {
		for k := range 12 {
			angle := heading + 2*math.Pi*float64(k)/12
			targets = append(targets, offset(start, reach*half*math.Cos(angle), reach*half*math.Sin(angle)))
		}
	}
	for _, target := range targets {
		out, err := router.Route(ctx, start, target, trails)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}
		// Turn around halfway.
		out = cutAt(out, half)
		if off := lengthOff(out, half); off < bestOff {
			best, bestOff = out, off
		}
		if bestOff < math.Log(1.1) {
			break
		}
	}
	if best == nil {
		return nil, lastErr
	}
	back := slices.Clone(best[:len(best)-1])
	slices.Reverse(back)
	return append(best, back...), nil
}

// cutAt returns the start of a path up to a distance in meters.
func cutAt(points []geo.Point, meters float64) []geo.Point {
	dists := geo.CumulativeDistances(points)
	for i := 1; i < len(points); i++ {
		if dists[i] >= meters {
			f := (meters - dists[i-1]) / (dists[i] - dists[i-1])
			return append(slices.Clone(points[:i]), geo.Interpolate(points[i-1], points[i], f))
		}
	}
	return points
}

// routeLoop routes a loop of about the distance from start through a few
// waypoints around it. The size of the loop is adjusted to get closer to
// the distance, as mapped paths are rarely straight.
func routeLoop(ctx context.Context, router Router, start geo.Point, meters float64, trails bool, r *rand.Rand) ([]geo.Point, error) {
	n := 3 + r.IntN(3)
	heading := r.Float64() * 2 * math.Pi
	angles := make([]float64, n)
	for i := range angles {
		angles[i] = 2*math.Pi*float64(i+1)/float64(n+1) + (r.Float64()-0.5)*0.6
	}
	radius := meters / (2 * math.Pi) / 1.3
	var best []geo.Point
	bestOff := math.Inf(1)
	var lastErr error
	for range 3 {
		// The loop is on a circle through the start.
		center := offset(start, radius*math.Cos(heading), radius*math.Sin(heading))
		waypoints := make([]geo.Point, n)
		for i, a := range angles {
			angle := heading + math.Pi + a
			waypoints[i] = offset(center, radius*math.Cos(angle), radius*math.Sin(angle))
		}
		points, err := routeThrough(ctx, router, start, waypoints, trails)
		if err != nil {
			lastErr = err
			radius *= 0.6
			continue
		}
		length := geo.CumulativeDistances(points)[len(points)-1]
		off := math.Abs(math.Log(length / meters))
		if off < bestOff {
			best, bestOff = points, off
		}
		if off < math.Log(1.25) {
			break
		}
		radius *= math.Min(math.Max(meters/length, 0.4), 2.5)
	}
	if best == nil {
		return nil, lastErr
	}
	return best, nil
}

// routeThrough routes from start through the waypoints and back. Waypoints
// that can't be reached are skipped.
func routeThrough(ctx context.Context, router Router, start geo.Point, waypoints []geo.Point, trails bool) ([]geo.Point, error) {
	var points []geo.Point
	from := start
	add := func(leg []geo.Point) {
		if len(points) > 0 && len(leg) > 0 {
			leg = leg[1:]
		}
		points = append(points, leg...)
	}
	for _, w := range waypoints {
		leg, err := router.Route(ctx, from, w, trails)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		add(leg)
		from = w
	}
	if from == start {
		return nil, errors.New("none of the waypoints could be reached")
	}
	leg, err := router.Route(ctx, from, start, trails)
	if err != nil {
		return nil, err
	}
	add(leg)
	if len(points) < 2 {
		return nil, errors.New("the loop is empty")
	}
	return points, nil
}

// densify returns the points with points added between them, so that
// they're at most about pointSpacing apart.
func densify(points []geo.Point) []geo.Point {
	result := []geo.Point{points[0]}
	for i := 1; i < len(points); i++ {
		dense := geo.Densify(points[i-1], points[i], pointSpacing)
		result = append(result, dense[1:]...)
	}
	return result
}

// smoothElevations returns the points with their elevations averaged over
// about smoothingMeters, and unknown elevations filled in.
func smoothElevations(points []geo.Point) []geo.Point {
	eles := make([]float64, len(points))
	last := 0.0
	for i, p := range points {
		if !math.IsNaN(p.Ele) {
			last = p.Ele
		}
		eles[i] = last
	}
	radius := smoothingMeters / pointSpacing / 2
	result := make([]geo.Point, len(points))
	for i, p := range points {
		lo, hi := max(i-radius, 0), min(i+radius+1, len(points))
		sum := 0.0
		for _, e := range eles[lo:hi] {
			sum += e
		}
		p.Ele = sum / float64(hi-lo)
		result[i] = p
	}
	return result
}

func readTrack(file string) []geo.Point {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var stored [][3]float64
	if err := json.Unmarshal(data, &stored); err != nil || len(stored) < 2 {
		return nil
	}
	points := make([]geo.Point, len(stored))
	for i, p := range stored {
		points[i] = geo.Point{Lat: p[0], Lng: p[1], Ele: p[2]}
	}
	return points
}

// writeTrack caches a track. Failing to cache it only means it's routed
// again.
func writeTrack(file string, points []geo.Point) {
	stored := make([][3]float64, len(points))
	for i, p := range points {
		stored[i] = [3]float64{round(p.Lat, 6), round(p.Lng, 6), math.Round(p.Ele*10) / 10}
	}
	data, err := json.Marshal(stored)
	if err != nil || os.MkdirAll(filepath.Dir(file), 0o755) != nil {
		return
	}
	tmp := file + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, file)
	}
}

// madeUp returns a made up loop of about the distance from the trailhead
// of an outing, rising to one or two high points and coming back down.
func madeUp(o outing, meters float64, r *rand.Rand) []geo.Point {
	s := o.sport
	climb := (s.minClimbPerKm + r.Float64()*(s.maxClimbPerKm-s.minClimbPerKm)) * meters / 1000
	points := loop(o.place.trailhead, meters, r)
	dists := geo.CumulativeDistances(points)
	total := dists[len(dists)-1]
	profile := elevationProfile(climb, s.sportType, r)
	base := 30.0
	for i := range points {
		points[i].Ele = base + profile(dists[i]/total) + 4*math.Sin(dists[i]/250)
	}
	return points
}

// loop returns the points of a loop of about the length in meters starting
// and ending at start, about pointSpacing apart.
func loop(start geo.Point, meters float64, r *rand.Rand) []geo.Point {
	phase1, phase2 := r.Float64()*2*math.Pi, r.Float64()*2*math.Pi
	heading := r.Float64() * 2 * math.Pi
	wobble1, wobble2 := 0.15+0.2*r.Float64(), 0.05+0.1*r.Float64()
	shape := func(radius float64, n int) []geo.Point {
		points := make([]geo.Point, n+1)
		for i := range n + 1 {
			theta := 2 * math.Pi * float64(i) / float64(n)
			rad := radius * (1 + wobble1*math.Sin(3*theta+phase1) + wobble2*math.Sin(7*theta+phase2))
			// The loop passes through start at theta 0.
			rad0 := radius * (1 + wobble1*math.Sin(phase1) + wobble2*math.Sin(phase2))
			x := rad*math.Cos(theta) - rad0
			y := rad * math.Sin(theta)
			east := x*math.Cos(heading) - y*math.Sin(heading)
			north := x*math.Sin(heading) + y*math.Cos(heading)
			points[i] = offset(start, east, north)
		}
		points[n] = start
		return points
	}
	n := max(int(meters/pointSpacing), 50)
	radius := meters / (2 * math.Pi)
	dists := geo.CumulativeDistances(shape(radius, n))
	return shape(radius*meters/dists[len(dists)-1], n)
}

// elevationProfile returns the elevation above the start at a fraction of
// the way, with high points adding up to about the climbing in meters.
func elevationProfile(climb float64, sportType string, r *rand.Rand) func(float64) float64 {
	if sportType == "Run" || sportType == "Ride" {
		amplitude := climb / 4
		phase := r.Float64() * 2 * math.Pi
		return func(f float64) float64 {
			return amplitude * (math.Sin(2*math.Pi*f*2+phase) - math.Sin(phase))
		}
	}
	type peak struct{ at, width, height float64 }
	peaks := []peak{{at: 0.35 + 0.2*r.Float64(), width: 0.18 + 0.1*r.Float64(), height: 1}}
	if r.Float64() < 0.6 {
		peaks = append(peaks, peak{at: 0.15 + 0.7*r.Float64(), width: 0.08 + 0.06*r.Float64(), height: 0.3 + 0.4*r.Float64()})
	}
	raw := func(f float64) float64 {
		sum := 0.0
		for _, p := range peaks {
			d := (f - p.at) / p.width
			sum += p.height * math.Exp(-d*d)
		}
		// Start and end at the trailhead.
		return sum * math.Pow(math.Sin(math.Pi*f), 0.6)
	}
	// Scale so that the climbing is about right.
	gain, previous := 0.0, raw(0)
	for i := 1; i <= 1000; i++ {
		value := raw(float64(i) / 1000)
		gain += math.Max(value-previous, 0)
		previous = value
	}
	scale := climb / math.Max(gain, 1e-6)
	return func(f float64) float64 { return scale * raw(f) }
}
