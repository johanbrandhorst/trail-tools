package fakestrava

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

// straightRouter routes in straight lines in its region, except into Howe
// Sound, west of Squamish, where there are no paths.
type straightRouter struct {
	region geo.Bounds
	calls  atomic.Int32
	fail   bool
}

var errNoPath = errors.New("no path")

func (r *straightRouter) Regions(context.Context) ([]geo.Bounds, error) {
	return []geo.Bounds{r.region}, nil
}

func (r *straightRouter) Route(_ context.Context, from, to geo.Point, _ bool) ([]geo.Point, error) {
	r.calls.Add(1)
	if r.fail || to.Lng < -123.19 || !r.region.Contains(from) || !r.region.Contains(to) {
		return nil, errNoPath
	}
	points := geo.Densify(from, to, 50)
	for i := range points {
		// Uphill to the east.
		points[i].Ele = 20 + (points[i].Lng+123.2)*10_000
	}
	return points, nil
}

var squamish = geo.Bounds{MinLat: 49.6, MinLng: -123.3, MaxLat: 50.0, MaxLng: -122.9}

func TestRoutedTracks(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	router := &straightRouter{region: squamish}
	cacheDir := t.TempDir()
	activities := generate(ctx, now, &tracer{router: router, regions: []geo.Bounds{squamish}, cacheDir: cacheDir})
	routed := 0
	for _, a := range activities {
		if a.manual {
			continue
		}
		if !a.routed {
			// Long rides from home may not find a loop of their length.
			if a.sportType != "Ride" {
				t.Errorf("activity %d (%s) isn't routed", a.id, a.name)
			}
			continue
		}
		routed++
		latlng := a.streams.latlng
		start, end := latlng[0], latlng[len(latlng)-1]
		if math.Abs(start[0]-end[0]) > 1e-4 || math.Abs(start[1]-end[1]) > 1e-4 {
			t.Errorf("activity %d (%s) isn't a loop: %v to %v", a.id, a.name, start, end)
		}
		// The track follows the router's elevations.
		if alt := a.streams.altitude[0]; math.Abs(alt-(20+(start[1]+123.2)*10_000)) > 20 {
			t.Errorf("activity %d starts at %v m, want the router's elevation", a.id, alt)
		}
	}
	if routed == 0 {
		t.Fatal("no routed activities")
	}

	// Routed tracks are cached, so routing again isn't needed.
	failing := &straightRouter{region: squamish, fail: true}
	again := generate(ctx, now, &tracer{router: failing, regions: []geo.Bounds{squamish}, cacheDir: cacheDir})
	for i, a := range again {
		if before := activities[i]; before.routed && (!a.routed || math.Abs(a.distance-before.distance) > before.distance/1000) {
			t.Errorf("activity %d differs with cached tracks: %.0f m, want %.0f m", a.id, a.distance, before.distance)
		}
	}

	// Without routing, or outside its regions, tracks are made up.
	elsewhere := &straightRouter{region: geo.Bounds{MinLat: 46, MinLng: 7, MaxLat: 47, MaxLng: 8}}
	for _, a := range generate(ctx, now, &tracer{router: elsewhere, regions: []geo.Bounds{elsewhere.region}}) {
		if a.routed {
			t.Errorf("activity %d is routed outside the router's regions", a.id)
		}
	}
	if n := elsewhere.calls.Load(); n != 0 {
		t.Errorf("routed %d times outside the router's regions", n)
	}
}

func TestRouteLoopSkipsUnreachableWaypoints(t *testing.T) {
	ctx := context.Background()
	router := &straightRouter{region: squamish}
	// Near the shore, some waypoints are in the water.
	for seed := range uint64(20) {
		points, err := routeLoop(ctx, router, estuary, 10_000, true, randFor(seed))
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		for _, p := range points {
			if p.Lng < -123.19 {
				t.Fatalf("seed %d: the loop goes into the water at %v", seed, p)
			}
		}
	}
}

func randFor(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, 1))
}

// trailRouter knows one trail, going east and west at a latitude. Points
// within 2 km of it are routed onto it.
type trailRouter struct{ lat float64 }

func (r trailRouter) Regions(context.Context) ([]geo.Bounds, error) {
	return []geo.Bounds{squamish}, nil
}

func (r trailRouter) Route(_ context.Context, from, to geo.Point, _ bool) ([]geo.Point, error) {
	if math.Abs(from.Lat-r.lat)*111_320 > 2000 || math.Abs(to.Lat-r.lat)*111_320 > 2000 {
		return nil, errNoPath
	}
	from.Lat, to.Lat = r.lat, r.lat
	return geo.Densify(from, to, 50), nil
}

func TestRouteTrackOutAndBack(t *testing.T) {
	ctx := context.Background()
	start := geo.Point{Lat: 49.7, Lng: -123.1}
	for seed := range uint64(10) {
		points, err := routeTrack(ctx, trailRouter{lat: start.Lat}, start, 12_000, true, randFor(seed))
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if off := lengthOff(points, 12_000); off > math.Log(1.25) {
			t.Errorf("seed %d: got %.0f m, want about 12 km", seed, geo.CumulativeDistances(points)[len(points)-1])
		}
		// Out along the trail, and back the same way.
		for i, p := range points {
			back := points[len(points)-1-i]
			if math.Abs(p.Lat-start.Lat) > 1e-9 || geo.Distance(p, back) > 100 && i == 0 {
				t.Fatalf("seed %d: point %d at %v isn't on the trail, or doesn't come back", seed, i, p)
			}
		}
	}
}
