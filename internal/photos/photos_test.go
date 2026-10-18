package photos

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	"google.golang.org/protobuf/proto"
)

func encodeTestImage(t *testing.T, w, h int, asPNG bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 100, A: 255})
		}
	}
	// A transparent corner, which must become white rather than black.
	img.Set(0, 0, color.NRGBA{})
	var buf bytes.Buffer
	var err error
	if asPNG {
		err = png.Encode(&buf, img)
	} else {
		err = jpeg.Encode(&buf, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestProcess(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		w, h                int
		png                 bool
		wantW, wantH        int
		wantThumbW, wantThW int
	}{
		{name: "large landscape JPEG", w: 4000, h: 3000, wantW: 2048, wantH: 1536, wantThumbW: 480, wantThW: 360},
		{name: "portrait PNG", w: 600, h: 1200, png: true, wantW: 600, wantH: 1200, wantThumbW: 240, wantThW: 480},
		{name: "small", w: 300, h: 200, wantW: 300, wantH: 200, wantThumbW: 300, wantThW: 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Process(t.Context(), encodeTestImage(t, tt.w, tt.h, tt.png))
			if err != nil {
				t.Fatal(err)
			}
			if got.Width != tt.wantW || got.Height != tt.wantH {
				t.Errorf("large is %dx%d, want %dx%d", got.Width, got.Height, tt.wantW, tt.wantH)
			}
			large, err := jpeg.Decode(bytes.NewReader(got.Large))
			if err != nil {
				t.Fatalf("large is not a JPEG: %v", err)
			}
			if b := large.Bounds(); b.Dx() != tt.wantW || b.Dy() != tt.wantH {
				t.Errorf("decoded large is %v", b)
			}
			thumb, err := jpeg.Decode(bytes.NewReader(got.Thumb))
			if err != nil {
				t.Fatalf("thumbnail is not a JPEG: %v", err)
			}
			if b := thumb.Bounds(); b.Dx() != tt.wantThumbW || b.Dy() != tt.wantThW {
				t.Errorf("thumbnail is %dx%d, want %dx%d", b.Dx(), b.Dy(), tt.wantThumbW, tt.wantThW)
			}
			if tt.png {
				if r, g, b, _ := large.At(0, 0).RGBA(); r>>8 < 200 || g>>8 < 200 || b>>8 < 200 {
					t.Errorf("transparent pixel is %v, want white", large.At(0, 0))
				}
			}
		})
	}
	for _, data := range [][]byte{[]byte("not an image"), []byte("GIF89a")} {
		if _, err := Process(t.Context(), data); err == nil {
			t.Errorf("Process(%q) succeeded", data)
		}
	}
}

// withOrientation returns a JPEG with an APP1 segment with the EXIF
// orientation, as phones store photos taken in portrait.
func withOrientation(data []byte, orientation uint16) []byte {
	tiff := []byte("II*\x00\x08\x00\x00\x00")
	tiff = binary.LittleEndian.AppendUint16(tiff, 1)
	// The orientation tag, a SHORT, with one value.
	tiff = binary.LittleEndian.AppendUint16(tiff, 0x0112)
	tiff = binary.LittleEndian.AppendUint16(tiff, 3)
	tiff = binary.LittleEndian.AppendUint32(tiff, 1)
	tiff = binary.LittleEndian.AppendUint16(tiff, orientation)
	tiff = append(tiff, 0, 0, 0, 0, 0, 0)
	segment := append([]byte("Exif\x00\x00"), tiff...)
	app1 := binary.BigEndian.AppendUint16([]byte{0xff, 0xe1}, uint16(len(segment)+2))
	out := append([]byte{}, data[:2]...)
	out = append(out, app1...)
	out = append(out, segment...)
	return append(out, data[2:]...)
}

func TestProcessTurnsPhotosUpright(t *testing.T) {
	t.Parallel()
	// The camera stored the pixels of a portrait photo sideways, as a
	// landscape image, with an orientation of 6.
	got, err := Process(t.Context(), withOrientation(encodeTestImage(t, 400, 300, false), 6))
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != 300 || got.Height != 400 {
		t.Errorf("large is %dx%d, want 300x400", got.Width, got.Height)
	}
	if got.Metadata.Orientation != 6 {
		t.Errorf("orientation is %d, want 6", got.Metadata.Orientation)
	}
	thumb, err := jpeg.Decode(bytes.NewReader(got.Thumb))
	if err != nil {
		t.Fatalf("thumbnail is not a JPEG: %v", err)
	}
	if b := thumb.Bounds(); b.Dx() != 300 || b.Dy() != 400 {
		t.Errorf("thumbnail is %v, want 300x400", b)
	}
}

