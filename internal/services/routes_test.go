package services_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/bs-tools/trailrunn.ing/internal/activities"
	geov1 "github.com/bs-tools/trailrunn.ing/internal/gen/geo/v1"
	routesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/routes/v1"
	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func latLngs(coords ...float64) []*geov1.LatLng {
	var points []*geov1.LatLng
	for i := 0; i+1 < len(coords); i += 2 {
		points = append(points, geov1.LatLng_builder{Lat: coords[i], Lng: coords[i+1]}.Build())
	}
	return points
}

func TestRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// Preview a route over 3 waypoints, climbing 2m per 0.001 degrees.
	preview, err := e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{
		Waypoints:     latLngs(0, 0, 0.01, 0, 0.02, 0),
		IncludePoints: true,
	}.Build())
	if err != nil {
		t.Fatalf("PreviewRoute: %v", err)
	}
	stats := preview.GetRoute().GetStats()
	if !approx(stats.GetDistanceMeters(), 2224, 5) || !approx(stats.GetElevationGainMeters(), 20, 0.5) {
		t.Errorf("unexpected preview stats %v", stats)
	}
	if len(preview.GetRoute().GetPoints()) != 21 {
		t.Errorf("got %d points, want 21", len(preview.GetRoute().GetPoints()))
	}
	terrain := stats.GetTerrain()
	if len(terrain) != 1 || terrain[0].GetTerrain() != geov1.Terrain_TERRAIN_TRAIL || !approx(terrain[0].GetFraction(), 1, 0.01) {
		t.Errorf("unexpected terrain %v", terrain)
	}
	if preview.GetRoute().GetId() != "" {
		t.Error("preview should not be saved")
	}
	if !stats.HasRepeatedDistanceMeters() || stats.GetRepeatedDistanceMeters() != 0 {
		t.Errorf("a route one way repeats %v m", stats.GetRepeatedDistanceMeters())
	}

	// An out-and-back repeats about half its distance.
	outAndBack, err := e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{
		Waypoints: latLngs(0, 0, 0.02, 0, 0, 0),
	}.Build())
	if err != nil {
		t.Fatalf("PreviewRoute: %v", err)
	}
	if repeated := outAndBack.GetRoute().GetStats().GetRepeatedDistanceMeters(); !approx(repeated, 2224, 200) {
		t.Errorf("an out-and-back of 4.4 km repeats %v m, want about 2.2 km", repeated)
	}

	// Unroutable waypoints are an invalid argument.
	_, err = e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{
		Waypoints: latLngs(85, 0, 85.01, 0),
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
	if !strings.Contains(err.Error(), "waypoint 1 to 2") {
		t.Errorf("error should name the leg: %v", err)
	}

	// Create the route and move the last waypoint: only the last leg is
	// routed again.
	created, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Name:      "Hill loop",
		Waypoints: routesv1.Waypoints_builder{Waypoints: latLngs(0, 0, 0.01, 0, 0.02, 0)}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	routeID := created.GetRoute().GetId()
	if created.GetRoute().GetSource() != routesv1.RouteSource_ROUTE_SOURCE_WAYPOINTS ||
		created.GetRoute().GetProfile() != routesv1.RoutingProfile_ROUTING_PROFILE_TRAIL {
		t.Errorf("unexpected route %v", created.GetRoute())
	}
	callsBefore := e.router.routeCallCount()
	updated, err := e.routes.UpdateRoute(ctx, routesv1.UpdateRouteRequest_builder{
		Id:        routeID,
		Name:      new("Bigger hill loop"),
		Waypoints: routesv1.Waypoints_builder{Waypoints: latLngs(0, 0, 0.01, 0, 0.03, 0)}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRoute: %v", err)
	}
	if calls := e.router.routeCallCount() - callsBefore; calls != 1 {
		t.Errorf("got %d routing calls, want 1", calls)
	}
	if updated.GetRoute().GetName() != "Bigger hill loop" || !approx(updated.GetRoute().GetStats().GetDistanceMeters(), 3336, 5) {
		t.Errorf("unexpected updated route %v", updated.GetRoute())
	}

	// Straight legs look up elevations.
	straight, err := e.routes.UpdateRoute(ctx, routesv1.UpdateRouteRequest_builder{
		Id: routeID,
		Waypoints: routesv1.Waypoints_builder{
			Waypoints: latLngs(0, 0, 0.01, 0, 0.03, 0),
			Profile:   routesv1.RoutingProfile_ROUTING_PROFILE_STRAIGHT,
		}.Build(),
		IncludePoints: true,
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRoute: %v", err)
	}
	if got := straight.GetRoute().GetStats().GetElevationGainMeters(); !approx(got, 30, 0.5) {
		t.Errorf("got gain %v, want 30", got)
	}
	if len(straight.GetRoute().GetStats().GetTerrain()) != 1 ||
		straight.GetRoute().GetStats().GetTerrain()[0].GetTerrain() != geov1.Terrain_TERRAIN_UNSPECIFIED {
		t.Errorf("straight legs should have unknown terrain: %v", straight.GetRoute().GetStats().GetTerrain())
	}

	// Routes are private.
	list, err := e.routes.ListRoutes(ctx, routesv1.ListRoutesRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("ListRoutes: %v", err)
	}
	if len(list.GetRoutes()) != 1 || len(list.GetRoutes()[0].GetPoints()) != 0 {
		t.Errorf("unexpected routes %v", list.GetRoutes())
	}

	// Download as GPX and import it again.
	req, err := http.NewRequest(http.MethodGet, e.srv.URL+"/routes/"+routeID+".gpx", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	gpx, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(gpx), "<trkpt") ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "Bigger-hill-loop.gpx") {
		t.Fatalf("unexpected GPX response %d %s", resp.StatusCode, gpx)
	}
	// And as a FIT course for Garmin devices.
	resp, fit := e.get("/routes/"+routeID+".fit", e.token)
	if resp.StatusCode != http.StatusOK || len(fit) < 100 || string(fit[8:12]) != ".FIT" ||
		resp.Header.Get("Content-Type") != "application/vnd.ant.fit" ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "Bigger-hill-loop.fit") {
		t.Fatalf("unexpected FIT response %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := e.get("/routes/"+routeID+".fit", e.otherToken); resp.StatusCode != http.StatusNotFound {
		t.Errorf("FIT course of another user's route: status %d, want 404", resp.StatusCode)
	}

	imported, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Gpx: new(string(gpx)),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute from GPX: %v", err)
	}
	if imported.GetRoute().GetName() != "Bigger hill loop" ||
		imported.GetRoute().GetSource() != routesv1.RouteSource_ROUTE_SOURCE_GPX ||
		!approx(imported.GetRoute().GetStats().GetDistanceMeters(), 3336, 5) {
		t.Errorf("unexpected imported route %v", imported.GetRoute())
	}
	// Imported tracks are split into editable legs about a kilometer long.
	if n := len(imported.GetRoute().GetWaypoints()); n < 3 || n > 5 {
		t.Errorf("got %d waypoints for a 3.3km import", n)
	}

	if _, err := e.routes.DeleteRoute(ctx, routesv1.DeleteRouteRequest_builder{Id: routeID}.Build()); err != nil {
		t.Fatalf("DeleteRoute: %v", err)
	}
	_, err = e.routes.GetRoute(ctx, routesv1.GetRouteRequest_builder{Id: routeID}.Build())
	wantCode(t, err, connect.CodeNotFound)
}

