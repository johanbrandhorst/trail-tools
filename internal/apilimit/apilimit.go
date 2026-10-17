// Package apilimit serves the Connect API with bounded requests, so that a
// single request can't exhaust the memory of the server, which runs in
// 512 MiB.
package apilimit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1/routesv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1/tripsv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/gen/users/v1/usersv1connect"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	// MaxRequestBytes limits the size of requests to most procedures, and of
	// all requests from callers that aren't signed in. Requests are decoded
	// before handlers check authentication, and decoded messages can take
	// far more memory than their encoding (see MaxRequestValues).
	MaxRequestBytes = 1 << 20
	// MaxUploadBytes limits the size of requests to the procedures that carry
	// files, from signed in callers. Trip report photos can be 20 MB, a third
	// more in JSON, which encodes bytes in base64, and GPX files of long
	// activities recorded every second can be tens of megabytes.
	MaxUploadBytes = 32 << 20
	// MaxRequestValues limits the messages, strings and bytes fields in a
	// request. Each is a few bytes encoded, but tens of bytes decoded: a
	// megabyte of empty messages in a repeated field decodes to about 40 MB,
	// and an upload's worth to more than a gigabyte. 100,000 is about 7 MB
	// decoded, and far more than any request needs.
	MaxRequestValues = 100_000
	// maxConcurrentUploads limits the requests larger than MaxRequestBytes
	// that are read and handled at once, as each can take several times its
	// size in memory. Others wait for their turn.
	maxConcurrentUploads = 2
	// maxNestingDepth limits the depth of nested messages that are counted.
	// No request of the API nests nearly this deep.
	maxNestingDepth = 64
)

// uploadProcedures are the procedures that carry files, which accept
// requests up to MaxUploadBytes from signed in callers.
var uploadProcedures = map[string]bool{
	routesv1connect.RouteServiceCreateRouteProcedure:         true,
	routesv1connect.RouteServiceCheckTrackCoverageProcedure:  true,
	routesv1connect.RouteServiceGetRouteForecastProcedure:    true,
	tripsv1connect.TripReportServiceUploadTripPhotoProcedure: true,
	usersv1connect.UserServiceUploadAvatarProcedure:          true,
}

// errTooManyValues is returned for requests with more than MaxRequestValues
// values.
var errTooManyValues = fmt.Errorf("the request has more than %d values; send less data at once", MaxRequestValues)

// Mount registers the server's services on mux, served with bounded
// requests. The handlers must be wrapped in the authn middleware, which
// identifies the callers allowed to upload files.
func Mount(mux *http.ServeMux, server *connect.Server) {
	handler := NewHandler(server)
	paths := map[string]bool{}
	for spec := range server.Specs() {
		paths[spec.Procedure[:strings.LastIndexByte(spec.Procedure, '/')+1]] = true
	}
	for path := range paths {
		mux.Handle(path, handler)
	}
}

// NewHandler returns a handler of the server's procedures with bounded
// requests: at most MaxRequestBytes, or MaxUploadBytes for the procedures
// that carry files when the caller is signed in, and at most
// MaxRequestValues values. Requests larger than MaxRequestBytes are handled
// at most maxConcurrentUploads at a time.
func NewHandler(server *connect.Server) http.Handler {
	codecs := connecthttp.WithCodecs(
		boundedCodec{Codec: connectproto.NewBinaryCodec(), count: countBinary},
		boundedCodec{Codec: connectproto.NewJSONCodec(), count: countJSON},
	)
	small := http.NewServeMux()
	connecthttp.Mount(small, server, codecs, connecthttp.WithReadMaxBytes(MaxRequestBytes))
	large := http.NewServeMux()
	connecthttp.Mount(large, server, codecs, connecthttp.WithReadMaxBytes(MaxUploadBytes))
	errorWriter := connecthttp.NewErrorWriter()
	uploads := make(chan struct{}, maxConcurrentUploads)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, signedIn := authn.GetUserID(r.Context())
		upload := uploadProcedures[r.URL.Path]
		if !signedIn || !upload {
			// Requests too large to read are rejected without reading
			// them, saying what to do, if their length is known. The
			// handlers reject the others once they've read too much.
			if r.ContentLength > MaxRequestBytes {
				err := connect.Errorf(connect.CodeResourceExhausted,
					"the request is %d bytes, but at most %d are accepted; send less data at once", r.ContentLength, MaxRequestBytes)
				if upload {
					err = connect.NewError(connect.CodeUnauthenticated, "sign in to upload files")
				}
				_ = errorWriter.Write(w, r, err)
				return
			}
			small.ServeHTTP(w, r)
			return
		}
		// Compressed requests can be larger than their length once
		// decompressed, and streamed ones have no length.
		if r.ContentLength < 0 || r.ContentLength > MaxRequestBytes || compressed(r) {
			select {
			case uploads <- struct{}{}:
				defer func() { <-uploads }()
			case <-r.Context().Done():
				return
			}
		}
		large.ServeHTTP(w, r)
	})
}

