package mcp

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"

	// Register the types of the services the tests don't otherwise use, so
	// every API method's schema can be validated.
	_ "github.com/bs-tools/trailrunn.ing/internal/gen/auth/v1"
	_ "github.com/bs-tools/trailrunn.ing/internal/gen/garmin/v1"
	_ "github.com/bs-tools/trailrunn.ing/internal/gen/oauth/v1"
	_ "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
	_ "github.com/bs-tools/trailrunn.ing/internal/gen/routing/v1"

	"github.com/bs-tools/trailrunn.ing/internal/apidesc"
	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// findMessage returns the descriptor of a message from the embedded
// descriptor set.
func findMessage(t *testing.T, name protoreflect.FullName) protoreflect.MessageDescriptor {
	t.Helper()
	files, err := apidesc.Files()
	if err != nil {
		t.Fatal(err)
	}
	d, err := files.FindDescriptorByName(name)
	if err != nil {
		t.Fatalf("finding %s: %v", name, err)
	}
	return d.(protoreflect.MessageDescriptor)
}

func propertyNames(s *jsonschema.Schema) []string {
	return s.PropertyOrder
}

func TestInputSchemaOneof(t *testing.T) {
	s := newInputSchema(findMessage(t, "routes.v1.CreateRouteRequest"))
	if s.Type != "object" {
		t.Errorf("type = %q, want object", s.Type)
	}
	want := []string{"name", "description", "waypoints", "gpx", "activityId", "routeId", "includePoints", "conversion"}
	if got := propertyNames(s); !slices.Equal(got, want) {
		t.Errorf("properties = %v, want %v", got, want)
	}

	gpx := s.Properties["gpx"]
	if gpx.Type != "string" {
		t.Errorf("gpx type = %q, want string", gpx.Type)
	}
	for _, want := range []string{"The contents of a GPX file.", `Mutually exclusive with "waypoints", "activityId", "routeId" (oneof "source")`} {
		if !strings.Contains(gpx.Description, want) {
			t.Errorf("gpx description %q does not contain %q", gpx.Description, want)
		}
	}

	waypoints := s.Properties["waypoints"]
	if waypoints.Type != "object" {
		t.Fatalf("waypoints type = %q, want object", waypoints.Type)
	}
	if !strings.HasPrefix(waypoints.Description, "Route between these waypoints.") {
		t.Errorf("waypoints description = %q", waypoints.Description)
	}
	list := waypoints.Properties["waypoints"]
	if list.Type != "array" || list.Items.Type != "object" {
		t.Fatalf("waypoints.waypoints = %+v, want array of objects", list)
	}
	if lat := list.Items.Properties["lat"]; lat == nil || lat.Type != "number" {
		t.Errorf("waypoints.waypoints[].lat = %+v, want number", lat)
	}

	profile := waypoints.Properties["profile"]
	if profile.Type != "string" {
		t.Errorf("profile type = %q, want string", profile.Type)
	}
	wantEnum := []any{"ROUTING_PROFILE_TRAIL", "ROUTING_PROFILE_SHORTEST", "ROUTING_PROFILE_STRAIGHT", "ROUTING_PROFILE_HEATMAP"}
	if !slices.Equal(profile.Enum, wantEnum) {
		t.Errorf("profile enum = %v, want %v", profile.Enum, wantEnum)
	}
	if !strings.Contains(profile.Description, "- ROUTING_PROFILE_STRAIGHT: A straight line between the waypoints") {
		t.Errorf("profile description %q does not describe enum values", profile.Description)
	}

	legProfiles := waypoints.Properties["legProfiles"]
	if legProfiles == nil || legProfiles.Type != "array" || legProfiles.Items.Type != "string" {
		t.Fatalf("waypoints.legProfiles = %+v, want array of strings", legProfiles)
	}
	if !slices.Contains(legProfiles.Items.Enum, any("ROUTING_PROFILE_HEATMAP")) {
		t.Errorf("legProfiles enum = %v, want the routing profiles", legProfiles.Items.Enum)
	}
}

