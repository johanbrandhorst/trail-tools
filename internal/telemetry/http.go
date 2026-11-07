package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
)

// baseTransport is the default transport, before TraceDefaultTransport
// replaces it.
var baseTransport = http.DefaultTransport

// NewClient returns an HTTP client whose requests are traced, with a
// timeout, or none if it's 0. Pass it to everything that makes requests.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: Transport(nil), Timeout: timeout}
}

// Transport returns a transport that traces the requests it sends with
// base, or the default transport if base is nil.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = baseTransport
	}
	return otelhttp.NewTransport(redactingTransport{base: base}, otelhttp.WithSpanNameFormatter(clientSpanName))
}

// secretQueryParameters are the query parameters whose values are secret,
// like the client secret Strava's push subscriptions take in the query, and
// the ones OpenTelemetry's semantic conventions redact by default.
var secretQueryParameters = []string{
	"access_token", "api_key", "client_secret", "code", "hub.verify_token", "key",
	"refresh_token", "token", "verify_token",
	"AWSAccessKeyId", "Signature", "sig", "X-Goog-Signature",
}

// redactedValue replaces secrets in span attributes, as in OpenTelemetry's
// semantic conventions.
const redactedValue = "REDACTED"

// redactingTransport redacts the secrets in the query of the url.full
// attribute of the client span otelhttp started, which it records as is.
// Traces are readable by everyone with viewer access to the project, so
// secrets would leak there. It runs inside otelhttp's transport, which
// passes it the request with its span in the context.
type redactingTransport struct {
	base http.RoundTripper
}

func (t redactingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if u, ok := redactQuery(r.URL); ok {
		// Setting an attribute again replaces its value.
		trace.SpanFromContext(r.Context()).SetAttributes(semconv.URLFull(u))
	}
	return t.base.RoundTrip(r)
}

// redactQuery returns the URL without user info and with the values of
// secret query parameters redacted, and whether it had any.
func redactQuery(u *url.URL) (string, bool) {
	if u == nil || u.RawQuery == "" {
		return "", false
	}
	query := u.Query()
	redacted := false
	for _, name := range secretQueryParameters {
		if values, ok := query[name]; ok {
			for i := range values {
				values[i] = redactedValue
			}
			redacted = true
		}
	}
	if !redacted {
		return "", false
	}
	c := *u
	c.User = nil
	c.RawQuery = query.Encode()
	return c.String(), true
}

// clientSpanName names a request after its method and host, e.g. "GET
// api.open-meteo.com". Many paths have IDs or names, like Strava's
// activities, elevation tiles and stored objects, which would make a span
// name for each. The span's url.full attribute has the whole URL.
func clientSpanName(_ string, r *http.Request) string {
	if r.URL == nil || r.URL.Host == "" {
		return r.Method
	}
	return r.Method + " " + r.URL.Host
}

// WithClient returns a context with an HTTP client for libraries that take
// theirs from the context, like golang.org/x/oauth2 and the Google API
// clients created with google.DefaultClient.
func WithClient(ctx context.Context, client *http.Client) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, client)
}

// GRPCDialOption traces the calls of a gRPC client, like the Google Cloud
// client libraries.
func GRPCDialOption() grpc.DialOption {
	return grpc.WithStatsHandler(otelgrpc.NewClientHandler())
}

// errDefaultClient is reported for requests sent with the default client.
var errDefaultClient = errors.New("give the caller a client from telemetry.NewClient")

// TraceDefaultTransport traces the requests sent with http.DefaultClient
// and http.DefaultTransport, by code that wasn't given a client, and reports
// them, so the code can be given one. The report's stack trace shows where
// the request came from.
func TraceDefaultTransport(report *errreport.Reporter) {
	traced := reportingTransport{traced: Transport(nil), report: report}
	http.DefaultTransport = traced
	http.DefaultClient = &http.Client{Transport: traced}
}

// reportingTransport reports each request.
type reportingTransport struct {
	traced http.RoundTripper
	report *errreport.Reporter
}

func (t reportingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.report.Report(r.Context(), "A request used the default HTTP client", errDefaultClient, slog.String("host", r.URL.Host))
	return t.traced.RoundTrip(r)
}

// Handler traces the requests to an HTTP server, except for static
// assets, which would only add noise. The rest of the paths starting with
// one of secretPathPrefixes, like the secret in the Strava webhook's path,
// are redacted from the url.path attribute.
//
// The server is on the public internet, so it doesn't trust the trace
// context of requests: each request starts a new trace, linked to the trace
// it came with, if any. Otherwise clients could choose to sample their
// requests, and pick the trace IDs of others' requests.
func Handler(h http.Handler, secretPathPrefixes ...string) http.Handler {
	if len(secretPathPrefixes) > 0 {
		h = redactPaths(h, secretPathPrefixes)
	}
	return otelhttp.NewHandler(h, "",
		otelhttp.WithSpanNameFormatter(serverSpanName),
		otelhttp.WithFilter(func(r *http.Request) bool { return !isStaticAsset(r.URL.Path) }),
		otelhttp.WithPublicEndpointFn(func(*http.Request) bool { return true }),
	)
}

// redactPaths redacts the rest of the paths starting with one of prefixes
// from the url.path attribute of the server span otelhttp started.
func redactPaths(h http.Handler, prefixes []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, prefix := range prefixes {
			if len(r.URL.Path) > len(prefix) && strings.HasPrefix(r.URL.Path, prefix) {
				// Setting an attribute again replaces its value.
				trace.SpanFromContext(r.Context()).SetAttributes(semconv.URLPath(prefix + redactedValue))
				break
			}
		}
		h.ServeHTTP(w, r)
	})
}

// Routes names the server spans of the requests a mux serves after the
// route they matched, e.g. "GET /routes/". Put it right around the mux:
// handlers in between may pass on a copy of the request, whose route the
// handler from Handler doesn't see.
func Routes(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		if r.Pattern == "" {
			return
		}
		span := trace.SpanFromContext(r.Context())
		span.SetName(serverSpanName("", r))
		_, route, ok := strings.Cut(r.Pattern, " ")
		if !ok {
			route = r.Pattern
		}
		span.SetAttributes(semconv.HTTPRoute(route))
	})
}

// isProcedure reports whether a path is a Connect RPC's: a service's full
// name and a method, e.g. /trips.v1.TripReportService/GetTripReport. Method
// names start with a capital letter, unlike the paths of /.well-known/.
func isProcedure(path string) bool {
	service, method, ok := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	return ok && strings.Contains(service, ".") && !strings.HasPrefix(service, ".") &&
		method != "" && !strings.Contains(method, "/") && unicode.IsUpper(rune(method[0]))
}

// serverSpanName names a request after its RPC, or its method and the
// route it matched once that's known, e.g. "GET /routes/".
func serverSpanName(_ string, r *http.Request) string {
	if isProcedure(r.URL.Path) {
		return r.URL.Path
	}
	if r.Pattern != "" {
		// Patterns can include the method, e.g. "GET /photos/".
		if _, pattern, ok := strings.Cut(r.Pattern, " "); ok {
			return r.Method + " " + pattern
		}
		return r.Method + " " + r.Pattern
	}
	return r.Method
}

// staticAssets are the paths of static assets, or their prefixes ending in
// a slash.
var staticAssets = []string{"/index.js", "/index.js.map", "/index.css", "/favicon.svg", "/fonts/", "/img/"}

func isStaticAsset(path string) bool {
	for _, asset := range staticAssets {
		if path == asset || (strings.HasSuffix(asset, "/") && strings.HasPrefix(path, asset)) {
			return true
		}
	}
	return false
}
