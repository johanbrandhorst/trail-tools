// Package services_test contains integration tests of the API services,
// running against a real database through the HTTP handlers and
// authentication middleware.
package services_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/bs-tools/trailrunn.ing/internal/activities"
	"github.com/bs-tools/trailrunn.ing/internal/apilimit"
	"github.com/bs-tools/trailrunn.ing/internal/authn"
	"github.com/bs-tools/trailrunn.ing/internal/avalanche"
	"github.com/bs-tools/trailrunn.ing/internal/blob"
	"github.com/bs-tools/trailrunn.ing/internal/errreport"
	activitiesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/activities/v1"
	"github.com/bs-tools/trailrunn.ing/internal/gen/activities/v1/activitiesv1connect"
	"github.com/bs-tools/trailrunn.ing/internal/gen/auth/v1/authv1connect"
	"github.com/bs-tools/trailrunn.ing/internal/gen/garmin/v1/garminv1connect"
	"github.com/bs-tools/trailrunn.ing/internal/gen/geo/v1/geov1connect"
	"github.com/bs-tools/trailrunn.ing/internal/gen/lactate/v1/lactatev1connect"
	"github.com/bs-tools/trailrunn.ing/internal/gen/oauth/v1/oauthv1connect"
	"github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1/plansv1connect"
	"github.com/bs-tools/trailrunn.ing/internal/gen/races/v1/racesv1connect"
	"github.com/bs-tools/trailrunn.ing/internal/gen/routes/v1/routesv1connect"
	"github.com/bs-tools/trailrunn.ing/internal/gen/routing/v1/routingv1connect"
	"github.com/bs-tools/trailrunn.ing/internal/gen/strava/v1/stravav1connect"
	"github.com/bs-tools/trailrunn.ing/internal/gen/trips/v1/tripsv1connect"
	"github.com/bs-tools/trailrunn.ing/internal/gen/users/v1/usersv1connect"
	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/bs-tools/trailrunn.ing/internal/lactate"
	"github.com/bs-tools/trailrunn.ing/internal/livetrack"
	"github.com/bs-tools/trailrunn.ing/internal/mapdata"
	"github.com/bs-tools/trailrunn.ing/internal/mcp"
	"github.com/bs-tools/trailrunn.ing/internal/oauth"
	"github.com/bs-tools/trailrunn.ing/internal/plans"
	"github.com/bs-tools/trailrunn.ing/internal/races"
	"github.com/bs-tools/trailrunn.ing/internal/routes"
	"github.com/bs-tools/trailrunn.ing/internal/routing"
	"github.com/bs-tools/trailrunn.ing/internal/services/activity"
	"github.com/bs-tools/trailrunn.ing/internal/services/auth"
	garminsvc "github.com/bs-tools/trailrunn.ing/internal/services/garmin"
	geosvc "github.com/bs-tools/trailrunn.ing/internal/services/geo"
	lactatesvc "github.com/bs-tools/trailrunn.ing/internal/services/lactate"
	mapdatasvc "github.com/bs-tools/trailrunn.ing/internal/services/mapdata"
	oauthsvc "github.com/bs-tools/trailrunn.ing/internal/services/oauth"
	"github.com/bs-tools/trailrunn.ing/internal/services/plan"
	"github.com/bs-tools/trailrunn.ing/internal/services/race"
	"github.com/bs-tools/trailrunn.ing/internal/services/raceplan"
	"github.com/bs-tools/trailrunn.ing/internal/services/racereport"
	"github.com/bs-tools/trailrunn.ing/internal/services/route"
	stravasvc "github.com/bs-tools/trailrunn.ing/internal/services/strava"
	"github.com/bs-tools/trailrunn.ing/internal/services/trip"
	"github.com/bs-tools/trailrunn.ing/internal/services/user"
	"github.com/bs-tools/trailrunn.ing/internal/store"
	"github.com/bs-tools/trailrunn.ing/internal/store/storetest"
	"github.com/bs-tools/trailrunn.ing/internal/strava"
	"github.com/bs-tools/trailrunn.ing/internal/trips"
	"github.com/bs-tools/trailrunn.ing/internal/users"
	"github.com/jackc/pgx/v5/pgtype"
)