func TestInputSchemaOptional(t *testing.T) {
	s := newInputSchema(findMessage(t, "routes.v1.UpdateRouteRequest"))
	want := []string{"id", "name", "description", "waypoints", "includePoints"}
	if got := propertyNames(s); !slices.Equal(got, want) {
		t.Errorf("properties = %v, want %v", got, want)
	}
	if desc := s.Properties["name"].Description; strings.Contains(desc, "Mutually exclusive") {
		t.Errorf("proto3 optional field described as oneof: %q", desc)
	}
}

func TestInputSchemaOutputOnly(t *testing.T) {
	s := newInputSchema(findMessage(t, "races.v1.UpdateRacePlanRequest"))
	mask := s.Properties["updateMask"]
	if mask.Type != "string" || !strings.Contains(mask.Description, "comma-separated list of lowerCamelCase field paths") {
		t.Errorf("updateMask = %+v, want comma-separated string", mask)
	}
	plan := s.Properties["plan"]
	for _, name := range []string{"raceId", "shareUrl", "createTime", "updateTime"} {
		if _, ok := plan.Properties[name]; ok {
			t.Errorf("output only field plan.%s is in the input schema", name)
		}
	}
	for _, name := range []string{"id", "goals", "checkpoints", "nutrition"} {
		if _, ok := plan.Properties[name]; !ok {
			t.Errorf("plan.%s is missing from the input schema", name)
		}
	}

	// Output only fields are hidden at any depth.
	route := newInputSchema(findMessage(t, "races.v1.CreateRaceRequest"))
	var check func(path string, s *jsonschema.Schema)
	check = func(path string, s *jsonschema.Schema) {
		for name, p := range s.Properties {
			if name == "createTime" || name == "updateTime" {
				t.Errorf("output only field %s.%s is in the input schema", path, name)
			}
			check(path+"."+name, p)
		}
		if s.Items != nil {
			check(path+"[]", s.Items)
		}
	}
	check("CreateRaceRequest", route)

	// But they are part of the output schema.
	out := newOutputSchema(findMessage(t, "races.v1.UpdateRacePlanResponse"))
	outPlan := out.Properties["plan"]
	for _, name := range []string{"id", "raceId", "shareUrl", "createTime", "updateTime"} {
		if _, ok := outPlan.Properties[name]; !ok {
			t.Errorf("plan.%s is missing from the output schema", name)
		}
	}
	if ct := outPlan.Properties["createTime"]; ct.Type != "string" || ct.Format != "date-time" {
		t.Errorf("plan.createTime = %+v, want date-time string", ct)
	}
}

func TestOutputSchemaEnumIncludesZero(t *testing.T) {
	s := newOutputSchema(findMessage(t, "routes.v1.GetRouteResponse"))
	source := s.Properties["route"].Properties["source"]
	if !slices.Contains(source.Enum, any("ROUTE_SOURCE_UNSPECIFIED")) {
		t.Errorf("output enum %v does not contain the zero value", source.Enum)
	}
}

func TestSchemaEnumOmitsDeprecated(t *testing.T) {
	// No request takes an enum with deprecated values, so the test takes a
	// message with a field of one, the workout type.
	files, err := apidesc.Files()
	if err != nil {
		t.Fatal(err)
	}
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/v1/test.proto"),
		Package:    proto.String("test.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"plans/v1/plans.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Request"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("type"),
				JsonName: proto.String("type"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
				TypeName: proto.String(".plans.v1.WorkoutType"),
			}},
		}},
	}, files)
	if err != nil {
		t.Fatal(err)
	}
	md := fd.Messages().ByName("Request")
	types := newInputSchema(md).Properties["type"].Enum
	if !slices.Contains(types, any("WORKOUT_TYPE_BASE")) || slices.Contains(types, any("WORKOUT_TYPE_EASY")) {
		t.Errorf("workout types %v, want only the current ones", types)
	}
	// Outputs may still have them.
	if types := newOutputSchema(md).Properties["type"].Enum; !slices.Contains(types, any("WORKOUT_TYPE_EASY")) {
		t.Errorf("output workout types %v, want the deprecated ones too", types)
	}
}

