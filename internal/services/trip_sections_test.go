package services_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	tripsv1 "github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// createHillReport creates a report from an activity that climbs from
// 500 m to 1,300 m in 5 km and goes back down, and returns its ID and its
// first section.
func (e *env) createHillReport() (string, *tripsv1.TripSection) {
	e.t.Helper()
	altitudes := make([]float64, 101)
	for i := range altitudes {
		altitudes[i] = 1300 - 16*float64(max(50-i, i-50))
	}
	activityID := e.createActivityWithAltitudes(altitudes)
	created, err := e.trips.CreateTripReport(context.Background(), tripsv1.CreateTripReportRequest_builder{ActivityId: activityID}.Build())
	if err != nil {
		e.t.Fatal(err)
	}
	return created.GetReport().GetId(), created.GetReport().GetSections()[0]
}

// uploadPhoto uploads a photo to a report, taken at the time if it's set,
// and returns its ID.
func (e *env) uploadPhoto(reportID string, takenTime *timestamppb.Timestamp) string {
	e.t.Helper()
	resp, err := e.trips.UploadTripPhoto(context.Background(), tripsv1.UploadTripPhotoRequest_builder{
		ReportId:  reportID,
		Data:      testJPEG(e.t, 300, 200),
		TakenTime: takenTime,
	}.Build())
	if err != nil {
		e.t.Fatalf("UploadTripPhoto: %v", err)
	}
	return resp.GetPhoto().GetId()
}

