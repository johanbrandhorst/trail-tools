// Package protomask applies update masks to protobuf messages.
package protomask

import (
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// Apply copies the fields named by the mask from src to dst. A path names a
// top level field, or a field of a message field with dots, like
// "run.lactate_test", which changes only that field and leaves the rest of
// the message as it is in dst. Each part of a path may be a field name
// (snake_case) or a JSON name (lowerCamelCase). If the mask is empty, all top
// level fields are copied. Fields named in keep are never copied, nor are
// the fields within them.
func Apply(dst, src proto.Message, mask *fieldmaskpb.FieldMask, keep ...protoreflect.Name) error {
	dstRefl, srcRefl := dst.ProtoReflect(), src.ProtoReflect()
	if len(mask.GetPaths()) == 0 {
		fields, err := Fields(dstRefl.Descriptor(), mask, keep...)
		if err != nil {
			return err
		}
		for _, fd := range fields {
			copyField(dstRefl, srcRefl, fd)
		}
		return nil
	}
	for _, path := range mask.GetPaths() {
		if err := applyPath(dstRefl, srcRefl, path, keep); err != nil {
			return err
		}
	}
	return nil
}

// applyPath copies the field a dotted path names from src to dst, creating
// the messages on the way to it in dst. A path into a field of a oneof that
// dst doesn't have set is refused, rather than switching dst's oneof to it
// and dropping the field that was set.
func applyPath(dst, src protoreflect.Message, path string, keep []protoreflect.Name) error {
	parts := strings.Split(path, ".")
	for i, part := range parts {
		fd := fieldByName(dst.Descriptor().Fields(), part)
		if fd == nil || (i == 0 && slices.Contains(keep, fd.Name())) {
			return fmt.Errorf("invalid update mask path %q", path)
		}
		if i == len(parts)-1 {
			copyField(dst, src, fd)
			return nil
		}
		if fd.Message() == nil || fd.IsList() || fd.IsMap() {
			return fmt.Errorf("invalid update mask path %q: %s isn't a message, so it has no fields to update", path, part)
		}
		if oneof := fd.ContainingOneof(); oneof != nil && !oneof.IsSynthetic() && !dst.Has(fd) {
			return fmt.Errorf("invalid update mask path %q: %s isn't set, so update it as a whole with the path %q", path, part, strings.Join(parts[:i+1], "."))
		}
		next := dst.Mutable(fd).Message()
		// An unset message in src is empty, so its fields clear dst's.
		src = src.Get(fd).Message()
		dst = next
	}
	return nil
}

// fieldByName returns the field with the name or JSON name, nil if there's
// none.
func fieldByName(fields protoreflect.FieldDescriptors, name string) protoreflect.FieldDescriptor {
	if fd := fields.ByName(protoreflect.Name(name)); fd != nil {
		return fd
	}
	return fields.ByJSONName(name)
}

// copyField sets dst's field to src's, or clears it if src's isn't set.
func copyField(dst, src protoreflect.Message, fd protoreflect.FieldDescriptor) {
	if src.Has(fd) {
		dst.Set(fd, src.Get(fd))
	} else {
		dst.Clear(fd)
	}
}

// Fields returns the top level fields of a message named by the mask, in
// mask order without duplicates. Paths may be field names (snake_case) or
// JSON names (lowerCamelCase). If the mask is empty, it returns all fields
// except those named in keep. Naming a field in keep is an error.
func Fields(md protoreflect.MessageDescriptor, mask *fieldmaskpb.FieldMask, keep ...protoreflect.Name) ([]protoreflect.FieldDescriptor, error) {
	fields := md.Fields()
	var result []protoreflect.FieldDescriptor
	if len(mask.GetPaths()) == 0 {
		for i := range fields.Len() {
			if fd := fields.Get(i); !slices.Contains(keep, fd.Name()) {
				result = append(result, fd)
			}
		}
		return result, nil
	}
	for _, path := range mask.GetPaths() {
		fd := fieldByName(fields, path)
		if fd == nil || slices.Contains(keep, fd.Name()) {
			return nil, fmt.Errorf("invalid update mask path %q", path)
		}
		if !slices.Contains(result, fd) {
			result = append(result, fd)
		}
	}
	return result, nil
}
