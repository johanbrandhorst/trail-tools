package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/reload"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/blob"
	"github.com/johanbrandhorst/trail-tools/internal/cloudrun"
	"github.com/johanbrandhorst/trail-tools/internal/cloudscheduler"
	"github.com/johanbrandhorst/trail-tools/internal/demo"
	"github.com/johanbrandhorst/trail-tools/internal/elevation"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/fakestrava"
	"github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1/activitiesv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/auth/v1/authv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1/geov1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/lactate/v1/lactatev1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/oauth/v1/oauthv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1/plansv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/races/v1/racesv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1/routesv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/routing/v1/routingv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/strava/v1/stravav1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1/tripsv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/users/v1/usersv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/html"
	"github.com/johanbrandhorst/trail-tools/internal/lactate"
	"github.com/johanbrandhorst/trail-tools/internal/livetrack"
	"github.com/johanbrandhorst/trail-tools/internal/mapdata"
	"github.com/johanbrandhorst/trail-tools/internal/mcp"
	"github.com/johanbrandhorst/trail-tools/internal/oauth"
	"github.com/johanbrandhorst/trail-tools/internal/plans"
	"github.com/johanbrandhorst/trail-tools/internal/races"
	"github.com/johanbrandhorst/trail-tools/internal/routes"
	"github.com/johanbrandhorst/trail-tools/internal/routing"
	"github.com/johanbrandhorst/trail-tools/internal/services/activity"
	"github.com/johanbrandhorst/trail-tools/internal/services/auth"
	geosvc "github.com/johanbrandhorst/trail-tools/internal/services/geo"
	lactatesvc "github.com/johanbrandhorst/trail-tools/internal/services/lactate"
	mapdatasvc "github.com/johanbrandhorst/trail-tools/internal/services/mapdata"
	oauthsvc "github.com/johanbrandhorst/trail-tools/internal/services/oauth"
	"github.com/johanbrandhorst/trail-tools/internal/services/plan"
	"github.com/johanbrandhorst/trail-tools/internal/services/race"
	"github.com/johanbrandhorst/trail-tools/internal/services/raceplan"
	"github.com/johanbrandhorst/trail-tools/internal/services/racereport"
	"github.com/johanbrandhorst/trail-tools/internal/services/route"
	stravasvc "github.com/johanbrandhorst/trail-tools/internal/services/strava"
	"github.com/johanbrandhorst/trail-tools/internal/services/trip"
	"github.com/johanbrandhorst/trail-tools/internal/services/user"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/strava"
	"github.com/johanbrandhorst/trail-tools/internal/telemetry"
	"github.com/johanbrandhorst/trail-tools/internal/trips"
	"github.com/johanbrandhorst/trail-tools/internal/users"
	"github.com/johanbrandhorst/trail-tools/web"
	"gitlab.com/greyxor/slogor"
	"google.golang.org/api/option"
)

const (
	defaultHostname = "localhost"
	defaultPort     = "8080"
	// mcpPath is the path of the MCP server.
	mcpPath = "/mcp"
)

// Timeouts of outgoing requests, as the clients had before they were
// traced.
const (
	mapDataTimeout   = 60 * time.Second
	stravaTimeout    = 60 * time.Second
	liveTrackTimeout = 20 * time.Second
	geofabrikTimeout = 2 * time.Minute
)

// maxRequestBytes limits the size of API requests. GPX files imported as
// routes and trip report photos can be several megabytes.
const maxRequestBytes = 32 << 20

