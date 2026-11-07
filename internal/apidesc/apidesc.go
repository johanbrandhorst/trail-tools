// Package apidesc provides access to the API's protobuf descriptors, including
// the information that isn't part of the generated Go code, such as comments.
//
// The descriptor set is generated from the api directory with
//
//	go tool buf build api --as-file-descriptor-set -o internal/apidesc/descriptor.binpb
//
// which is run as part of `make buf`.
package apidesc

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	// Register the custom option extensions so that they are resolved when
	// the descriptor set is unmarshaled, rather than left as unknown fields.
	_ "github.com/johanbrandhorst/trail-tools/internal/gen/mcp/v1"
)

// descriptorSet is a google.protobuf.FileDescriptorSet of all the API files and
// their imports, including source info.
//
//go:embed descriptor.binpb
var descriptorSet []byte

// files parses the embedded descriptor set on first use.
var files = sync.OnceValues(func() (*protoregistry.Files, error) {
	set := &descriptorpb.FileDescriptorSet{}
	// Resolve extensions in options (such as mcp.v1.method) against the
	// globally registered extension types, so they can be read with
	// proto.GetExtension.
	opts := proto.UnmarshalOptions{Resolver: protoregistry.GlobalTypes}
	if err := opts.Unmarshal(descriptorSet, set); err != nil {
		return nil, fmt.Errorf("unmarshaling descriptor set: %w", err)
	}
	fs, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, fmt.Errorf("creating files from descriptor set: %w", err)
	}
	return fs, nil
})

// Files returns the registry of all API files and their imports. The
// descriptors include source info (comments) and resolved custom options.
func Files() (*protoregistry.Files, error) {
	return files()
}

// Service returns the descriptor of the named service.
func Service(name protoreflect.FullName) (protoreflect.ServiceDescriptor, error) {
	fs, err := files()
	if err != nil {
		return nil, err
	}
	desc, err := fs.FindDescriptorByName(name)
	if err != nil {
		return nil, fmt.Errorf("finding service %q: %w", name, err)
	}
	sd, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%q is a %T, not a service", name, desc)
	}
	return sd, nil
}

// Comment returns the cleaned leading comment of a descriptor, or an empty
// string if it has none. The descriptor may come from any registry, such as
// protoregistry.GlobalFiles, which has no source info: it is looked up by
// name in the embedded descriptor set.
func Comment(desc protoreflect.Descriptor) string {
	if fs, err := files(); err == nil {
		if d, err := fs.FindDescriptorByName(desc.FullName()); err == nil {
			desc = d
		}
	}
	file := desc.ParentFile()
	if file == nil {
		return ""
	}
	loc := file.SourceLocations().ByDescriptor(desc)
	return cleanComment(loc.LeadingComments)
}

// listItemRe matches the start of a markdown style list item.
var listItemRe = regexp.MustCompile(`^(?:[-*+]|\d+[.)])\s`)

// cleanComment normalizes a proto comment. Paragraphs (separated by blank
// lines) and list items are kept on separate lines, while the remaining line
// breaks are collapsed into spaces.
func cleanComment(comment string) string {
	var paragraphs []string
	for p := range strings.SplitSeq(comment, "\n\n") {
		var lines []string
		for line := range strings.SplitSeq(p, "\n") {
			line = strings.Join(strings.Fields(line), " ")
			if line == "" {
				continue
			}
			if len(lines) == 0 || listItemRe.MatchString(line) {
				lines = append(lines, line)
				continue
			}
			lines[len(lines)-1] += " " + line
		}
		if len(lines) > 0 {
			paragraphs = append(paragraphs, strings.Join(lines, "\n"))
		}
	}
	return strings.Join(paragraphs, "\n\n")
}
