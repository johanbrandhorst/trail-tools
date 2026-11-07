package mcp

import (
	"fmt"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/johanbrandhorst/trail-tools/internal/apidesc"
	mcpv1 "github.com/johanbrandhorst/trail-tools/internal/gen/mcp/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	typeString  = "string"
	typeNumber  = "number"
	typeInteger = "integer"
	typeBoolean = "boolean"
	typeObject  = "object"
	typeArray   = "array"
	typeNull    = "null"
)

// isOutputOnly reports whether the field is annotated with
// (mcp.v1.field).output_only. Such fields are set by the server and are
// hidden from tool input schemas.
func isOutputOnly(fd protoreflect.FieldDescriptor) bool {
	ext, ok := proto.GetExtension(fd.Options(), mcpv1.E_Field).(*mcpv1.FieldOptions)
	return ok && ext.GetOutputOnly()
}

// isExcluded reports whether the method is annotated with
// (mcp.v1.method).exclude, and should not be exposed as a tool.
func isExcluded(md protoreflect.MethodDescriptor) bool {
	ext, ok := proto.GetExtension(md.Options(), mcpv1.E_Method).(*mcpv1.MethodOptions)
	return ok && ext.GetExclude()
}

// schemaBuilder generates JSON schemas for protobuf messages that describe
// their protojson encoding.
type schemaBuilder struct {
	// input is true when generating the schema for a tool's input, in which
	// case output only fields are hidden. Otherwise, the schema describes the
	// output of protojson.Marshal.
	input bool
	// visiting holds the messages currently being generated, to guard
	// against infinite recursion in recursive messages.
	visiting map[protoreflect.FullName]bool
}

// newInputSchema returns the schema of a tool input message.
func newInputSchema(md protoreflect.MessageDescriptor) *jsonschema.Schema {
	b := &schemaBuilder{input: true, visiting: map[protoreflect.FullName]bool{}}
	return b.message(md)
}

// newOutputSchema returns the schema of a tool output message, as encoded by
// protojson.Marshal.
func newOutputSchema(md protoreflect.MessageDescriptor) *jsonschema.Schema {
	b := &schemaBuilder{input: false, visiting: map[protoreflect.FullName]bool{}}
	s := b.message(md)
	// Top-level images are returned as image content, not in the JSON.
	fields := md.Fields()
	for i := range fields.Len() {
		if fd := fields.Get(i); isImage(fd) {
			delete(s.Properties, fd.JSONName())
			s.PropertyOrder = slices.DeleteFunc(s.PropertyOrder, func(name string) bool { return name == fd.JSONName() })
		}
	}
	return s
}

// isImage reports whether a field is a singular mcp.v1.Image, which tool
// results return as image content when it's a field of the response.
func isImage(fd protoreflect.FieldDescriptor) bool {
	return fd.Kind() == protoreflect.MessageKind && !fd.IsList() && !fd.IsMap() &&
		fd.Message().FullName() == (&mcpv1.Image{}).ProtoReflect().Descriptor().FullName()
}

// message returns an object schema with a property for each field of the
// message, keyed by the field's JSON name.
func (b *schemaBuilder) message(md protoreflect.MessageDescriptor) *jsonschema.Schema {
	if b.visiting[md.FullName()] {
		return &jsonschema.Schema{
			Type:        typeObject,
			Description: fmt.Sprintf("A recursive %s, with the same properties as its parent.", md.Name()),
		}
	}
	b.visiting[md.FullName()] = true
	defer delete(b.visiting, md.FullName())

	s := &jsonschema.Schema{
		Type:        typeObject,
		Description: apidesc.Comment(md),
		Properties:  map[string]*jsonschema.Schema{},
	}
	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if b.input && isOutputOnly(fd) {
			continue
		}
		name := fd.JSONName()
		s.Properties[name] = b.field(fd)
		s.PropertyOrder = append(s.PropertyOrder, name)
	}
	return s
}

// field returns the schema of a field, including its cardinality.
func (b *schemaBuilder) field(fd protoreflect.FieldDescriptor) *jsonschema.Schema {
	var (
		s     *jsonschema.Schema
		notes []string
	)
	switch {
	case fd.IsMap():
		value, note := b.singular(fd.MapValue())
		s = &jsonschema.Schema{
			Type:                 typeObject,
			AdditionalProperties: value,
		}
		notes = append(notes, note, mapKeyNote(fd.MapKey()))
	case fd.IsList():
		item, note := b.singular(fd)
		s = &jsonschema.Schema{
			Type:  typeArray,
			Items: item,
		}
		notes = append(notes, note)
	default:
		var note string
		s, note = b.singular(fd)
		notes = append(notes, note)
	}

	description := apidesc.Comment(fd)
	if description == "" && !fd.IsList() && !fd.IsMap() {
		// Fall back to the description of the field's message, if any.
		description = s.Description
	}
	if oneof := fd.ContainingOneof(); oneof != nil && !oneof.IsSynthetic() {
		notes = append(notes, b.oneofNote(fd, oneof))
	}
	for _, note := range notes {
		if note == "" {
			continue
		}
		if description != "" {
			description += "\n\n"
		}
		description += note
	}
	s.Description = description
	return s
}

// oneofNote describes the members of a oneof that a field belongs to.
func (b *schemaBuilder) oneofNote(fd protoreflect.FieldDescriptor, oneof protoreflect.OneofDescriptor) string {
	var others []string
	fields := oneof.Fields()
	for i := range fields.Len() {
		f := fields.Get(i)
		if f == fd || (b.input && isOutputOnly(f)) {
			continue
		}
		others = append(others, fmt.Sprintf("%q", f.JSONName()))
	}
	if len(others) == 0 {
		return ""
	}
	return fmt.Sprintf("Mutually exclusive with %s (oneof %q): set at most one of them.",
		strings.Join(others, ", "), oneof.Name())
}