func TestTripSections(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	e.createProfile("jane-doe")
	reportID, intro := e.createHillReport()
	// At 2 and 8 km, and one that isn't on the track.
	at8km := e.uploadPhoto(reportID, timestamppb.New(tripStart.Add(48*time.Minute)))
	at2km := e.uploadPhoto(reportID, timestamppb.New(tripStart.Add(12*time.Minute)))
	unplaced := e.uploadPhoto(reportID, nil)

	// An introduction, the climb and the descent.
	set := func(sections ...*tripsv1.TripSection) (*tripsv1.TripReport, error) {
		resp, err := e.trips.SetTripSections(ctx, tripsv1.SetTripSectionsRequest_builder{ReportId: reportID, Sections: sections}.Build())
		return resp.GetReport(), err
	}
	report, err := set(
		tripsv1.TripSection_builder{Id: intro.GetId(), Body: "Felt good"}.Build(),
		tripsv1.TripSection_builder{
			Title: " Up ", Body: "Up we go.", StartDistanceMeters: new(0.0), EndDistanceMeters: new(5000.0),
			PhotoIds: []string{unplaced, at2km},
		}.Build(),
		tripsv1.TripSection_builder{
			Title: "Down", Body: "And down again.", StartDistanceMeters: new(5000.0), EndDistanceMeters: new(10000.0),
			PhotoIds: []string{at8km},
		}.Build(),
	)
	if err != nil {
		t.Fatalf("SetTripSections: %v", err)
	}
	got := report.GetSections()
	if len(got) != 3 || got[0].GetId() != intro.GetId() || got[0].GetPhotoIds() != nil || got[1].GetTitle() != "Up" ||
		got[1].GetBody() != "Up we go." || got[1].GetEndDistanceMeters() != 5000 || got[2].GetBody() != "And down again." || got[1].GetId() == "" {
		t.Fatalf("unexpected sections %v", got)
	}
	if ids := photoIDs(report); ids != unplaced+","+at2km+","+at8km {
		t.Errorf("got photos %s, want them in the order of the story", ids)
	}
	if report.GetExcerpt() != "Felt good" {
		t.Errorf("got excerpt %q, want the introduction", report.GetExcerpt())
	}

	// New photos go in the section about where they were taken, or else the
	// last one.
	at3km := e.uploadPhoto(reportID, timestamppb.New(tripStart.Add(18*time.Minute)))
	another := e.uploadPhoto(reportID, nil)
	resp, err := e.trips.GetTripReport(ctx, tripsv1.GetTripReportRequest_builder{Id: reportID}.Build())
	if err != nil {
		t.Fatal(err)
	}
	got = resp.GetReport().GetSections()
	if ids := strings.Join(got[1].GetPhotoIds(), ","); ids != unplaced+","+at2km+","+at3km {
		t.Errorf("got photos %s in the climb, want the new one at 3 km last", ids)
	}
	if ids := strings.Join(got[2].GetPhotoIds(), ","); ids != at8km+","+another {
		t.Errorf("got photos %s in the descent, want the new one without a place last", ids)
	}

	// Removing a section moves its photos to the first section, unless
	// they're listed elsewhere.
	got[0].SetPhotoIds([]string{at3km})
	report, err = set(got[0], got[2])
	if err != nil {
		t.Fatalf("SetTripSections: %v", err)
	}
	if s := report.GetSections(); len(s) != 2 || strings.Join(s[0].GetPhotoIds(), ",") != at3km+","+unplaced+","+at2km {
		t.Errorf("got sections %v, want the climb's photos in the introduction", s)
	}

	// Lists show an excerpt, without the sections.
	list, err := e.trips.ListTripReports(ctx, tripsv1.ListTripReportsRequest_builder{}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if r := list.GetReports()[0]; r.GetExcerpt() != "Felt good" || len(r.GetSections()) != 0 {
		t.Errorf("unexpected listed report %v", r)
	}
	// Public pages include the sections.
	public, err := e.trips.GetPublicTripReport(ctx, tripsv1.GetPublicTripReportRequest_builder{Handle: "jane-doe", Slug: "ridge-run"}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if s := public.GetReport().GetSections(); len(s) != 2 || s[1].GetBody() != "And down again." {
		t.Errorf("got public sections %v", s)
	}

	standalone, err := e.trips.CreateTripReport(ctx, tripsv1.CreateTripReportRequest_builder{Title: "No track"}.Build())
	if err != nil {
		t.Fatal(err)
	}
	otherSection := standalone.GetReport().GetSections()[0].GetId()
	for name, sections := range map[string][]*tripsv1.TripSection{
		"none":             nil,
		"start only":       {tripsv1.TripSection_builder{StartDistanceMeters: new(0.0)}.Build()},
		"end before start": {tripsv1.TripSection_builder{StartDistanceMeters: new(500.0), EndDistanceMeters: new(500.0)}.Build()},
		"negative":         {tripsv1.TripSection_builder{StartDistanceMeters: new(-1.0), EndDistanceMeters: new(500.0)}.Build()},
		"long title":       {tripsv1.TripSection_builder{Title: strings.Repeat("a", 201)}.Build()},
		"other report":     {tripsv1.TripSection_builder{Id: otherSection}.Build()},
		"twice":            {got[0], got[0]},
		"photo twice":      {tripsv1.TripSection_builder{PhotoIds: []string{at2km, at2km}}.Build()},
		"bad photo":        {tripsv1.TripSection_builder{PhotoIds: []string{otherSection}}.Build()},
	} {
		if _, err := set(sections...); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("SetTripSections with %s: got %v, want invalid argument", name, err)
		}
	}
	_, err = e.otherTrips.SetTripSections(ctx, tripsv1.SetTripSectionsRequest_builder{
		ReportId: reportID,
		Sections: []*tripsv1.TripSection{tripsv1.TripSection_builder{Body: "Mine"}.Build()},
	}.Build())
	wantCode(t, err, connect.CodeNotFound)
}

func TestSuggestTripSections(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	reportID, _ := e.createHillReport()
	at8km := e.uploadPhoto(reportID, timestamppb.New(tripStart.Add(48*time.Minute)))
	at2km := e.uploadPhoto(reportID, timestamppb.New(tripStart.Add(12*time.Minute)))
	unplaced := e.uploadPhoto(reportID, nil)

	// Suggestions split the track at the top, with the photos taken along
	// each part in order.
	suggested, err := e.trips.SuggestTripSections(ctx, tripsv1.SuggestTripSectionsRequest_builder{ReportId: reportID}.Build())
	if err != nil {
		t.Fatalf("SuggestTripSections: %v", err)
	}
	sections := suggested.GetSections()
	if len(sections) != 2 {
		t.Fatalf("got %d suggested sections, want 2: %v", len(sections), sections)
	}
	if s := sections[0]; s.GetTitle() != "Climb to 1,300 m" || s.GetStartDistanceMeters() != 0 ||
		!approx(s.GetEndDistanceMeters(), 5000, 300) || strings.Join(s.GetPhotoIds(), ",") != unplaced+","+at2km {
		t.Errorf("unexpected first suggested section %v", s)
	}
	if s := sections[1]; s.GetTitle() != "Final descent to 500 m" || s.GetStartDistanceMeters() != sections[0].GetEndDistanceMeters() ||
		s.GetEndDistanceMeters() != 10000 || strings.Join(s.GetPhotoIds(), ",") != at8km {
		t.Errorf("unexpected second suggested section %v", s)
	}
	// Nothing is saved.
	resp, err := e.trips.GetTripReport(ctx, tripsv1.GetTripReportRequest_builder{Id: reportID}.Build())
	if err != nil || len(resp.GetReport().GetSections()) != 1 {
		t.Errorf("GetTripReport = %v, %v, want the one section", resp, err)
	}

	_, err = e.otherTrips.SuggestTripSections(ctx, tripsv1.SuggestTripSectionsRequest_builder{ReportId: reportID}.Build())
	wantCode(t, err, connect.CodeNotFound)
	standalone, err := e.trips.CreateTripReport(ctx, tripsv1.CreateTripReportRequest_builder{Title: "No track"}.Build())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.trips.SuggestTripSections(ctx, tripsv1.SuggestTripSectionsRequest_builder{ReportId: standalone.GetReport().GetId()}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)
}

// photoIDs returns the IDs of a report's photos, separated by commas.
func photoIDs(report *tripsv1.TripReport) string {
	ids := make([]string, len(report.GetPhotos()))
	for i, p := range report.GetPhotos() {
		ids[i] = p.GetId()
	}
	return strings.Join(ids, ",")
}

func TestTripPhotosInTripOrder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	// An out-and-back of an hour: 5 km north and back, every 36 seconds.
	id, err := e.activities.UpsertActivity(ctx, e.userID, &activities.ActivitySummary{
		StravaID: 77, Name: "Out and back", SportType: "TrailRun", StartTime: tripStart,
		StartDateLocal: time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC), Timezone: "Europe/Zurich",
	})
	if err != nil {
		t.Fatal(err)
	}
	streams := &activitiesv1.ActivityStreams{}
	for i := range 101 {
		streams.SetTimeSeconds(append(streams.GetTimeSeconds(), int32(i*36)))
		streams.SetDistanceMeters(append(streams.GetDistanceMeters(), float64(i)*100))
		streams.SetLat(append(streams.GetLat(), float64(min(i, 100-i))*0.0009))
		streams.SetLng(append(streams.GetLng(), 0))
	}
	e.storeActivityDetail(id, nil, streams, &geo.Bounds{MaxLat: 0.045})
	created, err := e.trips.CreateTripReport(ctx, tripsv1.CreateTripReportRequest_builder{ActivityId: store.UUIDToString(id)}.Build())
	if err != nil {
		t.Fatal(err)
	}
	reportID := created.GetReport().GetId()
	upload := func(minutes int, location *geov1.LatLng) string {
		t.Helper()
		b := tripsv1.UploadTripPhotoRequest_builder{ReportId: reportID, Data: testJPEG(t, 300, 200), Location: location}
		if minutes >= 0 {
			b.TakenTime = timestamppb.New(tripStart.Add(time.Duration(minutes) * time.Minute))
		}
		resp, err := e.trips.UploadTripPhoto(ctx, b.Build())
		if err != nil {
			t.Fatalf("UploadTripPhoto: %v", err)
		}
		return resp.GetPhoto().GetId()
	}
	// Uploaded out of order. The photos on the way out and back were taken
	// at the same place, 1.2 km from the start, and are ordered by time.
	back := upload(48, geov1.LatLng_builder{Lat: 0.0108}.Build())
	out := upload(12, geov1.LatLng_builder{Lat: 0.0108}.Build())
	turn := upload(30, nil)
	// Without a time, a photo is ordered by how far along the track it is,
	// here 200 m.
	start := upload(-1, geov1.LatLng_builder{Lat: 0.0018}.Build())

	got, err := e.trips.GetTripReport(ctx, tripsv1.GetTripReportRequest_builder{Id: reportID}.Build())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{start, out, turn, back}
	if ids := got.GetReport().GetSections()[0].GetPhotoIds(); !slices.Equal(ids, want) {
		t.Errorf("got photos %v, want %v: in the order they were taken", ids, want)
	}
}