// env is a running server for a test, with clients authenticated as a user.
type env struct {
	t      *testing.T
	db     *store.DB
	userID pgtype.UUID
	// The user's API token, and session ID.
	token      string
	session    string
	otherToken string
	// The other user, whose token is otherToken.
	otherUserID pgtype.UUID
	// The directory trip report photos are stored in.
	photoDir string
	// Stores activities' streams and Strava's responses.
	activityBlobs *blob.Dir
	srv           *httptest.Server
	mapData       *fakeMapData
	router        *fakeRouter
	avalanches    *fakeAvalanches
	activities    *activities.Repository
	syncer        *stravasvc.Syncer
	strava        *fakeStrava
	liveTrack     *fakeLiveTrack
	garmin        *fakeGarmin
	garminSyncer  *garminsvc.Syncer
	garminTrigger *fakeTrigger
	// The URL Garmin sends notifications to.
	garminWebhookURL string

	// The user's settings, and OAuth connections below, are signed in with
	// the session; the other clients with the API token.
	users     usersv1connect.UserServiceClient
	activity  activitiesv1connect.ActivityServiceClient
	stravaAPI stravav1connect.StravaServiceClient
	garminAPI garminv1connect.GarminServiceClient
	// The Garmin service signed in with the session, to disconnect.
	garminSession garminv1connect.GarminServiceClient
	routes        routesv1connect.RouteServiceClient
	geo           geov1connect.GeoServiceClient
	mapDataAPI    routingv1connect.MapDataServiceClient
	imports       *fakeImportRunner
	plans         plansv1connect.TrainingPlanServiceClient
	exercises     plansv1connect.ExerciseServiceClient
	// Workout shares of the user, anyone and another user.
	shares      plansv1connect.WorkoutShareServiceClient
	anonShares  plansv1connect.WorkoutShareServiceClient
	otherShares plansv1connect.WorkoutShareServiceClient
	races       racesv1connect.RaceServiceClient
	racePlans   racesv1connect.RacePlanServiceClient
	// Race reports of the user, anyone and another user.
	raceReports      racesv1connect.RaceReportServiceClient
	anonRaceReports  racesv1connect.RaceReportServiceClient
	otherRaceReports racesv1connect.RaceReportServiceClient
	anonPlans        racesv1connect.RacePlanServiceClient
	otherRaces       racesv1connect.RaceServiceClient
	otherPlans       racesv1connect.RacePlanServiceClient
	// Another user's training plans.
	otherTraining  plansv1connect.TrainingPlanServiceClient
	otherExercises plansv1connect.ExerciseServiceClient
	oauth          oauthv1connect.OAuthServiceClient
	anonOAuth      oauthv1connect.OAuthServiceClient
	trips          tripsv1connect.TripReportServiceClient
	anonTrips      tripsv1connect.TripReportServiceClient
	otherTrips     tripsv1connect.TripReportServiceClient
	// Lactate tests of the user, and of another user.
	lactate      lactatev1connect.LactateTestServiceClient
	otherLactate lactatev1connect.LactateTestServiceClient
}

// consentPage stands in for the frontend's OAuth consent page.
const consentPage = "consent page"

