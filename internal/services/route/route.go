package route

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routes"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"gitlab.com/greyxor/slogor"
)

const (
	// trackToleranceMeters is how far the path of a route may stray from the
	// recorded track it follows exactly, well within the accuracy of GPS.
	// Simplifying drops most points of tracks recorded every second.
	trackToleranceMeters = 1
	// trackElevationToleranceMeters is how far the elevation of a route may
	// stray from the recorded track it follows exactly, within the noise of
	// recorded elevations.
	trackElevationToleranceMeters = 2
	// maxTrackPoints limits the points of a recorded track, about five days
	// recorded every second.
	maxTrackPoints = 500_000
	// maxRouteTrackPoints limits the points of a route that follows a
	// recorded track exactly, once simplified, which are all stored and sent
	// with the route.
	maxRouteTrackPoints = 100_000
	// maxTrackMeters is the longest recorded track a route can be made from.
	// Longer tracks usually have stray points far from the rest.
	maxTrackMeters = 5_000_000
)

func (s *Service) PreviewRoute(ctx context.Context, req *routesv1.PreviewRouteRequest) (*routesv1.PreviewRouteResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	route := &routes.Route{
		Profile:   profileFromProto(req.GetProfile()),
		Source:    routes.SourceWaypoints,
		Waypoints: waypointsFromProto(req.GetWaypoints()),
	}
	profiles, err := legProfilesFromProto(route.Profile, req.GetLegProfiles(), len(route.Waypoints))
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to compute route")
	}
	var existing []routes.Leg
	if req.GetRouteId() != "" {
		saved, err := s.getRoute(ctx, req.GetRouteId())
		if err != nil {
			return nil, err
		}
		existing = saved.Legs
	}
	legs, err := s.buildLegs(ctx, userID, route.Waypoints, profiles, existing)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to compute route")
	}
	route.Legs = legs
	computeStats(route)
	return routesv1.PreviewRouteResponse_builder{
		Route: routeToProto(route, req.GetIncludePoints()),
	}.Build(), nil
}

func (s *Service) CreateRoute(ctx context.Context, req *routesv1.CreateRouteRequest) (*routesv1.CreateRouteResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	route := &routes.Route{
		UserID:      userID,
		Name:        strings.TrimSpace(req.GetName()),
		Description: req.GetDescription(),
		Profile:     routes.ProfileTrail,
	}
	var warning string
	switch req.WhichSource() {
	case routesv1.CreateRouteRequest_Waypoints_case:
		route.Source = routes.SourceWaypoints
		route.Profile = profileFromProto(req.GetWaypoints().GetProfile())
		route.Waypoints = waypointsFromProto(req.GetWaypoints().GetWaypoints())
		profiles, err := legProfilesFromProto(route.Profile, req.GetWaypoints().GetLegProfiles(), len(route.Waypoints))
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to compute route")
		}
		legs, err := s.buildLegs(ctx, userID, route.Waypoints, profiles, nil)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to compute route")
		}
		route.Legs = legs
	default:
		track, err := s.recordedTrack(ctx, userID, req)
		if err != nil {
			return nil, err
		}
		if track == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, "one of waypoints, gpx, activity_id or route_id must be set")
		}
		if err := s.fillMissingElevations(ctx, track.points); err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, err.Error()).WithCause(err)
		}
		route.Source = track.source
		if warning, err = s.routeFromTrack(ctx, route, track.points, req.GetConversion()); err != nil {
			return nil, err
		}
		if route.Name == "" {
			route.Name = track.name
		}
	}
	if route.Name == "" {
		route.Name = "Untitled route"
	}
	computeStats(route)
	created, err := s.routes.CreateRoute(ctx, route)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to create route")
	}
	return routesv1.CreateRouteResponse_builder{
		Route:   routeToProto(created, req.GetIncludePoints()),
		Warning: warning,
	}.Build(), nil
}

func (s *Service) GetRoute(ctx context.Context, req *routesv1.GetRouteRequest) (*routesv1.GetRouteResponse, error) {
	route, err := s.getRoute(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return routesv1.GetRouteResponse_builder{
		Route: routeToProto(route, req.GetIncludePoints()),
	}.Build(), nil
}

func (s *Service) ListRoutes(ctx context.Context, req *routesv1.ListRoutesRequest) (*routesv1.ListRoutesResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	list, err := s.routes.ListRoutes(ctx, userID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list routes")
	}
	protoRoutes := make([]*routesv1.Route, len(list))
	for i, route := range list {
		protoRoutes[i] = routeToProto(route, false)
	}
	return routesv1.ListRoutesResponse_builder{
		Routes: protoRoutes,
	}.Build(), nil
}

func (s *Service) UpdateRoute(ctx context.Context, req *routesv1.UpdateRouteRequest) (*routesv1.UpdateRouteResponse, error) {
	route, err := s.getRoute(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if req.HasName() {
		route.Name = strings.TrimSpace(req.GetName())
		if route.Name == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, "name must not be empty")
		}
	}
	if req.HasDescription() {
		route.Description = req.GetDescription()
	}
	if req.HasWaypoints() {
		waypoints := waypointsFromProto(req.GetWaypoints().GetWaypoints())
		profile := profileFromProto(req.GetWaypoints().GetProfile())
		profiles, err := legProfilesFromProto(profile, req.GetWaypoints().GetLegProfiles(), len(waypoints))
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to compute route")
		}
		legs, err := s.buildLegs(ctx, route.UserID, waypoints, profiles, route.Legs)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to compute route")
		}
		route.Waypoints, route.Profile, route.Legs = waypoints, profile, legs
		computeStats(route)
	}
	updated, err := s.routes.UpdateRoute(ctx, route)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to update route")
	}
	return routesv1.UpdateRouteResponse_builder{
		Route: routeToProto(updated, req.GetIncludePoints()),
	}.Build(), nil
}

