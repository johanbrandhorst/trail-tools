package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/johanbrandhorst/trail-tools/internal/elevation"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routing"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/telemetry"
)

// importOSM runs the import-osm command, which loads OpenStreetMap extracts
// into the routing graph, and returns the exit code.
func importOSM(args []string) int {
	fs := flag.NewFlagSet("import-osm", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s import-osm [flags]\n\n", os.Args[0])
		fmt.Fprintln(fs.Output(), "Loads the foot accessible ways of OpenStreetMap PBF extracts into the routing graph.")
		fmt.Fprintln(fs.Output(), "Importing a region again replaces it. Extracts can be downloaded from https://download.geofabrik.de.")
		fmt.Fprintln(fs.Output())
		fs.PrintDefaults()
	}
	databaseURL := fs.String("database-url", "", "The URL of the postgres database")
	importID := fs.String("import-id", "", "Run an import started on the map data page, by its ID. Replaces -region, -pbf and -bounds")
	region := fs.String("region", "", "The name of the region, e.g. switzerland")
	source := fs.String("pbf", "", "The OpenStreetMap PBF extract: a file path or URL, e.g. https://download.geofabrik.de/europe/switzerland-latest.osm.pbf. Separate neighboring extracts with commas to import them together")
	bounds := fs.String("bounds", "", "Only import ways within south,west,north,east, e.g. 46.8,8.2,47.6,9.0. Defaults to the whole extract")
	demURL := fs.String("dem-url", elevation.DefaultURL, "The base URL of the HGT elevation tiles")
	demCache := fs.String("dem-cache", elevation.DefaultCacheDir, "The directory to cache elevation tiles in, if -dem-bucket is unset")
	demBucket := fs.String("dem-bucket", "", "The Google Cloud Storage bucket to cache elevation tiles in, using the application default credentials, instead of in -dem-cache")
	logLevel := fs.String("log-level", "info", "The log level to use")
	logFormat := fs.String("log-format", "text", "The log format: \"text\" for terminals, or \"json\" for Cloud Logging")
	errorReportingProject := fs.String("error-reporting-project", "", "The Google Cloud project to report errors to with Error Reporting. If unset, errors are logged")
	otlpEndpoint := fs.String("otlp-endpoint", telemetry.LocalEndpoint, "The OTLP gRPC endpoint to export traces to. Empty disables tracing")
	traceProject := fs.String("trace-project", "", "The Google Cloud project to export traces to, using the application default credentials")
	traceSampleRate := fs.Float64("trace-sample-rate", 1, "The fraction of traces to sample, from 0 to 1")
	_ = fs.Parse(args)
	if err := setFlagsFromEnv(fs); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set flags from the environment: %v\n", err)
		return 1
	}
	log, err := newLogger(*logLevel, *logFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set up logging: %v\n", err)
		return 1
	}
	stopTracing, err := initTracing(context.Background(), *otlpEndpoint, *traceProject, *traceSampleRate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set up tracing: %v\n", err)
		return 1
	}
	report, err := newReporter(context.Background(), log, *errorReportingProject)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set up error reporting: %v\n", err)
		return 1
	}
	telemetry.TraceDefaultTransport(report)
	// Send the traces and errors not yet sent before exiting. Error
	// Reporting goes last, to report failing to send the traces.
	defer func() {
		if err := stopTracing(); err != nil {
			report.Report(context.Background(), "Failed to send the remaining traces", err)
		}
		if err := report.Close(); err != nil {
			log.Warn("Failed to send the remaining error reports", slog.String("error", err.Error()))
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runImportOSM(ctx, log, report, *databaseURL, *importID, *region, *source, *bounds, *demURL, *demCache, *demBucket); err != nil {
		report.Report(ctx, "Failed to import the OpenStreetMap extract", err)
		return 1
	}
	return 0
}

func runImportOSM(ctx context.Context, log *slog.Logger, report *errreport.Reporter, databaseURL, importID, region, source, rawBounds, demURL, demCache, demBucket string) error {
	switch {
	case databaseURL == "":
		return errors.New("database-url is required")
	case importID != "":
		// The import's region, extracts and bounds are recorded.
	case region == "":
		return errors.New("region is required")
	case source == "":
		return errors.New("pbf is required")
	}
	bounds, err := parseBounds(rawBounds)
	if err != nil {
		return err
	}
	dbURL, err := url.Parse(databaseURL)
	if err != nil {
		return fmt.Errorf("failed to parse database-url: %w", err)
	}
	// Only log warnings from the database, as the import makes large queries.
	db, err := store.New(ctx, slog.New(slog.DiscardHandler), dbURL)
	if err != nil {
		return fmt.Errorf("failed to create store: %w", err)
	}
	defer db.Close()
	// Extracts and elevation tiles are downloaded with traced clients.
	client := telemetry.NewClient(0)
	dem, err := newDEM(telemetry.WithClient(ctx, client), log, report, demURL, demCache, demBucket)
	if err != nil {
		return err
	}
	if importID != "" {
		id, err := store.StringToUUID(importID)
		if err != nil {
			return fmt.Errorf("invalid import-id: %w", err)
		}
		return routing.RunImport(ctx, log, report, client, db, dem, id)
	}
	_, err = routing.Import(ctx, log, db, routing.ImportConfig{
		Region:     region,
		Sources:    strings.Split(source, ","),
		Bounds:     bounds,
		DEM:        dem,
		HTTPClient: client,
	})
	return err
}

// parseBounds parses south,west,north,east bounds. Empty bounds are zero.
func parseBounds(s string) (geo.Bounds, error) {
	if s == "" {
		return geo.Bounds{}, nil
	}
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return geo.Bounds{}, fmt.Errorf("bounds must be south,west,north,east, got %q", s)
	}
	var values [4]float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return geo.Bounds{}, fmt.Errorf("invalid bounds %q: %w", s, err)
		}
		values[i] = v
	}
	b := geo.Bounds{MinLat: values[0], MinLng: values[1], MaxLat: values[2], MaxLng: values[3]}
	if b.MinLat >= b.MaxLat || b.MinLng >= b.MaxLng {
		return geo.Bounds{}, fmt.Errorf("bounds must have south < north and west < east, got %q", s)
	}
	return b, nil
}
