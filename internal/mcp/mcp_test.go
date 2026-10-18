package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1/activitiesv1connect"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1/geov1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/lactate/v1/lactatev1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1/plansv1connect"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/races/v1/racesv1connect"
	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1/routesv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/strava/v1/stravav1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1/tripsv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/users/v1/usersv1connect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// allServices returns every API service, with unimplemented handlers.
func allServices() []Service {
	return []Service{
		{Name: activitiesv1connect.ActivityServiceName, Handler: activitiesv1connect.UnimplementedActivityServiceHandler{}},
		{Name: geov1connect.GeoServiceName, Handler: geov1connect.UnimplementedGeoServiceHandler{}},
		{Name: lactatev1connect.LactateTestServiceName, Handler: lactatev1connect.UnimplementedLactateTestServiceHandler{}},
		{Name: plansv1connect.TrainingPlanServiceName, Handler: plansv1connect.UnimplementedTrainingPlanServiceHandler{}},
		{Name: racesv1connect.RaceServiceName, Handler: racesv1connect.UnimplementedRaceServiceHandler{}},
		{Name: racesv1connect.RacePlanServiceName, Handler: racesv1connect.UnimplementedRacePlanServiceHandler{}},
		{Name: routesv1connect.RouteServiceName, Handler: routesv1connect.UnimplementedRouteServiceHandler{}},
		{Name: stravav1connect.StravaServiceName, Handler: stravav1connect.UnimplementedStravaServiceHandler{}},
		{Name: tripsv1connect.TripReportServiceName, Handler: tripsv1connect.UnimplementedTripReportServiceHandler{}},
		{Name: usersv1connect.UserServiceName, Handler: usersv1connect.UnimplementedUserServiceHandler{}},
	}
}

func TestNewServer(t *testing.T) {
	srv, err := NewServer(discardLogger(), errreport.NewLocal(discardLogger()), allServices()...)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tool := range srv.Tools() {
		tools[tool.Name] = tool
	}

	for _, name := range []string{
		"CreateAPIToken", "ListAPITokens", "DeleteAPIToken", "GetSharedRacePlan", "DisconnectStrava",
		"UploadTripPhoto", "PublishTripReport", "UnpublishTripReport", "UpdateProfile", "GetPublicProfile", "GetPublicTripReport",
	} {
		if _, ok := tools[name]; ok {
			t.Errorf("excluded method %s is a tool", name)
		}
	}
	for _, name := range []string{
		"GetCurrentUser", "CreateRoute", "UpdateRacePlan", "SearchPlaces", "SummarizeActivities", "SyncStrava",
		"CreateTripReport", "UpdateTripReport", "UpdateTripPhoto", "GetProfile",
	} {
		if _, ok := tools[name]; !ok {
			t.Errorf("method %s is not a tool", name)
		}
	}

	tests := []struct {
		name                              string
		title                             string
		readOnly, destructive, idempotent bool
		description                       string
	}{
		{
			name:        "CreateRoute",
			title:       "Create Route",
			description: "Create and save a new route from waypoints, a GPX file, an existing activity or another route. A recorded track can be kept exactly, or turned into an editable route that follows it with as few waypoints as possible (see the conversion field).",
		},
		{
			name:        "GetRoute",
			title:       "Get Route",
			readOnly:    true,
			idempotent:  true,
			description: "Get a route by ID.",
		},
		{
			name:        "PreviewRoute",
			title:       "Preview Route",
			readOnly:    true,
			idempotent:  true,
			description: "Compute a route through the given waypoints without saving it. Returns the route statistics and, optionally, the full path. Use this to iterate on a route design before calling CreateRoute, or to try out changes to a saved route (set route_id to reuse its unchanged legs) before calling UpdateRoute. To check that a loop doesn't double back on itself, compare the stats' repeated_distance_meters with distance_meters.",
		},
		{
			name:        "DeleteRoute",
			title:       "Delete Route",
			destructive: true,
			idempotent:  true,
			description: "Delete a route.",
		},
		{
			name:       "UpdateRoute",
			title:      "Update Route",
			idempotent: true,
		},
		{
			name:       "SummarizeActivities",
			title:      "Summarize Activities",
			readOnly:   true,
			idempotent: true,
		},
		{
			name:       "FindPointsOfInterest",
			title:      "Find Points Of Interest",
			readOnly:   true,
			idempotent: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := tools[tt.name]
			if tool == nil {
				t.Fatal("tool not found")
			}
			if tool.Title != tt.title {
				t.Errorf("title = %q, want %q", tool.Title, tt.title)
			}
			if tt.description != "" && tool.Description != tt.description {
				t.Errorf("description = %q, want %q", tool.Description, tt.description)
			}
			a := tool.Annotations
			if a.ReadOnlyHint != tt.readOnly {
				t.Errorf("readOnlyHint = %v, want %v", a.ReadOnlyHint, tt.readOnly)
			}
			if *a.DestructiveHint != tt.destructive {
				t.Errorf("destructiveHint = %v, want %v", *a.DestructiveHint, tt.destructive)
			}
			if a.IdempotentHint != tt.idempotent {
				t.Errorf("idempotentHint = %v, want %v", a.IdempotentHint, tt.idempotent)
			}
			if *a.OpenWorldHint {
				t.Error("openWorldHint = true, want false")
			}
			if tool.InputSchema.(*jsonschema.Schema).Type != "object" {
				t.Error("input schema is not an object")
			}
			if tool.OutputSchema.(*jsonschema.Schema).Type != "object" {
				t.Error("output schema is not an object")
			}
		})
	}

	// Every tool from a documented service has a description.
	for _, tool := range srv.Tools() {
		if tool.Description == "" && !slices.Contains([]string{
			"CreateAthlete", "ListAthletes", "DeleteAthlete", "CreateActivity", "CreateBloodLactateMeasure",
		}, tool.Name) {
			t.Errorf("tool %s has no description", tool.Name)
		}
	}
}

