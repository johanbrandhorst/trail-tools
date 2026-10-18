// Package mcp exposes the API's Connect services as a Model Context Protocol
// (MCP) server. Every RPC becomes a tool, with the tool's name, description
// and JSON schemas derived from the protobuf descriptors at startup. Methods
// can be excluded with the (mcp.v1.method).exclude option, and fields that are
// set by the server can be hidden from tool inputs with the
// (mcp.v1.field).output_only option.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"unicode"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/apidesc"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// implementationName is the name the server reports to MCP clients.
const implementationName = "trail-tools"

// instructionsPreamble is the start of the server instructions, followed by
// the description of each service.
const instructionsPreamble = `Tools for planning trail running: routes, training plans, races and race plans, recorded activities and trip reports. Each tool is an RPC of one of the services described below.`

var (
	// readOnlyPrefixes are the method name prefixes of methods that don't
	// modify any state.
	readOnlyPrefixes = []string{"Get", "List", "Search", "Find", "Summarize", "Preview"}

	contextType = reflect.TypeFor[context.Context]()
	errorType   = reflect.TypeFor[error]()
)

// Service is a Connect service to expose as MCP tools.
type Service struct {
	// Name is the fully qualified name of the service, e.g.
	// routesv1connect.RouteServiceName.
	Name protoreflect.FullName
	// Handler is the implementation of the service, e.g. the value passed to
	// routesv1connect.RegisterRouteServiceHandler. It must have a method for
	// every RPC that isn't excluded, with the signature
	//
	//	func(context.Context, *Req) (*Res, error)
	Handler any
}

// Server is an MCP server exposing Connect services as tools.
type Server struct {
	server  *mcp.Server
	handler http.Handler
	tools   []*mcp.Tool
}

// NewServer creates an MCP server with a tool for every RPC of the services,
// except those that are excluded with the (mcp.v1.method).exclude option.
// It returns an error if a service or method can't be found, a method has an
// unexpected signature, or two services have a method with the same name.
func NewServer(log *slog.Logger, report *errreport.Reporter, services ...Service) (*Server, error) {
	type tool struct {
		tool    *mcp.Tool
		handler mcp.ToolHandler
	}
	var (
		tools        []tool
		toolServices = map[string]protoreflect.FullName{}
		instructions = []string{instructionsPreamble}
	)
	for _, svc := range services {
		sd, err := apidesc.Service(svc.Name)
		if err != nil {
			return nil, fmt.Errorf("getting service descriptor: %w", err)
		}
		if svc.Handler == nil {
			return nil, fmt.Errorf("service %s has no handler", svc.Name)
		}
		exposed := 0
		methods := sd.Methods()
		for i := range methods.Len() {
			md := methods.Get(i)
			if isExcluded(md) {
				log.Debug("Excluding method from MCP tools", slog.String("method", string(md.FullName())))
				continue
			}
			name := string(md.Name())
			if other, ok := toolServices[name]; ok {
				return nil, fmt.Errorf("duplicate tool name %q from %s and %s", name, other, svc.Name)
			}
			toolServices[name] = svc.Name
			handler, err := newToolHandler(log, report, svc.Handler, md)
			if err != nil {
				return nil, fmt.Errorf("creating tool handler for %s: %w", md.FullName(), err)
			}
			tools = append(tools, tool{tool: newTool(md), handler: handler})
			exposed++
		}
		if comment := apidesc.Comment(sd); exposed > 0 && comment != "" {
			instructions = append(instructions, fmt.Sprintf("## %s\n\n%s", sd.FullName(), comment))
		}
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    implementationName,
		Title:   "trailrunn.ing",
		Version: "1.0.0",
	}, &mcp.ServerOptions{
		Instructions: strings.Join(instructions, "\n\n"),
		Logger:       log,
	})
	s := &Server{server: server}
	for _, t := range tools {
		server.AddTool(t.tool, t.handler)
		s.tools = append(s.tools, t.tool)
	}
	s.handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		// The tools are stateless, so there is no need for sessions or
		// streaming responses. Each tool call is a single POST request.
		Stateless:                    true,
		JSONResponse:                 true,
		PropagateRequestCancellation: true,
		Logger:                       log,
	})
	return s, nil
}

// Handler returns the streamable HTTP handler of the MCP server. The context
// of each HTTP request, including any values added by middleware, is passed
// on to the service implementations.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Tools returns the tools exposed by the server, in service and method order.
func (s *Server) Tools() []*mcp.Tool {
	return s.tools
}

// newTool returns the tool definition for a method.
func newTool(md protoreflect.MethodDescriptor) *mcp.Tool {
	name := string(md.Name())
	readOnly := hasPrefix(name, readOnlyPrefixes...)
	return &mcp.Tool{
		Name:         name,
		Title:        title(name),
		Description:  apidesc.Comment(md),
		InputSchema:  newInputSchema(md.Input()),
		OutputSchema: newOutputSchema(md.Output()),
		Annotations: &mcp.ToolAnnotations{
			Title:           title(name),
			ReadOnlyHint:    readOnly,
			DestructiveHint: new(hasPrefix(name, "Delete")),
			// Most methods are declarative (get, update, delete, set), and
			// repeating them has no further effect, but creating twice
			// creates two resources.
			IdempotentHint: !hasPrefix(name, "Create"),
			// The tools only operate on the user's own data.
			OpenWorldHint: new(false),
		},
	}
}

