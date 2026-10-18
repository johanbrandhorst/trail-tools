package services_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	tripsv1 "github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// testEXIF is the EXIF metadata of a test photo.
type testEXIF struct {
	// How the stored pixels are turned, 1 if upright.
	orientation uint16
	// When it was taken, as EXIF records it ("2006:01:02 15:04:05"), and its
	// UTC offset ("+02:00"), if set.
	taken, offset string
	// The GPS position, if not 0, 0.
	lat, lng float64
}

// exifJPEG returns a JPEG of w by h pixels with EXIF metadata in an APP1
// segment, as phones and cameras write them.
func exifJPEG(t *testing.T, w, h int, x testEXIF) []byte {
	t.Helper()
	type entry struct {
		tag, kind uint16
		count     uint32
		data      []byte
	}
	le := binary.LittleEndian
	ascii := func(tag uint16, s string) entry {
		data := append([]byte(s), 0)
		return entry{tag, 2, uint32(len(data)), data}
	}
	long := func(tag uint16, v uint32) entry {
		return entry{tag, 4, 1, le.AppendUint32(nil, v)}
	}
	// degrees writes a coordinate as degrees, with zero minutes and seconds.
	degrees := func(tag uint16, deg float64) entry {
		var data []byte
		for _, v := range [][2]uint32{{uint32(math.Round(math.Abs(deg) * 1e7)), 1e7}, {0, 1}, {0, 1}} {
			data = le.AppendUint32(le.AppendUint32(data, v[0]), v[1])
		}
		return entry{tag, 5, 3, data}
	}
	ifd0 := []entry{{0x0112, 3, 1, le.AppendUint16(nil, x.orientation)}}
	var exif, gps []entry
	if x.taken != "" {
		exif = append(exif, ascii(0x9003, x.taken))
		if x.offset != "" {
			exif = append(exif, ascii(0x9011, x.offset))
		}
	}
	if x.lat != 0 || x.lng != 0 {
		latRef, lngRef := "N", "E"
		if x.lat < 0 {
			latRef = "S"
		}
		if x.lng < 0 {
			lngRef = "W"
		}
		gps = []entry{ascii(1, latRef), degrees(2, x.lat), ascii(3, lngRef), degrees(4, x.lng)}
	}
	// The header, IFD0 with pointers to the Exif and GPS IFDs, those IFDs,
	// and then the values that don't fit in their entries.
	ifdSize := func(n int) int { return 2 + 12*n + 4 }
	exifAt := 8 + ifdSize(len(ifd0)+2)
	gpsAt := exifAt + ifdSize(len(exif))
	dataAt := gpsAt + ifdSize(len(gps))
	ifd0 = append(ifd0, long(0x8769, uint32(exifAt)), long(0x8825, uint32(gpsAt)))
	tiff := make([]byte, dataAt)
	copy(tiff, "II*\x00\x08\x00\x00\x00")
	var values []byte
	for _, ifd := range []struct {
		at      int
		entries []entry
	}{{8, ifd0}, {exifAt, exif}, {gpsAt, gps}} {
		le.PutUint16(tiff[ifd.at:], uint16(len(ifd.entries)))
		for i, e := range ifd.entries {
			at := ifd.at + 2 + 12*i
			le.PutUint16(tiff[at:], e.tag)
			le.PutUint16(tiff[at+2:], e.kind)
			le.PutUint32(tiff[at+4:], e.count)
			if len(e.data) <= 4 {
				copy(tiff[at+8:], e.data)
			} else {
				le.PutUint32(tiff[at+8:], uint32(dataAt+len(values)))
				values = append(values, e.data...)
			}
		}
	}
	segment := append(append([]byte("Exif\x00\x00"), tiff...), values...)
	jpg := testJPEG(t, w, h)
	out := append([]byte{}, jpg[:2]...)
	out = append(out, 0xff, 0xe1)
	out = binary.BigEndian.AppendUint16(out, uint16(len(segment)+2))
	out = append(out, segment...)
	return append(out, jpg[2:]...)
}