// frontendPage is what the test server answers for the frontend's pages
// under paths the server also serves files at.
const frontendPage = "frontend"

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	report := errreport.NewLocal(log)
	db := storetest.New(t)
	userRepo, err := users.NewRepository(log, db)
	if err != nil {
		t.Fatal(err)
	}
	activityBlobs, err := blob.NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	activityRepo, err := activities.NewRepository(report, db, activityBlobs)
	if err != nil {
		t.Fatal(err)
	}
	routeRepo, err := routes.NewRepository(log, db)
	if err != nil {
		t.Fatal(err)
	}
	raceRepo, err := races.NewRepository(log, db)
	if err != nil {
		t.Fatal(err)
	}
	planRepo, err := plans.NewRepository(log, db)
	if err != nil {
		t.Fatal(err)
	}
	tripRepo, err := trips.NewRepository(log, db)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: db, mapData: &fakeMapData{}, router: &fakeRouter{}, avalanches: &fakeAvalanches{}, activities: activityRepo, activityBlobs: activityBlobs, strava: newFakeStrava()}
	e.userID = storetest.NewUser(t, db)
	_, e.token, err = userRepo.CreateAPIToken(ctx, e.userID, "test")
	if err != nil {
		t.Fatal(err)
	}
	e.session, err = userRepo.CreateSession(ctx, e.userID, nil, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	e.otherUserID = storetest.NewUser(t, db)
	_, e.otherToken, err = userRepo.CreateAPIToken(ctx, e.otherUserID, "other")
	if err != nil {
		t.Fatal(err)
	}

	syncCtx, cancel := context.WithCancel(ctx)
	e.syncer = stravasvc.NewSyncer(syncCtx, log, report, activityRepo, e.strava, true, nil)
	t.Cleanup(func() {
		cancel()
		e.syncer.Wait()
	})

	authService, err := auth.NewService(log, report, userRepo, "http://localhost:8080", bootstrapToken)
	if err != nil {
		t.Fatal(err)
	}
	lactateRepo, err := lactate.NewRepository(log, db)
	if err != nil {
		t.Fatal(err)
	}
	activityService := activity.NewService(log, report, activityRepo, lactateRepo, strava.WebURL)
	stravaService := stravasvc.NewService(log, report, activityRepo, e.syncer, nil, strava.WebURL)
	routeService := route.NewService(log, report, routeRepo, activityRepo, e.router, e.mapData)
	routeService.SetAvalanches(e.avalanches)
	geoService := geosvc.NewService(log, report, e.mapData, e.mapData, activityRepo)
	geoService.SetAvalanches(e.avalanches)
	planService := plan.NewService(log, report, planRepo, routeRepo, activityRepo, e.mapData)
	planService.SetSharing(planRepo, "https://trail.example")
	planService.SetCalendars(planRepo)
	planService.SetForests(routing.NewFinder(e.db))
	planService.SetAvalanches(e.avalanches)
	e.garmin = newFakeGarmin()
	// Notifications are handled when tests say, as by the scheduled
	// sync the trigger runs.
	e.garminTrigger = &fakeTrigger{}
	e.garminSyncer = garminsvc.NewSyncer(log, report, e.garmin, activityRepo, planRepo, routeRepo, lactateRepo, e.garminTrigger)
	planService.OnWorkoutsChanged(e.garminSyncer.WorkoutsChanged)
	garminService := garminsvc.NewService(report, activityRepo, e.garminSyncer)
	raceService := race.NewService(log, report, raceRepo, routeRepo)
	e.liveTrack = &fakeLiveTrack{}
	racePlanService := raceplan.NewService(log, report, raceRepo, routeRepo, e.liveTrack, "https://trail.example")
	e.photoDir = t.TempDir()
	photos, err := blob.NewDir(e.photoDir)
	if err != nil {
		t.Fatal(err)
	}
	userService := user.NewService(log, report, userRepo, userRepo, userRepo, photos)
	tripService := trip.NewService(log, report, tripRepo, activityRepo, e.syncer, photos, userRepo, &http.Client{}, "https://trail.example")
	raceReportService := racereport.NewService(log, report, raceRepo, activityRepo, e.syncer, tripRepo, racePlanService, userRepo, photos, tripRepo, "https://trail.example")

	// Start listening before creating the handler, as the OAuth server needs
	// the URL of the server.
	e.srv = httptest.NewUnstartedServer(nil)
	t.Cleanup(e.srv.Close)
	oauthServer := oauth.NewServer(log, report, userRepo, "http://"+e.srv.Listener.Addr().String(), "/mcp")
	resourceMetadataURL := oauthServer.ResourceMetadataURL()

	mux := http.NewServeMux()
	server := connect.NewServer()
	oauthv1connect.RegisterOAuthServiceHandler(server, oauthsvc.NewService(log, report, oauthServer, userRepo))
	connecthttp.Mount(mux, server)
	oauthServer.RegisterHandlers(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, consentPage)
	}))
	srv2 := connect.NewServer()
	usersv1connect.RegisterUserServiceHandler(srv2, userService)
	authv1connect.RegisterAuthServiceHandler(srv2, authService)
	activitiesv1connect.RegisterActivityServiceHandler(srv2, activityService)
	stravav1connect.RegisterStravaServiceHandler(srv2, stravaService)
	garminv1connect.RegisterGarminServiceHandler(srv2, garminService)
	routesv1connect.RegisterRouteServiceHandler(srv2, routeService)
	geov1connect.RegisterGeoServiceHandler(srv2, geoService)
	plansv1connect.RegisterTrainingPlanServiceHandler(srv2, planService)
	plansv1connect.RegisterExerciseServiceHandler(srv2, plan.NewExerciseService(log, report, planRepo))
	plansv1connect.RegisterWorkoutShareServiceHandler(srv2, planService)
	racesv1connect.RegisterRaceServiceHandler(srv2, raceService)
	racesv1connect.RegisterRacePlanServiceHandler(srv2, racePlanService)
	tripsv1connect.RegisterTripReportServiceHandler(srv2, tripService)
	racesv1connect.RegisterRaceReportServiceHandler(srv2, raceReportService)
	lactateService := lactatesvc.NewService(log, report, lactateRepo, "https://trail.example")
	lactatev1connect.RegisterLactateTestServiceHandler(srv2, lactateService)
	apilimit.Mount(mux, srv2)
	mux.Handle(trip.PhotoPath, tripService.PhotoHandler())
	mux.Handle(racereport.PhotoPath, raceReportService.PhotoHandler())
	mux.Handle(user.AvatarPath, userService.AvatarHandler())
	mux.Handle(geosvc.SlopeTilePath, geoService.SlopeTileHandler())
	mux.Handle(lactatesvc.WorkoutPath, lactateService.WorkoutHandler())
	mux.Handle(trip.ProfilePath, raceReportService.PageHandler(false, tripService.PageHandler(false)))

	mux.Handle("/routes/", routeService.DownloadHandler(http.NotFoundHandler()))
	mux.Handle("/workouts/", planService.DownloadHandler(http.NotFoundHandler()))
	mux.Handle(plan.ShareDownloadPath, planService.ShareDownloadHandler())
	// The frontend's calendar pages answer with frontendPage.
	mux.Handle(plan.CalendarPath, planService.CalendarHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, frontendPage)
	})))
	mux.Handle("/race-plans/", racePlanService.CourseHandler(http.NotFoundHandler()))
	e.imports = &fakeImportRunner{}
	srv3 := connect.NewServer()
	routingv1connect.RegisterMapDataServiceHandler(srv3, mapdatasvc.NewService(log, report, db, fakeExtracts{}, e.imports))
	connecthttp.Mount(mux, srv3)
	mcpServer, err := mcp.NewServer(log, report,
		mcp.Service{Name: racesv1connect.RaceServiceName, Handler: raceService},
		mcp.Service{Name: routesv1connect.RouteServiceName, Handler: routeService},
	)
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle("/mcp", authn.RequireUser(resourceMetadataURL, mcpServer.Handler()))
	handler, err := authn.NewAuthnMiddleware(ctx, log, report, userRepo, lactatesvc.RecorderMiddleware(mux), authn.WithResourceMetadata(resourceMetadataURL))
	if err != nil {
		t.Fatal(err)
	}
	// Upload links authenticate with their own token, which the authn
	// middleware would take for an API token, so they aren't behind it, as
	// in main.go.
	root := http.NewServeMux()
	root.Handle(trip.PhotoUploadPath, tripService.PhotoUploadHandler())
	root.Handle(racereport.PhotoUploadPath, raceReportService.PhotoUploadHandler())
	root.Handle("/", handler)
	garminWebhook, err := garminsvc.NewWebhook(log, report, activityRepo, e.garminSyncer, "http://"+e.srv.Listener.Addr().String()+garminsvc.WebhookPath, garminWebhookSecret, garminClientID)
	if err != nil {
		t.Fatal(err)
	}
	root.Handle(garminWebhook.Pattern(), garminWebhook)
	e.srv.Config.Handler = root
	e.srv.Start()

	client := e.srv.Client()
	authed := connect.NewClient(connecthttp.NewTransport(client, e.srv.URL), bearerInterceptor(e.token))
	anon := connect.NewClient(connecthttp.NewTransport(client, e.srv.URL))
	// Accounts are managed in the web app, which is signed in with a session
	// cookie rather than a token.
	signedIn := connect.NewClient(connecthttp.NewTransport(client, e.srv.URL), sessionInterceptor(e.session))
	e.users = usersv1connect.NewUserServiceClient(signedIn)
	e.activity = activitiesv1connect.NewActivityServiceClient(authed)
	e.stravaAPI = stravav1connect.NewStravaServiceClient(authed)
	e.garminAPI = garminv1connect.NewGarminServiceClient(authed)
	e.garminSession = garminv1connect.NewGarminServiceClient(signedIn)
	e.garminWebhookURL = garminWebhook.CallbackURL()
	e.routes = routesv1connect.NewRouteServiceClient(authed)
	e.geo = geov1connect.NewGeoServiceClient(authed)
	e.mapDataAPI = routingv1connect.NewMapDataServiceClient(authed)
	e.plans = plansv1connect.NewTrainingPlanServiceClient(authed)
	e.exercises = plansv1connect.NewExerciseServiceClient(authed)
	e.shares = plansv1connect.NewWorkoutShareServiceClient(authed)
	e.anonShares = plansv1connect.NewWorkoutShareServiceClient(anon)
	e.races = racesv1connect.NewRaceServiceClient(authed)
	e.racePlans = racesv1connect.NewRacePlanServiceClient(authed)
	e.anonPlans = racesv1connect.NewRacePlanServiceClient(anon)
	other := connect.NewClient(connecthttp.NewTransport(client, e.srv.URL), bearerInterceptor(e.otherToken))
	e.otherRaces = racesv1connect.NewRaceServiceClient(other)
	e.otherPlans = racesv1connect.NewRacePlanServiceClient(other)
	e.otherTraining = plansv1connect.NewTrainingPlanServiceClient(other)
	e.otherExercises = plansv1connect.NewExerciseServiceClient(other)
	e.otherShares = plansv1connect.NewWorkoutShareServiceClient(other)
	e.oauth = oauthv1connect.NewOAuthServiceClient(signedIn)
	e.anonOAuth = oauthv1connect.NewOAuthServiceClient(anon)
	e.trips = tripsv1connect.NewTripReportServiceClient(authed)
	e.anonTrips = tripsv1connect.NewTripReportServiceClient(anon)
	e.otherTrips = tripsv1connect.NewTripReportServiceClient(other)
	e.raceReports = racesv1connect.NewRaceReportServiceClient(authed)
	e.anonRaceReports = racesv1connect.NewRaceReportServiceClient(anon)
	e.otherRaceReports = racesv1connect.NewRaceReportServiceClient(other)
	e.lactate = lactatev1connect.NewLactateTestServiceClient(authed)
	e.otherLactate = lactatev1connect.NewLactateTestServiceClient(other)
	return e
}