func (s *Service) DeleteRoute(ctx context.Context, req *routesv1.DeleteRouteRequest) (*routesv1.DeleteRouteResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	routeID, err := store.StringToUUID(req.GetId())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid route ID: %v", err).WithCause(err)
	}
	if err := s.routes.DeleteRoute(ctx, userID, routeID); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete route")
	}
	return routesv1.DeleteRouteResponse_builder{}.Build(), nil
}

func (s *Service) CheckTrackCoverage(ctx context.Context, req *routesv1.CheckTrackCoverageRequest) (*routesv1.CheckTrackCoverageResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	track, err := s.recordedTrack(ctx, userID, req)
	if err != nil {
		return nil, err
	}
	if track == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "one of gpx, activity_id or route_id must be set")
	}
	regions, err := s.router.Regions(ctx)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to check the track's coverage")
	}
	return routesv1.CheckTrackCoverageResponse_builder{
		CoveredFraction: trackCoverage(track.points, regions),
	}.Build(), nil
}

// trackSource is a request that names a recorded track.
type trackSource interface {
	HasGpx() bool
	GetGpx() string
	HasActivityId() bool
	GetActivityId() string
	HasRouteId() bool
	GetRouteId() string
}

// recording is a recorded track a route can be made from.
type recording struct {
	points []geo.Point
	// The name of the GPX track, activity or route.
	name   string
	source routes.Source
}

// recordedTrack returns the recorded track a request names: a GPX file, an
// activity or a saved route, checked with checkTrack. It returns nil if the
// request names none.
func (s *Service) recordedTrack(ctx context.Context, userID pgtype.UUID, req trackSource) (*recording, error) {
	track, err := s.uncheckedTrack(ctx, userID, req)
	if err != nil || track == nil {
		return nil, err
	}
	if err := checkTrack(track.points); err != nil {
		return nil, err
	}
	return track, nil
}

// uncheckedTrack returns the recorded track a request names, or nil if it
// names none.
func (s *Service) uncheckedTrack(ctx context.Context, userID pgtype.UUID, req trackSource) (*recording, error) {
	switch {
	case req.HasGpx():
		parsed, err := geo.ParseGPX(strings.NewReader(req.GetGpx()))
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
		return &recording{points: parsed.Points, name: parsed.Name, source: routes.SourceGPX}, nil
	case req.HasActivityId():
		activityID, err := store.StringToUUID(req.GetActivityId())
		if err != nil {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid activity ID: %v", err).WithCause(err)
		}
		activity, points, err := s.activityTrack(ctx, userID, activityID)
		if err != nil {
			return nil, err
		}
		return &recording{points: points, name: activity.Name, source: routes.SourceActivity}, nil
	case req.HasRouteId():
		saved, err := s.getRoute(ctx, req.GetRouteId())
		if err != nil {
			return nil, err
		}
		points, _ := saved.Path()
		if len(points) < 2 {
			return nil, connect.NewError(connect.CodeFailedPrecondition, "the route has no path")
		}
		return &recording{points: points, name: saved.Name, source: saved.Source}, nil
	default:
		return nil, nil
	}
}

// checkTrack checks that a route can be made from a recorded track.
func checkTrack(points []geo.Point) error {
	if len(points) > maxTrackPoints {
		return connect.Errorf(connect.CodeInvalidArgument,
			"the track has %d points, but at most %d are supported; split it into shorter tracks", len(points), maxTrackPoints)
	}
	length := 0.0
	for i := 1; i < len(points); i++ {
		length += geo.Distance(points[i-1], points[i])
	}
	if length > maxTrackMeters {
		return connect.Errorf(connect.CodeInvalidArgument,
			"the track is %.0f km long, but at most %d km is supported; remove any stray points far from the rest, or split it into shorter tracks",
			length/1000, maxTrackMeters/1000)
	}
	return nil
}