var (
	logLevel = flag.String(
		"log-level",
		"info",
		"The log level to use",
	)
	logFormat = flag.String(
		"log-format",
		"text",
		"The log format: \"text\" for terminals, or \"json\" for structured logging platforms like Cloud Logging",
	)
	errorReportingProject = flag.String(
		"error-reporting-project",
		"",
		"The Google Cloud project to report errors to with Error Reporting, using the application default credentials. If unset, errors are logged",
	)
	otlpEndpoint = flag.String(
		"otlp-endpoint",
		telemetry.LocalEndpoint,
		"The OTLP gRPC endpoint to export traces to: a local collector like Grafana Tempo, or https://telemetry.googleapis.com with -trace-project. Empty disables tracing",
	)
	traceProject = flag.String(
		"trace-project",
		"",
		"The Google Cloud project to export traces to, using the application default credentials",
	)
	traceSampleRate = flag.Float64(
		"trace-sample-rate",
		1,
		"The fraction of traces to sample, from 0 to 1",
	)
	databaseURL = flag.String(
		"database-url",
		"",
		"The URL of the postgres database in which to store the state of the application",
	)
	hostname = flag.String(
		"hostname",
		defaultHostname,
		"The hostname on which to serve the application",
	)
	port = flag.String(
		"port",
		defaultPort,
		"The port on which to serve the application",
	)
	tlsKeyPath = flag.String(
		"tls-key",
		"",
		"The path to the private key used for serving the application over HTTPS",
	)
	tlsCertPath = flag.String(
		"tls-cert",
		"",
		"The path to the certificate used for serving the application over HTTPS",
	)
	serveDir = flag.String(
		"serve-dir",
		"",
		"Optionally, the directory to serve as the root of the web application."+
			" If unset, will serve the compiled web application from the web package",
	)
	publicURL = flag.String(
		"public-url",
		"",
		"The public URL of the application, used for OAuth redirects and share links."+
			" Defaults to http(s)://<hostname>:<port>",
	)
	stravaClientID = flag.String(
		"strava-client-id",
		"",
		"The client ID of the Strava API application. If unset, Strava sync is disabled",
	)
	stravaClientSecret = flag.String(
		"strava-client-secret",
		"",
		"The client secret of the Strava API application",
	)
	fakeStrava = flag.Bool(
		"fake-strava",
		false,
		"Connect to a fake Strava served at "+fakestrava.Path+" instead, with a made-up athlete and months of"+
			" generated activities, for running locally without a Strava API application. Ignores -strava-client-id",
	)
	stravaSyncMode = flag.String(
		"strava-sync-mode",
		"background",
		"How Strava syncs run: \"background\" runs them in the server, \"scheduled\" runs them when"+
			" a scheduler calls "+stravasvc.SyncPath+", for servers that don't run between requests, like Cloud Run",
	)
	stravaWebhookVerifyToken = flag.String(
		"strava-webhook-verify-token",
		"",
		"A secret Strava sends back when validating the webhook. If set, the server subscribes to Strava's webhook events"+
			" at "+stravasvc.WebhookPath+" on startup, to sync new activities right away. Requires a public URL",
	)
	stravaBackgroundReserve = flag.Float64(
		"strava-background-reserve",
		strava.DefaultBackgroundReserve,
		"The fraction of Strava's rate limits, in each 15 minute window and each day, that syncing the backlog of"+
			" activities leaves for interactive requests, like webhook events and activities users open. 0 disables the reserve",
	)
	stravaSyncJob = flag.String(
		"strava-sync-job",
		"",
		"The Cloud Scheduler job that calls "+stravasvc.SyncPath+", as projects/<project>/locations/<region>/jobs/<job>."+
			" With -strava-sync-mode=scheduled, it runs when a user asks to sync, so the sync runs right away."+
			" If unset, those syncs run within the request for a while instead",
	)
	schedulerServiceAccount = flag.String(
		"scheduler-service-account",
		"",
		"The email of the service account allowed to call "+stravasvc.SyncPath+" with a Google signed ID token,"+
			" e.g. from Cloud Scheduler. Required with -strava-sync-mode=scheduled",
	)
	schedulerAudience = flag.String(
		"scheduler-audience",
		"trail-tools-scheduler",
		"The audience of the scheduler's ID tokens",
	)
	adminBootstrapToken = flag.String(
		"admin-bootstrap-token",
		"",
		"A secret that can be used as an invitation to create the first administrator,"+
			" at /invite/<token>, while there are no administrators",
	)
	importJob = flag.String(
		"import-job",
		"",
		"The Cloud Run job that imports OpenStreetMap data, as projects/<project>/locations/<region>/jobs/<job>."+
			" If unset, imports run in the server",
	)
	photoBucket = flag.String(
		"photo-bucket",
		"",
		"The Google Cloud Storage bucket to store trip report photos in, using the application default credentials."+
			" If unset, photos are stored in -photo-dir",
	)
	photoDir = flag.String(
		"photo-dir",
		"photos",
		"The local directory to store trip report photos in, if -photo-bucket is unset",
	)
	activityBucket = flag.String(
		"activity-bucket",
		"",
		"The Google Cloud Storage bucket to store activities' streams and Strava's responses in,"+
			" using the application default credentials. If unset, they're stored in -activity-dir",
	)
	activityDir = flag.String(
		"activity-dir",
		"activity-data",
		"The local directory to store activities' streams and Strava's responses in, if -activity-bucket is unset",
	)
	demBucket = flag.String(
		"dem-bucket",
		"",
		"The Google Cloud Storage bucket to cache elevation tiles in, using the application default credentials,"+
			" so they're downloaded once rather than after every restart, instead of on local disk",
	)
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "import-osm" {
		os.Exit(importOSM(os.Args[2:]))
	}
	flag.Parse()
	if err := setFlagsFromEnv(flag.CommandLine); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set flags from the environment: %v\n", err)
		os.Exit(1)
	}
	logger, err := newLogger(*logLevel, *logFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set up logging: %v\n", err)
		os.Exit(1)
	}
	stopTracing, err := initTracing(context.Background(), *otlpEndpoint, *traceProject, *traceSampleRate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set up tracing: %v\n", err)
		os.Exit(1)
	}
	report, err := newReporter(context.Background(), logger, *errorReportingProject)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set up error reporting: %v\n", err)
		os.Exit(1)
	}
	telemetry.TraceDefaultTransport(report)
	// Cloud Run sends SIGTERM to stop instances.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(
		ctx,
		logger,
		report,
		*databaseURL,
		*hostname,
		*port,
		*tlsKeyPath,
		*tlsCertPath,
		*serveDir,
		*publicURL,
		*stravaClientID,
		*stravaClientSecret,
		*fakeStrava,
		*adminBootstrapToken,
		*stravaSyncMode,
		*stravaWebhookVerifyToken,
		*stravaBackgroundReserve,
		*stravaSyncJob,
		*schedulerServiceAccount,
		*schedulerAudience,
		*importJob,
		*photoBucket,
		*photoDir,
		*activityBucket,
		*activityDir,
		*demBucket,
		mapdata.Config{},
	)
	stop()
	if err != nil {
		report.Report(context.Background(), "Failed running app", err)
	}
	// Send the traces and errors not yet sent before exiting. Error
	// Reporting goes last, to report failing to send the traces.
	if traceErr := stopTracing(); traceErr != nil {
		report.Report(context.Background(), "Failed to send the remaining traces", traceErr)
	}
	if closeErr := report.Close(); closeErr != nil {
		logger.Warn("Failed to send the remaining error reports", slog.String("error", closeErr.Error()))
	}
	if err != nil {
		os.Exit(1)
	}
}