func TestRouteForecast(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// A 22km route north along a meridian, climbing to 200m.
	created, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Name:      "Long day",
		Waypoints: routesv1.Waypoints_builder{Waypoints: latLngs(0, 0, 0.2, 0)}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	start := time.Now().Add(24 * time.Hour).UTC().Truncate(24 * time.Hour).Add(9 * time.Hour)
	resp, err := e.routes.GetRouteForecast(ctx, routesv1.GetRouteForecastRequest_builder{
		RouteId:            new(created.GetRoute().GetId()),
		StartTime:          timestamppb.New(start),
		SpeedKmh:           10,
		ClimbMetersPerHour: 400,
	}.Build())
	if err != nil {
		t.Fatalf("GetRouteForecast: %v", err)
	}
	// 22.2km at 10km/h plus 200m at 400m/h is 2.72h.
	if got := time.Duration(resp.GetDurationSeconds()) * time.Second; !approx(got.Hours(), 2.72, 0.02) {
		t.Errorf("got duration %v", got)
	}
	var kinds []routesv1.ForecastPointKind
	for _, p := range resp.GetPoints() {
		kinds = append(kinds, p.GetKind())
	}
	// The high point is the finish, so it isn't repeated.
	want := []routesv1.ForecastPointKind{
		routesv1.ForecastPointKind_FORECAST_POINT_KIND_START,
		routesv1.ForecastPointKind_FORECAST_POINT_KIND_INTERVAL,
		routesv1.ForecastPointKind_FORECAST_POINT_KIND_INTERVAL,
		routesv1.ForecastPointKind_FORECAST_POINT_KIND_FINISH,
	}
	if len(kinds) != len(want) {
		t.Fatalf("got kinds %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("got kinds %v, want %v", kinds, want)
		}
	}
	second := resp.GetPoints()[1]
	if !second.GetTime().AsTime().Equal(start.Add(time.Hour)) || second.GetWeatherDescription() != "Thunderstorm" {
		t.Errorf("unexpected second point %v", second)
	}
	finish := resp.GetPoints()[len(resp.GetPoints())-1]
	if !approx(finish.GetSnowDepthCm(), 80, 1e-9) || finish.GetSnowfallPast_72HoursCm() != 40 {
		t.Errorf("got %v cm deep and %v cm new at the finish, want 80 and 40", finish.GetSnowDepthCm(), finish.GetSnowfallPast_72HoursCm())
	}
	if w := resp.GetWarnings(); len(w) != 2 || !strings.HasPrefix(w[0], "Thunderstorms possible: ") ||
		!strings.HasPrefix(w[1], "Heavy recent snowfall, check the avalanche forecast: ") || !strings.Contains(w[1], "40 cm in the past 3 days") {
		t.Errorf("unexpected warnings %v", w)
	}

	// A GPX file or a list of coordinates can be forecast too.
	_, err = e.routes.GetRouteForecast(ctx, routesv1.GetRouteForecastRequest_builder{
		Path:      routesv1.Path_builder{Points: latLngs(0, 0, 0.05, 0)}.Build(),
		StartTime: timestamppb.New(start),
		SpeedKmh:  8,
	}.Build())
	if err != nil {
		t.Fatalf("GetRouteForecast with path: %v", err)
	}

	// Forecasts are only available for the next 2 weeks.
	_, err = e.routes.GetRouteForecast(ctx, routesv1.GetRouteForecastRequest_builder{
		RouteId:   new(created.GetRoute().GetId()),
		StartTime: timestamppb.New(time.Now().AddDate(0, 1, 0)),
		SpeedKmh:  10,
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
}

func profiles(p ...routesv1.RoutingProfile) []routesv1.RoutingProfile {
	return p
}

func wantLegProfiles(t *testing.T, route *routesv1.Route, want ...routesv1.RoutingProfile) {
	t.Helper()
	got := route.GetLegProfiles()
	if len(got) != len(want) {
		t.Fatalf("got leg profiles %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got leg profiles %v, want %v", got, want)
		}
	}
}

func TestRouteLegProfiles(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	const (
		trail    = routesv1.RoutingProfile_ROUTING_PROFILE_TRAIL
		shortest = routesv1.RoutingProfile_ROUTING_PROFILE_SHORTEST
		straight = routesv1.RoutingProfile_ROUTING_PROFILE_STRAIGHT
		heatmap  = routesv1.RoutingProfile_ROUTING_PROFILE_HEATMAP
		unset    = routesv1.RoutingProfile_ROUTING_PROFILE_UNSPECIFIED
	)

	// A trail leg followed by a straight leg: only the trail leg is routed.
	preview, err := e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{
		Waypoints:   latLngs(0, 0, 0.01, 0, 0.02, 0),
		LegProfiles: profiles(trail, straight),
	}.Build())
	if err != nil {
		t.Fatalf("PreviewRoute: %v", err)
	}
	if calls := e.router.routeCallCount(); calls != 1 {
		t.Errorf("got %d routing calls, want 1", calls)
	}
	wantLegProfiles(t, preview.GetRoute(), trail, straight)
	stats := preview.GetRoute().GetStats()
	terrain := stats.GetTerrain()
	if len(terrain) != 2 || terrain[0].GetTerrain() != geov1.Terrain_TERRAIN_TRAIL || !approx(terrain[0].GetFraction(), 0.5, 0.01) ||
		terrain[1].GetTerrain() != geov1.Terrain_TERRAIN_UNSPECIFIED {
		t.Errorf("unexpected terrain %v", terrain)
	}

	// There must be one profile per leg.
	_, err = e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{
		Waypoints:   latLngs(0, 0, 0.01, 0, 0.02, 0),
		LegProfiles: profiles(trail),
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
	if !strings.Contains(err.Error(), "one entry per leg") {
		t.Errorf("error should explain leg_profiles: %v", err)
	}

	// Unset leg profiles use the route's profile. Legs routed for the preview
	// are reused.
	created, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Name: "Mixed",
		Waypoints: routesv1.Waypoints_builder{
			Waypoints:   latLngs(0, 0, 0.01, 0, 0.02, 0),
			Profile:     straight,
			LegProfiles: profiles(trail, unset),
		}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	if calls := e.router.routeCallCount(); calls != 1 {
		t.Errorf("got %d routing calls, want 1", calls)
	}
	wantLegProfiles(t, created.GetRoute(), trail, straight)
	if created.GetRoute().GetProfile() != straight {
		t.Errorf("got profile %v, want STRAIGHT", created.GetRoute().GetProfile())
	}
	routeID := created.GetRoute().GetId()

	// Extending the route with a heatmap leg only routes the new leg. The user
	// has no activities, so it is routed like a trail leg.
	updated, err := e.routes.UpdateRoute(ctx, routesv1.UpdateRouteRequest_builder{
		Id: routeID,
		Waypoints: routesv1.Waypoints_builder{
			Waypoints:   latLngs(0, 0, 0.01, 0, 0.02, 0, 0.03, 0),
			LegProfiles: profiles(trail, straight, heatmap),
		}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRoute: %v", err)
	}
	if calls := e.router.routeCallCount(); calls != 2 {
		t.Errorf("got %d routing calls, want 2", calls)
	}
	wantLegProfiles(t, updated.GetRoute(), trail, straight, heatmap)
	if !approx(updated.GetRoute().GetStats().GetDistanceMeters(), 3336, 5) {
		t.Errorf("unexpected distance %v", updated.GetRoute().GetStats().GetDistanceMeters())
	}

	// Previewing changes to the saved route reuses its legs and only routes
	// the changed leg, without saving.
	preview, err = e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{
		Waypoints:   latLngs(0, 0, 0.01, 0, 0.02, 0, 0.03, 0),
		LegProfiles: profiles(trail, shortest, heatmap),
		RouteId:     routeID,
	}.Build())
	if err != nil {
		t.Fatalf("PreviewRoute: %v", err)
	}
	if calls := e.router.routeCallCount(); calls != 3 {
		t.Errorf("got %d routing calls, want 3", calls)
	}
	wantLegProfiles(t, preview.GetRoute(), trail, shortest, heatmap)
	got, err := e.routes.GetRoute(ctx, routesv1.GetRouteRequest_builder{Id: routeID}.Build())
	if err != nil {
		t.Fatalf("GetRoute: %v", err)
	}
	wantLegProfiles(t, got.GetRoute(), trail, straight, heatmap)

	// The way back of an out and back route reuses the outbound legs.
	before := e.router.routeCallCount()
	outAndBack, err := e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{
		Waypoints: latLngs(1, 0, 1.01, 0, 1.02, 0, 1.01, 0, 1, 0),
	}.Build())
	if err != nil {
		t.Fatalf("PreviewRoute: %v", err)
	}
	if calls := e.router.routeCallCount() - before; calls != 2 {
		t.Errorf("got %d routing calls, want 2", calls)
	}
	stats = outAndBack.GetRoute().GetStats()
	if !approx(stats.GetDistanceMeters(), 4448, 5) || !approx(stats.GetElevationGainMeters(), 20, 0.5) || !approx(stats.GetElevationLossMeters(), 20, 0.5) {
		t.Errorf("unexpected out and back stats %v", stats)
	}
	if terrain := stats.GetTerrain(); len(terrain) != 1 || terrain[0].GetTerrain() != geov1.Terrain_TERRAIN_TRAIL {
		t.Errorf("unexpected out and back terrain %v", terrain)
	}

	// Previewing another user's route is not allowed.
	_, err = e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{
		Waypoints: latLngs(0, 0, 0.01, 0),
		RouteId:   "00000000-0000-0000-0000-000000000000",
	}.Build())
	wantCode(t, err, connect.CodeNotFound)
}