func TestSchemaFieldKinds(t *testing.T) {
	// descriptor.proto has fields of most kinds, as well as recursive
	// messages.
	md := findMessage(t, "google.protobuf.FieldDescriptorProto")
	in := newInputSchema(md)
	if got := in.Properties["number"].Type; got != "integer" {
		t.Errorf("int32 type = %q, want integer", got)
	}
	if got := in.Properties["proto3Optional"].Type; got != "boolean" {
		t.Errorf("bool type = %q, want boolean", got)
	}

	uninterpreted := findMessage(t, "google.protobuf.UninterpretedOption")
	in = newInputSchema(uninterpreted)
	out := newOutputSchema(uninterpreted)
	if got := in.Properties["positiveIntValue"].Types; !slices.Equal(got, []string{"integer", "string"}) {
		t.Errorf("uint64 types = %v, want integer and string", got)
	}
	if got := in.Properties["negativeIntValue"].Types; !slices.Equal(got, []string{"integer", "string"}) {
		t.Errorf("int64 types = %v, want integer and string", got)
	}
	if got := in.Properties["doubleValue"].Type; got != "number" {
		t.Errorf("double input type = %q, want number", got)
	}
	if got := out.Properties["doubleValue"].AnyOf; len(got) != 2 {
		t.Errorf("double output anyOf = %v, want number or non-finite string", got)
	}
	if got := in.Properties["stringValue"]; got.Type != "string" || got.ContentEncoding != "base64" {
		t.Errorf("bytes = %+v, want base64 string", got)
	}

	// DescriptorProto is recursive through nested_type.
	desc := newInputSchema(findMessage(t, "google.protobuf.DescriptorProto"))
	nested := desc.Properties["nestedType"]
	if nested.Type != "array" || nested.Items.Type != "object" || len(nested.Items.Properties) != 0 {
		t.Errorf("recursive nestedType = %+v, want array of property-less objects", nested)
	}
}

// fill sets every field of a message to a non-zero value, recursing into
// messages up to a fixed depth. If nonFinite is set, floating point fields
// are set to non-finite values.
func fill(t *testing.T, m protoreflect.Message, depth int, nonFinite bool) {
	t.Helper()
	if depth > 5 {
		return
	}
	switch m.Descriptor().FullName() {
	case "google.protobuf.Any", "google.protobuf.Struct", "google.protobuf.Value",
		"google.protobuf.ListValue":
		return
	case "google.protobuf.FieldMask":
		paths := m.Descriptor().Fields().ByName("paths")
		list := m.Mutable(paths).List()
		list.Append(protoreflect.ValueOfString("foo"))
		list.Append(protoreflect.ValueOfString("bar_baz"))
		return
	case "google.protobuf.Timestamp", "google.protobuf.Duration":
		m.Set(m.Descriptor().Fields().ByName("seconds"), protoreflect.ValueOfInt64(1700000000))
		m.Set(m.Descriptor().Fields().ByName("nanos"), protoreflect.ValueOfInt32(500))
		return
	}
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		switch {
		case fd.IsMap():
			mp := m.Mutable(fd).Map()
			key := scalarValue(fd.MapKey(), nonFinite).MapKey()
			if isMessage(fd.MapValue()) {
				v := mp.NewValue()
				fill(t, v.Message(), depth+1, nonFinite)
				mp.Set(key, v)
			} else {
				mp.Set(key, scalarValue(fd.MapValue(), nonFinite))
			}
		case fd.IsList():
			list := m.Mutable(fd).List()
			for range 2 {
				if isMessage(fd) {
					v := list.NewElement()
					fill(t, v.Message(), depth+1, nonFinite)
					list.Append(v)
				} else {
					list.Append(scalarValue(fd, nonFinite))
				}
			}
		case isMessage(fd):
			fill(t, m.Mutable(fd).Message(), depth+1, nonFinite)
		default:
			m.Set(fd, scalarValue(fd, nonFinite))
		}
	}
}

