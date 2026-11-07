package telemetry

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"go.opentelemetry.io/otel/attribute"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestServerSpanName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		method, path, pattern, want string
	}{
		{"POST", "/trips.v1.TripReportService/GetTripReport", "/trips.v1.TripReportService/", "/trips.v1.TripReportService/GetTripReport"},
		{"GET", "/routes/abc.fit", "/routes/", "GET /routes/"},
		{"GET", "/photos/abc/large.jpg", "GET /photos/", "GET /photos/"},
		{"GET", "/u/jane", "", "GET"},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Pattern = tc.pattern
		if got := serverSpanName("", r); got != tc.want {
			t.Errorf("serverSpanName(%s %s, %q) = %q, want %q", tc.method, tc.path, tc.pattern, got, tc.want)
		}
	}
}

func TestIsProcedure(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		"/trips.v1.TripReportService/GetTripReport": true,
		"/users.v1.UserService/GetCurrentUser":      true,
		"/.well-known/oauth-authorization-server":   false,
		"/trips.v1.TripReportService/":              false,
		"/trips.v1.TripReportService/Get/Extra":     false,
		"/routes/abc.fit":                           false,
		"/index.js":                                 false,
		"/":                                         false,
	} {
		if got := isProcedure(path); got != want {
			t.Errorf("isProcedure(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestTraceDefaultTransport checks that requests with the default client
// are traced and reported.
func TestTraceDefaultTransport(t *testing.T) {
	// Replaces the default transport, so not parallel.
	prevTransport, prevClient := http.DefaultTransport, http.DefaultClient
	t.Cleanup(func() {
		http.DefaultTransport, http.DefaultClient = prevTransport, prevClient
	})
	var buf bytes.Buffer
	TraceDefaultTransport(errreport.NewLocal(slog.New(slog.NewTextHandler(&buf, nil))))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := buf.String(); !strings.Contains(got, `msg="A request used the default HTTP client"`) {
		t.Errorf("the request wasn't reported: %q", got)
	}
}

func TestIsStaticAsset(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		"/index.js":             true,
		"/fonts/lexend.woff2":   true,
		"/img/og.png":           true,
		"/":                     false,
		"/routes/abc.gpx":       false,
		"/index.json":           false,
		"/users.v1.UserService": false,
	} {
		if got := isStaticAsset(path); got != want {
			t.Errorf("isStaticAsset(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestTracing checks that a request with a traced client to a traced server
// makes a client span, and a server span in a new trace linked to it, with
// secrets redacted from both.
func TestTracing(t *testing.T) {
	// Sets the global trace provider and propagator, so not parallel.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prevProvider, prevPropagator := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevProvider)
		otel.SetTextMapPropagator(prevPropagator)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /routes/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("POST /webhook/", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("GET /index.js", func(w http.ResponseWriter, r *http.Request) {})
	// A middleware passing on a copy of the request, like the live reload.
	copying := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Routes(mux).ServeHTTP(w, r.WithContext(r.Context()))
	})
	srv := httptest.NewServer(Handler(copying, "/webhook/"))
	t.Cleanup(srv.Close)

	const secret = "hunter2"
	client := NewClient(0)
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/routes/abc.gpx?client_secret=" + secret + "&page=1"},
		{http.MethodPost, "/webhook/" + secret},
		{http.MethodGet, "/index.js"},
	} {
		req, err := http.NewRequestWithContext(context.Background(), r.method, srv.URL+r.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}

	var clientSpans []tracetest.SpanStub
	serverSpans := map[string]tracetest.SpanStub{}
	var names []string
	for _, s := range exporter.GetSpans() {
		names = append(names, s.Name)
		switch s.SpanKind {
		case trace.SpanKindClient:
			clientSpans = append(clientSpans, s)
		case trace.SpanKindServer:
			serverSpans[s.Name] = s
			for _, a := range s.Attributes {
				if strings.Contains(a.Value.String(), secret) {
					t.Errorf("server span %q has the secret in %s: %q", s.Name, a.Key, a.Value.String())
				}
			}
		}
	}
	// Static assets aren't traced on the server, only by the client.
	if len(clientSpans) != 3 || len(serverSpans) != 2 {
		t.Fatalf("got spans %v, want 3 client spans and 2 server spans", names)
	}
	host := srv.Listener.Addr().String()
	for _, s := range clientSpans {
		if _, h, _ := strings.Cut(s.Name, " "); h != host {
			t.Errorf("client span %q, want it named after the method and host", s.Name)
		}
		// The span has the URL, with its path.
		if !slices.ContainsFunc(s.Attributes, func(a attribute.KeyValue) bool {
			return a.Key == "url.full" && strings.HasPrefix(a.Value.AsString(), srv.URL+"/")
		}) {
			t.Errorf("client span %q has no url.full: %v", s.Name, s.Attributes)
		}
	}
	if !slices.ContainsFunc(clientSpans, func(s tracetest.SpanStub) bool {
		return slices.Contains(s.Attributes, attribute.String("url.full", srv.URL+"/routes/abc.gpx?client_secret=REDACTED&page=1"))
	}) {
		t.Errorf("no client span has the URL with the client secret redacted")
	}
	server, ok := serverSpans["GET /routes/"]
	if !ok {
		t.Fatalf("got server spans %v, want one named after the route", names)
	}
	// The server doesn't trust the trace context from the client: it starts
	// a new trace, linked to the client's.
	if server.Parent.IsValid() {
		t.Errorf("the server span has the parent %v, want none", server.Parent)
	}
	if !slices.ContainsFunc(clientSpans, func(c tracetest.SpanStub) bool {
		return server.SpanContext.TraceID() != c.SpanContext.TraceID() &&
			slices.ContainsFunc(server.Links, func(l sdktrace.Link) bool {
				return l.SpanContext.SpanID() == c.SpanContext.SpanID()
			})
	}) {
		t.Errorf("the server span isn't in a new trace linked to a client span")
	}
	webhook := serverSpans["POST /webhook/"]
	if !slices.Contains(webhook.Attributes, attribute.String("url.path", "/webhook/REDACTED")) {
		t.Errorf("the webhook's server span has no redacted url.path: %v", webhook.Attributes)
	}
}

func TestInitWithoutEndpoint(t *testing.T) {
	t.Parallel()
	shutdown, err := Init(context.Background(), &Config{})
	if err != nil {
		t.Fatalf("Init without an endpoint: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
	if _, err := Init(context.Background(), &Config{Endpoint: LocalEndpoint, SampleRate: 2}); err == nil {
		t.Error("Init accepted a sample rate of 2")
	}
}