// newReporter returns a reporter sending errors to Error Reporting in a
// project, from the Cloud Run service or job it runs in, or logging them if
// the project is empty.
func newReporter(ctx context.Context, log *slog.Logger, project string) (*errreport.Reporter, error) {
	if project == "" {
		return errreport.NewLocal(log), nil
	}
	return errreport.New(ctx, project, serviceName(), serviceVersion(), log,
		option.WithGRPCDialOption(telemetry.GRPCDialOption()))
}

// serviceName returns the name of the Cloud Run service or job the app runs
// as, or "trail-tools" outside of Cloud Run.
func serviceName() string {
	return cmp.Or(os.Getenv("K_SERVICE"), os.Getenv("CLOUD_RUN_JOB"), "trail-tools")
}

// serviceVersion returns the Cloud Run revision or job execution the app
// runs as, if any.
func serviceVersion() string {
	return cmp.Or(os.Getenv("K_REVISION"), os.Getenv("CLOUD_RUN_EXECUTION"))
}

// tracingShutdownTimeout bounds sending the remaining traces on exit.
const tracingShutdownTimeout = 5 * time.Second

// initTracing exports traces to an OTLP endpoint. The returned function
// sends the remaining traces.
func initTracing(ctx context.Context, endpoint, project string, sampleRate float64) (func() error, error) {
	shutdown, err := telemetry.Init(ctx, &telemetry.Config{
		Endpoint:       endpoint,
		GCPProject:     project,
		ServiceName:    serviceName(),
		ServiceVersion: serviceVersion(),
		SampleRate:     sampleRate,
	})
	if err != nil {
		return nil, err
	}
	return func() error {
		ctx, cancel := context.WithTimeout(context.Background(), tracingShutdownTimeout)
		defer cancel()
		return shutdown(ctx)
	}, nil
}