func TestHeatmapRouting(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// The user has run a path between two points.
	from, to := geo.Point{Lat: 0, Lng: 0}, geo.Point{Lat: 0.01, Lng: 0}
	if _, _, err := e.activities.UpsertActivity(ctx, e.userID, &activities.ActivitySummary{
		StravaID:        1,
		Name:            "Usual loop",
		SportType:       "TrailRun",
		StartTime:       time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
		StartDateLocal:  time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Timezone:        "(GMT+00:00) Europe/London",
		SummaryPolyline: geo.EncodePolyline(fakeRoutePath(from, to, 0.003)),
		RawJSON:         []byte("{}"),
	}); err != nil {
		t.Fatal(err)
	}

	// The heatmap profile asks routing to prefer the user's activities.
	preview, err := e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{
		Waypoints: latLngs(from.Lat, from.Lng, to.Lat, to.Lng),
		Profile:   routesv1.RoutingProfile_ROUTING_PROFILE_HEATMAP,
	}.Build())
	if err != nil {
		t.Fatalf("PreviewRoute: %v", err)
	}
	wantLegProfiles(t, preview.GetRoute(), routesv1.RoutingProfile_ROUTING_PROFILE_HEATMAP)
	prefer := e.router.lastPrefer()
	if len(prefer) != 1 || len(prefer[0]) < 3 {
		t.Fatalf("got preferred paths %v, want the activity's track", prefer)
	}
	var maxLng float64
	for _, p := range prefer[0] {
		maxLng = max(maxLng, p.Lng)
	}
	if !approx(maxLng, 0.003, 0.0001) {
		t.Errorf("preferred path reaches longitude %v, want 0.003", maxLng)
	}

	// The trail profile doesn't.
	if _, err := e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{
		Waypoints: latLngs(from.Lat, from.Lng, 0.02, 0),
	}.Build()); err != nil {
		t.Fatalf("PreviewRoute: %v", err)
	}
	if prefer := e.router.lastPrefer(); len(prefer) != 0 {
		t.Errorf("trail profile preferred %d paths", len(prefer))
	}

	// Away from the user's activities, there is nothing to prefer.
	if _, err := e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{
		Waypoints: latLngs(10, 10, 10.01, 10),
		Profile:   routesv1.RoutingProfile_ROUTING_PROFILE_HEATMAP,
	}.Build()); err != nil {
		t.Fatalf("PreviewRoute: %v", err)
	}
	if prefer := e.router.lastPrefer(); len(prefer) != 0 {
		t.Errorf("got %d preferred paths away from the activities", len(prefer))
	}
}

