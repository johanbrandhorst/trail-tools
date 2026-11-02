package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// checkRemovedFields returns an error naming the first field of the
// arguments that was removed from the API, its name reserved in the
// message. protojson skips reserved fields, so arguments written for an
// older version of the API would otherwise lose them without a word, and an
// agent would believe it had set what it meant to.
func checkRemovedFields(args json.RawMessage, md protoreflect.MessageDescriptor) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(args, &object); err != nil {
		// Not an object; protojson reports what's wrong with it.
		return nil
	}
	fields := md.Fields()
	for key, value := range object {
		fd := fields.ByJSONName(key)
		if fd == nil {
			fd = fields.ByName(protoreflect.Name(key))
		}
		if fd == nil {
			if name := snakeCase(key); md.ReservedNames().Has(protoreflect.Name(name)) {
				return fmt.Errorf("%s no longer has the field %s, which was removed from the API; check the tool input schema for what replaced it", md.Name(), key)
			}
			continue
		}
		if fd.Message() == nil || fd.IsMap() || fd.Message().FullName().Parent() == "google.protobuf" {
			continue
		}
		if fd.IsList() {
			var items []json.RawMessage
			if json.Unmarshal(value, &items) != nil {
				continue
			}
			for _, item := range items {
				if err := checkRemovedFields(item, fd.Message()); err != nil {
					return err
				}
			}
			continue
		}
		if err := checkRemovedFields(value, fd.Message()); err != nil {
			return err
		}
	}
	return nil
}

// snakeCase converts a camel case JSON name to the proto field name it's
// usually the JSON name of.
func snakeCase(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
