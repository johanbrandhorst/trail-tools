package services_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
	stravav1 "github.com/johanbrandhorst/trail-tools/internal/gen/strava/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	stravasvc "github.com/johanbrandhorst/trail-tools/internal/services/strava"
	"github.com/johanbrandhorst/trail-tools/internal/strava"
)

// fakeStrava is a Strava API with a list of activities. The first request
// for streams hits the rate limit.
type fakeStrava struct {
	mu          sync.Mutex
	activities  []*strava.Activity
	photos      map[int64][]*strava.Photo
	rateLimited bool
	// Whether the athlete revoked access.
	revoked     bool
	refreshes   int
	streamCalls int
	// The activities streams were fetched for, and the priorities.
	streamIDs        []int64
	streamPriorities []strava.Priority
	// If set, fetching the streams of this activity once signals
	// streamsBlocked and waits for unblockStreams.
	blockStreams   int64
	streamsBlocked chan struct{}
	unblockStreams chan struct{}
	// The before times activities were listed with.
	listed []time.Time
	// The page size of listed activities, if not zero.
	pageSize int
	// If set, listing activities fails at the rate limit once, after
	// listing this many pages.
	listLimitAfter int
	// If set, listing activities fails with it.
	listErr error
	// Fetching the streams of these activities fails.
	failStreams map[int64]bool
	// The number of activities fetched.
	activityCalls int
	// If set, each refresh replaces the refresh token, and refreshing with
	// a replaced one fails as if access was revoked, as Strava may. The
	// current refresh token is validRefreshToken, or any if unset.
	rotateTokens      bool
	validRefreshToken string
	// How long refreshing takes, to let concurrent refreshes overlap.
	refreshDelay time.Duration
	// Whether the list of activities reports no photos, as Strava's
	// sometimes does for activities whose details have them.
	listWithoutPhotos bool
}

func newFakeStrava() *fakeStrava {
	f := &fakeStrava{photos: map[int64][]*strava.Photo{}, failStreams: map[int64]bool{}}
	start := time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC) // A Monday.
	for i := range 3 {
		a := &strava.Activity{
			ID:                 int64(1000 + i),
			Name:               fmt.Sprintf("Run %d", i),
			SportType:          "TrailRun",
			StartDate:          start.AddDate(0, 0, i*4),
			StartDateLocal:     start.AddDate(0, 0, i*4).Format(time.RFC3339),
			Timezone:           "(GMT+00:00) Europe/London",
			Distance:           10000 + float64(i)*1000,
			MovingTime:         3600,
			ElapsedTime:        3700,
			TotalElevationGain: 500,
			AverageCadence:     85,
		}
		a.Map.SummaryPolyline = geo.EncodePolyline([]geo.Point{{Lat: 0, Lng: 0}, {Lat: 0.01, Lng: 0}})
		a.Raw, _ = json.Marshal(a)
		f.activities = append(f.activities, a)
	}
	// The third activity is a manual entry without streams.
	f.activities[2].Map.SummaryPolyline = ""
	// The first activity has two photos, one with a location.
	f.activities[0].TotalPhotoCount = 2
	f.photos[1000] = []*strava.Photo{
		{
			UniqueID: "p1", Caption: "Summit", URL: "https://strava.example/p1.jpg", Width: 2048, Height: 1536,
			Lat: 0.005, Lng: 0, HasLocation: true, CreatedAt: start.Add(30 * time.Minute), Raw: []byte("{}"),
		},
		{UniqueID: "p2", URL: "https://strava.example/p2.jpg", CreatedAt: start.Add(45 * time.Minute), Raw: []byte("{}")},
	}
	return f
}

