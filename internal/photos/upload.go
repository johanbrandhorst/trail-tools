package photos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	// maxUploadRequestBytes limits the size of upload requests: a photo, and
	// the caption and boundaries of a multipart form.
	maxUploadRequestBytes = MaxBytes + 1<<20
	// maxCaptionBytes limits the size of a caption form field read into
	// memory. The services limit captions to fewer characters.
	maxCaptionBytes = 64 << 10
)

// errTooLarge is the cause of the error for photos larger than MaxBytes.
var errTooLarge = errors.New("photo too large")

// Upload is a photo uploaded with an upload link.
type Upload struct {
	// The file, as uploaded.
	Data []byte
	// The caption, if any.
	Caption string
}

// UploadHandler serves the links AI agents upload photos with. Agents can't
// send files to MCP tools, but those with a shell can send them to a link
// with curl. Each POST or PUT request uploads one photo, as the request body
// or the file of a multipart form, with an optional caption in the caption
// query parameter or form field, and the link's secret token in an
// "Authorization: Bearer <token>" header. The token isn't in the URL, as
// request logs record URLs, and curl doesn't send fragments.
//
// find returns what the link with a token uploads to, and upload adds a
// photo to it and returns the response to send as JSON. They return Connect
// errors, which are sent as JSON with their HTTP status, as their messages
// tell agents what to do. Other errors are reported. The token is the only
// authentication, like a password, so the handler isn't behind the authn
// middleware, which would take it for an API token.
func UploadHandler[L any](report *errreport.Reporter, find func(ctx context.Context, token string) (L, error), upload func(ctx context.Context, link L, u *Upload) (proto.Message, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			w.Header().Set("Allow", "POST, PUT")
			writeError(w, report, r, connect.NewError(connect.CodeUnimplemented,
				"upload a photo with a POST request with the file as the body, e.g. curl -H 'Authorization: Bearer <token>' --data-binary @photo.jpg '<url>'"),
				http.StatusMethodNotAllowed)
			return
		}
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeError(w, report, r, connect.NewError(connect.CodeUnauthenticated,
				"send the upload link's token in an Authorization header, e.g. curl -H 'Authorization: Bearer <token>' --data-binary @photo.jpg '<url>'"), 0)
			return
		}
		link, err := find(r.Context(), token)
		if err != nil {
			writeError(w, report, r, err, 0)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
		u, err := readUpload(r)
		if maxErr, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, report, r, connect.Errorf(connect.CodeInvalidArgument,
				"the request is larger than %d MB; scale the photo down first", maxErr.Limit>>20), http.StatusRequestEntityTooLarge)
			return
		}
		if err != nil {
			status := 0
			if errors.Is(err, errTooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			writeError(w, report, r, err, status)
			return
		}
		resp, err := upload(r.Context(), link, u)
		if err != nil {
			writeError(w, report, r, err, 0)
			return
		}
		data, err := protojson.MarshalOptions{EmitDefaultValues: true}.Marshal(resp)
		if err != nil {
			writeError(w, report, r, fmt.Errorf("failed to encode the response: %w", err), 0)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(data)
	})
}

// readUpload reads the photo and caption of an upload request.
func readUpload(r *http.Request) (*Upload, error) {
	u := &Upload{Caption: r.URL.Query().Get("caption")}
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "multipart/form-data" {
		data, err := io.ReadAll(io.LimitReader(r.Body, MaxBytes+1))
		if err != nil {
			return nil, readError(err)
		}
		u.Data = data
		return u, checkSize(u.Data)
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid multipart form: %v", err)
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, readError(err)
		}
		switch {
		case part.FileName() != "" && u.Data == nil:
			if u.Data, err = io.ReadAll(io.LimitReader(part, MaxBytes+1)); err != nil {
				return nil, readError(err)
			}
		case part.FileName() == "" && part.FormName() == "caption":
			caption, err := io.ReadAll(io.LimitReader(part, maxCaptionBytes))
			if err != nil {
				return nil, readError(err)
			}
			u.Caption = string(caption)
		}
	}
	return u, checkSize(u.Data)
}

// readError returns the error of a failure to read an upload request. The
// client caused it, e.g. by sending too much or hanging up, so it isn't
// reported.
func readError(err error) error {
	return connect.Errorf(connect.CodeInvalidArgument, "failed to read the photo: %v", err).WithCause(err)
}

// checkSize checks that an uploaded photo isn't empty or too large.
func checkSize(data []byte) error {
	switch {
	case len(data) == 0:
		return connect.NewError(connect.CodeInvalidArgument,
			"the request has no photo; send the file as the request body, e.g. curl -H 'Authorization: Bearer <token>' --data-binary @photo.jpg '<url>', or as a file of a multipart form")
	case len(data) > MaxBytes:
		return connect.Errorf(connect.CodeInvalidArgument, "the photo must be at most %d MB; scale it down first", MaxBytes>>20).WithCause(errTooLarge)
	}
	return nil
}

// writeError sends an error as JSON, in the format of Connect's errors:
// {"code": "not_found", "message": "..."}. Errors that aren't Connect errors
// are reported, and sent as internal errors. The status is the code's, unless
// it's set.
func writeError(w http.ResponseWriter, report *errreport.Reporter, r *http.Request, err error, status int) {
	connectErr, ok := errors.AsType[*connect.Error](err)
	if !ok {
		report.Report(r.Context(), "Failed to upload a photo with a link", err)
		connectErr = connect.NewError(connect.CodeInternal, "failed to upload the photo")
	}
	if status == 0 {
		status = httpStatus(connectErr.Code())
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Messages show agents commands like curl ... '<url>', which read
	// better without escaping for HTML.
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]string{
		"code":    connectErr.Code().String(),
		"message": connectErr.Message(),
	})
}

// httpStatus returns the HTTP status of a Connect error code, as in the
// Connect protocol.
func httpStatus(code connect.Code) int {
	switch code {
	case connect.CodeCanceled:
		return 499
	case connect.CodeInvalidArgument, connect.CodeFailedPrecondition, connect.CodeOutOfRange:
		return http.StatusBadRequest
	case connect.CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	case connect.CodeNotFound:
		return http.StatusNotFound
	case connect.CodeAlreadyExists, connect.CodeAborted:
		return http.StatusConflict
	case connect.CodePermissionDenied:
		return http.StatusForbidden
	case connect.CodeUnauthenticated:
		return http.StatusUnauthorized
	case connect.CodeResourceExhausted:
		return http.StatusTooManyRequests
	case connect.CodeUnimplemented:
		return http.StatusNotImplemented
	case connect.CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