func run(
	ctx context.Context,
	log *slog.Logger,
	report *errreport.Reporter,
	databaseURL string,
	hostname string,
	port string,
	tlsKeyPath string,
	tlsCertPath string,
	serveDir string,
	publicURL string,
	stravaClientID string,
	stravaClientSecret string,
	fakeStrava bool,
	adminBootstrapToken string,
	stravaSyncMode string,
	stravaWebhookVerifyToken string,
	stravaBackgroundReserve float64,
	stravaSyncJob string,
	schedulerServiceAccount string,
	schedulerAudience string,
	importJob string,
	photoBucket string,
	photoDir string,
	activityBucket string,
	activityDir string,
	demBucket string,
	mapConfig mapdata.Config,
) error {
	switch {
	case databaseURL == "":
		return errors.New("database-url is required")
	case hostname == "":
		return errors.New("address is required")
	case port == "":
		return errors.New("port is required")
	case tlsKeyPath != "" && tlsCertPath == "" || tlsKeyPath == "" && tlsCertPath != "":
		return errors.New("tls-cert and tls-key must be set together")
	case stravaClientID != "" && stravaClientSecret == "":
		return errors.New("strava-client-secret is required when strava-client-id is set")
	case stravaSyncMode != "background" && stravaSyncMode != "scheduled":
		return errors.New(`strava-sync-mode must be "background" or "scheduled"`)
	case stravaBackgroundReserve < 0 || stravaBackgroundReserve >= 1:
		return errors.New("strava-background-reserve must be at least 0 and less than 1")
	case stravaSyncMode == "scheduled" && schedulerServiceAccount == "":
		return errors.New("scheduler-service-account is required with -strava-sync-mode=scheduled")
	case photoBucket == "" && photoDir == "":
		return errors.New("photo-bucket or photo-dir is required")
	case activityBucket == "" && activityDir == "":
		return errors.New("activity-bucket or activity-dir is required")
	}
	dbURL, err := url.Parse(databaseURL)
	if err != nil {
		return fmt.Errorf("failed to parse database-url: %w", err)
	}
	address := net.JoinHostPort(hostname, port)
	baseURL := "http://" + address
	var cert tls.Certificate
	if tlsCertPath != "" && tlsKeyPath != "" {
		cert, err = tls.LoadX509KeyPair(tlsCertPath, tlsKeyPath)
		if err != nil {
			return fmt.Errorf("failed to load TLS certificate: %w", err)
		}
		baseURL = "https://" + address
	}
	if publicURL != "" {
		baseURL = strings.TrimSuffix(publicURL, "/")
	}
	db, err := store.New(ctx, log, dbURL)
	if err != nil {
		return fmt.Errorf("failed to create store: %w", err)
	}
	users, err := users.NewRepository(log, db)
	if err != nil {
		return fmt.Errorf("failed to create user directory: %w", err)
	}
	routeRepo, err := routes.NewRepository(log, db)
	if err != nil {
		return fmt.Errorf("failed to create route repository: %w", err)
	}
	raceRepo, err := races.NewRepository(log, db)
	if err != nil {
		return fmt.Errorf("failed to create race repository: %w", err)
	}
	lactateRepo, err := lactate.NewRepository(log, db)
	if err != nil {
		return fmt.Errorf("failed to create lactate test repository: %w", err)
	}
	planRepo, err := plans.NewRepository(log, db)
	if err != nil {
		return fmt.Errorf("failed to create plan repository: %w", err)
	}
	tripRepo, err := trips.NewRepository(log, db)
	if err != nil {
		return fmt.Errorf("failed to create trip repository: %w", err)
	}
	// Every outgoing request is traced: clients are made by the telemetry
	// package, and Google's API clients take theirs from the context.
	googleCtx := telemetry.WithClient(ctx, telemetry.NewClient(0))

	// Photos and activity data are stored in Cloud Storage on Cloud Run,
	// whose file system doesn't outlive an instance, and in local directories
	// otherwise.
	photos, err := newBlobStore(googleCtx, log, "photos", photoBucket, photoDir)
	if err != nil {
		return err
	}
	activityBlobs, err := newBlobStore(googleCtx, log, "activity data", activityBucket, activityDir)
	if err != nil {
		return err
	}
	activityRepo, err := activities.NewRepository(report, db, activityBlobs)
	if err != nil {
		return fmt.Errorf("failed to create activity repository: %w", err)
	}
	// Elevations come from public elevation tiles, for routes and for imports
	// run in the server.
	dem, err := newDEM(googleCtx, log, report, elevation.DefaultURL, elevation.DefaultCacheDir, demBucket)
	if err != nil {
		return err
	}
	mapConfig.Elevation = dem
	mapConfig.HTTPClient = cmp.Or(mapConfig.HTTPClient, telemetry.NewClient(mapDataTimeout))
	mapData := mapdata.New(mapConfig)

	// Strava is optional, as it requires API credentials.
	var stravaSyncer *stravasvc.Syncer
	var stravaOAuth *stravasvc.OAuth
	var stravaWebhook *stravasvc.Webhook
	var fakeStravaServer *fakestrava.Server
	// Links to activities and athletes go to Strava's website, or the fake's.
	stravaWebURL := strava.WebURL
	if fakeStrava {
		// The fake Strava is served by this server, and its API is called
		// without going through the network.
		fakeStravaServer = fakestrava.New(baseURL, fakestrava.NewRouter(routing.NewRouter(db)), fakestrava.DefaultCacheDir)
		stravaClientID, stravaClientSecret, stravaWebhookVerifyToken = "fake", rand.Text(), ""
		stravaWebURL = fakeStravaServer.WebURL()
		log.InfoContext(ctx, "Using a fake Strava with generated activities", slog.String("url", baseURL+fakestrava.Path))
		// The fake athlete's trails and elevations are embedded, so the
		// activities follow trails without importing or downloading map data.
		go func() {
			if err := demo.Load(ctx, log, db, dem); err != nil && ctx.Err() == nil {
				report.Report(ctx, "Failed to load the map data of Squamish", err)
			}
			if err := fakeStravaServer.Prepare(ctx); err != nil && ctx.Err() == nil {
				report.Report(ctx, "Failed to generate the fake Strava's activities", err)
			}
		}()
	}
	if stravaClientID != "" {
		conf := strava.Config{
			ClientID:     stravaClientID,
			ClientSecret: stravaClientSecret,
			RedirectURL:  baseURL + stravasvc.CallbackPath,
			// The client treats 0 as the default.
			BackgroundReserve: cmp.Or(stravaBackgroundReserve, -1),
			HTTPClient:        telemetry.NewClient(stravaTimeout),
		}
		if fakeStravaServer != nil {
			conf.BaseURL = fakeStravaServer.APIURL()
			conf.OAuthBaseURL = fakeStravaServer.OAuthURL()
			conf.HTTPClient = &http.Client{Transport: telemetry.Transport(fakeStravaServer.Client().Transport)}
		}
		stravaClient := strava.New(conf)
		background := stravaSyncMode == "background"
		var trigger stravasvc.Trigger
		if !background && stravaSyncJob != "" {
			job, err := cloudscheduler.NewJob(googleCtx, stravaSyncJob)
			if err != nil {
				return fmt.Errorf("failed to create the Strava sync job client: %w", err)
			}
			trigger = job
		}
		stravaSyncer = stravasvc.NewSyncer(ctx, log, report, activityRepo, stravaClient, background, trigger)
		stravaOAuth = stravasvc.NewOAuth(log, report, stravaClient, activityRepo, stravaSyncer, []byte(stravaClientSecret))
		if stravaWebhookVerifyToken != "" {
			stravaWebhook = stravasvc.NewWebhook(log, report, activityRepo, stravaSyncer, stravaClient,
				baseURL+stravasvc.WebhookPath, stravaWebhookVerifyToken)
		}
		if background {
			go stravaSyncer.RunBackground(ctx)
			defer stravaSyncer.Wait()
		}
	} else {
		log.InfoContext(ctx, "Strava sync disabled, set -strava-client-id and -strava-client-secret to enable it")
	}

	userService := user.NewService(log, report, users, users, users, photos)
	authService, err := auth.NewService(log, report, users, baseURL, adminBootstrapToken)
	if err != nil {
		return fmt.Errorf("failed to create auth service: %w", err)
	}
	activityService := activity.NewService(log, report, activityRepo, stravaWebURL)
	stravaService := stravasvc.NewService(log, report, activityRepo, stravaSyncer, stravaOAuth, stravaWebURL)
	routeService := route.NewService(log, report, routeRepo, activityRepo, routing.NewRouter(db), mapData)
	geoService := geosvc.NewService(log, report, mapData, routing.NewFinder(db), activityRepo)
	planService := plan.NewService(log, report, planRepo, routeRepo, activityRepo, mapData)
	raceService := race.NewService(log, report, raceRepo, routeRepo)
	racePlanService := raceplan.NewService(log, report, raceRepo, routeRepo, livetrack.New("", telemetry.NewClient(liveTrackTimeout)), baseURL)
	// Activities are synced from Strava before importing their photos into
	// trip reports, if Strava is configured.
	var activitySyncer trip.ActivitySyncer
	if stravaSyncer != nil {
		activitySyncer = stravaSyncer
	}
	tripService := trip.NewService(log, report, tripRepo, activityRepo, activitySyncer, photos, users, telemetry.NewClient(0), baseURL)
	raceReportService := racereport.NewService(log, report, raceRepo, activityRepo, tripRepo, racePlanService, users, baseURL)
	lactateService := lactatesvc.NewService(log, report, lactateRepo, baseURL)
	// MCP clients that require OAuth, like claude.ai, authorize with this
	// authorization server instead of API tokens.
	oauthServer := oauth.NewServer(log, report, users, baseURL, mcpPath)
	oauthService := oauthsvc.NewService(log, report, oauthServer, users)
	// Imports of OpenStreetMap data need more memory than the server has on
	// Cloud Run, so they run as a Cloud Run job there.
	var importRunner mapdatasvc.ImportRunner
	if importJob != "" {
		job, err := cloudrun.NewJob(googleCtx, importJob)
		if err != nil {
			return fmt.Errorf("failed to create the import job client: %w", err)
		}
		importRunner = jobImportRunner{job: job}
	} else {
		localRunner := routing.NewLocalRunner(ctx, log, report, telemetry.NewClient(0), db, dem)
		defer localRunner.Wait()
		importRunner = localRunner
	}
	mapDataService := mapdatasvc.NewService(log, report, db, routing.NewExtracts(routing.DefaultGeofabrikIndexURL, telemetry.NewClient(geofabrikTimeout)), importRunner)

	// Register all services on a single Connect server, served by the API mux
	server := connect.NewServer()
	usersv1connect.RegisterUserServiceHandler(server, userService)
	authv1connect.RegisterAuthServiceHandler(server, authService)
	activitiesv1connect.RegisterActivityServiceHandler(server, activityService)
	stravav1connect.RegisterStravaServiceHandler(server, stravaService)
	routesv1connect.RegisterRouteServiceHandler(server, routeService)
	geov1connect.RegisterGeoServiceHandler(server, geoService)
	routingv1connect.RegisterMapDataServiceHandler(server, mapDataService)
	plansv1connect.RegisterTrainingPlanServiceHandler(server, planService)
	racesv1connect.RegisterRaceServiceHandler(server, raceService)
	racesv1connect.RegisterRacePlanServiceHandler(server, racePlanService)
	racesv1connect.RegisterRaceReportServiceHandler(server, raceReportService)
	lactatev1connect.RegisterLactateTestServiceHandler(server, lactateService)
	oauthv1connect.RegisterOAuthServiceHandler(server, oauthService)
	tripsv1connect.RegisterTripReportServiceHandler(server, tripService)
	apiMux := http.NewServeMux()
	connecthttp.Mount(apiMux, server, connecthttp.WithReadMaxBytes(maxRequestBytes))

	// Wrap the API mux in the authn middleware. Each service checks
	// authentication itself.
	// Browsers that opened a lactate test recording link record with its
	// cookie, signed in or not.
	authHandler, err := authn.NewAuthnMiddleware(ctx, log, report, users, lactatesvc.RecorderMiddleware(apiMux))
	if err != nil {
		return fmt.Errorf("failed to create authentication middleware: %w", err)
	}
	authenticate := func(next http.Handler) http.Handler {
		// NewAuthnMiddleware never returns an error.
		h, _ := authn.NewAuthnMiddleware(ctx, log, report, users, next)
		return h
	}

	// Create the core mux. This contains both the Connect API and HTML handlers.
	// Only the Connect API endpoints (registered to apiMux) are authenticated.
	mux := http.NewServeMux()
	// Serve the Connect API handlers through the authn middleware
	for _, path := range servicePaths(server) {
		mux.Handle(path, authHandler)
	}

	// Serve the same API to AI agents over MCP. MCP clients authenticate
	// with API tokens or OAuth access tokens.
	mcpServer, err := mcp.NewServer(log, report,
		mcp.Service{Name: usersv1connect.UserServiceName, Handler: userService},
		mcp.Service{Name: activitiesv1connect.ActivityServiceName, Handler: activityService},
		mcp.Service{Name: stravav1connect.StravaServiceName, Handler: stravaService},
		mcp.Service{Name: routesv1connect.RouteServiceName, Handler: routeService},
		mcp.Service{Name: geov1connect.GeoServiceName, Handler: geoService},
		mcp.Service{Name: plansv1connect.TrainingPlanServiceName, Handler: planService},
		mcp.Service{Name: racesv1connect.RaceServiceName, Handler: raceService},
		mcp.Service{Name: racesv1connect.RacePlanServiceName, Handler: racePlanService},
		mcp.Service{Name: tripsv1connect.TripReportServiceName, Handler: tripService},
		mcp.Service{Name: racesv1connect.RaceReportServiceName, Handler: raceReportService},
		mcp.Service{Name: lactatev1connect.LactateTestServiceName, Handler: lactateService},
	)
	if err != nil {
		return fmt.Errorf("failed to create MCP server: %w", err)
	}
	resourceMetadataURL := oauthServer.ResourceMetadataURL()
	mcpHandler, err := authn.NewAuthnMiddleware(ctx, log, report, users,
		authn.RequireUser(resourceMetadataURL, mcpServer.Handler()),
		authn.WithResourceMetadata(resourceMetadataURL),
	)
	if err != nil {
		return fmt.Errorf("failed to create MCP authentication middleware: %w", err)
	}
	mux.Handle(mcpPath, mcpHandler)

	// Serve Strava OAuth handlers
	if stravaOAuth != nil {
		stravaOAuth.RegisterHandlers(mux, authenticate)
	}
	// Serve Strava's webhook events, which aren't authenticated
	if stravaWebhook != nil {
		mux.Handle(stravasvc.WebhookPath, stravaWebhook)
	}
	// Serve the scheduled sync endpoint, only for the scheduler
	if stravaSyncer != nil && !stravaSyncer.Background() {
		syncHandler, err := authn.RequireGoogleServiceAccount(googleCtx, log, schedulerAudience, schedulerServiceAccount,
			stravasvc.SyncHandler(report, stravaSyncer))
		if err != nil {
			return fmt.Errorf("failed to create scheduled sync handler: %w", err)
		}
		mux.Handle(stravasvc.SyncPath, syncHandler)
	}

	// Register the logout handler directly on the mux
	authn.RegisterLogoutHandler(report, mux, users)

	// Create the FS used to serve the frontend.
	webFs, _ := fs.Sub(web.Dist, "dist")
	if serveDir != "" {
		webFs = os.DirFS(serveDir)
	}
	var fileServer http.Handler = http.FileServerFS(webFs)
	if serveDir != "" {
		// The watched bundle can be rebuilt within the second the browser
		// cached it, which Last-Modified can't tell apart, so browsers
		// mustn't keep it at all.
		next := fileServer
		fileServer = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
		})
	}

	// Serve index.js, index.js.map, index.css, favicon.svg, img/ and fonts/ directly
	mux.Handle("/index.js", fileServer)
	mux.Handle("/index.js.map", fileServer)
	mux.Handle("/index.css", fileServer)
	mux.Handle("/favicon.svg", fileServer)
	mux.Handle("/img/", fileServer)
	mux.Handle("/fonts/", fileServer)
	// For all other requests, serve the contents of index.html
	indexHandler := html.ServeIndexHTML(serveDir != "")
	// A local server without an administrator sends its first visitor to
	// the invitation to create one.
	mux.Handle("/", authService.BootstrapHandler(indexHandler))
	if fakeStravaServer != nil {
		mux.Handle(fakestrava.Path, fakeStravaServer)
	}
	// Serve the OAuth endpoints, with the frontend's consent page for valid
	// authorization requests.
	oauthServer.RegisterHandlers(mux, indexHandler)
	// Serve routes as GPX files, falling back to the frontend for other
	// /routes/ pages.
	mux.Handle("/routes/", authenticate(routeService.DownloadHandler(indexHandler)))
	mux.Handle("/race-plans/", authenticate(racePlanService.CourseHandler(indexHandler)))
	// Registering /routes/ makes the mux redirect /routes to /routes/, so
	// serve the frontend for /routes explicitly.
	mux.Handle("/routes", indexHandler)
	// Serve trip report photos, and the public trip report pages with their
	// metadata for search engines and link previews.
	mux.Handle(trip.PhotoPath, authenticate(tripService.PhotoHandler()))
	mux.Handle(user.AvatarPath, userService.AvatarHandler())
	// The guided lactate test as a Garmin workout. It holds no user data.
	mux.Handle(lactatesvc.WorkoutPath, lactateService.WorkoutHandler())
	mux.Handle(lactatesvc.SharePath, lactateService.ShareHandler())
	mux.Handle(trip.ProfilePath, authenticate(raceReportService.PageHandler(serveDir != "", tripService.PageHandler(serveDir != ""))))

	// Requests are traced, and named after the routes they match.
	srvHandler := telemetry.Routes(mux)
	// Add auto-reloading middleware if serving from a directory
	if serveDir != "" {
		srvHandler, err = reload.NewMiddleware(srvHandler, serveDir, log)
		if err != nil {
			return fmt.Errorf("failed to create reload middleware: %w", err)
		}
	}

	// Serve HTTP/2 without TLS (h2c) alongside HTTP/1.1.
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	srv := &http.Server{
		Addr:      address,
		Handler:   telemetry.Handler(srvHandler),
		Protocols: protocols,
	}
	serveFn := srv.ListenAndServe
	if cert.Certificate != nil {
		// Over TLS, negotiate HTTP/2 via ALPN instead of h2c.
		protocols.SetHTTP2(true)
		srv.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
		serveFn = func() error {
			return srv.ListenAndServeTLS("", "")
		}
	}
	go func() {
		<-ctx.Done()
		log.InfoContext(ctx, "Interrupt received, shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			report.Report(ctx, "Failed to shutdown server", err)
		}
	}()
	if stravaWebhook != nil {
		// Strava calls the webhook while subscribing, so subscribe once the
		// server is serving. Listing the existing subscription first gives
		// it time to start.
		go func() {
			if err := stravaWebhook.Subscribe(ctx); err != nil {
				report.Report(ctx, "Failed to subscribe to Strava webhook events, new activities sync periodically instead", err)
			}
		}()
	}
	log.InfoContext(ctx, "Serving on", slog.String("addr", address))
	if err := serveFn(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to serve: %w", err)
	}
	return nil
}