// newToolHandler returns a tool handler calling the method on the Connect
// service implementation impl.
func newToolHandler(log *slog.Logger, report *errreport.Reporter, impl any, md protoreflect.MethodDescriptor) (mcp.ToolHandler, error) {
	inType, err := protoregistry.GlobalTypes.FindMessageByName(md.Input().FullName())
	if err != nil {
		return nil, fmt.Errorf("finding input type %s: %w", md.Input().FullName(), err)
	}
	outType, err := protoregistry.GlobalTypes.FindMessageByName(md.Output().FullName())
	if err != nil {
		return nil, fmt.Errorf("finding output type %s: %w", md.Output().FullName(), err)
	}

	// The method name comes from the descriptor, not from user input.
	fn := reflect.ValueOf(impl).MethodByName(string(md.Name()))
	if !fn.IsValid() {
		return nil, fmt.Errorf("%T has no method %s", impl, md.Name())
	}
	if err := validateSignature(fn.Type(), inType, outType); err != nil {
		return nil, fmt.Errorf("method %s: %w", md.Name(), err)
	}

	method := string(md.FullName())
	return func(ctx context.Context, req *mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
		defer func() {
			if r := recover(); r != nil {
				// The report's stack trace is where the panic happened, as
				// deferred functions run on top of it.
				report.Report(ctx, "Panic in MCP tool", fmt.Errorf("panic: %v", r), slog.String("method", method))
				result, err = errorResult(connect.NewError(connect.CodeInternal, "internal error")), nil
			}
		}()

		in := inType.New().Interface()
		if args := req.Params.Arguments; len(args) > 0 && !bytes.Equal(bytes.TrimSpace(args), []byte("null")) {
			if err := protojson.Unmarshal(args, in); err != nil {
				return errorResult(connect.Errorf(connect.CodeInvalidArgument,
					"invalid arguments, check them against the tool input schema: %v", err)), nil
			}
		}
		// Output only fields are not part of the input schema and are
		// ignored by the server, so make sure they're not set.
		clearOutputOnly(in.ProtoReflect())

		out := fn.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(in)})
		if errVal := out[1]; !errVal.IsNil() {
			err := errVal.Interface().(error)
			log.DebugContext(ctx, "MCP tool call failed", slog.String("method", method), slog.Any("error", err))
			// Services report internal errors where they happen, but not
			// errors they didn't expect to return.
			if connect.CodeOf(err) == connect.CodeUnknown {
				report.Report(ctx, "MCP tool call failed", err, slog.String("method", method))
			}
			return errorResult(err), nil
		}
		if out[0].IsNil() {
			return nil, fmt.Errorf("%s returned a nil response", method)
		}
		msg := out[0].Interface().(proto.Message)
		data, err := outputOptions.Marshal(msg)
		if err != nil {
			return nil, fmt.Errorf("marshaling %s response: %w", method, err)
		}
		return &mcp.CallToolResult{
			StructuredContent: json.RawMessage(data),
			// "For backwards compatibility, a tool that returns structured
			// content SHOULD also return the serialized JSON in a
			// TextContent block."
			Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
		}, nil
	}, nil
}

// validateSignature checks that a method has the signature of a unary Connect
// handler method for the input and output types:
//
//	func(context.Context, *In) (*Out, error)
func validateSignature(fnType reflect.Type, inType, outType protoreflect.MessageType) error {
	if fnType.NumIn() != 2 || fnType.NumOut() != 2 ||
		fnType.In(0) != contextType || fnType.Out(1) != errorType {
		return fmt.Errorf("unexpected signature %s, want func(context.Context, *T) (*U, error)", fnType)
	}
	if want := reflect.TypeOf(inType.Zero().Interface()); fnType.In(1) != want {
		return fmt.Errorf("request type is %s, want %s", fnType.In(1), want)
	}
	if want := reflect.TypeOf(outType.Zero().Interface()); fnType.Out(0) != want {
		return fmt.Errorf("response type is %s, want %s", fnType.Out(0), want)
	}
	return nil
}

// outputOptions encode tool results. They include fields with their default
// values, like false and 0, which protojson leaves out by default, so agents
// can tell a draft report from a published one rather than seeing no status.
// That makes results about 3% larger.
var outputOptions = protojson.MarshalOptions{EmitDefaultValues: true}

// clearOutputOnly recursively clears all output only fields of the message.
func clearOutputOnly(m protoreflect.Message) {
	var toClear []protoreflect.FieldDescriptor
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case isOutputOnly(fd):
			toClear = append(toClear, fd)
		case fd.IsMap():
			if isMessage(fd.MapValue()) {
				v.Map().Range(func(_ protoreflect.MapKey, v protoreflect.Value) bool {
					clearOutputOnly(v.Message())
					return true
				})
			}
		case fd.IsList():
			if isMessage(fd) {
				list := v.List()
				for i := range list.Len() {
					clearOutputOnly(list.Get(i).Message())
				}
			}
		case isMessage(fd):
			clearOutputOnly(v.Message())
		}
		return true
	})
	for _, fd := range toClear {
		m.Clear(fd)
	}
}

func isMessage(fd protoreflect.FieldDescriptor) bool {
	return fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind
}

// errorResult returns a tool result for an error from a Connect handler. It's
// a tool error rather than a protocol error, so that the model can see the
// message and correct itself.
func errorResult(err error) *mcp.CallToolResult {
	msg := err.Error()
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		msg = connectErr.Message()
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("%s: %s", connect.CodeOf(err), msg),
		}},
	}
}

func hasPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// title splits a method name into words, e.g. "CreateRoute" becomes
// "Create Route" and "CreateAPIToken" becomes "Create API Token".
func title(name string) string {
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prevLower := unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if prevLower || (unicode.IsUpper(runes[i-1]) && nextLower) {
				b.WriteByte(' ')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}