func (f *fakeStrava) Refresh(_ context.Context, refreshToken string) (*strava.Token, error) {
	f.mu.Lock()
	delay := f.refreshDelay
	f.mu.Unlock()
	time.Sleep(delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revoked || f.validRefreshToken != "" && refreshToken != f.validRefreshToken {
		return nil, strava.ErrUnauthorized
	}
	f.refreshes++
	if f.rotateTokens {
		refreshToken = fmt.Sprintf("refresh-%d", f.refreshes)
		f.validRefreshToken = refreshToken
	}
	return &strava.Token{AccessToken: "fresh", RefreshToken: refreshToken, Expiry: time.Now().Add(6 * time.Hour)}, nil
}

func (f *fakeStrava) ListActivities(_ context.Context, token string, before time.Time, perPage int) ([]*strava.Activity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if token != "fresh" {
		return nil, strava.ErrUnauthorized
	}
	if f.listLimitAfter > 0 && len(f.listed) == f.listLimitAfter {
		f.listLimitAfter = 0
		return nil, &strava.RateLimitError{ResetAt: time.Now()}
	}
	f.listed = append(f.listed, before)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.pageSize > 0 {
		perPage = f.pageSize
	}
	// Like Strava, newest first.
	var result []*strava.Activity
	for _, a := range f.activities {
		if before.IsZero() || a.StartDate.Before(before) {
			if f.listWithoutPhotos {
				listed := *a
				listed.TotalPhotoCount = 0
				a = &listed
			}
			result = append(result, a)
		}
	}
	slices.SortFunc(result, func(a, b *strava.Activity) int {
		return b.StartDate.Compare(a.StartDate)
	})
	return result[:min(perPage, len(result))], nil
}

func (f *fakeStrava) GetActivity(_ context.Context, _ string, id int64) (*strava.Activity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activityCalls++
	for _, a := range f.activities {
		if a.ID == id {
			detail := *a
			detail.Description = "Felt good"
			return &detail, nil
		}
	}
	return nil, strava.ErrNotFound
}

func (f *fakeStrava) GetStreams(ctx context.Context, _ string, id int64) (*strava.Streams, error) {
	f.mu.Lock()
	if id != 0 && id == f.blockStreams {
		f.blockStreams = 0
		f.mu.Unlock()
		f.streamsBlocked <- struct{}{}
		select {
		case <-f.unblockStreams:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		f.mu.Lock()
	}
	defer f.mu.Unlock()
	f.streamCalls++
	f.streamIDs = append(f.streamIDs, id)
	f.streamPriorities = append(f.streamPriorities, strava.PriorityFrom(ctx))
	if !f.rateLimited {
		f.rateLimited = true
		return nil, &strava.RateLimitError{ResetAt: time.Now()}
	}
	if f.failStreams[id] {
		return nil, fmt.Errorf("unexpected status 500 from Strava for activity %d", id)
	}
	if id == 1002 {
		return nil, strava.ErrNotFound
	}
	streams := &strava.Streams{}
	for i := range 101 {
		streams.Time = append(streams.Time, int32(i*36))
		streams.Distance = append(streams.Distance, float64(i)*100)
		streams.LatLng = append(streams.LatLng, [2]float64{float64(i) * 0.0009, 0})
		streams.Altitude = append(streams.Altitude, float64(i)*5)
		streams.Cadence = append(streams.Cadence, 85)
	}
	return streams, nil
}

// connectStrava stores a Strava connection with an expired token for the
// user.
func (e *env) connectStrava() {
	e.t.Helper()
	e.connectStravaAs(e.userID, 7)
}

// connectStravaAs stores a Strava connection with an expired token for a
// user, as the athlete.
func (e *env) connectStravaAs(userID pgtype.UUID, athleteID int64) {
	e.t.Helper()
	if _, err := e.activities.UpsertStravaConnection(context.Background(), &activities.StravaConnection{
		UserID:       userID,
		AthleteID:    athleteID,
		AthleteName:  "Test Runner",
		AccessToken:  "expired",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(-time.Hour),
	}); err != nil {
		e.t.Fatal(err)
	}
}

// waitForSync waits until the sync is idle.
func (e *env) waitForSync() *stravav1.StravaConnection {
	e.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := e.stravaAPI.GetStravaConnection(context.Background(), stravav1.GetStravaConnectionRequest_builder{}.Build())
		if err != nil {
			e.t.Fatal(err)
		}
		conn := resp.GetConnection()
		if !e.syncer.Running(e.userID) && conn.GetSyncState() != stravav1.SyncState_SYNC_STATE_RUNNING {
			return conn
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatal("sync did not finish")
	return nil
}

func TestStravaSyncAndActivities(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	_, err := e.stravaAPI.SyncStrava(ctx, stravav1.SyncStravaRequest_builder{}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)

	e.connectStrava()
	if _, err := e.stravaAPI.SyncStrava(ctx, stravav1.SyncStravaRequest_builder{}.Build()); err != nil {
		t.Fatalf("SyncStrava: %v", err)
	}
	conn := e.waitForSync()
	if conn.GetSyncState() != stravav1.SyncState_SYNC_STATE_IDLE || conn.GetActivityCount() != 3 || conn.GetPendingStreamCount() != 0 {
		t.Fatalf("unexpected connection after sync %v", conn)
	}
	if conn.GetAthleteName() != "Test Runner" || !conn.HasLastSyncTime() || conn.GetAthleteUrl() != "https://www.strava.com/athletes/7" {
		t.Errorf("unexpected connection %v", conn)
	}
	// The expired token was refreshed, and the rate limited request retried.
	if e.strava.refreshes != 1 || e.strava.streamCalls != 4 {
		t.Errorf("got %d refreshes and %d stream calls, want 1 and 4", e.strava.refreshes, e.strava.streamCalls)
	}

	// Activities are listed most recent first, paginated.
	page1, err := e.activity.ListActivities(ctx, activitiesv1.ListActivitiesRequest_builder{PageSize: 2}.Build())
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if len(page1.GetActivities()) != 2 || page1.GetActivities()[0].GetName() != "Run 2" || page1.GetNextPageToken() == "" {
		t.Fatalf("unexpected first page %v", page1)
	}
	page2, err := e.activity.ListActivities(ctx, activitiesv1.ListActivitiesRequest_builder{
		PageSize:  2,
		PageToken: page1.GetNextPageToken(),
	}.Build())
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if len(page2.GetActivities()) != 1 || page2.GetNextPageToken() != "" {
		t.Fatalf("unexpected second page %v", page2)
	}
	run0 := page2.GetActivities()[0]
	if run0.GetDescription() != "Felt good" || !run0.GetHasStreams() || run0.GetAverageCadenceSpm() != 170 ||
		run0.GetStartDateLocal() != "2026-09-07" || run0.GetTimezone() != "Europe/London" {
		t.Errorf("unexpected activity %v", run0)
	}
	filtered, err := e.activity.ListActivities(ctx, activitiesv1.ListActivitiesRequest_builder{
		StartDate:  "2026-09-08",
		EndDate:    "2026-09-12",
		SportTypes: []string{"TrailRun"},
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.GetActivities()) != 1 || filtered.GetActivities()[0].GetName() != "Run 1" {
		t.Errorf("unexpected filtered activities %v", filtered.GetActivities())
	}

	// Activities can be sorted biggest first. Run 0 climbed the most, and
	// Run 1 took the longest.
	if _, err := e.db.Exec(ctx, `update strava_activities set
		elevation_gain_meters = case name when 'Run 0' then 900 when 'Run 1' then 300 else 600 end,
		moving_time_seconds = case name when 'Run 1' then 5400 else 3000 end`); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		sort activitiesv1.ActivitySort
		want []string
	}{
		{activitiesv1.ActivitySort_ACTIVITY_SORT_DISTANCE, []string{"Run 2", "Run 1", "Run 0"}},
		{activitiesv1.ActivitySort_ACTIVITY_SORT_ELEVATION_GAIN, []string{"Run 0", "Run 2", "Run 1"}},
		{activitiesv1.ActivitySort_ACTIVITY_SORT_MOVING_TIME, []string{"Run 1", "Run 2", "Run 0"}},
	} {
		sorted, err := e.activity.ListActivities(ctx, activitiesv1.ListActivitiesRequest_builder{Sort: tt.sort}.Build())
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, a := range sorted.GetActivities() {
			names = append(names, a.GetName())
		}
		if !slices.Equal(names, tt.want) {
			t.Errorf("sorted by %v: got %v, want %v", tt.sort, names, tt.want)
		}
	}
	if _, err := e.db.Exec(ctx, `update strava_activities set elevation_gain_meters = 500, moving_time_seconds = 3600`); err != nil {
		t.Fatal(err)
	}

	// Streams are downsampled.
	detail, err := e.activity.GetActivity(ctx, activitiesv1.GetActivityRequest_builder{
		Id:              run0.GetId(),
		IncludeStreams:  true,
		MaxStreamPoints: 11,
	}.Build())
	if err != nil {
		t.Fatalf("GetActivity: %v", err)
	}
	if got := detail.GetActivity().GetStravaUrl(); got != "https://www.strava.com/activities/"+run0.GetStravaId() {
		t.Errorf("got Strava URL %q", got)
	}
	streams := detail.GetStreams()
	if len(streams.GetLat()) != 11 || streams.GetAltitudeMeters()[10] != 500 || streams.GetCadenceSpm()[0] != 170 {
		t.Errorf("unexpected streams %v", streams)
	}
	// Photos are synced with the details.
	photos := detail.GetPhotos()
	if run0.GetPhotoCount() != 2 || len(photos) != 2 || photos[0].GetId() != "p1" || photos[0].GetCaption() != "Summit" ||
		photos[0].GetLocation().GetLat() != 0.005 || photos[0].GetWidth() != 2048 || photos[1].HasLocation() ||
		!photos[1].GetTakenTime().AsTime().Equal(time.Date(2026, 9, 7, 8, 45, 0, 0, time.UTC)) {
		t.Errorf("unexpected photos %v", photos)
	}

	// Weekly summaries start on Monday and include empty weeks.
	summary, err := e.activity.SummarizeActivities(ctx, activitiesv1.SummarizeActivitiesRequest_builder{
		StartDate: "2026-09-09",
		EndDate:   "2026-09-27",
	}.Build())
	if err != nil {
		t.Fatalf("SummarizeActivities: %v", err)
	}
	weeks := summary.GetSummaries()
	if len(weeks) != 3 || weeks[0].GetStartDate() != "2026-09-07" || weeks[0].GetActivityCount() != 2 ||
		weeks[0].GetDistanceMeters() != 21000 || weeks[1].GetActivityCount() != 1 || weeks[2].GetActivityCount() != 0 {
		t.Errorf("unexpected weekly summaries %v", weeks)
	}
	if weeks[0].GetLongestDistanceMeters() != 11000 || weeks[0].GetElevationGainMeters() != 1000 {
		t.Errorf("unexpected first week %v", weeks[0])
	}

	// The heatmap counts each activity once per cell.
	heatmap, err := e.geo.GetPersonalHeatmap(ctx, geov1.GetPersonalHeatmapRequest_builder{
		Bounds:         geov1.BoundingBox_builder{South: -0.001, West: -0.001, North: 0.02, East: 0.001}.Build(),
		CellSizeMeters: 100,
	}.Build())
	if err != nil {
		t.Fatalf("GetPersonalHeatmap: %v", err)
	}
	if heatmap.GetActivityCount() != 2 || len(heatmap.GetCells()) == 0 || heatmap.GetCells()[0].GetActivityCount() != 2 {
		t.Errorf("unexpected heatmap: %d activities, %d cells", heatmap.GetActivityCount(), len(heatmap.GetCells()))
	}
	// The tracks for drawing it are clipped to the bounds and simplified:
	// the straight tracks become two points each.
	heatTracks, err := e.geo.ListHeatmapTracks(ctx, geov1.ListHeatmapTracksRequest_builder{
		Bounds: geov1.BoundingBox_builder{South: -0.001, West: -0.001, North: 0.005, East: 0.001}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("ListHeatmapTracks: %v", err)
	}
	if len(heatTracks.GetPolylines()) != 2 || heatTracks.GetTruncated() {
		t.Fatalf("got %d heatmap tracks, want 2", len(heatTracks.GetPolylines()))
	}
	for _, polyline := range heatTracks.GetPolylines() {
		points, err := geo.DecodePolyline(polyline)
		if err != nil {
			t.Fatal(err)
		}
		if len(points) != 2 || points[1].Lat > 0.005 {
			t.Errorf("unexpected heatmap track %v", points)
		}
	}

	// An activity can become a route.
	route, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		ActivityId: new(run0.GetId()),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute from activity: %v", err)
	}
	if route.GetRoute().GetName() != "Run 0" || route.GetRoute().GetSource() != routesv1.RouteSource_ROUTE_SOURCE_ACTIVITY ||
		!approx(route.GetRoute().GetStats().GetDistanceMeters(), 10007, 5) || !approx(route.GetRoute().GetStats().GetElevationGainMeters(), 500, 1) {
		t.Errorf("unexpected route %v", route.GetRoute())
	}
	// A manual activity has no track.
	_, err = e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		ActivityId: new(page1.GetActivities()[0].GetId()),
	}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)
}

func TestGeo(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	center := geov1.LatLng_builder{Lat: 46, Lng: 7}.Build()

	places, err := e.geo.SearchPlaces(ctx, geov1.SearchPlacesRequest_builder{Query: "Matterhorn"}.Build())
	if err != nil {
		t.Fatalf("SearchPlaces: %v", err)
	}
	if len(places.GetPlaces()) != 1 || places.GetPlaces()[0].GetBounds().GetNorth() != 1.1 {
		t.Errorf("unexpected places %v", places.GetPlaces())
	}

	trails, err := e.geo.FindTrails(ctx, geov1.FindTrailsRequest_builder{Center: center}.Build())
	if err != nil {
		t.Fatalf("FindTrails: %v", err)
	}
	trail := trails.GetTrails()[0]
	if trail.GetName() != "Ridge Trail" || trail.GetTerrain() != geov1.Terrain_TERRAIN_TRAIL || !approx(trail.GetLengthMeters(), 445, 2) || len(trail.GetGeometry()) != 0 {
		t.Errorf("unexpected trail %v", trail)
	}

	// The geometry can be simplified: the straight trail is its ends.
	for _, tt := range []struct {
		tolerance float64
		want      int
	}{{0, 5}, {10, 2}} {
		trails, err := e.geo.FindTrails(ctx, geov1.FindTrailsRequest_builder{
			Center:                  center,
			IncludeGeometry:         true,
			GeometryToleranceMeters: tt.tolerance,
		}.Build())
		if err != nil {
			t.Fatalf("FindTrails: %v", err)
		}
		if got := len(trails.GetTrails()[0].GetGeometry()); got != tt.want {
			t.Errorf("got %d points at a tolerance of %v meters, want %d", got, tt.tolerance, tt.want)
		}
	}

	pois, err := e.geo.FindPointsOfInterest(ctx, geov1.FindPointsOfInterestRequest_builder{
		Center: center,
		Kinds:  []geov1.PointOfInterestKind{geov1.PointOfInterestKind_POINT_OF_INTEREST_KIND_PEAK, geov1.PointOfInterestKind_POINT_OF_INTEREST_KIND_WATER},
	}.Build())
	if err != nil {
		t.Fatalf("FindPointsOfInterest: %v", err)
	}
	points := pois.GetPoints()
	if len(points) != 2 || points[0].GetKind() != geov1.PointOfInterestKind_POINT_OF_INTEREST_KIND_WATER || points[1].GetElevationMeters() != 1234 {
		t.Errorf("unexpected points %v", points)
	}

	if points[0].GetElevationMeters() != 0 || points[1].GetOsmId() != "1" {
		t.Errorf("unexpected points %v", points)
	}

	_, err = e.geo.FindTrails(ctx, geov1.FindTrailsRequest_builder{Center: center, RadiusMeters: 50000}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	// Areas without imported map data are reported as such.
	antarctica := geov1.LatLng_builder{Lat: -75, Lng: 0}.Build()
	_, err = e.geo.FindTrails(ctx, geov1.FindTrailsRequest_builder{Center: antarctica}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)
	_, err = e.geo.FindPointsOfInterest(ctx, geov1.FindPointsOfInterestRequest_builder{Center: antarctica}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)
}

func TestScheduledStravaSync(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	e.connectStrava()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	syncer := stravasvc.NewSyncer(ctx, log, errreport.NewLocal(log), e.activities, e.strava, false, nil)

	// Background syncs are disabled.
	if syncer.Start(e.userID, false) {
		t.Fatal("Start should do nothing without background syncs")
	}
	// The first sync stops at the rate limit instead of waiting.
	if !syncer.SyncOnce(ctx, e.userID, false) {
		t.Fatal("SyncOnce didn't run")
	}
	conn, err := e.activities.GetStravaConnection(ctx, e.userID)
	if err != nil {
		t.Fatal(err)
	}
	if conn.SyncState != activities.SyncStateRateLimited {
		t.Fatalf("got sync state %q, want rate limited", conn.SyncState)
	}
	// The rate limit has reset, so the scheduled sync continues it.
	if err := syncer.SyncDue(ctx); err != nil {
		t.Fatalf("SyncDue: %v", err)
	}
	if conn, err = e.activities.GetStravaConnection(ctx, e.userID); err != nil {
		t.Fatal(err)
	}
	total, pending, err := e.activities.CountActivities(ctx, e.userID, allActivities)
	if err != nil {
		t.Fatal(err)
	}
	if conn.SyncState != activities.SyncStateIdle || total != 3 || pending != 0 {
		t.Errorf("got state %q with %d activities and %d pending, want idle, 3 and 0", conn.SyncState, total, pending)
	}
	// Recently synced connections aren't due.
	calls := e.strava.streamCalls
	if err := syncer.SyncDue(ctx); err != nil || e.strava.streamCalls != calls {
		t.Errorf("an up to date connection was synced again: %v", err)
	}

	// An incremental sync picks up new activities, and stops listing at the
	// ones from before the latest synced activity.
	latest := e.strava.activities[2]
	e.strava.upsertActivity(&strava.Activity{
		ID: 2000, Name: "New run", SportType: "Run",
		StartDate: latest.StartDate.AddDate(0, 0, 1), StartDateLocal: latest.StartDate.AddDate(0, 0, 1).Format(time.RFC3339),
	})
	e.strava.listed = nil
	if !syncer.SyncOnce(ctx, e.userID, false) {
		t.Fatal("SyncOnce didn't run")
	}
	if total, _, err = e.activities.CountActivities(ctx, e.userID, allActivities); err != nil || total != 4 {
		t.Errorf("got %d activities (%v), want 4", total, err)
	}
	// The oldest activity is before the overlap, so the second page isn't
	// listed.
	if len(e.strava.listed) != 1 {
		t.Errorf("listed activities %d times, want once", len(e.strava.listed))
	}
}

func TestStravaSyncResumesListing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	e.connectStrava()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	syncer := stravasvc.NewSyncer(ctx, log, errreport.NewLocal(log), e.activities, e.strava, false, nil)
	e.strava.pageSize = 2
	e.strava.listLimitAfter = 1

	// The first sync lists the two newest activities, then stops at the rate
	// limit.
	syncer.SyncOnce(ctx, e.userID, false)
	conn, err := e.activities.GetStravaConnection(ctx, e.userID)
	if err != nil {
		t.Fatal(err)
	}
	total, _, err := e.activities.CountActivities(ctx, e.userID, allActivities)
	if err != nil {
		t.Fatal(err)
	}
	if conn.SyncState != activities.SyncStateRateLimited || total != 2 || !conn.ListBefore.Equal(e.strava.activities[1].StartDate) {
		t.Fatalf("got state %q with %d activities listed before %v, want rate limited with 2 before %v",
			conn.SyncState, total, conn.ListBefore, e.strava.activities[1].StartDate)
	}
	// The next incremental syncs continue the listing, although the oldest
	// activity is more than a week older than the latest. The second stops
	// at the rate limit for streams.
	syncer.SyncOnce(ctx, e.userID, false)
	syncer.SyncOnce(ctx, e.userID, false)
	if conn, err = e.activities.GetStravaConnection(ctx, e.userID); err != nil {
		t.Fatal(err)
	}
	if total, _, err = e.activities.CountActivities(ctx, e.userID, allActivities); err != nil {
		t.Fatal(err)
	}
	if conn.SyncState != activities.SyncStateIdle || total != 3 || !conn.ListBefore.IsZero() {
		t.Errorf("got state %q with %d activities listed before %v, want idle with 3 and the listing finished",
			conn.SyncState, total, conn.ListBefore)
	}
}

// fakeTrigger is a Cloud Scheduler job that syncs due connections.
type fakeTrigger struct {
	mu   sync.Mutex
	runs int
	err  error
}

func (f *fakeTrigger) Run(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs++
	return f.err
}

func TestTriggeredStravaSync(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	e.connectStrava()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	trigger := &fakeTrigger{}
	syncer := stravasvc.NewSyncer(ctx, log, errreport.NewLocal(log), e.activities, e.strava, false, trigger)

	// A requested sync is marked running and triggered, without syncing
	// within the request.
	if err := syncer.Request(ctx, e.userID, false); err != nil {
		t.Fatalf("Request: %v", err)
	}
	conn, err := e.activities.GetStravaConnection(ctx, e.userID)
	if err != nil {
		t.Fatal(err)
	}
	if trigger.runs != 1 || conn.SyncState != activities.SyncStateRunning || !syncer.Active(conn) || len(e.strava.listed) != 0 {
		t.Fatalf("got %d runs, state %q and %d listings, want 1 run of an active sync and no listings", trigger.runs, conn.SyncState, len(e.strava.listed))
	}
	// Requesting it again while it's starting doesn't trigger it again.
	if err := syncer.Request(ctx, e.userID, false); err != nil || trigger.runs != 1 {
		t.Fatalf("got %d runs (%v), want 1", trigger.runs, err)
	}
	// The triggered scheduled sync runs it, stopping at the rate limit.
	if err := syncer.SyncDue(ctx); err != nil {
		t.Fatalf("SyncDue: %v", err)
	}
	if conn, err = e.activities.GetStravaConnection(ctx, e.userID); err != nil {
		t.Fatal(err)
	}
	if conn.SyncState != activities.SyncStateRateLimited {
		t.Fatalf("got state %q, want rate limited", conn.SyncState)
	}

	// A running sync that hasn't made progress for a while was interrupted.
	if err := e.activities.UpdateStravaSyncState(ctx, e.userID, activities.SyncStateRunning, "Fetched 3 activities", time.Time{}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(ctx, "update strava_connections set sync_state_time = now() - interval '10 minutes'"); err != nil {
		t.Fatal(err)
	}
	if conn, err = e.activities.GetStravaConnection(ctx, e.userID); err != nil {
		t.Fatal(err)
	}
	if syncer.Active(conn) {
		t.Error("a stale running sync is active")
	}

	// If the trigger fails, a full sync runs within the request instead.
	trigger.err = errors.New("unavailable")
	if err := syncer.Request(ctx, e.userID, true); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if conn, err = e.activities.GetStravaConnection(ctx, e.userID); err != nil {
		t.Fatal(err)
	}
	total, pending, err := e.activities.CountActivities(ctx, e.userID, allActivities)
	if err != nil {
		t.Fatal(err)
	}
	if trigger.runs != 2 || conn.SyncState != activities.SyncStateIdle || total != 3 || pending != 0 || !conn.ListBefore.IsZero() {
		t.Errorf("got %d runs, state %q, %d activities and %d pending, want 2 runs and an idle connection with 3 and none pending",
			trigger.runs, conn.SyncState, total, pending)
	}
	if last := e.strava.listed[len(e.strava.listed)-2]; last.IsZero() {
		t.Errorf("the full sync listed from the latest activity, want from the time it was requested")
	}
}