// garminClientID is the Garmin app's client ID in tests.
const garminClientID = "garmin-client"

// garminWebhookSecret is the secret in the Garmin webhook's path in tests.
const garminWebhookSecret = "garmin-webhook-secret-for-tests"

// bootstrapToken is the admin bootstrap token in tests.
const bootstrapToken = "bootstrap-secret"

// bearerInterceptor authenticates requests with an API token.
func bearerInterceptor(token string) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			if info, ok := connect.CallInfoForClientContext(ctx); ok {
				info.RequestHeader().Set("Authorization", "Bearer "+token)
			}
			return next(ctx, spec)
		}
	}
}

// sessionInterceptor authenticates requests with a session cookie, like the
// web app.
func sessionInterceptor(sessionID string) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			if info, ok := connect.CallInfoForClientContext(ctx); ok {
				info.RequestHeader().Set("Cookie", "__Host-session="+sessionID)
			}
			return next(ctx, spec)
		}
	}
}

// wantCode fails the test unless err is a connect error with the code.
func wantCode(t *testing.T, err error, code connect.Code) {
	t.Helper()
	if connect.CodeOf(err) != code {
		t.Fatalf("got error %v, want code %v", err, code)
	}
}

// fakeRouter routes legs as straight lines with 11 points, where the
// elevation is 1000 times the latitude and the terrain is trail. Points north
// of 80 degrees can't be routed. It records the preferred paths of the last
// request.
type fakeRouter struct {
	mu         sync.Mutex
	routeCalls int
	prefer     [][]geo.Point
}

