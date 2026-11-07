package route

import (
	"fmt"

	geov1 "github.com/bs-tools/trailrunn.ing/internal/gen/geo/v1"
	routesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/routes/v1"
	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/bs-tools/trailrunn.ing/internal/routes"
	"github.com/bs-tools/trailrunn.ing/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// maxResponsePoints is the maximum number of path points returned.
const maxResponsePoints = 5000

func profileFromProto(p routesv1.RoutingProfile) routes.Profile {
	switch p {
	case routesv1.RoutingProfile_ROUTING_PROFILE_SHORTEST:
		return routes.ProfileShortest
	case routesv1.RoutingProfile_ROUTING_PROFILE_STRAIGHT:
		return routes.ProfileStraight
	case routesv1.RoutingProfile_ROUTING_PROFILE_HEATMAP:
		return routes.ProfileHeatmap
	default:
		return routes.ProfileTrail
	}
}

// legProfilesFromProto returns the profile of each leg between n waypoints:
// the leg's own profile if set, or the default profile.
func legProfilesFromProto(defaultProfile routes.Profile, legProfiles []routesv1.RoutingProfile, n int) ([]routes.Profile, error) {
	legs := max(n-1, 0)
	if len(legProfiles) != 0 && len(legProfiles) != legs {
		return nil, fmt.Errorf("%w: leg_profiles must be empty or have one entry per leg; got %d entries for %d waypoints (%d legs)", errInvalidRoute, len(legProfiles), n, legs)
	}
	profiles := make([]routes.Profile, legs)
	for i := range profiles {
		profiles[i] = defaultProfile
		if i < len(legProfiles) && legProfiles[i] != routesv1.RoutingProfile_ROUTING_PROFILE_UNSPECIFIED {
			profiles[i] = profileFromProto(legProfiles[i])
		}
	}
	return profiles, nil
}

func profileToProto(p routes.Profile) routesv1.RoutingProfile {
	switch p {
	case routes.ProfileTrail:
		return routesv1.RoutingProfile_ROUTING_PROFILE_TRAIL
	case routes.ProfileShortest:
		return routesv1.RoutingProfile_ROUTING_PROFILE_SHORTEST
	case routes.ProfileStraight:
		return routesv1.RoutingProfile_ROUTING_PROFILE_STRAIGHT
	case routes.ProfileHeatmap:
		return routesv1.RoutingProfile_ROUTING_PROFILE_HEATMAP
	default:
		return routesv1.RoutingProfile_ROUTING_PROFILE_UNSPECIFIED
	}
}

func sourceToProto(s routes.Source) routesv1.RouteSource {
	switch s {
	case routes.SourceWaypoints:
		return routesv1.RouteSource_ROUTE_SOURCE_WAYPOINTS
	case routes.SourceGPX:
		return routesv1.RouteSource_ROUTE_SOURCE_GPX
	case routes.SourceActivity:
		return routesv1.RouteSource_ROUTE_SOURCE_ACTIVITY
	default:
		return routesv1.RouteSource_ROUTE_SOURCE_UNSPECIFIED
	}
}

// TerrainToProto converts a terrain to its API representation.
func TerrainToProto(t geo.Terrain) geov1.Terrain {
	switch t {
	case geo.TerrainPaved:
		return geov1.Terrain_TERRAIN_PAVED
	case geo.TerrainGravel:
		return geov1.Terrain_TERRAIN_GRAVEL
	case geo.TerrainTrail:
		return geov1.Terrain_TERRAIN_TRAIL
	case geo.TerrainTechnical:
		return geov1.Terrain_TERRAIN_TECHNICAL
	default:
		return geov1.Terrain_TERRAIN_UNSPECIFIED
	}
}

// TerrainFromProto converts a terrain from its API representation.
func TerrainFromProto(t geov1.Terrain) geo.Terrain {
	switch t {
	case geov1.Terrain_TERRAIN_PAVED:
		return geo.TerrainPaved
	case geov1.Terrain_TERRAIN_GRAVEL:
		return geo.TerrainGravel
	case geov1.Terrain_TERRAIN_TRAIL:
		return geo.TerrainTrail
	case geov1.Terrain_TERRAIN_TECHNICAL:
		return geo.TerrainTechnical
	default:
		return geo.TerrainUnknown
	}
}

// terrainOrder is the order terrain shares are listed in.
var terrainOrder = []geo.Terrain{geo.TerrainPaved, geo.TerrainGravel, geo.TerrainTrail, geo.TerrainTechnical, geo.TerrainUnknown}

// StatsToProto converts route statistics to their API representation.
func StatsToProto(stats geo.TrackStats) *routesv1.RouteStats {
	var shares []*routesv1.TerrainShare
	for _, t := range terrainOrder {
		d := stats.TerrainMeters[t]
		if d <= 0 {
			continue
		}
		fraction := 0.0
		if stats.DistanceMeters > 0 {
			fraction = d / stats.DistanceMeters
		}
		shares = append(shares, routesv1.TerrainShare_builder{
			Terrain:        TerrainToProto(t),
			DistanceMeters: d,
			Fraction:       fraction,
		}.Build())
	}
	return routesv1.RouteStats_builder{
		DistanceMeters:         stats.DistanceMeters,
		ElevationGainMeters:    stats.ElevationGainMeters,
		ElevationLossMeters:    stats.ElevationLossMeters,
		MinElevationMeters:     stats.MinElevationMeters,
		MaxElevationMeters:     stats.MaxElevationMeters,
		MaxClimbGradePercent:   stats.MaxClimbGrade,
		MaxDescentGradePercent: stats.MaxDescentGrade,
		Terrain:                shares,
		RepeatedDistanceMeters: stats.RepeatedMeters,
	}.Build()
}

// PathToProto converts a path to route points with cumulative distances,
// downsampled to at most limit points.
func PathToProto(points []geo.Point, limit int) []*routesv1.RoutePoint {
	dists := geo.CumulativeDistances(points)
	idxs := geo.Downsample(len(points), limit)
	result := make([]*routesv1.RoutePoint, len(idxs))
	for i, idx := range idxs {
		p := points[idx]
		result[i] = routesv1.RoutePoint_builder{
			Lat:             p.Lat,
			Lng:             p.Lng,
			ElevationMeters: p.Ele,
			DistanceMeters:  dists[idx],
		}.Build()
	}
	return result
}

func routeToProto(route *routes.Route, includePoints bool) *routesv1.Route {
	waypoints := make([]*geov1.LatLng, len(route.Waypoints))
	for i, wp := range route.Waypoints {
		waypoints[i] = geov1.LatLng_builder{Lat: wp.Lat, Lng: wp.Lng}.Build()
	}
	legProfiles := make([]routesv1.RoutingProfile, len(route.Legs))
	for i, leg := range route.Legs {
		legProfiles[i] = profileToProto(leg.Profile)
	}
	b := routesv1.Route_builder{
		Id:          store.UUIDToString(route.ID),
		Name:        route.Name,
		Description: route.Description,
		Profile:     profileToProto(route.Profile),
		Waypoints:   waypoints,
		LegProfiles: legProfiles,
		Stats:       StatsToProto(route.Stats),
		Source:      sourceToProto(route.Source),
		Warnings:    routeWarnings(route),
	}
	if !route.CreateTime.IsZero() {
		b.CreateTime = timestamppb.New(route.CreateTime)
		b.UpdateTime = timestamppb.New(route.UpdateTime)
	}
	if includePoints {
		points, _ := route.Path()
		b.Points = PathToProto(points, maxResponsePoints)
	}
	return b.Build()
}

func waypointsFromProto(waypoints []*geov1.LatLng) []routes.LatLng {
	result := make([]routes.LatLng, len(waypoints))
	for i, wp := range waypoints {
		result[i] = routes.LatLng{Lat: wp.GetLat(), Lng: wp.GetLng()}
	}
	return result
}
