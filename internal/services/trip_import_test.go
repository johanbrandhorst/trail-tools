package services_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	tripsv1 "github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

func TestImportStravaPhotos(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	image := testJPEG(t, 300, 200)
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/p1.jpg", "/p2.jpg", "/p3.jpg":
			_, _ = w.Write(image)
		case "/text.jpg":
			_, _ = w.Write([]byte("not an image"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(cdn.Close)

	activityID := e.createActivity()
	id, err := store.StringToUUID(activityID)
	if err != nil {
		t.Fatal(err)
	}
	// The activity isn't connected to Strava here, so the photos from the
	// last sync are imported.
	e.storeActivityDetail(id, &activities.Detail{Description: "Felt good", DeviceName: "Watch", RawJSON: []byte("{}"), Photos: []activities.Photo{
		{StravaID: "p1", Caption: "Halfway", URL: cdn.URL + "/p1.jpg", Location: &geo.Point{Lat: 0.045, Lng: 0}, RawJSON: []byte("{}")},
		// 18 minutes in, 30 points along the track.
		{StravaID: "p2", URL: cdn.URL + "/p2.jpg", TakenTime: tripStart.Add(18 * time.Minute), RawJSON: []byte("{}")},
		// A GPS position far off the track, and a time during the activity:
		// 36 minutes in, 60 points along the track.
		{
			StravaID: "p3", URL: cdn.URL + "/p3.jpg", Location: &geo.Point{Lat: 1, Lng: 1},
			TakenTime: tripStart.Add(36 * time.Minute), RawJSON: []byte("{}"),
		},
		{StravaID: "gone", URL: cdn.URL + "/gone.jpg", RawJSON: []byte("{}")},
		{StravaID: "text", URL: cdn.URL + "/text.jpg", RawJSON: []byte("{}")},
	}}, nil, nil)
	created, err := e.trips.CreateTripReport(ctx, tripsv1.CreateTripReportRequest_builder{ActivityId: activityID}.Build())
	if err != nil {
		t.Fatal(err)
	}
	report := created.GetReport()
	// The story starts from the activity's description on Strava.
	if sections := report.GetSections(); len(sections) != 1 || sections[0].GetBody() != "Felt good" || len(sections[0].GetPhotoIds()) != 3 {
		t.Errorf("got sections %v, want one with the activity's description and the photos", sections)
	}

	// The report includes the activity's photos from the start.
	photos := report.GetPhotos()
	if len(photos) != 3 {
		t.Fatalf("got %d photos, want the 3 importable ones", len(photos))
	}
	// They're in order along the track: at 3, 5 and 6 km.
	if p := photos[0]; p.GetLocationSource() != tripsv1.LocationSource_LOCATION_SOURCE_TRACK_TIME ||
		!approx(p.GetLocation().GetLat(), 0.027, 1e-6) || !approx(p.GetTrackDistanceMeters(), 3000, 1) {
		t.Errorf("unexpected photo placed by time %v", p)
	}
	// Without a time, a photo is placed at its GPS position.
	if p := photos[1]; p.GetCaption() != "Halfway" || p.GetLocationSource() != tripsv1.LocationSource_LOCATION_SOURCE_GPS ||
		!approx(p.GetTrackDistanceMeters(), 5000, 1) || p.GetWidth() != 300 {
		t.Errorf("unexpected photo with a location %v", p)
	}
	// With a time during the activity, the track is trusted over a GPS
	// position.
	if p := photos[2]; p.GetLocationSource() != tripsv1.LocationSource_LOCATION_SOURCE_TRACK_TIME ||
		!approx(p.GetLocation().GetLat(), 0.054, 1e-6) || !approx(p.GetTrackDistanceMeters(), 6000, 1) {
		t.Errorf("unexpected photo with a GPS position off the track %v", p)
	}
	if n := e.storedImages(); n != 6 {
		t.Errorf("got %d stored images, want 6", n)
	}

	// Importing again skips the imported photos.
	resp, err := e.trips.ImportStravaPhotos(ctx, tripsv1.ImportStravaPhotosRequest_builder{ReportId: report.GetId()}.Build())
	if err != nil {
		t.Fatalf("ImportStravaPhotos: %v", err)
	}
	if len(resp.GetPhotos()) != 0 || resp.GetSkippedCount() != 3 || resp.GetFailedCount() != 2 {
		t.Errorf("got %d photos, %d skipped and %d failed, want 0, 3 and 2", len(resp.GetPhotos()), resp.GetSkippedCount(), resp.GetFailedCount())
	}
	got, err := e.trips.GetTripReport(ctx, tripsv1.GetTripReportRequest_builder{Id: report.GetId()}.Build())
	if err != nil || len(got.GetReport().GetPhotos()) != 3 {
		t.Fatalf("GetTripReport: got %v, %v", got, err)
	}

	// Reports without an activity have no photos on Strava, and other
	// users' reports can't be imported into.
	other, err := e.trips.CreateTripReport(ctx, tripsv1.CreateTripReportRequest_builder{Title: "Memories"}.Build())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.trips.ImportStravaPhotos(ctx, tripsv1.ImportStravaPhotosRequest_builder{ReportId: other.GetReport().GetId()}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)
	_, err = e.otherTrips.ImportStravaPhotos(ctx, tripsv1.ImportStravaPhotosRequest_builder{ReportId: report.GetId()}.Build())
	wantCode(t, err, connect.CodeNotFound)
}
