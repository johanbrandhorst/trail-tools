// Package telemetry traces the app with OpenTelemetry: it exports traces
// with OTLP, to a local collector like Grafana Tempo or to Google Cloud, and
// traces HTTP servers and clients. Only traces are exported; Cloud Run
// already has metrics of the requests.
package telemetry

import (
	"context"
	"crypto/tls"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
)

// LocalEndpoint is the OTLP gRPC endpoint of a collector running locally,
// like the grafana/otel-lgtm container.
const LocalEndpoint = "http://localhost:4317"

// Config configures tracing.
type Config struct {
	// The OTLP gRPC endpoint to export traces to, e.g.
	// http://localhost:4317 for a local collector, or
	// https://telemetry.googleapis.com for Google Cloud. Empty disables
	// exporting.
	Endpoint string
	// The Google Cloud project to export traces to, with the application
	// default credentials. Empty for a local collector.
	GCPProject     string
	ServiceName    string
	ServiceVersion string
	// The fraction of traces to sample, from 0 to 1.
	SampleRate float64
}

// Init sets up the global trace provider and propagator. The returned
// function sends the remaining traces and stops exporting; call it before
// exiting.
func Init(ctx context.Context, c *Config) (func(context.Context) error, error) {
	// Trace context is propagated with requests to other services, in the
	// W3C Trace Context format, which Cloud Run uses too. Baggage isn't:
	// nothing sets it, and baggage from clients would be passed on to the
	// APIs the server calls. Handler doesn't trust the trace context of
	// requests to the server.
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if c.Endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	if c.SampleRate < 0 || c.SampleRate > 1 {
		return nil, fmt.Errorf("the sample rate must be from 0 to 1, got %v", c.SampleRate)
	}

	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpointURL(c.Endpoint)}
	attrs := []attribute.KeyValue{
		semconv.ServiceName(c.ServiceName),
		semconv.ServiceVersion(c.ServiceVersion),
	}
	if c.GCPProject != "" {
		creds, err := oauth.NewApplicationDefault(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to find the default credentials: %w", err)
		}
		opts = append(opts,
			// The credentials may have access to several projects.
			otlptracegrpc.WithHeaders(map[string]string{"x-goog-user-project": c.GCPProject}),
			otlptracegrpc.WithDialOption(grpc.WithPerRPCCredentials(creds)),
			otlptracegrpc.WithTLSCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13})),
		)
		attrs = append(attrs,
			semconv.CloudProviderGCP,
			semconv.CloudAccountID(c.GCPProject),
			// Google Cloud's OTLP endpoint requires the project.
			attribute.String("gcp.project_id", c.GCPProject),
		)
	} else {
		// A local collector may not be running, which isn't worth
		// reporting: traces show up once it is.
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {}))
	}
	res, err := resource.New(ctx,
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(attrs...),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create the telemetry resource: %w", err)
	}
	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create the OTLP trace exporter: %w", err)
	}
	sampler := sdktrace.TraceIDRatioBased(c.SampleRate)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		// Requests to the server start new traces, as Handler doesn't trust
		// the trace context they come with, so the sampler decides for each
		// request, and its spans follow.
		sdktrace.WithSampler(sdktrace.ParentBased(sampler)),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}
