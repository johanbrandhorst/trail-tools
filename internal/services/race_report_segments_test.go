package services_test

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	usersv1 "github.com/johanbrandhorst/trail-tools/internal/gen/users/v1"
)

func TestRaceReportSegments(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	// A 10 km activity on the race day, climbing 500 m.
	e.createActivity()
	race, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
		Race: racesv1.Race_builder{Name: "Ridge 10K", Date: "2026-09-07", DistanceMeters: 10_000}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.raceReports.CreateRaceReport(ctx, racesv1.CreateRaceReportRequest_builder{RaceId: race.GetRace().GetId()}.Build())
	if err != nil {
		t.Fatalf("CreateRaceReport: %v", err)
	}
	reportID := created.GetReport().GetId()

	// The report copied the activity's track.
	got, err := e.raceReports.GetRaceReport(ctx, racesv1.GetRaceReportRequest_builder{Id: reportID, IncludeTrack: true}.Build())
	if err != nil {
		t.Fatalf("GetRaceReport: %v", err)
	}
	track := got.GetTrack()
	if !got.GetReport().GetHasTrack() || len(track) < 2 || len(got.GetReport().GetSegments()) != 0 {
		t.Fatalf("got a report with track %v and segments %v, want a track and no segments", len(track), got.GetReport().GetSegments())
	}
	length := track[len(track)-1].GetDistanceMeters()

	// Suggestions follow the climb, without text.
	suggested, err := e.raceReports.SuggestRaceReportSegments(ctx, racesv1.SuggestRaceReportSegmentsRequest_builder{ReportId: reportID}.Build())
	if err != nil {
		t.Fatalf("SuggestRaceReportSegments: %v", err)
	}
	if segs := suggested.GetSegments(); len(segs) == 0 || segs[0].GetTitle() == "" || segs[0].GetBody() != "" {
		t.Errorf("got suggestions %v, want named segments without text", segs)
	}

	// The runner comments on two stretches.
	set, err := e.raceReports.SetRaceReportSegments(ctx, racesv1.SetRaceReportSegmentsRequest_builder{
		ReportId: reportID,
		Segments: []*racesv1.RaceReportSegment{
			racesv1.RaceReportSegment_builder{Title: " The first climb ", Body: "Went out too fast.", StartDistanceMeters: 0, EndDistanceMeters: 4000}.Build(),
			racesv1.RaceReportSegment_builder{Title: "The ridge", Body: "Settled in.", StartDistanceMeters: 4000, EndDistanceMeters: length}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("SetRaceReportSegments: %v", err)
	}
	if segs := set.GetReport().GetSegments(); len(segs) != 2 || segs[0].GetTitle() != "The first climb" || segs[1].GetEndDistanceMeters() != length {
		t.Errorf("got segments %v", segs)
	}
	got, err = e.raceReports.GetRaceReport(ctx, racesv1.GetRaceReportRequest_builder{Id: reportID}.Build())
	if err != nil {
		t.Fatalf("GetRaceReport: %v", err)
	}
	if segs := got.GetReport().GetSegments(); len(segs) != 2 || segs[1].GetBody() != "Settled in." || len(got.GetTrack()) != 0 {
		t.Errorf("got segments %v and %d track points, want the 2 segments and no track", segs, len(got.GetTrack()))
	}

	// Segments must be within the track, and in order along it.
	for name, segs := range map[string][]*racesv1.RaceReportSegment{
		"past the end": {racesv1.RaceReportSegment_builder{StartDistanceMeters: 0, EndDistanceMeters: length + 500}.Build()},
		"backwards":    {racesv1.RaceReportSegment_builder{StartDistanceMeters: 3000, EndDistanceMeters: 2000}.Build()},
		"out of order": {
			racesv1.RaceReportSegment_builder{StartDistanceMeters: 5000, EndDistanceMeters: 6000}.Build(),
			racesv1.RaceReportSegment_builder{StartDistanceMeters: 1000, EndDistanceMeters: 2000}.Build(),
		},
	} {
		_, err := e.raceReports.SetRaceReportSegments(ctx, racesv1.SetRaceReportSegmentsRequest_builder{ReportId: reportID, Segments: segs}.Build())
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: got error %v, want invalid argument", name, err)
		}
	}

	// Other users can't set segments on the report.
	_, err = e.otherRaceReports.SetRaceReportSegments(ctx, racesv1.SetRaceReportSegmentsRequest_builder{ReportId: reportID}.Build())
	wantCode(t, err, connect.CodeNotFound)

	// The public report has the segments and the whole track.
	e.createProfile("jane-doe")
	if _, err := e.raceReports.PublishRaceReport(ctx, racesv1.PublishRaceReportRequest_builder{Id: reportID}.Build()); err != nil {
		t.Fatalf("PublishRaceReport: %v", err)
	}
	public, err := e.anonRaceReports.GetPublicRaceReport(ctx, racesv1.GetPublicRaceReportRequest_builder{Handle: "jane-doe", Slug: "ridge-10k"}.Build())
	if err != nil {
		t.Fatalf("GetPublicRaceReport: %v", err)
	}
	if len(public.GetReport().GetSegments()) != 2 || len(public.GetTrack()) != len(track) {
		t.Errorf("got %d segments and %d track points, want 2 and the whole track of %d", len(public.GetReport().GetSegments()), len(public.GetTrack()), len(track))
	}

	// A privacy zone around the middle hides the track there.
	if _, err := e.users.SetPrivacyZones(ctx, usersv1.SetPrivacyZonesRequest_builder{
		Zones: []*usersv1.PrivacyZone{usersv1.PrivacyZone_builder{Lat: 0.045, Lng: 0, RadiusMeters: 500}.Build()},
	}.Build()); err != nil {
		t.Fatalf("SetPrivacyZones: %v", err)
	}
	if public, err = e.anonRaceReports.GetPublicRaceReport(ctx, racesv1.GetPublicRaceReportRequest_builder{Handle: "jane-doe", Slug: "ridge-10k"}.Build()); err != nil {
		t.Fatalf("GetPublicRaceReport: %v", err)
	}
	if visible, breaks := public.GetTrack(), public.GetTrackBreaks(); len(visible) >= len(track) || len(breaks) != 1 {
		t.Errorf("got %d of %d track points with breaks %v, want the middle hidden", len(visible), len(track), breaks)
	}
}
