package route

import (
	"fmt"

	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routes"
)

// offMapWarningMeters is how far from a mapped path a waypoint of a routed
// leg must be to warn about it: further than map errors and a click beside
// a trail put it, but close enough to catch spurs to a waypoint off it.
const offMapWarningMeters = 25

// routeWarnings returns what to check about a route: the sections that
// retrace ground it already covered, and the waypoints of routed legs off
// any mapped path. It needs the route's legs.
func routeWarnings(route *routes.Route) []*routesv1.RouteWarning {
	if len(route.Legs) == 0 {
		return nil
	}
	var warnings []*routesv1.RouteWarning
	// The distance along the route to the start of each leg, and of each
	// waypoint off the map, by its index.
	along := 0.0
	offMap := map[int]float64{}
	waypointAlong := map[int]float64{}
	for i, leg := range route.Legs {
		waypointAlong[i] = along
		if leg.FromOffMapMeters > offMapWarningMeters {
			offMap[i] = max(offMap[i], leg.FromOffMapMeters)
		}
		points := leg.GeoPoints()
		if len(points) > 0 {
			along += geo.CumulativeDistances(points)[len(points)-1]
		}
		waypointAlong[i+1] = along
		if leg.ToOffMapMeters > offMapWarningMeters {
			offMap[i+1] = max(offMap[i+1], leg.ToOffMapMeters)
		}
	}
	for i := range len(route.Legs) + 1 {
		meters, ok := offMap[i]
		if !ok {
			continue
		}
		at := waypointAlong[i]
		warnings = append(warnings, routesv1.RouteWarning_builder{
			Kind: routesv1.RouteWarningKind_ROUTE_WARNING_KIND_OFF_MAP_WAYPOINT,
			Message: fmt.Sprintf("Waypoint %d, at km %.1f, is %.0f m from the nearest mapped path, so the route goes straight to it off the trail.",
				i+1, at/1000, meters),
			StartDistanceMeters: at,
			EndDistanceMeters:   at,
			WaypointIndex:       new(int32(i)),
		}.Build())
	}
	path, _ := route.Path()
	for _, s := range geo.RepeatedSections(path) {
		message := fmt.Sprintf("Km %.1f-%.1f repeats km %.1f-%.1f.", s.StartMeters/1000, s.EndMeters/1000, s.EarlierStartMeters/1000, s.EarlierEndMeters/1000)
		if s.EarlierEndMeters < s.EarlierStartMeters {
			message = fmt.Sprintf("Km %.1f-%.1f goes back over km %.1f-%.1f.", s.StartMeters/1000, s.EndMeters/1000, s.EarlierEndMeters/1000, s.EarlierStartMeters/1000)
		}
		warnings = append(warnings, routesv1.RouteWarning_builder{
			Kind:                       routesv1.RouteWarningKind_ROUTE_WARNING_KIND_REPEATED_SECTION,
			Message:                    message,
			StartDistanceMeters:        s.StartMeters,
			EndDistanceMeters:          s.EndMeters,
			EarlierStartDistanceMeters: new(s.EarlierStartMeters),
			EarlierEndDistanceMeters:   new(s.EarlierEndMeters),
		}.Build())
	}
	return warnings
}
