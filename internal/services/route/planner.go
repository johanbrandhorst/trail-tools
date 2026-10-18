package route

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/mapdata"
	"github.com/johanbrandhorst/trail-tools/internal/routes"
	"github.com/johanbrandhorst/trail-tools/internal/routing"
	"golang.org/x/sync/errgroup"
)

const (
	// maxWaypoints is the maximum number of waypoints in a route.
	maxWaypoints = 200
	// maxLegMeters is the longest distance between the waypoints of a leg
	// that is routed or drawn straight. Longer legs take a large area of the
	// map to route, and straight ones points every straightSpacingMeters,
	// which add up to more memory than the server has for legs around the
	// world.
	maxLegMeters = 50_000
	// straightSpacingMeters is the spacing of points on straight legs, used
	// to look up elevations.
	straightSpacingMeters = 25
	// maxElevationLookups is the maximum number of elevation lookups for a
	// single track.
	maxElevationLookups = 500
	// importWaypointSpacingMeters is the distance between waypoints created
	// when importing a track, so it can be edited.
	importWaypointSpacingMeters = 1000
	// maxImportWaypoints is the maximum number of waypoints created when
	// importing a track.
	maxImportWaypoints = 60
	// maxConcurrentLegs is the maximum number of legs routed concurrently.
	// Routing mostly waits for round trips to the database, which it barely
	// loads, so it uses most of the connections the store opens (45, see
	// store.New), leaving some for other requests.
	maxConcurrentLegs = 32
)

// Router routes paths on the map.
type Router interface {
	Route(ctx context.Context, req routing.Request) (*routing.Path, error)
	// Regions returns the bounds of the areas paths can be routed in.
	Regions(ctx context.Context) ([]geo.Bounds, error)
}

// MapData provides elevation and weather data.
type MapData interface {
	FillElevations(ctx context.Context, points []geo.Point, maxLookups int) error
	Forecast(ctx context.Context, queries []mapdata.ForecastQuery) ([]mapdata.Weather, error)
}

// errInvalidRoute is wrapped by errors caused by invalid input, such as
// waypoints that can't be routed.
var errInvalidRoute = errors.New("invalid route")