// envPrefix is the prefix of environment variables that set flags.
const envPrefix = "TRAIL_TOOLS_"

// setFlagsFromEnv sets flags that weren't set on the command line from
// environment variables named TRAIL_TOOLS_<FLAG>, e.g. TRAIL_TOOLS_DATABASE_URL
// for -database-url. This allows passing secrets through the environment.
func setFlagsFromEnv(fs *flag.FlagSet) error {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var err error
	fs.VisitAll(func(f *flag.Flag) {
		if set[f.Name] || err != nil {
			return
		}
		name := envPrefix + strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))
		if value, ok := os.LookupEnv(name); ok {
			if setErr := f.Value.Set(value); setErr != nil {
				err = fmt.Errorf("invalid value for %s: %w", name, setErr)
			}
		}
	})
	return err
}

// newBlobStore returns a store of what it stores in the Cloud Storage
// bucket, if set, or else in the local directory.
func newBlobStore(ctx context.Context, log *slog.Logger, what, bucket, dir string) (blob.Store, error) {
	if bucket != "" {
		blobs, err := blob.NewGCS(ctx, bucket)
		if err != nil {
			return nil, fmt.Errorf("failed to create the %s bucket client: %w", what, err)
		}
		log.InfoContext(ctx, "Storing "+what+" in Cloud Storage", slog.String("bucket", bucket))
		return blobs, nil
	}
	blobs, err := blob.NewDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to create the %s directory: %w", what, err)
	}
	log.InfoContext(ctx, "Storing "+what+" in a local directory", slog.String("dir", dir))
	return blobs, nil
}

