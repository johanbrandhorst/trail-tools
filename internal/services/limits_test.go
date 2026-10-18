package services_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/apilimit"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
	tripsv1 "github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// gpxOf returns a GPX file with a track through the points.
func gpxOf(points []geo.Point) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><gpx version="1.1"><trk><name>Test</name><trkseg>` + "\n")
	for _, p := range points {
		fmt.Fprintf(&b, `<trkpt lat="%.7f" lon="%.7f"><ele>%.1f</ele></trkpt>`+"\n", p.Lat, p.Lng, p.Ele)
	}
	b.WriteString("</trkseg></trk></gpx>\n")
	return b.String()
}

func TestRequestLimits(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// Requests larger than a megabyte are rejected before they're read.
	_, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
		Race: racesv1.Race_builder{Name: strings.Repeat("a", apilimit.MaxRequestBytes), Date: "2027-06-12"}.Build(),
	}.Build())
	wantCode(t, err, connect.CodeResourceExhausted)
	if err == nil || !strings.Contains(err.Error(), "send less data") {
		t.Errorf("got error %v, want it to say to send less data", err)
	}

	// Signed in users can upload larger files, like a GPX file of a 40 km
	// track recorded every meter.
	var track []geo.Point
	for i := range 40_000 {
		track = append(track, geo.Point{Lat: 46 + float64(i)/111_320, Lng: 7, Ele: 1000})
	}
	gpx := gpxOf(track)
	if len(gpx) <= apilimit.MaxRequestBytes {
		t.Fatalf("the GPX file is %d bytes, want more than %d", len(gpx), apilimit.MaxRequestBytes)
	}
	imported, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Gpx:           new(gpx),
		IncludePoints: true,
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute from a large GPX file: %v", err)
	}
	// The straight track is simplified to the ends of its legs, about a
	// kilometer long.
	if n := len(imported.GetRoute().GetPoints()); n > 100 {
		t.Errorf("got %d points for a straight 40 km track, want it simplified", n)
	}
	if d := imported.GetRoute().GetStats().GetDistanceMeters(); !approx(d, 40_000, 100) {
		t.Errorf("got a %.0f m route, want 40 km", d)
	}

	// Callers that aren't signed in can't, and are told to sign in without
	// reading their request.
	_, err = e.anonTrips.UploadTripPhoto(ctx, tripsv1.UploadTripPhotoRequest_builder{
		ReportId: "00000000-0000-0000-0000-000000000000",
		Data:     make([]byte, apilimit.MaxRequestBytes+1),
	}.Build())
	wantCode(t, err, connect.CodeUnauthenticated)
	_, err = e.anonTrips.UploadTripPhoto(ctx, tripsv1.UploadTripPhotoRequest_builder{
		ReportId: "00000000-0000-0000-0000-000000000000",
		Data:     []byte("small"),
	}.Build())
	wantCode(t, err, connect.CodeUnauthenticated)

	// Requests with more values than any needs, which take far more memory
	// decoded than encoded, are rejected before they're decoded, however
	// small.
	waypoints := make([]*geov1.LatLng, apilimit.MaxRequestValues+1)
	for i := range waypoints {
		waypoints[i] = geov1.LatLng_builder{}.Build()
	}
	_, err = e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{Waypoints: waypoints}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
	if err == nil || !strings.Contains(err.Error(), "send less data") {
		t.Errorf("got error %v, want it to say to send less data", err)
	}
}

func TestRouteLimits(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	latLng := func(lat, lng float64) *geov1.LatLng { return geov1.LatLng_builder{Lat: lat, Lng: lng}.Build() }

	// Legs can be up to 50 km long.
	for _, profile := range []routesv1.RoutingProfile{routesv1.RoutingProfile_ROUTING_PROFILE_STRAIGHT, routesv1.RoutingProfile_ROUTING_PROFILE_TRAIL} {
		_, err := e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{
			Waypoints: []*geov1.LatLng{latLng(46, 7), latLng(46.4, 7)},
			Profile:   profile,
		}.Build())
		if err != nil {
			t.Errorf("PreviewRoute with a 44 km %v leg: %v", profile, err)
		}
		// Longer legs would take too much memory, as straight legs have
		// points every 25 m.
		_, err = e.routes.PreviewRoute(ctx, routesv1.PreviewRouteRequest_builder{
			Waypoints: []*geov1.LatLng{latLng(46, 7), latLng(46.4, 7), latLng(-46, -173)},
			Profile:   profile,
		}.Build())
		wantCode(t, err, connect.CodeInvalidArgument)
		if err == nil || !strings.Contains(err.Error(), "from waypoint 2 to 3") || !strings.Contains(err.Error(), "add waypoints") {
			t.Errorf("got error %v for a leg around the world, want it to name the leg and say to add waypoints", err)
		}
	}

	// A tiny GPX file can't make a track around the world, which would be
	// tens of millions of points resampled, either exactly or snapped.
	var hops []geo.Point
	for i := range 50 {
		hops = append(hops, geo.Point{Lng: float64(i%2) * 180})
	}
	for _, conversion := range []routesv1.TrackConversion{routesv1.TrackConversion_TRACK_CONVERSION_EXACT, routesv1.TrackConversion_TRACK_CONVERSION_SNAP} {
		_, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
			Gpx:        new(gpxOf(hops)),
			Conversion: conversion,
		}.Build())
		wantCode(t, err, connect.CodeInvalidArgument)
		if err == nil || !strings.Contains(err.Error(), "stray points") {
			t.Errorf("got error %v for a track around the world, want it to say to remove stray points", err)
		}
	}
	for name, req := range map[string]*routesv1.GetRouteForecastRequest{
		"gpx":  routesv1.GetRouteForecastRequest_builder{Gpx: new(gpxOf(hops))}.Build(),
		"path": routesv1.GetRouteForecastRequest_builder{Path: routesv1.Path_builder{Points: []*geov1.LatLng{latLng(0, 0), latLng(0, 180), latLng(0, 0)}}.Build()}.Build(),
	} {
		req.SetStartTime(timestamppb.New(time.Now().Add(24 * time.Hour)))
		req.SetSpeedKmh(10)
		_, err := e.routes.GetRouteForecast(ctx, req)
		wantCode(t, err, connect.CodeInvalidArgument)
		if err == nil || !strings.Contains(err.Error(), "stray points") {
			t.Errorf("GetRouteForecast from a %s around the world: got error %v, want it to say to remove stray points", name, err)
		}
	}

	// A track with a long gap, like where a recording paused, is snapped
	// without filling in the gap.
	var gap []geo.Point
	for i := range 100 {
		gap = append(gap, geo.Point{Lat: 46 + float64(i)*10/111_320, Lng: 7})
	}
	for i := range 100 {
		gap = append(gap, geo.Point{Lat: 47 + float64(i)*10/111_320, Lng: 7})
	}
	snapped, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Gpx:        new(gpxOf(gap)),
		Conversion: routesv1.TrackConversion_TRACK_CONVERSION_SNAP,
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute from a track with a gap: %v", err)
	}
	if d := snapped.GetRoute().GetStats().GetDistanceMeters(); d < 110_000 || d > 115_000 {
		t.Errorf("got a %.0f m route, want about 113 km", d)
	}
}

func TestHeatmapLimits(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// More activities in an area than are listed at once.
	for i := range activities.MaxTracksInBounds + 1 {
		lng := 7 + float64(i)*1e-5
		e.addTrackedActivity(int64(i+1), []geo.Point{{Lat: 46, Lng: lng}, {Lat: 46.01, Lng: lng}}, nil)
	}
	bounds := geov1.BoundingBox_builder{South: 45.99, West: 6.99, North: 46.03, East: 7.02}.Build()
	tracks, err := e.geo.ListHeatmapTracks(ctx, geov1.ListHeatmapTracksRequest_builder{Bounds: bounds, ToleranceMeters: 50}.Build())
	if err != nil {
		t.Fatalf("ListHeatmapTracks: %v", err)
	}
	if n := len(tracks.GetPolylines()); !tracks.GetTruncated() || n != activities.MaxTracksInBounds {
		t.Errorf("got %d tracks, truncated %v, want %d truncated", n, tracks.GetTruncated(), activities.MaxTracksInBounds)
	}
	heat, err := e.geo.GetPersonalHeatmap(ctx, geov1.GetPersonalHeatmapRequest_builder{Bounds: bounds}.Build())
	if err != nil {
		t.Fatalf("GetPersonalHeatmap: %v", err)
	}
	if !heat.GetTruncated() || heat.GetActivityCount() != activities.MaxTracksInBounds {
		t.Errorf("got %d activities, truncated %v, want %d truncated", heat.GetActivityCount(), heat.GetTruncated(), activities.MaxTracksInBounds)
	}
	// A smaller area has them all.
	small := geov1.BoundingBox_builder{South: 45.99, West: 6.99, North: 46.03, East: 7.00505}.Build()
	tracks, err = e.geo.ListHeatmapTracks(ctx, geov1.ListHeatmapTracksRequest_builder{Bounds: small, ToleranceMeters: 50}.Build())
	if err != nil {
		t.Fatalf("ListHeatmapTracks: %v", err)
	}
	if n := len(tracks.GetPolylines()); tracks.GetTruncated() || n != 506 {
		t.Errorf("got %d tracks in a smaller area, truncated %v, want 506 untruncated", n, tracks.GetTruncated())
	}
}

// TestHeatmapLargeArea checks that heatmaps of large areas are made from the
// activities' summary polylines, which take much less memory.
func TestHeatmapLargeArea(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// A run whose summary polyline is 0.001° east of its track.
	var track []geo.Point
	for i := range 201 {
		track = append(track, geo.Point{Lat: 46 + float64(i)*10/111_320, Lng: 7})
	}
	e.addTrackedActivity(1, []geo.Point{{Lat: 46, Lng: 7.001}, {Lat: track[200].Lat, Lng: 7.001}}, track)
	for name, tt := range map[string]struct {
		bounds  *geov1.BoundingBox
		wantLng float64
	}{
		"small": {geov1.BoundingBox_builder{South: 45.99, West: 6.99, North: 46.03, East: 7.01}.Build(), 7},
		"large": {geov1.BoundingBox_builder{South: 45, West: 6, North: 47, East: 8}.Build(), 7.001},
	} {
		resp, err := e.geo.ListHeatmapTracks(ctx, geov1.ListHeatmapTracksRequest_builder{Bounds: tt.bounds, ToleranceMeters: 1}.Build())
		if err != nil {
			t.Fatalf("ListHeatmapTracks: %v", err)
		}
		if len(resp.GetPolylines()) != 1 {
			t.Fatalf("%s area: got %d tracks, want 1", name, len(resp.GetPolylines()))
		}
		points, err := geo.DecodePolyline(resp.GetPolylines()[0])
		if err != nil {
			t.Fatal(err)
		}
		if !approx(points[0].Lng, tt.wantLng, 1e-5) {
			t.Errorf("%s area: got a track at longitude %v, want %v", name, points[0].Lng, tt.wantLng)
		}
	}
}