// mapKeyNote describes the keys of a map field, which are always strings in
// JSON.
func mapKeyNote(fd protoreflect.FieldDescriptor) string {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return ""
	case protoreflect.BoolKind:
		return `Keys are "true" or "false".`
	default:
		return "Keys are integers encoded as strings."
	}
}

// singular returns the schema of a single value of the field's type, ignoring
// its cardinality, along with an optional note to add to the field
// description.
func (b *schemaBuilder) singular(fd protoreflect.FieldDescriptor) (*jsonschema.Schema, string) {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return &jsonschema.Schema{Type: typeBoolean}, ""
	case protoreflect.StringKind:
		return &jsonschema.Schema{Type: typeString}, ""
	case protoreflect.BytesKind:
		return &jsonschema.Schema{
			Type:            typeString,
			ContentEncoding: "base64",
		}, "Base64 encoded bytes."
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return &jsonschema.Schema{Type: typeInteger}, ""
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		// protojson encodes 64 bit integers as strings, and accepts both.
		return &jsonschema.Schema{
			Types:   []string{typeInteger, typeString},
			Pattern: `^-?[0-9]+$`,
		}, ""
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return &jsonschema.Schema{
			Types:   []string{typeInteger, typeString},
			Pattern: `^[0-9]+$`,
		}, ""
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return b.float(), ""
	case protoreflect.EnumKind:
		return b.enum(fd.Enum())
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return b.messageValue(fd.Message())
	default:
		// Not reachable for valid descriptors. Accept anything.
		return &jsonschema.Schema{}, ""
	}
}

// float returns the schema of a float or double.
func (b *schemaBuilder) float() *jsonschema.Schema {
	if b.input {
		return &jsonschema.Schema{Type: typeNumber}
	}
	// protojson encodes non-finite values as strings.
	return &jsonschema.Schema{
		AnyOf: []*jsonschema.Schema{
			{Type: typeNumber},
			{Type: typeString, Enum: []any{"NaN", "Infinity", "-Infinity"}},
		},
	}
}

// enum returns the schema of an enum, which protojson encodes as the value
// name.
func (b *schemaBuilder) enum(ed protoreflect.EnumDescriptor) (*jsonschema.Schema, string) {
	if ed.FullName() == "google.protobuf.NullValue" {
		return &jsonschema.Schema{Type: typeNull}, ""
	}
	s := &jsonschema.Schema{Type: typeString}
	var lines []string
	values := ed.Values()
	for i := range values.Len() {
		v := values.Get(i)
		// The zero value means unset, so it's not useful as input. It may
		// still appear in output, e.g. in a list of enums.
		if b.input && v.Number() == 0 {
			continue
		}
		// Deprecated values are still accepted, but not offered as input.
		if opts, ok := v.Options().(*descriptorpb.EnumValueOptions); b.input && ok && opts.GetDeprecated() {
			continue
		}
		s.Enum = append(s.Enum, string(v.Name()))
		if comment := apidesc.Comment(v); comment != "" && v.Number() != 0 {
			lines = append(lines, fmt.Sprintf("- %s: %s", v.Name(), comment))
		}
	}
	return s, strings.Join(lines, "\n")
}

// messageValue returns the schema of a message typed value, handling the
// well-known types that have a special protojson encoding.
func (b *schemaBuilder) messageValue(md protoreflect.MessageDescriptor) (*jsonschema.Schema, string) {
	switch md.FullName() {
	case "google.protobuf.Timestamp":
		return &jsonschema.Schema{
			Type:   typeString,
			Format: "date-time",
		}, `An RFC 3339 timestamp, e.g. "2026-05-01T07:30:00Z".`
	case "google.protobuf.Duration":
		return &jsonschema.Schema{
			Type:    typeString,
			Pattern: `^-?[0-9]+(\.[0-9]+)?s$`,
		}, `A duration in seconds with an "s" suffix, e.g. "3600s" or "1.5s".`
	case "google.protobuf.FieldMask":
		return &jsonschema.Schema{
			Type: typeString,
		}, `A comma-separated list of lowerCamelCase field paths, e.g. "name,description".`
	case "google.protobuf.Struct":
		return &jsonschema.Schema{Type: typeObject}, "An arbitrary JSON object."
	case "google.protobuf.Value":
		return &jsonschema.Schema{}, "An arbitrary JSON value."
	case "google.protobuf.ListValue":
		return &jsonschema.Schema{Type: typeArray, Items: &jsonschema.Schema{}}, "An arbitrary JSON array."
	case "google.protobuf.Empty":
		return &jsonschema.Schema{Type: typeObject}, ""
	case "google.protobuf.Any":
		return &jsonschema.Schema{
			Type: typeObject,
			Properties: map[string]*jsonschema.Schema{
				"@type": {Type: typeString},
			},
		}, `A message of any type, identified by its "@type" URL.`
	case "google.protobuf.DoubleValue", "google.protobuf.FloatValue",
		"google.protobuf.Int64Value", "google.protobuf.UInt64Value",
		"google.protobuf.Int32Value", "google.protobuf.UInt32Value",
		"google.protobuf.BoolValue", "google.protobuf.StringValue",
		"google.protobuf.BytesValue":
		// Wrappers are encoded as their wrapped value.
		return b.singular(md.Fields().ByNumber(1))
	}
	return b.message(md), ""
}