// routeFromTrack sets a route's waypoints and legs from a recorded track,
// keeping the track exactly or snapping it to the map. If the track can't be
// snapped, the route follows it with straight legs between waypoints about a
// kilometer apart instead, which the user can still move or route along
// trails, and warning says so.
func (s *Service) routeFromTrack(ctx context.Context, route *routes.Route, track []geo.Point, conversion routesv1.TrackConversion) (warning string, err error) {
	if conversion == routesv1.TrackConversion_TRACK_CONVERSION_SNAP {
		waypoints, legs, err := s.snapTrack(ctx, track)
		switch {
		case err == nil:
			route.Waypoints, route.Legs = waypoints, legs
			return "", nil
		case ctx.Err() != nil:
			return "", s.toConnectError(ctx, err, "failed to snap the track to the map")
		}
		// Tracks outside the imported map data can't be snapped, but other
		// failures are unexpected.
		reason := ""
		if errors.Is(err, errInvalidRoute) {
			reason = fmt.Sprintf(" (%v)", errors.Unwrap(err))
			s.log.InfoContext(ctx, "Following a track with straight legs, as it can't be snapped to the map", slogor.Err(err))
		} else {
			s.report.Report(ctx, "Failed to snap a track to the map, following it with straight legs", err)
		}
		route.Waypoints, route.Legs = legsFromTrack(track)
		if err := checkRoutePoints(route.Legs); err != nil {
			return "", err
		}
		for i := range route.Legs {
			route.Legs[i].Profile = routes.ProfileStraight
		}
		return fmt.Sprintf("The track couldn't be snapped to trails%s, so the route follows it as recorded with straight legs. "+
			"Move its waypoints, or route legs along trails, to adjust it.", reason), nil
	}
	route.Waypoints, route.Legs = legsFromTrack(track)
	return "", checkRoutePoints(route.Legs)
}

// checkRoutePoints checks that the legs of a route that follows a recorded
// track have at most maxRouteTrackPoints points.
func checkRoutePoints(legs []routes.Leg) error {
	n := 0
	for _, leg := range legs {
		n += len(leg.Points)
	}
	if n > maxRouteTrackPoints {
		return connect.Errorf(connect.CodeInvalidArgument,
			"the track has %d points even when simplified, but at most %d are supported; split it into shorter tracks, or convert it with SNAP",
			n, maxRouteTrackPoints)
	}
	return nil
}

// getRoute gets one of the authenticated user's routes by ID.
func (s *Service) getRoute(ctx context.Context, id string) (*routes.Route, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	routeID, err := store.StringToUUID(id)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid route ID: %v", err).WithCause(err)
	}
	route, err := s.routes.GetRoute(ctx, userID, routeID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get route")
	}
	return route, nil
}

// activityTrack returns an activity and its GPS track, from its streams if
// synced or its summary polyline otherwise.
func (s *Service) activityTrack(ctx context.Context, userID, activityID pgtype.UUID) (*activities.Activity, []geo.Point, error) {
	activity, err := s.activities.GetActivity(ctx, userID, activityID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, connect.NewError(connect.CodeNotFound, "activity not found")
		}
		return nil, nil, s.toConnectError(ctx, err, "failed to get activity")
	}
	var points []geo.Point
	streams, err := s.activities.GetActivityStreams(ctx, userID, activityID)
	switch {
	case err == nil:
		points = streamPoints(streams)
	case errors.Is(err, store.ErrNotFound):
		if activity.SummaryPolyline != "" {
			points, err = geo.DecodePolyline(activity.SummaryPolyline)
			if err != nil {
				return nil, nil, s.toConnectError(ctx, err, "failed to decode activity polyline")
			}
		}
	default:
		return nil, nil, s.toConnectError(ctx, err, "failed to get activity streams")
	}
	if len(points) < 2 {
		return nil, nil, connect.NewError(connect.CodeFailedPrecondition, "the activity has no GPS track")
	}
	return activity, points, nil
}

// fillMissingElevations looks up elevations if any point lacks one.
func (s *Service) fillMissingElevations(ctx context.Context, points []geo.Point) error {
	for _, p := range points {
		if math.IsNaN(p.Ele) {
			return s.mapData.FillElevations(ctx, points, maxElevationLookups)
		}
	}
	return nil
}

// streamPoints returns the points of activity streams.
func streamPoints(streams *activitiesv1.ActivityStreams) []geo.Point {
	lats, lngs, alts := streams.GetLat(), streams.GetLng(), streams.GetAltitudeMeters()
	points := make([]geo.Point, 0, len(lats))
	for i := range lats {
		if i >= len(lngs) {
			break
		}
		ele := math.NaN()
		if i < len(alts) {
			ele = alts[i]
		}
		points = append(points, geo.Point{Lat: lats[i], Lng: lngs[i], Ele: ele})
	}
	return points
}

func (s *Service) toConnectError(ctx context.Context, err error, msg string) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, "route not found")
	case errors.Is(err, errInvalidRoute):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err.Error()).WithCause(err)
	default:
		s.report.Report(ctx, msg, err)
		return connect.NewError(connect.CodeInternal, msg).WithCause(err)
	}
}