// offMapLat is the latitude of points the fake router finds no mapped path
// near, routing to a point about 220 m north of them instead.
const offMapLat = 0.0123

// fakeRoutePath returns the points of a path that bows east by bow degrees
// in the middle.
func fakeRoutePath(from, to geo.Point, bow float64) []geo.Point {
	var points []geo.Point
	for i := range 11 {
		f := float64(i) / 10
		p := geo.Interpolate(from, to, f)
		p.Lng += bow * math.Sin(math.Pi*f)
		p.Ele = p.Lat * 1000
		points = append(points, p)
	}
	return points
}

func (f *fakeRouter) Route(_ context.Context, req routing.Request) (*routing.Path, error) {
	f.mu.Lock()
	f.routeCalls++
	f.prefer = req.Prefer
	f.mu.Unlock()
	if req.From.Lat > 80 {
		return nil, fmt.Errorf("%w: there is no mapped path near the point", routing.ErrNotRoutable)
	}
	if req.From.Lat > 60 && req.From.Lat < 70 {
		return nil, errors.New("the routing graph is unavailable")
	}
	path := &routing.Path{Points: fakeRoutePath(req.From, req.To, 0)}
	if req.To.Lat == offMapLat {
		// The nearest mapped path ends about 220 m north of the point.
		path.Points[len(path.Points)-1].Lat += 0.002
	}
	for range path.Points {
		path.Terrains = append(path.Terrains, geo.TerrainTrail)
	}
	path.Terrains[10] = geo.TerrainUnknown
	return path, nil
}

