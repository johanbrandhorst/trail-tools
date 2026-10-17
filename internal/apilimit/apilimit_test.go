package apilimit

import (
	"strings"
	"testing"

	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
	"google.golang.org/protobuf/proto"
)

func TestCountBinary(t *testing.T) {
	t.Parallel()
	latLngs := func(n int) []*geov1.LatLng {
		points := make([]*geov1.LatLng, n)
		for i := range points {
			points[i] = geov1.LatLng_builder{Lat: 46, Lng: 7}.Build()
		}
		return points
	}
	for name, tt := range map[string]struct {
		msg     proto.Message
		wantErr bool
	}{
		"empty": {msg: routesv1.PreviewRouteRequest_builder{}.Build()},
		"at most values": {
			msg: routesv1.PreviewRouteRequest_builder{Waypoints: latLngs(MaxRequestValues)}.Build(),
		},
		"too many messages": {
			msg:     routesv1.PreviewRouteRequest_builder{Waypoints: latLngs(MaxRequestValues + 1)}.Build(),
			wantErr: true,
		},
		// The path and its points.
		"too many nested messages": {
			msg: routesv1.GetRouteForecastRequest_builder{
				Path: routesv1.Path_builder{Points: latLngs(MaxRequestValues)}.Build(),
			}.Build(),
			wantErr: true,
		},
		// A large string is one value.
		"large string": {
			msg: routesv1.CreateRouteRequest_builder{Gpx: new(strings.Repeat("<trkpt/>", 100_000))}.Build(),
		},
		// Packed scalars take no more memory decoded than encoded.
		"packed enums": {
			msg: routesv1.PreviewRouteRequest_builder{
				LegProfiles: make([]routesv1.RoutingProfile, 2*MaxRequestValues),
			}.Build(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data, err := proto.Marshal(tt.msg)
			if err != nil {
				t.Fatal(err)
			}
			if err := countBinary(data, tt.msg.ProtoReflect().Descriptor()); (err != nil) != tt.wantErr {
				t.Errorf("countBinary returned %v, want error %v", err, tt.wantErr)
			}
		})
	}
}

func TestCountJSON(t *testing.T) {
	t.Parallel()
	repeat := func(s string, n int) string {
		return strings.TrimSuffix(strings.Repeat(s+",", n), ",")
	}
	for name, tt := range map[string]struct {
		json    string
		wantErr bool
	}{
		"empty objects":             {json: `{"waypoints":[` + repeat("{}", MaxRequestValues-1) + `]}`},
		"too many empty objects":    {json: `{"waypoints":[` + repeat("{}", MaxRequestValues) + `]}`, wantErr: true},
		"keys aren't counted":       {json: `{"waypoints":[` + repeat(`{"lat":1,"lng":2}`, MaxRequestValues-1) + `]}`},
		"too many strings":          {json: `{"legProfiles":[` + repeat(`"TRAIL"`, MaxRequestValues) + `]}`, wantErr: true},
		"strings with spaces":       {json: `{"legProfiles": [` + repeat(` "TRAIL" `, MaxRequestValues) + `]}`, wantErr: true},
		"numbers aren't counted":    {json: `{"elevations":[` + repeat("1", 2*MaxRequestValues) + `]}`},
		"braces in strings":         {json: `{"gpx":"` + strings.Repeat(`{\"}`, MaxRequestValues) + `"}`},
		"escaped quotes in strings": {json: `{"gpx":"` + strings.Repeat(`\"\\`, MaxRequestValues) + `"}`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := countJSON([]byte(tt.json), nil); (err != nil) != tt.wantErr {
				t.Errorf("countJSON returned %v, want error %v", err, tt.wantErr)
			}
		})
	}
}