// post sends a photo to an upload link, and returns the response with its
// body read.
func (e *env) post(url string, body io.Reader, contentType string) (*http.Response, []byte) {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+strings.TrimPrefix(url, "https://trail.example"), body)
	if err != nil {
		e.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp, data
}

// multipartPhoto returns a multipart form with a photo file and a caption,
// as curl -F sends.
func multipartPhoto(t *testing.T, data []byte, caption string) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	f, err := w.CreateFormFile("file", "IMG_1234.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("caption", caption); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func TestTripPhotoUploadLinks(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	created, err := e.trips.CreateTripReport(ctx, tripsv1.CreateTripReportRequest_builder{ActivityId: e.createActivity()}.Build())
	if err != nil {
		t.Fatal(err)
	}
	reportID := created.GetReport().GetId()

	// An agent creates a link to upload the user's photos with.
	before := time.Now()
	resp, err := e.trips.CreateTripPhotoUpload(ctx, tripsv1.CreateTripPhotoUploadRequest_builder{ReportId: reportID}.Build())
	if err != nil {
		t.Fatalf("CreateTripPhotoUpload: %v", err)
	}
	link := resp.GetUpload()
	if !strings.HasPrefix(link.GetUrl(), "https://trail.example/trip-photo-uploads/") ||
		link.GetExpireTime().AsTime().Before(before.Add(59*time.Minute)) || link.GetExpireTime().AsTime().After(time.Now().Add(time.Hour)) {
		t.Errorf("unexpected upload link %v", link)
	}
	upload := func(body io.Reader, contentType, query string) *tripsv1.TripPhoto {
		t.Helper()
		resp, data := e.post(link.GetUrl()+query, body, contentType)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("upload: status %d: %s", resp.StatusCode, data)
		}
		var uploaded tripsv1.UploadTripPhotoResponse
		if err := protojson.Unmarshal(data, &uploaded); err != nil {
			t.Fatalf("upload response %s: %v", data, err)
		}
		return uploaded.GetPhoto()
	}

	// A phone's photo, taken in portrait half an hour in, and stored
	// sideways, is turned upright and placed on the track at the time it was
	// taken.
	phone := upload(bytes.NewReader(exifJPEG(t, 400, 300, testEXIF{
		orientation: 6, taken: "2026:09:07 10:30:00", offset: "+02:00", lat: 0.03, lng: 0.01,
	})), "image/jpeg", "?caption=On+the+ridge")
	if phone.GetCaption() != "On the ridge" || phone.GetWidth() != 300 || phone.GetHeight() != 400 ||
		phone.GetLocationSource() != tripsv1.LocationSource_LOCATION_SOURCE_TRACK_TIME || !approx(phone.GetTrackDistanceMeters(), 5000, 1e-6) ||
		!phone.GetTakenTime().AsTime().Equal(tripStart.Add(30*time.Minute)) {
		t.Errorf("unexpected phone photo %v", phone)
	}
	// A camera records the local time, which is in the trip's timezone, here
	// UTC+2.
	body, contentType := multipartPhoto(t, exifJPEG(t, 400, 300, testEXIF{orientation: 1, taken: "2026:09:07 10:15:00"}), "The first climb")
	camera := upload(body, contentType, "")
	if camera.GetCaption() != "The first climb" || !approx(camera.GetTrackDistanceMeters(), 2500, 1e-6) ||
		!camera.GetTakenTime().AsTime().Equal(tripStart.Add(15*time.Minute)) {
		t.Errorf("unexpected camera photo %v", camera)
	}
	// Without a time, the GPS position places the photo.
	gps := upload(bytes.NewReader(exifJPEG(t, 400, 300, testEXIF{orientation: 1, lat: 0.0181})), "", "")
	if gps.GetLocationSource() != tripsv1.LocationSource_LOCATION_SOURCE_GPS || gps.GetTrackDistanceMeters() != 2000 || gps.HasTakenTime() {
		t.Errorf("unexpected GPS photo %v", gps)
	}
	// The photos are in the story in the order they were taken.
	got, err := e.trips.GetTripReport(ctx, tripsv1.GetTripReportRequest_builder{Id: reportID}.Build())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range got.GetReport().GetPhotos() {
		ids = append(ids, p.GetId())
	}
	if want := []string{gps.GetId(), camera.GetId(), phone.GetId()}; strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("photos in order %v, want %v", ids, want)
	}

	// Errors say what to do.
	heic := append([]byte{0, 0, 0, 24}, "ftypheic\x00\x00\x00\x00mif1heic"...)
	for name, tt := range map[string]struct {
		url, body  string
		wantStatus int
		wantText   string
	}{
		"HEIC":           {link.GetUrl(), string(heic), http.StatusBadRequest, "convert it to JPEG"},
		"empty":          {link.GetUrl(), "", http.StatusBadRequest, "curl --data-binary"},
		"long caption":   {link.GetUrl() + "?caption=" + strings.Repeat("a", 2001), string(testJPEG(t, 10, 10)), http.StatusBadRequest, "caption"},
		"unknown link":   {"/trip-photo-uploads/guess", string(testJPEG(t, 10, 10)), http.StatusNotFound, "CreateTripPhotoUpload"},
		"race link path": {strings.Replace(link.GetUrl(), "/trip-photo-uploads/", "/race-report-photo-uploads/", 1), string(testJPEG(t, 10, 10)), http.StatusNotFound, "CreateRaceReportPhotoUpload"},
	} {
		resp, data := e.post(tt.url, strings.NewReader(tt.body), "")
		if resp.StatusCode != tt.wantStatus || !strings.Contains(string(data), tt.wantText) {
			t.Errorf("%s: status %d: %s, want %d with %q", name, resp.StatusCode, data, tt.wantStatus, tt.wantText)
		}
	}
	if resp, _ := e.get(link.GetUrl(), ""); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET of the link: status %d, want 405", resp.StatusCode)
	}

	// Other users can't create links to the user's reports.
	_, err = e.otherTrips.CreateTripPhotoUpload(ctx, tripsv1.CreateTripPhotoUploadRequest_builder{ReportId: reportID}.Build())
	wantCode(t, err, connect.CodeNotFound)
	_, err = e.anonTrips.CreateTripPhotoUpload(ctx, tripsv1.CreateTripPhotoUploadRequest_builder{ReportId: reportID}.Build())
	wantCode(t, err, connect.CodeUnauthenticated)

	// Expired links don't upload.
	if _, err := e.db.Exec(ctx, "update photo_upload_links set expire_time = now() - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	if resp, data := e.post(link.GetUrl(), bytes.NewReader(testJPEG(t, 10, 10)), ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("expired link: status %d: %s, want 404", resp.StatusCode, data)
	}
	// Creating a link deletes the user's expired ones.
	if _, err := e.trips.CreateTripPhotoUpload(ctx, tripsv1.CreateTripPhotoUploadRequest_builder{ReportId: reportID}.Build()); err != nil {
		t.Fatal(err)
	}
	var links int
	if err := e.db.QueryRow(ctx, "select count(*) from photo_upload_links").Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 1 {
		t.Errorf("%d upload links stored, want 1", links)
	}
	// Deleting the report deletes its links and photos.
	if _, err := e.trips.DeleteTripReport(ctx, tripsv1.DeleteTripReportRequest_builder{Id: reportID}.Build()); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRow(ctx, "select count(*) from photo_upload_links").Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 0 || e.storedImages() != 0 {
		t.Errorf("%d upload links and %d images left, want none", links, e.storedImages())
	}
}