// Regions covers everything up to latitude 80, matching where Route finds
// paths.
func (f *fakeRouter) Regions(context.Context) ([]geo.Bounds, error) {
	return []geo.Bounds{{MinLat: -90, MinLng: -180, MaxLat: 80, MaxLng: 180}}, nil
}

func (f *fakeRouter) routeCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.routeCalls
}

func (f *fakeRouter) lastPrefer() [][]geo.Point {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prefer
}

// fakeMapData looks up elevations as 1000 times the latitude.
type fakeMapData struct {
	mu        sync.Mutex
	forecasts []mapdata.ForecastQuery
	// The points of the HourlyForecast calls.
	hourlyForecasts []geo.Point
	// The number of FillElevations calls.
	fills int
	// The gusts HourlyForecast forecasts in kilometers per hour, from
	// setGusts.
	gustsKMH float64
}

// setGusts sets the gusts of the forecasts.
func (f *fakeMapData) setGusts(kmh float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gustsKMH = kmh
}

func (f *fakeMapData) FillElevations(_ context.Context, points []geo.Point, _ int) error {
	f.mu.Lock()
	f.fills++
	f.mu.Unlock()
	for i := range points {
		points[i].Ele = points[i].Lat * 1000
	}
	return nil
}

func (f *fakeMapData) Elevations(_ context.Context, points []geo.Point) ([]float64, error) {
	elevations := make([]float64, len(points))
	for i, p := range points {
		elevations[i] = p.Lat * 1000
	}
	return elevations, nil
}