func scalarValue(fd protoreflect.FieldDescriptor, nonFinite bool) protoreflect.Value {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(true)
	case protoreflect.StringKind:
		return protoreflect.ValueOfString("foo")
	case protoreflect.BytesKind:
		return protoreflect.ValueOfBytes([]byte("\xff\x00bytes"))
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32(-42)
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(42)
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return protoreflect.ValueOfInt64(-1 << 60)
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return protoreflect.ValueOfUint64(1 << 63)
	case protoreflect.FloatKind:
		if nonFinite {
			return protoreflect.ValueOfFloat32(float32(math.Inf(-1)))
		}
		return protoreflect.ValueOfFloat32(1.5)
	case protoreflect.DoubleKind:
		if nonFinite {
			return protoreflect.ValueOfFloat64(math.NaN())
		}
		return protoreflect.ValueOfFloat64(-2.25)
	case protoreflect.EnumKind:
		// The last value that isn't deprecated, as input schemas leave
		// deprecated values out.
		values := fd.Enum().Values()
		for i := values.Len() - 1; i > 0; i-- {
			if opts, ok := values.Get(i).Options().(*descriptorpb.EnumValueOptions); !ok || !opts.GetDeprecated() {
				return protoreflect.ValueOfEnum(values.Get(i).Number())
			}
		}
		return protoreflect.ValueOfEnum(values.Get(values.Len() - 1).Number())
	default:
		panic("unexpected kind " + fd.Kind().String())
	}
}

// validate checks that the protojson encoding of m validates against s.
func validate(t *testing.T, s *jsonschema.Schema, m protoreflect.Message) {
	t.Helper()
	resolved, err := s.Resolve(nil)
	if err != nil {
		t.Fatalf("resolving schema: %v", err)
	}
	data, err := protojson.Marshal(m.Interface())
	if err != nil {
		t.Fatalf("marshaling: %v", err)
	}
	var instance any
	if err := json.Unmarshal(data, &instance); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(instance); err != nil {
		t.Errorf("%s does not validate against its schema: %v\n%s", m.Descriptor().FullName(), err, data)
	}
}

// TestSchemasValidate checks that the protojson encoding of fully populated
// request and response messages of every method validates against the
// generated schemas.
func TestSchemasValidate(t *testing.T) {
	files, err := apidesc.Files()
	if err != nil {
		t.Fatal(err)
	}
	var services []protoreflect.ServiceDescriptor
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		for i := range fd.Services().Len() {
			services = append(services, fd.Services().Get(i))
		}
		return true
	})
	if len(services) == 0 {
		t.Fatal("no services found")
	}
	for _, sd := range services {
		for i := range sd.Methods().Len() {
			md := sd.Methods().Get(i)
			t.Run(string(md.FullName()), func(t *testing.T) {
				for _, nonFinite := range []bool{false, true} {
					inType, err := protoregistry.GlobalTypes.FindMessageByName(md.Input().FullName())
					if err != nil {
						t.Fatal(err)
					}
					in := inType.New()
					fill(t, in, 0, nonFinite)
					clearOutputOnly(in)
					if !nonFinite {
						// Non-finite numbers aren't part of the input schema.
						validate(t, newInputSchema(md.Input()), in)
					}

					outType, err := protoregistry.GlobalTypes.FindMessageByName(md.Output().FullName())
					if err != nil {
						t.Fatal(err)
					}
					out := outType.New()
					fill(t, out, 0, nonFinite)
					validate(t, newOutputSchema(md.Output()), out)
				}
			})
		}
	}
}