// newDEM creates a DEM that downloads tiles from baseURL and caches them in
// the Cloud Storage bucket, if set, or else in cacheDir. On Cloud Run, local
// disk is memory, and a cache directory would grow with every tile used until
// the instance runs out of memory, so with a bucket tiles aren't cached on
// disk.
func newDEM(ctx context.Context, log *slog.Logger, report *errreport.Reporter, baseURL, cacheDir, bucket string) (*elevation.DEM, error) {
	if bucket != "" {
		cacheDir = ""
	}
	dem := elevation.NewDEM(baseURL, cacheDir, telemetry.NewClient(0))
	if bucket != "" {
		tiles, err := blob.NewGCS(ctx, bucket)
		if err != nil {
			return nil, fmt.Errorf("failed to create the elevation tile bucket client: %w", err)
		}
		dem.SetTileStore(tiles, report)
		log.InfoContext(ctx, "Caching elevation tiles in Cloud Storage", slog.String("bucket", bucket))
	}
	return dem, nil
}

// newLogger returns a logger logging at level in format: "text" for
// terminals, or "json" for Cloud Logging.
func newLogger(level, format string) (*slog.Logger, error) {
	var slogLevel slog.Level
	if err := slogLevel.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid log level: %w", err)
	}
	switch format {
	case "text":
		return slog.New(slogor.NewHandler(os.Stdout, slogor.SetTimeFormat(time.Stamp), slogor.SetLevel(slogLevel))), nil
	case "json":
		return slog.New(newJSONLogHandler(slogLevel)), nil
	default:
		return nil, fmt.Errorf("invalid log format %q, expected text or json", format)
	}
}

// newJSONLogHandler returns a handler logging JSON lines in the format of
// Google Cloud Logging's structured logs.
func newJSONLogHandler(level slog.Level) slog.Handler {
	return slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return a
			}
			switch a.Key {
			case slog.MessageKey:
				a.Key = "message"
			case slog.LevelKey:
				a.Key = "severity"
				if lvl, ok := a.Value.Any().(slog.Level); ok && lvl == slog.LevelWarn {
					a.Value = slog.StringValue("WARNING")
				}
			}
			return a
		},
	})
}

// jobImportRunner runs imports as executions of a Cloud Run job, which runs
// the import-osm command.
type jobImportRunner struct {
	job *cloudrun.Job
}

func (r jobImportRunner) Run(ctx context.Context, id pgtype.UUID) error {
	return r.job.Run(ctx, []string{"import-osm", "-import-id", store.UUIDToString(id)})
}

// servicePaths returns the URL path prefixes of the services registered on
// server, e.g. /users.v1.UserService/.
func servicePaths(server *connect.Server) []string {
	var paths []string
	for spec := range server.Specs() {
		path := spec.Procedure[:strings.LastIndexByte(spec.Procedure, '/')+1]
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	return paths
}