// Forecast returns clear, mild weather, with a thunderstorm in the second
// hour and darkness from 18:00 UTC.
func (f *fakeMapData) Forecast(_ context.Context, queries []mapdata.ForecastQuery) ([]mapdata.Weather, error) {
	f.mu.Lock()
	f.forecasts = append(f.forecasts, queries...)
	f.mu.Unlock()
	weather := make([]mapdata.Weather, len(queries))
	for i, q := range queries {
		hour := q.Time.UTC().Truncate(time.Hour)
		weather[i] = mapdata.Weather{
			Time:                hour,
			TemperatureCelsius:  10,
			ApparentTemperature: 8,
			WeatherCode:         1,
			FreezingLevelMeters: 3000,
			IsDay:               hour.Hour() < 18,
		}
		if hour.Equal(queries[0].Time.UTC().Truncate(time.Hour).Add(time.Hour)) {
			weather[i].WeatherCode = 95
		}
		// The last point is deep in snow, with 40 cm in the past 3 days.
		if i == len(queries)-1 && len(queries) > 1 {
			weather[i].SnowDepthMeters, weather[i].SnowfallPast72HoursCM = 0.8, 40
		}
	}
	return weather, nil
}

// HourlyForecast returns light rain from 10 to 12 UTC and otherwise clear
// weather, at 5 degrees at midnight UTC warming by a degree an hour, which
// it also feels like, with the gusts set by setGusts, if any. The freezing
// level is at 2000 m at midnight UTC, rising 10 m an hour, on 25 cm of
// snow, 12 cm of which fell in the past 3 days.
func (f *fakeMapData) HourlyForecast(_ context.Context, point geo.Point, start, end time.Time) ([]mapdata.Weather, error) {
	f.mu.Lock()
	f.hourlyForecasts = append(f.hourlyForecasts, point)
	gusts := f.gustsKMH
	f.mu.Unlock()
	var weather []mapdata.Weather
	for hour := start; !hour.After(end.Add(23 * time.Hour)); hour = hour.Add(time.Hour) {
		w := mapdata.Weather{
			Time:                     hour,
			TemperatureCelsius:       5 + float64(hour.Hour()),
			ApparentTemperature:      5 + float64(hour.Hour()),
			PrecipitationProbability: 10,
			WindSpeedKMH:             float64(hour.Hour()),
			WindGustsKMH:             gusts,
			IsDay:                    true,
			FreezingLevelMeters:      2000 + 10*float64(hour.Hour()),
			SnowDepthMeters:          0.25,
			SnowfallPast72HoursCM:    12,
		}
		if hour.Hour() >= 10 && hour.Hour() < 12 {
			w.WeatherCode, w.PrecipitationProbability, w.PrecipitationMM = 61, 80, 0.5
		}
		weather = append(weather, w)
	}
	return weather, nil
}

func (f *fakeMapData) Search(_ context.Context, query string, limit int) ([]mapdata.Place, error) {
	return []mapdata.Place{{Name: query, Lat: 1, Lng: 2, Category: "natural", Type: "peak", BoundingBox: []float64{0.9, 1.1, 1.9, 2.1}}}, nil
}

// FindTrails returns a trail north from the center. Areas south of 60°S
// haven't been imported.
func (f *fakeMapData) FindTrails(_ context.Context, center geo.Point, radius float64, limit int) ([]routing.Trail, error) {
	if center.Lat < -60 {
		return nil, routing.ErrNoMapData
	}
	trail := routing.Trail{WayID: 42, Name: "Ridge Trail", Highway: "path", Surface: "dirt", Terrain: geo.TerrainTrail}
	for i := range 5 {
		trail.Points = append(trail.Points, geo.Point{Lat: center.Lat + float64(i)*0.001, Lng: center.Lng})
	}
	return []routing.Trail{trail}, nil
}

// FamiliarWays returns the ridge trail, run by every track. Areas south of
// 60°S haven't been imported.
func (f *fakeMapData) FamiliarWays(_ context.Context, bounds geo.Bounds, tracks [][]geo.Point, _ int) ([]routing.FamiliarWay, error) {
	if bounds.MaxLat < -60 {
		return nil, routing.ErrNoMapData
	}
	way := routing.FamiliarWay{ActivityCount: len(tracks), RunLengthMeters: 400, LengthMeters: 500}
	way.Trail = routing.Trail{WayID: 42, Name: "Ridge Trail", Highway: "path", Terrain: geo.TerrainTrail, Points: []geo.Point{{Lat: bounds.MinLat}, {Lat: bounds.MaxLat}}}
	return []routing.FamiliarWay{way}, nil
}