func TestProcessWaitsForASlot(t *testing.T) {
	// Not parallel, as it takes every processing slot.
	for range maxProcessing {
		processing <- struct{}{}
	}
	defer func() {
		for range maxProcessing {
			<-processing
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Process(ctx, encodeTestImage(t, 10, 10, false)); !errors.Is(err, context.Canceled) {
		t.Errorf("Process while all slots are taken returned %v, want context.Canceled", err)
	}
}

// testLink is what a test upload link uploads to.
type testLink struct {
	reportID string
}

func TestUploadHandler(t *testing.T) {
	t.Parallel()
	const path = "/uploads/"
	photo := encodeTestImage(t, 20, 10, false)
	find := func(_ context.Context, token string) (*testLink, error) {
		if token != "secret" {
			return nil, connect.NewError(connect.CodeNotFound, "this upload link doesn't exist or has expired")
		}
		return &testLink{reportID: "report"}, nil
	}
	// The upload responds with a message of the caption's length and the
	// photo's size, as a LatLng.
	var uploaded []*Upload
	upload := func(_ context.Context, link *testLink, u *Upload) (proto.Message, error) {
		if link.reportID != "report" {
			t.Errorf("uploaded to %q, want report", link.reportID)
		}
		if u.Caption == "fail" {
			return nil, errors.New("the database is down")
		}
		uploaded = append(uploaded, u)
		return geov1.LatLng_builder{Lat: float64(len(u.Caption)), Lng: float64(len(u.Data))}.Build(), nil
	}
	report := errreport.NewLocal(slog.New(slog.DiscardHandler))
	handler := UploadHandler(report, path, find, upload)

	multipartBody := func(caption string) (io.Reader, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		if err := mw.WriteField("caption", caption); err != nil {
			t.Fatal(err)
		}
		fw, err := mw.CreateFormFile("file", "IMG_1234.jpg")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(photo)
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		return &buf, mw.FormDataContentType()
	}

	for _, tt := range []struct {
		name        string
		method      string
		target      string
		body        func() (io.Reader, string)
		wantStatus  int
		wantCode    string
		wantCaption string
	}{
		{
			name:   "body",
			method: http.MethodPost,
			target: path + "secret?caption=On+the+ridge",
			body: func() (io.Reader, string) {
				return bytes.NewReader(photo), "application/octet-stream"
			},
			wantStatus:  http.StatusCreated,
			wantCaption: "On the ridge",
		},
		{
			name:   "multipart form",
			method: http.MethodPut,
			target: path + "secret",
			body: func() (io.Reader, string) {
				return multipartBody("The summit")
			},
			wantStatus:  http.StatusCreated,
			wantCaption: "The summit",
		},
		{
			name:   "unknown link",
			method: http.MethodPost,
			target: path + "guess",
			body: func() (io.Reader, string) {
				return bytes.NewReader(photo), ""
			},
			wantStatus: http.StatusNotFound,
			wantCode:   "not_found",
		},
		{
			name:   "get",
			method: http.MethodGet,
			target: path + "secret",
			body: func() (io.Reader, string) {
				return http.NoBody, ""
			},
			wantStatus: http.StatusMethodNotAllowed,
			wantCode:   "unimplemented",
		},
		{
			name:   "empty",
			method: http.MethodPost,
			target: path + "secret",
			body: func() (io.Reader, string) {
				return http.NoBody, ""
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_argument",
		},
		{
			name:   "too large",
			method: http.MethodPost,
			target: path + "secret",
			body: func() (io.Reader, string) {
				return bytes.NewReader(make([]byte, maxUploadRequestBytes+1)), ""
			},
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   "invalid_argument",
		},
		{
			name:   "internal error",
			method: http.MethodPost,
			target: path + "secret?caption=fail",
			body: func() (io.Reader, string) {
				return bytes.NewReader(photo), ""
			},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal",
		},
	} {
		body, contentType := tt.body()
		req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, body)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		uploaded = nil
		handler.ServeHTTP(rec, req)
		if rec.Code != tt.wantStatus {
			t.Errorf("%s: status %d, want %d: %s", tt.name, rec.Code, tt.wantStatus, rec.Body)
			continue
		}
		if tt.wantCode != "" {
			var got struct{ Code, Message string }
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Errorf("%s: response %q isn't a JSON error: %v", tt.name, rec.Body, err)
			}
			if got.Code != tt.wantCode || got.Message == "" {
				t.Errorf("%s: error %+v, want code %q with a message", tt.name, got, tt.wantCode)
			}
			continue
		}
		if len(uploaded) != 1 || !bytes.Equal(uploaded[0].Data, photo) || uploaded[0].Caption != tt.wantCaption {
			t.Errorf("%s: uploaded %d photos, want the photo with caption %q", tt.name, len(uploaded), tt.wantCaption)
		}
		if !strings.Contains(rec.Body.String(), `"lng":`) {
			t.Errorf("%s: response %q isn't the upload's", tt.name, rec.Body)
		}
	}
}