// buildLegs computes the legs between consecutive waypoints, where
// profiles[i] is the profile of the leg from waypoints[i] to
// waypoints[i+1]. Existing legs with the same endpoints are reused, unless
// they were routed with a different profile, and so are legs recently
// routed for the user. Legs are also reused in the opposite direction, e.g.
// for the way back of an out and back route.
func (s *Service) buildLegs(ctx context.Context, userID pgtype.UUID, waypoints []routes.LatLng, profiles []routes.Profile, existing []routes.Leg) ([]routes.Leg, error) {
	if len(waypoints) < 2 {
		return nil, fmt.Errorf("%w: at least 2 waypoints are required", errInvalidRoute)
	}
	if len(waypoints) > maxWaypoints {
		return nil, fmt.Errorf("%w: at most %d waypoints are allowed", errInvalidRoute, maxWaypoints)
	}
	if len(profiles) != len(waypoints)-1 {
		return nil, fmt.Errorf("%w: got %d leg profiles for %d legs", errInvalidRoute, len(profiles), len(waypoints)-1)
	}
	for i, wp := range waypoints {
		if math.IsNaN(wp.Lat) || math.IsNaN(wp.Lng) || wp.Lat < -90 || wp.Lat > 90 || wp.Lng < -180 || wp.Lng > 180 {
			return nil, fmt.Errorf("%w: waypoint %d has invalid coordinates", errInvalidRoute, i+1)
		}
	}
	legs := make([]routes.Leg, len(waypoints)-1)
	// The legs that need routing, legs that repeat one of them (in either
	// direction) and the endpoints of the legs that prefer the user's
	// heatmap.
	var toRoute []int
	repeats := map[int]int{}
	var heatmapEnds []geo.Point
	for i := range legs {
		from, to, profile := waypoints[i], waypoints[i+1], profiles[i]
		if leg, ok := findLeg(existing, from, to, profile); ok {
			legs[i] = leg
			continue
		}
		if leg, ok := s.legs.get(userID, from, to, profile); ok {
			legs[i] = leg
			continue
		}
		if j, ok := findRepeat(toRoute, waypoints, profiles, i); ok {
			repeats[i] = j
			continue
		}
		fromPoint, toPoint := geo.Point{Lat: from.Lat, Lng: from.Lng}, geo.Point{Lat: to.Lat, Lng: to.Lng}
		if d := geo.Distance(fromPoint, toPoint); d > maxLegMeters {
			return nil, fmt.Errorf("%w: the leg from waypoint %d to %d is %.0f km long, but legs can be at most %d km; add waypoints in between",
				errInvalidRoute, i+1, i+2, d/1000, maxLegMeters/1000)
		}
		toRoute = append(toRoute, i)
		if profile == routes.ProfileHeatmap {
			heatmapEnds = append(heatmapEnds, fromPoint, toPoint)
		}
	}
	var heat *heatmap
	if len(heatmapEnds) > 0 {
		var err error
		heat, err = s.loadHeatmap(ctx, userID, heatmapEnds)
		if err != nil {
			return nil, err
		}
	}
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentLegs)
	for _, i := range toRoute {
		from, to, profile := waypoints[i], waypoints[i+1], profiles[i]
		g.Go(func() error {
			leg, err := s.routeLeg(ctx, heat, from, to, profile)
			if err != nil {
				return fmt.Errorf("could not compute the leg from waypoint %d to %d: %w", i+1, i+2, err)
			}
			s.legs.put(userID, leg)
			legs[i] = leg
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	for i, j := range repeats {
		legs[i] = legs[j]
		if legs[j].From != waypoints[i] {
			legs[i] = legs[j].Reversed()
		}
	}
	return legs, nil
}

// findRepeat returns the leg in toRoute with the same endpoints and profile
// as leg i, in either direction.
func findRepeat(toRoute []int, waypoints []routes.LatLng, profiles []routes.Profile, i int) (int, bool) {
	for _, j := range toRoute {
		if profiles[j] != profiles[i] {
			continue
		}
		same := waypoints[j] == waypoints[i] && waypoints[j+1] == waypoints[i+1]
		reversed := waypoints[j] == waypoints[i+1] && waypoints[j+1] == waypoints[i]
		if same || reversed {
			return j, true
		}
	}
	return 0, false
}

func findLeg(existing []routes.Leg, from, to routes.LatLng, profile routes.Profile) (routes.Leg, bool) {
	for _, leg := range existing {
		// Imported legs have no profile and are kept until their endpoints
		// move.
		if leg.Profile != "" && leg.Profile != profile {
			continue
		}
		if leg.From == from && leg.To == to {
			return leg, true
		}
		if leg.From == to && leg.To == from {
			return leg.Reversed(), true
		}
	}
	return routes.Leg{}, false
}

// routeLeg computes a leg with a profile. heat is the user's heatmap, used
// by the heatmap profile.
func (s *Service) routeLeg(ctx context.Context, heat *heatmap, from, to routes.LatLng, profile routes.Profile) (routes.Leg, error) {
	fromPoint := geo.Point{Lat: from.Lat, Lng: from.Lng}
	toPoint := geo.Point{Lat: to.Lat, Lng: to.Lng}
	var path *routing.Path
	var err error
	switch profile {
	case routes.ProfileStraight:
		points := geo.Densify(fromPoint, toPoint, straightSpacingMeters)
		if err := s.mapData.FillElevations(ctx, points, maxElevationLookups); err != nil {
			return routes.Leg{}, err
		}
		return routes.NewLeg(from, to, profile, points, nil), nil
	case routes.ProfileTrail:
		path, err = s.router.Route(ctx, routing.Request{From: fromPoint, To: toPoint, Profile: routing.ProfileTrail})
	case routes.ProfileShortest:
		path, err = s.router.Route(ctx, routing.Request{From: fromPoint, To: toPoint, Profile: routing.ProfileShortest})
	case routes.ProfileHeatmap:
		// Prefer the paths of the user's activities near the leg, if any.
		path, err = s.router.Route(ctx, routing.Request{From: fromPoint, To: toPoint, Profile: routing.ProfileTrail, Prefer: heat.near(fromPoint, toPoint)})
	default:
		return routes.Leg{}, fmt.Errorf("%w: unknown profile %q", errInvalidRoute, profile)
	}
	if errors.Is(err, routing.ErrNotRoutable) {
		return routes.Leg{}, fmt.Errorf("%w: %w; try moving the waypoints closer to a trail or road, or use the STRAIGHT profile", errInvalidRoute, err)
	}
	if err != nil {
		return routes.Leg{}, err
	}
	return legFromPath(from, to, profile, path), nil
}

// legFromPath creates a leg from a routed path.
func legFromPath(from, to routes.LatLng, profile routes.Profile, path *routing.Path) routes.Leg {
	// Make the leg start and end exactly at the waypoints, so the route is
	// continuous even if routing snapped them to the nearest way.
	points := append([]geo.Point{{Lat: from.Lat, Lng: from.Lng, Ele: path.Points[0].Ele}}, path.Points...)
	points = append(points, geo.Point{Lat: to.Lat, Lng: to.Lng, Ele: path.Points[len(path.Points)-1].Ele})
	terrains := append([]geo.Terrain{geo.TerrainUnknown}, path.Terrains...)
	terrains[len(terrains)-1] = geo.TerrainUnknown
	terrains = append(terrains, geo.TerrainUnknown)
	points, terrains = dedupe(points, terrains)
	return routes.NewLeg(from, to, profile, points, terrains)
}

// dedupe removes consecutive duplicate points, keeping the terrain of the
// segment that follows.
func dedupe(points []geo.Point, terrains []geo.Terrain) ([]geo.Point, []geo.Terrain) {
	var outPoints []geo.Point
	var outTerrains []geo.Terrain
	for i, p := range points {
		if n := len(outPoints); n > 0 && outPoints[n-1].Lat == p.Lat && outPoints[n-1].Lng == p.Lng {
			outTerrains[n-1] = terrains[i]
			continue
		}
		outPoints = append(outPoints, p)
		outTerrains = append(outTerrains, terrains[i])
	}
	return outPoints, outTerrains
}

// legsFromTrack splits a recorded or imported track into editable legs, with
// waypoints spaced along the track. Each leg is simplified to
// trackToleranceMeters and trackElevationToleranceMeters, as tracks
// recorded every second have far more points than needed.
func legsFromTrack(points []geo.Point) ([]routes.LatLng, []routes.Leg) {
	dists := geo.CumulativeDistances(points)
	total := dists[len(dists)-1]
	spacing := math.Max(importWaypointSpacingMeters, total/maxImportWaypoints)
	idxs := []int{0}
	next := spacing
	for i := 1; i < len(points)-1; i++ {
		// Avoid a tiny last leg.
		if dists[i] >= next && total-dists[i] > spacing/2 {
			idxs = append(idxs, i)
			next = dists[i] + spacing
		}
	}
	idxs = append(idxs, len(points)-1)
	waypoints := make([]routes.LatLng, len(idxs))
	for i, idx := range idxs {
		waypoints[i] = routes.LatLng{Lat: points[idx].Lat, Lng: points[idx].Lng}
	}
	legs := make([]routes.Leg, len(idxs)-1)
	for i := range legs {
		legs[i] = routes.NewLeg(waypoints[i], waypoints[i+1], "", geo.SimplifyTrack(points[idxs[i]:idxs[i+1]+1], trackToleranceMeters, trackElevationToleranceMeters), nil)
	}
	return waypoints, legs
}

// computeStats computes the statistics of a route's path.
func computeStats(route *routes.Route) {
	points, terrains := route.Path()
	route.Stats = geo.ComputeStats(points, terrains)
}