func TestNewServerErrors(t *testing.T) {
	tests := []struct {
		name     string
		services []Service
		wantErr  string
	}{
		{
			name: "unknown service",
			services: []Service{
				{Name: "foo.v1.FooService", Handler: struct{}{}},
			},
			wantErr: "foo.v1.FooService",
		},
		{
			name: "nil handler",
			services: []Service{
				{Name: routesv1connect.RouteServiceName},
			},
			wantErr: "no handler",
		},
		{
			name: "missing method",
			services: []Service{
				{Name: routesv1connect.RouteServiceName, Handler: struct{}{}},
			},
			wantErr: "has no method PreviewRoute",
		},
		{
			name: "wrong service implementation",
			services: []Service{
				{Name: routesv1connect.RouteServiceName, Handler: wrongSignature{}},
			},
			wantErr: "want *routesv1.PreviewRouteRequest",
		},
		{
			name: "duplicate tool name",
			services: []Service{
				{Name: routesv1connect.RouteServiceName, Handler: routesv1connect.UnimplementedRouteServiceHandler{}},
				{Name: routesv1connect.RouteServiceName, Handler: routesv1connect.UnimplementedRouteServiceHandler{}},
			},
			wantErr: `duplicate tool name "PreviewRoute"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewServer(discardLogger(), errreport.NewLocal(discardLogger()), tt.services...)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

type wrongSignature struct{}

func (wrongSignature) PreviewRoute(context.Context, *routesv1.GetRouteRequest) (*routesv1.PreviewRouteResponse, error) {
	return nil, nil
}

func TestValidateSignature(t *testing.T) {
	in := (&routesv1.GetRouteRequest{}).ProtoReflect().Type()
	out := (&routesv1.GetRouteResponse{}).ProtoReflect().Type()
	tests := []struct {
		name    string
		fn      any
		wantErr bool
	}{
		{
			name: "valid",
			fn:   routesv1connect.UnimplementedRouteServiceHandler{}.GetRoute,
		},
		{
			name:    "wrong response",
			fn:      routesv1connect.UnimplementedRouteServiceHandler{}.DeleteRoute,
			wantErr: true,
		},
		{
			name: "no context",
			fn: func(*routesv1.GetRouteRequest) (*routesv1.GetRouteResponse, error) {
				return nil, nil
			},
			wantErr: true,
		},
		{
			name: "wrong request",
			fn: func(context.Context, *routesv1.DeleteRouteRequest) (*routesv1.GetRouteResponse, error) {
				return nil, nil
			},
			wantErr: true,
		},
		{
			name: "no response",
			fn: func(context.Context, *routesv1.GetRouteRequest) error {
				return nil
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSignature(reflect.TypeOf(tt.fn), in, out)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSignature() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestInstructions(t *testing.T) {
	srv, err := NewServer(discardLogger(), errreport.NewLocal(discardLogger()), allServices()...)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	session := connectClient(t, srv.Handler())
	initRes := session.InitializeResult()
	if initRes == nil {
		t.Fatal("no initialize result")
	}
	if initRes.ServerInfo.Name != "trail-tools" {
		t.Errorf("server name = %q, want trail-tools", initRes.ServerInfo.Name)
	}
	for _, want := range []string{
		"## routes.v1.RouteService\n\nRouteService creates and edits running routes.",
		"## plans.v1.TrainingPlanService\n\n",
		"\n1. Months: the training phase (e.g. base, build, peak, taper) and monthly volume targets.\n2. Weeks:",
		"## races.v1.RacePlanService\n\n",
		"## trips.v1.TripReportService\n\nTripReportService writes trip reports",
	} {
		if !strings.Contains(initRes.Instructions, want) {
			t.Errorf("instructions do not contain %q:\n%s", want, initRes.Instructions)
		}
	}

	var names []string
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	if len(names) != len(srv.Tools()) {
		t.Errorf("listed %d tools, want %d", len(names), len(srv.Tools()))
	}
}

type ctxKey struct{}

// fakeRoutes is a route service implementation for testing.
type fakeRoutes struct {
	routesv1connect.UnimplementedRouteServiceHandler

	mu       sync.Mutex
	requests []proto.Message
}

func (f *fakeRoutes) record(m proto.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, m)
}

func (f *fakeRoutes) GetRoute(ctx context.Context, req *routesv1.GetRouteRequest) (*routesv1.GetRouteResponse, error) {
	f.record(req)
	user, ok := ctx.Value(ctxKey{}).(string)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "no user in context")
	}
	if req.GetId() == "missing" {
		return nil, connect.NewError(connect.CodeNotFound, "route not found")
	}
	return routesv1.GetRouteResponse_builder{
		Route: routesv1.Route_builder{
			Id:   req.GetId(),
			Name: "Route of " + user,
			Waypoints: []*geov1.LatLng{
				geov1.LatLng_builder{Lat: 51.5, Lng: -0.1}.Build(),
			},
			Source:     routesv1.RouteSource_ROUTE_SOURCE_WAYPOINTS,
			CreateTime: timestamppb.New(time.Date(2026, 5, 1, 7, 30, 0, 0, time.UTC)),
		}.Build(),
	}.Build(), nil
}

func (f *fakeRoutes) DeleteRoute(context.Context, *routesv1.DeleteRouteRequest) (*routesv1.DeleteRouteResponse, error) {
	return nil, errors.New("database is down")
}

func (f *fakeRoutes) ListRoutes(context.Context, *routesv1.ListRoutesRequest) (*routesv1.ListRoutesResponse, error) {
	panic("boom")
}

// fakeRacePlans is a race plan service implementation for testing.
type fakeRacePlans struct {
	racesv1connect.UnimplementedRacePlanServiceHandler

	got chan *racesv1.UpdateRacePlanRequest
}

func (f *fakeRacePlans) UpdateRacePlan(_ context.Context, req *racesv1.UpdateRacePlanRequest) (*racesv1.UpdateRacePlanResponse, error) {
	f.got <- req
	return racesv1.UpdateRacePlanResponse_builder{Plan: req.GetPlan()}.Build(), nil
}

// connectClient connects an MCP client to an HTTP server serving h.
func connectClient(t *testing.T, h http.Handler) *mcp.ClientSession {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestCallTool(t *testing.T) {
	routes := &fakeRoutes{}
	racePlans := &fakeRacePlans{got: make(chan *racesv1.UpdateRacePlanRequest, 1)}
	srv, err := NewServer(discardLogger(), errreport.NewLocal(discardLogger()),
		Service{Name: routesv1connect.RouteServiceName, Handler: routes},
		Service{Name: racesv1connect.RacePlanServiceName, Handler: racePlans},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate authentication middleware that puts the user on the context.
	authn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), ctxKey{}, "alice")
		srv.Handler().ServeHTTP(w, r.WithContext(ctx))
	})
	session := connectClient(t, authn)
	ctx := t.Context()

	t.Run("success", func(t *testing.T) {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "GetRoute",
			Arguments: map[string]any{"id": "r1", "includePoints": true},
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("tool error: %v", res.Content)
		}
		got := &routesv1.GetRouteResponse{}
		data, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := protojson.Unmarshal(data, got); err != nil {
			t.Fatalf("unmarshaling structured content %s: %v", data, err)
		}
		if got.GetRoute().GetName() != "Route of alice" {
			t.Errorf("route name = %q, want the context value to reach the handler", got.GetRoute().GetName())
		}
		if got.GetRoute().GetId() != "r1" {
			t.Errorf("route id = %q, want r1", got.GetRoute().GetId())
		}
		// Fields with default values are included, so agents can tell
		// them from fields they don't know.
		var fields struct {
			Route map[string]any `json:"route"`
		}
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if v, ok := fields.Route["description"]; !ok || v != "" {
			t.Errorf("route description = %v (present %v), want empty", v, ok)
		}
		if len(res.Content) != 1 {
			t.Fatalf("got %d content blocks, want 1", len(res.Content))
		}
		text := res.Content[0].(*mcp.TextContent).Text
		textMsg := &routesv1.GetRouteResponse{}
		if err := protojson.Unmarshal([]byte(text), textMsg); err != nil {
			t.Fatalf("unmarshaling text content: %v", err)
		}
		if !proto.Equal(got, textMsg) {
			t.Errorf("text content %v != structured content %v", textMsg, got)
		}

		// The structured content validates against the output schema.
		var tool *mcp.Tool
		for _, tl := range srv.Tools() {
			if tl.Name == "GetRoute" {
				tool = tl
			}
		}
		resolved, err := tool.OutputSchema.(*jsonschema.Schema).Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		var instance any
		if err := json.Unmarshal(data, &instance); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(instance); err != nil {
			t.Errorf("structured content does not validate against the output schema: %v", err)
		}
	})

	t.Run("no arguments", func(t *testing.T) {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "GetRoute"})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("tool error: %v", res.Content)
		}
	})

	tests := []struct {
		name     string
		tool     string
		args     map[string]any
		wantText string
	}{
		{
			name:     "connect error",
			tool:     "GetRoute",
			args:     map[string]any{"id": "missing"},
			wantText: "not_found: route not found",
		},
		{
			name:     "plain error",
			tool:     "DeleteRoute",
			args:     map[string]any{"id": "r1"},
			wantText: "unknown: database is down",
		},
		{
			name:     "unimplemented",
			tool:     "PreviewRoute",
			wantText: "unimplemented: routes.v1.RouteService.PreviewRoute is not implemented",
		},
		{
			name:     "panic",
			tool:     "ListRoutes",
			wantText: "internal: internal error",
		},
		{
			name:     "unknown field",
			tool:     "GetRoute",
			args:     map[string]any{"routeId": "r1"},
			wantText: `invalid_argument: invalid arguments, check them against the tool input schema: proto:`,
		},
		{
			name:     "wrong type",
			tool:     "GetRoute",
			args:     map[string]any{"id": 1},
			wantText: `invalid_argument: invalid arguments`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tt.tool, Arguments: tt.args})
			if err != nil {
				t.Fatal(err)
			}
			if !res.IsError {
				t.Fatal("expected a tool error")
			}
			text := res.Content[0].(*mcp.TextContent).Text
			if !strings.HasPrefix(text, tt.wantText) {
				t.Errorf("error text = %q, want prefix %q", text, tt.wantText)
			}
		})
	}

	t.Run("output only fields are cleared", func(t *testing.T) {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "UpdateRacePlan",
			Arguments: map[string]any{
				"plan": map[string]any{
					"id":         "p1",
					"raceId":     "r1",
					"createTime": "2026-01-01T00:00:00Z",
					"goals":      map[string]any{"aFinishSeconds": 36000},
				},
				"updateMask": "goals",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("tool error: %v", res.Content)
		}
		got := <-racePlans.got
		plan := got.GetPlan()
		if plan.GetId() != "p1" {
			t.Errorf("plan ID = %q, want p1", plan.GetId())
		}
		if plan.GetRaceId() != "" || plan.HasCreateTime() {
			t.Errorf("output only fields were not cleared: %v", plan)
		}
		if plan.GetGoals().GetAFinishSeconds() != 36000 {
			t.Errorf("goals = %v, want aFinishSeconds 36000", plan.GetGoals())
		}
		if paths := got.GetUpdateMask().GetPaths(); !slices.Equal(paths, []string{"goals"}) {
			t.Errorf("update mask = %v, want [goals]", paths)
		}
	})
}