// FindFeatures returns a peak and a water source north of the center, of
// any kind. Areas south of 60°S haven't been imported.
func (f *fakeMapData) FindFeatures(_ context.Context, center geo.Point, radius float64, kinds []geo.FeatureKind) ([]routing.Feature, error) {
	if center.Lat < -60 {
		return nil, routing.ErrNoMapData
	}
	return []routing.Feature{
		{ID: 1, Kind: geo.FeaturePeak, Name: "Far Peak", Point: geo.Point{Lat: center.Lat + 0.01, Lng: center.Lng, Ele: 1234}},
		{ID: 2, Kind: geo.FeatureWater, Point: geo.Point{Lat: center.Lat + 0.001, Lng: center.Lng, Ele: math.NaN()}},
	}, nil
}

// fakeAvalanches forecasts avalanches west of longitude -115, the
// Canadian Rockies and further west, and fails at longitude -100. Its
// forecast covers today and the next two days in UTC: Considerable today,
// High in the alpine tomorrow, and Moderate the day after.
type fakeAvalanches struct {
	mu sync.Mutex
	// The points forecasts were asked for.
	points []geo.Point
}

func (f *fakeAvalanches) Forecast(_ context.Context, point geo.Point) (*avalanche.Forecast, error) {
	f.mu.Lock()
	f.points = append(f.points, point)
	f.mu.Unlock()
	switch {
	case point.Lng == -100:
		return nil, errors.New("avalanche forecasts are down")
	case point.Lng > -115:
		return nil, nil
	}
	today := time.Now().UTC()
	rating := func(d avalanche.Danger) avalanche.Rating {
		return avalanche.Rating{Danger: d, Description: d.String()}
	}
	day := func(days int, alpine, treeline avalanche.Danger) avalanche.Day {
		return avalanche.Day{
			Date:   today.AddDate(0, 0, days).Format(time.DateOnly),
			Alpine: rating(alpine), Treeline: rating(treeline), BelowTreeline: rating(avalanche.DangerModerate),
		}
	}
	return &avalanche.Forecast{
		Title:      "Glacier",
		Forecaster: "Parks Canada",
		URL:        "https://avalanche.ca/forecasts/glacier",
		IssueTime:  today.Add(-time.Hour),
		Highlights: "New snow and wind have built fresh wind slabs.",
		Days: []avalanche.Day{
			day(0, avalanche.DangerConsiderable, avalanche.DangerConsiderable),
			day(1, avalanche.DangerHigh, avalanche.DangerConsiderable),
			day(2, avalanche.DangerModerate, avalanche.DangerModerate),
		},
		Problems: []avalanche.Problem{{
			Type: "windslab", Name: "Wind Slab", Elevations: []string{"Alpine"}, Aspects: []string{"N", "NE"},
			Likelihood: "Likely", MinSize: 1, MaxSize: 2.5,
		}},
	}, nil
}

// requestedPoints returns the points forecasts were asked for.
func (f *fakeAvalanches) requestedPoints() []geo.Point {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.points)
}

// fakeLiveTrack returns a fixed position for a session.
type fakeLiveTrack struct {
	mu    sync.Mutex
	track livetrack.Track
}

func (f *fakeLiveTrack) Track(_ context.Context, s livetrack.Session) (livetrack.Track, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s.ID != "aaaaaaaa-bbbb" || s.Token != "TOKEN" {
		return livetrack.Track{}, fmt.Errorf("the LiveTrack session was not found; check the link")
	}
	return f.track, nil
}

func approx(a, b, tolerance float64) bool {
	return math.Abs(a-b) <= tolerance
}

// storeActivityDetail stores the detail and streams of one of the user's
// activities, as a sync does.
func (e *env) storeActivityDetail(activityID pgtype.UUID, detail *activities.Detail, streams *activitiesv1.ActivityStreams, bounds *geo.Bounds) {
	e.t.Helper()
	ctx := context.Background()
	a, err := e.activities.GetActivity(ctx, e.userID, activityID)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.activities.StoreActivityDetail(ctx, e.userID, activityID, a.StravaID, detail, streams, bounds); err != nil {
		e.t.Fatal(err)
	}
}