// compressed reports whether a request's messages are compressed, in any of
// the protocols Connect serves.
func compressed(r *http.Request) bool {
	for _, header := range []string{"Content-Encoding", "Connect-Content-Encoding", "Grpc-Encoding"} {
		if v := r.Header.Get(header); v != "" && v != "identity" {
			return true
		}
	}
	return false
}

// boundedCodec is a codec that rejects messages with more than
// MaxRequestValues values before decoding them.
type boundedCodec struct {
	connect.Codec
	// count checks that an encoded message of the type has at most
	// MaxRequestValues values, and returns errTooManyValues otherwise.
	count func(data []byte, desc protoreflect.MessageDescriptor) error
}

func (c boundedCodec) UnmarshalRead(ctx context.Context, src io.Reader, msg any) error {
	m, ok := msg.(proto.Message)
	if !ok {
		return c.Codec.UnmarshalRead(ctx, src, msg)
	}
	// The transport passes the buffered message, so this usually doesn't
	// copy it.
	var data []byte
	if buf, ok := src.(*bytes.Buffer); ok {
		data = buf.Bytes()
	} else {
		var err error
		if data, err = io.ReadAll(src); err != nil {
			return err
		}
	}
	if err := c.count(data, m.ProtoReflect().Descriptor()); err != nil {
		return err
	}
	return c.Codec.UnmarshalRead(ctx, bytes.NewReader(data), msg)
}

// countBinary checks the values of a binary encoded message: each message,
// string and bytes field, or element of a repeated one.
func countBinary(data []byte, desc protoreflect.MessageDescriptor) error {
	n := 0
	return countBinaryMessage(data, desc, &n, 0)
}

func countBinaryMessage(data []byte, desc protoreflect.MessageDescriptor, n *int, depth int) error {
	if depth > maxNestingDepth {
		return errors.New("the request is nested too deeply")
	}
	for len(data) > 0 {
		num, typ, tagLen := protowire.ConsumeTag(data)
		if tagLen < 0 {
			// The decoder reports the invalid encoding.
			return nil
		}
		valueLen := protowire.ConsumeFieldValue(num, typ, data[tagLen:])
		if valueLen < 0 {
			return nil
		}
		value := data[tagLen : tagLen+valueLen]
		data = data[tagLen+valueLen:]
		// Unknown fields are kept as raw bytes, without being decoded.
		field := desc.Fields().ByNumber(num)
		if field == nil {
			continue
		}
		switch field.Kind() {
		case protoreflect.MessageKind:
			if typ != protowire.BytesType {
				continue
			}
			value, _ = protowire.ConsumeBytes(value)
		case protoreflect.GroupKind:
			if typ != protowire.StartGroupType {
				continue
			}
			value, _ = protowire.ConsumeGroup(num, value)
		case protoreflect.StringKind, protoreflect.BytesKind:
			if *n++; *n > MaxRequestValues {
				return errTooManyValues
			}
			continue
		default:
			// Scalars, packed or not, take no more memory decoded than
			// encoded.
			continue
		}
		if *n++; *n > MaxRequestValues {
			return errTooManyValues
		}
		if err := countBinaryMessage(value, field.Message(), n, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// countJSON checks the values of a JSON encoded message: each object and
// string other than the keys of objects, which bounds the messages, strings
// and bytes fields it decodes to.
func countJSON(data []byte, _ protoreflect.MessageDescriptor) error {
	n := 0
	count := func() error {
		if n++; n > MaxRequestValues {
			return errTooManyValues
		}
		return nil
	}
	// afterString is whether the last token was a string, which is counted
	// unless a colon follows, making it a key.
	inString, escaped, afterString := false, false, false
	for _, b := range data {
		switch {
		case escaped:
			escaped = false
		case inString:
			switch b {
			case '\\':
				escaped = true
			case '"':
				inString, afterString = false, true
			}
		case b == ' ' || b == '\t' || b == '\n' || b == '\r':
		default:
			if afterString && b != ':' {
				if err := count(); err != nil {
					return err
				}
			}
			afterString = false
			switch b {
			case '"':
				inString = true
			case '{':
				if err := count(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