func TestCheckTrackCoverage(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	coverage := func(req *routesv1.CheckTrackCoverageRequest) float64 {
		t.Helper()
		resp, err := e.routes.CheckTrackCoverage(ctx, req)
		if err != nil {
			t.Fatalf("CheckTrackCoverage: %v", err)
		}
		return resp.GetCoveredFraction()
	}

	// The fake router has map data up to latitude 80.
	if got := coverage(routesv1.CheckTrackCoverageRequest_builder{Gpx: new(cornerGPX(0, 0))}.Build()); got != 1 {
		t.Errorf("got coverage %v inside the map data, want 1", got)
	}
	if got := coverage(routesv1.CheckTrackCoverageRequest_builder{Gpx: new(cornerGPX(85, 0))}.Build()); got != 0 {
		t.Errorf("got coverage %v outside the map data, want 0", got)
	}
	if got := coverage(routesv1.CheckTrackCoverageRequest_builder{Gpx: new(cornerGPX(79.995, 0))}.Build()); got <= 0 || got >= 1 {
		t.Errorf("got coverage %v across the edge of the map data, want between 0 and 1", got)
	}

	// A track that doesn't move is covered if its points are.
	stillGPX := func(lat, lng float64) *string {
		return new(fmt.Sprintf(`<?xml version="1.0"?><gpx version="1.1" creator="test"><trk><trkseg>`+
			`<trkpt lat="%[1]f" lon="%[2]f"></trkpt><trkpt lat="%[1]f" lon="%[2]f"></trkpt></trkseg></trk></gpx>`, lat, lng))
	}
	if got := coverage(routesv1.CheckTrackCoverageRequest_builder{Gpx: stillGPX(0, 0)}.Build()); got != 1 {
		t.Errorf("got coverage %v for a track standing still inside the map data, want 1", got)
	}
	if got := coverage(routesv1.CheckTrackCoverageRequest_builder{Gpx: stillGPX(85, 0)}.Build()); got != 0 {
		t.Errorf("got coverage %v for a track standing still outside the map data, want 0", got)
	}

	// Saved routes can be checked, e.g. before making an exact route
	// editable.
	created, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Gpx: new(cornerGPX(85, 0)),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	if got := coverage(routesv1.CheckTrackCoverageRequest_builder{RouteId: new(created.GetRoute().GetId())}.Build()); got != 0 {
		t.Errorf("got coverage %v for a route outside the map data, want 0", got)
	}

	_, err = e.routes.CheckTrackCoverage(ctx, routesv1.CheckTrackCoverageRequest_builder{}.Build())
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("got %v without a track, want InvalidArgument", err)
	}
}

