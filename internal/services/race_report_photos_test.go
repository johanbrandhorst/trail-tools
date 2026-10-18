package services_test

import (
	"bytes"
	"context"
	"image/jpeg"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	tripsv1 "github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1"
	usersv1 "github.com/johanbrandhorst/trail-tools/internal/gen/users/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRaceReportPhotos(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	// A 10 km activity on the race day, from 08:00 UTC, in Zurich.
	e.createActivity()
	e.createProfile("jane-doe")
	race, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
		Race: racesv1.Race_builder{Name: "Ridge 10K", Date: "2026-09-07", DistanceMeters: 10_000}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.raceReports.CreateRaceReport(ctx, racesv1.CreateRaceReportRequest_builder{RaceId: race.GetRace().GetId()}.Build())
	if err != nil {
		t.Fatal(err)
	}
	reportID := created.GetReport().GetId()
	data := testJPEG(t, 3000, 2000)
	upload := func(b racesv1.UploadRaceReportPhotoRequest_builder) *racesv1.RaceReportPhoto {
		t.Helper()
		b.ReportId = reportID
		if b.Data == nil {
			b.Data = data
		}
		resp, err := e.raceReports.UploadRaceReportPhoto(ctx, b.Build())
		if err != nil {
			t.Fatalf("UploadRaceReportPhoto: %v", err)
		}
		return resp.GetPhoto()
	}

	// Photos are placed on the track at the time they were taken, like trip
	// photos.
	timed := upload(racesv1.UploadRaceReportPhotoRequest_builder{
		TakenTime: timestamppb.New(tripStart.Add(30*time.Minute + 18*time.Second)),
	})
	if timed.GetLocationSource() != tripsv1.LocationSource_LOCATION_SOURCE_TRACK_TIME || !approx(timed.GetTrackDistanceMeters(), 5050, 1e-6) ||
		timed.GetWidth() != 2048 || timed.GetHeight() != 1365 {
		t.Errorf("unexpected timed photo %v", timed)
	}
	if !strings.HasPrefix(timed.GetUrl(), "https://trail.example/race-report-photos/") || !strings.HasSuffix(timed.GetThumbnailUrl(), "/thumb.jpg") {
		t.Errorf("unexpected photo URLs %q, %q", timed.GetUrl(), timed.GetThumbnailUrl())
	}
	// Or else at their GPS position.
	gps := upload(racesv1.UploadRaceReportPhotoRequest_builder{
		Caption:  " The start ",
		Location: geov1.LatLng_builder{Lat: 0.0181, Lng: 0.0001}.Build(),
	})
	if gps.GetLocationSource() != tripsv1.LocationSource_LOCATION_SOURCE_GPS || gps.GetTrackDistanceMeters() != 2000 || gps.GetCaption() != "The start" {
		t.Errorf("unexpected GPS photo %v", gps)
	}
	// Local times are in the activity's timezone, here UTC+2.
	local := upload(racesv1.UploadRaceReportPhotoRequest_builder{TakenLocalTime: "2026-09-07T10:15:00"})
	if !approx(local.GetTrackDistanceMeters(), 2500, 1e-6) || !local.GetTakenTime().AsTime().Equal(tripStart.Add(15*time.Minute)) {
		t.Errorf("unexpected local time photo %v", local)
	}
	for name, req := range map[string]racesv1.UploadRaceReportPhotoRequest_builder{
		"empty":      {ReportId: reportID},
		"not image":  {ReportId: reportID, Data: []byte("<svg></svg>")},
		"bad time":   {ReportId: reportID, Data: data, TakenLocalTime: "yesterday"},
		"bad lat":    {ReportId: reportID, Data: data, Location: geov1.LatLng_builder{Lat: 91}.Build()},
		"bad report": {ReportId: "nope", Data: data},
	} {
		_, err := e.raceReports.UploadRaceReportPhoto(ctx, req.Build())
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("UploadRaceReportPhoto %s: got %v, want invalid argument", name, err)
		}
	}
	_, err = e.otherRaceReports.UploadRaceReportPhoto(ctx, racesv1.UploadRaceReportPhotoRequest_builder{ReportId: reportID, Data: data}.Build())
	wantCode(t, err, connect.CodeNotFound)
	if n := e.storedImages(); n != 6 {
		t.Errorf("%d images stored, want 6", n)
	}

	// The report has its photos in the order they were taken.
	photoIDs := func(photos []*racesv1.RaceReportPhoto) string {
		var ids []string
		for _, p := range photos {
			ids = append(ids, p.GetId())
		}
		return strings.Join(ids, ",")
	}
	got, err := e.raceReports.GetRaceReport(ctx, racesv1.GetRaceReportRequest_builder{Id: reportID}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if ids, want := photoIDs(got.GetReport().GetPhotos()), strings.Join([]string{gps.GetId(), local.GetId(), timed.GetId()}, ","); ids != want {
		t.Errorf("photos in order %v, want %v", ids, want)
	}
	// Lists leave them out.
	list, err := e.raceReports.ListRaceReports(ctx, &racesv1.ListRaceReportsRequest{})
	if err != nil || len(list.GetReports()) != 1 || len(list.GetReports()[0].GetPhotos()) != 0 {
		t.Errorf("ListRaceReports = %v, %v, want the report without photos", list, err)
	}

	// Changing the race's activity places the photos on its track: those
	// placed by time at their time in the new activity, which started 15
	// minutes later, and the others where they are, along the new track.
	altitudes := make([]float64, 101)
	later := e.createActivityAt(4343, tripStart.Add(15*time.Minute), altitudes)
	firstActivity := got.GetReport().GetActivityId()
	setActivity := func(activityID string) map[string]*racesv1.RaceReportPhoto {
		t.Helper()
		resp, err := e.raceReports.UpdateRaceReport(ctx, racesv1.UpdateRaceReportRequest_builder{Id: reportID, ActivityId: &activityID}.Build())
		if err != nil {
			t.Fatalf("UpdateRaceReport: %v", err)
		}
		byID := map[string]*racesv1.RaceReportPhoto{}
		for _, p := range resp.GetReport().GetPhotos() {
			byID[p.GetId()] = p
		}
		return byID
	}
	moved := setActivity(later)
	if p := moved[timed.GetId()]; p.GetLocationSource() != tripsv1.LocationSource_LOCATION_SOURCE_TRACK_TIME || !approx(p.GetTrackDistanceMeters(), 2550, 1e-6) {
		t.Errorf("timed photo on the new track %v, want it at 2550 m", p)
	}
	if p := moved[local.GetId()]; !approx(p.GetTrackDistanceMeters(), 0, 1e-6) {
		t.Errorf("local time photo on the new track %v, want it at the start", p)
	}
	if p := moved[gps.GetId()]; p.GetLocationSource() != tripsv1.LocationSource_LOCATION_SOURCE_GPS || p.GetTrackDistanceMeters() != 2000 {
		t.Errorf("GPS photo on the new track %v, want it where it was", p)
	}
	// Without an activity, photos stay where they are, without distances.
	unset := setActivity("")
	if p := unset[timed.GetId()]; !approx(p.GetLocation().GetLat(), 0.02295, 1e-9) || p.HasTrackDistanceMeters() {
		t.Errorf("timed photo without a track %v, want it where it was without a distance", p)
	}
	if p := setActivity(firstActivity)[timed.GetId()]; !approx(p.GetTrackDistanceMeters(), 5050, 1e-6) {
		t.Errorf("timed photo back on the first track %v, want it at 5050 m", p)
	}

	// Agents look at photos to write their captions.
	image, err := e.raceReports.GetRaceReportPhoto(ctx, racesv1.GetRaceReportPhotoRequest_builder{Id: timed.GetId(), Size: tripsv1.PhotoSize_PHOTO_SIZE_LARGE}.Build())
	if err != nil {
		t.Fatalf("GetRaceReportPhoto: %v", err)
	}
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(image.GetImage().GetData())); err != nil || cfg.Width != 2048 || image.GetPhoto().GetId() != timed.GetId() {
		t.Errorf("got a %d wide image (%v) of photo %v, want the large image", cfg.Width, err, image.GetPhoto())
	}
	captioned, err := e.raceReports.UpdateRaceReportPhoto(ctx, racesv1.UpdateRaceReportPhotoRequest_builder{
		Id:       timed.GetId(),
		Caption:  new("Halfway"),
		Location: geov1.LatLng_builder{Lat: 0.09, Lng: 0}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRaceReportPhoto: %v", err)
	}
	if p := captioned.GetPhoto(); p.GetCaption() != "Halfway" || p.GetLocationSource() != tripsv1.LocationSource_LOCATION_SOURCE_MANUAL || p.GetTrackDistanceMeters() != 10000 {
		t.Errorf("unexpected updated photo %v", p)
	}
	cleared, err := e.raceReports.UpdateRaceReportPhoto(ctx, racesv1.UpdateRaceReportPhotoRequest_builder{Id: timed.GetId(), ClearLocation: true}.Build())
	if err != nil || cleared.GetPhoto().HasLocation() || cleared.GetPhoto().HasTrackDistanceMeters() {
		t.Errorf("UpdateRaceReportPhoto clearing the location = %v, %v", cleared, err)
	}
	// Other users can't see or change the photos.
	_, err = e.otherRaceReports.GetRaceReportPhoto(ctx, racesv1.GetRaceReportPhotoRequest_builder{Id: timed.GetId()}.Build())
	wantCode(t, err, connect.CodeNotFound)
	_, err = e.otherRaceReports.UpdateRaceReportPhoto(ctx, racesv1.UpdateRaceReportPhotoRequest_builder{Id: timed.GetId(), Caption: new("Mine")}.Build())
	wantCode(t, err, connect.CodeNotFound)
	_, err = e.otherRaceReports.DeleteRaceReportPhoto(ctx, racesv1.DeleteRaceReportPhotoRequest_builder{Id: timed.GetId()}.Build())
	wantCode(t, err, connect.CodeNotFound)
	_, err = e.otherRaceReports.CreateRaceReportPhotoUpload(ctx, racesv1.CreateRaceReportPhotoUploadRequest_builder{ReportId: reportID}.Build())
	wantCode(t, err, connect.CodeNotFound)

	// Agents upload photos with a link, which reads where and when they were
	// taken from their metadata.
	link, err := e.raceReports.CreateRaceReportPhotoUpload(ctx, racesv1.CreateRaceReportPhotoUploadRequest_builder{ReportId: reportID}.Build())
	if err != nil {
		t.Fatalf("CreateRaceReportPhotoUpload: %v", err)
	}
	url := link.GetUpload().GetUrl()
	if !strings.HasPrefix(url, "https://trail.example/race-report-photo-uploads/") {
		t.Errorf("unexpected upload link %v", link.GetUpload())
	}
	resp, body := e.post(url+"?caption=The+finish", bytes.NewReader(exifJPEG(t, 400, 300, testEXIF{
		orientation: 8, taken: "2026:09:07 08:59:00", offset: "Z", lat: 0.01,
	})), "image/jpeg")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: status %d: %s", resp.StatusCode, body)
	}
	var uploaded racesv1.UploadRaceReportPhotoResponse
	if err := protojson.Unmarshal(body, &uploaded); err != nil {
		t.Fatalf("upload response %s: %v", body, err)
	}
	// The offset isn't valid, so the time is local, in Zurich, before the
	// race, and the GPS position places the photo.
	finish := uploaded.GetPhoto()
	if finish.GetCaption() != "The finish" || finish.GetWidth() != 300 || finish.GetHeight() != 400 ||
		finish.GetLocationSource() != tripsv1.LocationSource_LOCATION_SOURCE_GPS || !approx(finish.GetLocation().GetLat(), 0.01, 1e-9) ||
		!finish.GetTakenTime().AsTime().Equal(tripStart.Add(59*time.Minute-2*time.Hour)) {
		t.Errorf("unexpected uploaded photo %v", finish)
	}
	// A race report's link doesn't upload to trip reports.
	tripURL := strings.Replace(url, "/race-report-photo-uploads/", "/trip-photo-uploads/", 1)
	if resp, body := e.post(tripURL, bytes.NewReader(testJPEG(t, 10, 10)), ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("race link on the trip path: status %d: %s, want 404", resp.StatusCode, body)
	}

	// Photos of drafts are only served to their author.
	for name, token := range map[string]string{"anonymous": "", "other user": e.otherToken} {
		if resp, _ := e.get(gps.GetUrl(), token); resp.StatusCode != http.StatusNotFound {
			t.Errorf("draft photo for %s: status %d, want 404", name, resp.StatusCode)
		}
	}
	resp, body = e.get(gps.GetThumbnailUrl(), e.token)
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(body)); resp.StatusCode != http.StatusOK || err != nil || cfg.Width != 480 ||
		!strings.HasPrefix(resp.Header.Get("Cache-Control"), "private") {
		t.Errorf("draft thumbnail for the author: status %d, %v, %v, headers %v", resp.StatusCode, cfg, err, resp.Header)
	}
	if _, err := e.raceReports.PublishRaceReport(ctx, racesv1.PublishRaceReportRequest_builder{Id: reportID}.Build()); err != nil {
		t.Fatal(err)
	}
	if resp, _ := e.get(gps.GetUrl(), ""); resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("published photo: status %d, headers %v", resp.StatusCode, resp.Header)
	}
	// Link previews show the first photo.
	if resp, body := e.get("/u/jane-doe/races/ridge-10k", ""); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), finish.GetUrl()) {
		t.Errorf("public page: status %d, want the first photo in its preview\n%s", resp.StatusCode, body)
	}

	// The public report hides where photos in privacy zones were taken.
	if _, err := e.users.SetPrivacyZones(ctx, usersv1.SetPrivacyZonesRequest_builder{
		Zones: []*usersv1.PrivacyZone{usersv1.PrivacyZone_builder{Name: "Home", Lat: 0.018, Lng: 0, RadiusMeters: 200}.Build()},
	}.Build()); err != nil {
		t.Fatalf("SetPrivacyZones: %v", err)
	}
	public, err := e.anonRaceReports.GetPublicRaceReport(ctx, racesv1.GetPublicRaceReportRequest_builder{Handle: "jane-doe", Slug: "ridge-10k"}.Build())
	if err != nil {
		t.Fatal(err)
	}
	// The GPS photo is in the zone, and the timed one has no location.
	for _, p := range public.GetReport().GetPhotos() {
		wantLocation := p.GetId() == local.GetId() || p.GetId() == finish.GetId()
		if p.HasLocation() != wantLocation {
			t.Errorf("public photo %v has a location: %v, want %v", p.GetId(), p.HasLocation(), wantLocation)
		}
	}

	// Deleting a photo deletes its images, and deleting the report the rest.
	if _, err := e.raceReports.DeleteRaceReportPhoto(ctx, racesv1.DeleteRaceReportPhotoRequest_builder{Id: gps.GetId()}.Build()); err != nil {
		t.Fatalf("DeleteRaceReportPhoto: %v", err)
	}
	if n := e.storedImages(); n != 6 {
		t.Errorf("%d images stored, want 6", n)
	}
	if resp, _ := e.get(gps.GetUrl(), e.token); resp.StatusCode != http.StatusNotFound {
		t.Errorf("deleted photo: status %d, want 404", resp.StatusCode)
	}
	if _, err := e.raceReports.DeleteRaceReport(ctx, racesv1.DeleteRaceReportRequest_builder{Id: reportID}.Build()); err != nil {
		t.Fatalf("DeleteRaceReport: %v", err)
	}
	if n := e.storedImages(); n != 0 {
		t.Errorf("%d images left, want none", n)
	}
	_, err = e.raceReports.DeleteRaceReport(ctx, racesv1.DeleteRaceReportRequest_builder{Id: reportID}.Build())
	wantCode(t, err, connect.CodeNotFound)
}