// cornerGPX returns a GPX track from (lat, lng) north for about 1.1km and
// then east for about 1.1km, with points every 10m that wobble 5m from side
// to side like a GPS recording.
func cornerGPX(lat, lng float64) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><gpx version="1.1" creator="test"><trk><name>Corner</name><trkseg>`)
	for i := 0; i <= 200; i++ {
		wobble := 0.000045 * float64(i%2*2-1)
		p := geo.Point{Lat: lat + 0.01*float64(min(i, 100))/100, Lng: lng + wobble}
		if i > 100 {
			p = geo.Point{Lat: lat + 0.01 + wobble, Lng: lng + 0.01*float64(i-100)/100}
		}
		fmt.Fprintf(&b, `<trkpt lat="%f" lon="%f"><ele>%f</ele></trkpt>`, p.Lat, p.Lng, p.Lat*1000)
	}
	b.WriteString(`</trkseg></trk></gpx>`)
	return b.String()
}

func TestSnapTrack(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	const (
		trail    = routesv1.RoutingProfile_ROUTING_PROFILE_TRAIL
		straight = routesv1.RoutingProfile_ROUTING_PROFILE_STRAIGHT
		unset    = routesv1.RoutingProfile_ROUTING_PROFILE_UNSPECIFIED
		snap     = routesv1.TrackConversion_TRACK_CONVERSION_SNAP
	)

	// A recorded track along mapped ways becomes a route with a waypoint
	// at each end and one at the corner.
	snapped, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Gpx:        new(cornerGPX(0, 0)),
		Conversion: snap,
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	route := snapped.GetRoute()
	wantLegProfiles(t, route, trail, trail)
	corner := route.GetWaypoints()[1]
	if !approx(corner.GetLat(), 0.01, 0.0003) || !approx(corner.GetLng(), 0, 0.0003) {
		t.Errorf("got corner waypoint %v, want near 0.01, 0", corner)
	}
	if !approx(route.GetStats().GetDistanceMeters(), 2224, 30) || route.GetSource() != routesv1.RouteSource_ROUTE_SOURCE_GPX || route.GetName() != "Corner" {
		t.Errorf("unexpected snapped route %v", route)
	}

	// The snapped route is edited like any other: moving the corner only
	// routes the two legs next to it.
	before := e.router.routeCallCount()
	waypoints := route.GetWaypoints()
	_, err = e.routes.UpdateRoute(ctx, routesv1.UpdateRouteRequest_builder{
		Id: route.GetId(),
		Waypoints: routesv1.Waypoints_builder{
			Waypoints:   []*geov1.LatLng{waypoints[0], latLngs(0.02, 0)[0], waypoints[2]},
			LegProfiles: route.GetLegProfiles(),
		}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRoute: %v", err)
	}
	if calls := e.router.routeCallCount() - before; calls != 2 {
		t.Errorf("got %d routing calls, want 2", calls)
	}

	// Where nothing can be routed, the track is followed with straight legs,
	// with its recorded elevations rather than looked up ones.
	fills := e.mapData.fills
	offTrail, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Gpx:        new(cornerGPX(85, 0)),
		Conversion: snap,
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	wantLegProfiles(t, offTrail.GetRoute(), straight, straight)
	if e.mapData.fills != fills || offTrail.GetWarning() != "" {
		t.Errorf("got %d elevation lookups and warning %q, want none", e.mapData.fills-fills, offTrail.GetWarning())
	}
	if gain := offTrail.GetRoute().GetStats().GetElevationGainMeters(); !approx(gain, 10, 1) {
		t.Errorf("got %v m gain, want the recorded 10 m", gain)
	}

	// If snapping fails, the track is followed with straight legs that can
	// be edited, with a warning.
	unsnapped, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Gpx:        new(cornerGPX(65, 0)),
		Conversion: snap,
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	if unsnapped.GetWarning() == "" || len(unsnapped.GetRoute().GetWaypoints()) < 2 {
		t.Errorf("got warning %q and %d waypoints, want a warning and the track", unsnapped.GetWarning(), len(unsnapped.GetRoute().GetWaypoints()))
	}
	for _, profile := range unsnapped.GetRoute().GetLegProfiles() {
		if profile != straight {
			t.Fatalf("unsnapped route has leg profiles %v, want straight legs", unsnapped.GetRoute().GetLegProfiles())
		}
	}
	// The recorded track, with its wobbles, is longer than the snapped one.
	if !approx(unsnapped.GetRoute().GetStats().GetDistanceMeters(), 2292, 10) {
		t.Errorf("got %v m, want the recorded track's 2292 m", unsnapped.GetRoute().GetStats().GetDistanceMeters())
	}

	// By default the track is kept exactly, and the result can be snapped
	// later.
	exact, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Gpx: new(cornerGPX(0, 0)),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	for _, profile := range exact.GetRoute().GetLegProfiles() {
		if profile != unset {
			t.Fatalf("exact import has leg profiles %v", exact.GetRoute().GetLegProfiles())
		}
	}
	copied, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		RouteId:    new(exact.GetRoute().GetId()),
		Conversion: snap,
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute from route: %v", err)
	}
	wantLegProfiles(t, copied.GetRoute(), trail, trail)
	if copied.GetRoute().GetName() != "Corner" || copied.GetRoute().GetId() == exact.GetRoute().GetId() {
		t.Errorf("unexpected copy %v", copied.GetRoute())
	}
}

func TestRouteWarnings(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// An out-and-back repeats the way out, which the preview points out,
	// from a little after the turnaround, at 1.1 km, to the end.
	preview, err := e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{
		Waypoints: latLngs(0, 0, 0.01, 0, 0, 0),
	}.Build())
	if err != nil {
		t.Fatalf("PreviewRoute: %v", err)
	}
	warnings := preview.GetRoute().GetWarnings()
	if len(warnings) != 1 || warnings[0].GetKind() != routesv1.RouteWarningKind_ROUTE_WARNING_KIND_REPEATED_SECTION ||
		!approx(warnings[0].GetStartDistanceMeters(), 1300, 200) || !approx(warnings[0].GetEndDistanceMeters(), 2224, 100) ||
		warnings[0].GetEarlierStartDistanceMeters() < warnings[0].GetEarlierEndDistanceMeters() ||
		!strings.Contains(warnings[0].GetMessage(), "goes back over") {
		t.Errorf("unexpected warnings of an out-and-back %v", warnings)
	}

	// A waypoint far from any mapped path is pointed out, but not one on a
	// straight leg, which is meant to leave the map.
	created, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Name: "Off the map",
		Waypoints: routesv1.Waypoints_builder{
			Waypoints:   latLngs(0, 0, offMapLat, 0, 0.02, 0.01),
			LegProfiles: []routesv1.RoutingProfile{routesv1.RoutingProfile_ROUTING_PROFILE_TRAIL, routesv1.RoutingProfile_ROUTING_PROFILE_STRAIGHT},
		}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	warnings = created.GetRoute().GetWarnings()
	if len(warnings) != 1 || warnings[0].GetKind() != routesv1.RouteWarningKind_ROUTE_WARNING_KIND_OFF_MAP_WAYPOINT ||
		warnings[0].GetWaypointIndex() != 1 || !strings.Contains(warnings[0].GetMessage(), "Waypoint 2") {
		t.Errorf("unexpected warnings of a waypoint off the map %v", warnings)
	}
	// They're kept with the route, but not listed.
	got, err := e.routes.GetRoute(ctx, routesv1.GetRouteRequest_builder{Id: created.GetRoute().GetId()}.Build())
	if err != nil || len(got.GetRoute().GetWarnings()) != 1 {
		t.Errorf("GetRoute: got warnings %v (%v), want the off-map waypoint", got.GetRoute().GetWarnings(), err)
	}
	listed, err := e.routes.ListRoutes(ctx, routesv1.ListRoutesRequest_builder{}.Build())
	if err != nil || len(listed.GetRoutes()) != 1 || len(listed.GetRoutes()[0].GetWarnings()) != 0 {
		t.Errorf("ListRoutes: got %v (%v), want the route without warnings", listed.GetRoutes(), err)
	}
}
