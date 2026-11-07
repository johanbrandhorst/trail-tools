package services_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestRaceReports(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	// A 10 km activity on the race day, an hour long at a steady pace.
	activityID := e.createActivity()
	race, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
		Race: racesv1.Race_builder{Name: "Ridge 10K", Date: "2026-09-07", DistanceMeters: 10_000, OnCalendar: true}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	raceID := race.GetRace().GetId()
	plan, err := e.racePlans.CreateRacePlan(ctx, racesv1.CreateRacePlanRequest_builder{RaceId: raceID}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.racePlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{
		Plan: racesv1.RacePlan_builder{
			Id:    plan.GetPlan().GetId(),
			Goals: racesv1.RaceGoals_builder{AFinishSeconds: 3500, BFinishSeconds: 3700, CFinishSeconds: 4000}.Build(),
			Checkpoints: []*racesv1.Checkpoint{
				racesv1.Checkpoint_builder{Name: "Halfway", DistanceMeters: 5000, PlannedSegmentSeconds: 1700}.Build(),
				racesv1.Checkpoint_builder{Name: "Finish", DistanceMeters: 10_000, PlannedSegmentSeconds: 1700}.Build(),
			},
		}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"goals", "checkpoints"}},
	}.Build()); err != nil {
		t.Fatalf("UpdateRacePlan: %v", err)
	}

	// The report picks the activity on the race date, its elapsed time is
	// the finish time, and it's compared with the plan.
	created, err := e.raceReports.CreateRaceReport(ctx, racesv1.CreateRaceReportRequest_builder{RaceId: raceID}.Build())
	if err != nil {
		t.Fatalf("CreateRaceReport: %v", err)
	}
	report := created.GetReport()
	if report.GetActivityId() != activityID || report.GetFinishSeconds() != 3600 || report.GetTitle() != "Ridge 10K" ||
		report.GetSlug() != "ridge-10k" || report.GetRaceDate() != "2026-09-07" || report.GetUrl() != "" {
		t.Errorf("unexpected report %v", report)
	}
	comparison := created.GetComparison()
	goals := comparison.GetGoals()
	if !comparison.GetHasPlan() || comparison.GetPlannedFinishSeconds() != 3400 || len(goals) != 3 ||
		goals[0].GetMet() || goals[0].GetDifferenceSeconds() != 100 || !goals[1].GetMet() || !goals[2].GetMet() {
		t.Errorf("unexpected comparison %v", comparison)
	}
	checkpoints := comparison.GetCheckpoints()
	if comparison.GetActualTimesSource() != racesv1.ActualTimesSource_ACTUAL_TIMES_SOURCE_ACTIVITY || len(checkpoints) != 2 ||
		checkpoints[0].GetActualArrivalSeconds() != 1800 || checkpoints[0].GetDifferenceSeconds() != 100 ||
		checkpoints[1].GetActualArrivalSeconds() != 3600 {
		t.Errorf("unexpected checkpoints %v", checkpoints)
	}

	// The story and result are edited.
	updated, err := e.raceReports.UpdateRaceReport(ctx, racesv1.UpdateRaceReportRequest_builder{
		Id:            report.GetId(),
		Placement:     new(" 3rd of 40 "),
		Summary:       new("A solid B goal."),
		Lessons:       new("Start slower."),
		FinishSeconds: new(int32(3590)),
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRaceReport: %v", err)
	}
	if r := updated.GetReport(); r.GetPlacement() != "3rd of 40" || r.GetSummary() != "A solid B goal." || r.GetLessons() != "Start slower." ||
		r.GetFinishSeconds() != 3590 || r.GetPreparation() != "" {
		t.Errorf("unexpected updated report %v", r)
	}
	_, err = e.raceReports.UpdateRaceReport(ctx, racesv1.UpdateRaceReportRequest_builder{Id: report.GetId(), Title: new("  ")}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	// Reports are private until published, which requires a profile.
	_, err = e.otherRaceReports.GetRaceReport(ctx, racesv1.GetRaceReportRequest_builder{Id: report.GetId()}.Build())
	wantCode(t, err, connect.CodeNotFound)
	_, err = e.raceReports.PublishRaceReport(ctx, racesv1.PublishRaceReportRequest_builder{Id: report.GetId()}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)
	e.createProfile("jane-doe")
	publicReq := racesv1.GetPublicRaceReportRequest_builder{Handle: "jane-doe", Slug: "ridge-10k"}.Build()
	_, err = e.anonRaceReports.GetPublicRaceReport(ctx, publicReq)
	wantCode(t, err, connect.CodeNotFound)
	published, err := e.raceReports.PublishRaceReport(ctx, racesv1.PublishRaceReportRequest_builder{Id: report.GetId()}.Build())
	if err != nil {
		t.Fatalf("PublishRaceReport: %v", err)
	}
	if r := published.GetReport(); !r.GetPublished() || r.GetUrl() != "https://trail.example/u/jane-doe/races/ridge-10k" {
		t.Errorf("unexpected published report %v", r)
	}
	public, err := e.anonRaceReports.GetPublicRaceReport(ctx, publicReq)
	if err != nil {
		t.Fatalf("GetPublicRaceReport: %v", err)
	}
	if public.GetAuthorName() != "Jane Doe" || public.GetReport().GetSummary() != "A solid B goal." || !public.GetComparison().GetHasPlan() {
		t.Errorf("unexpected public report %v", public)
	}
	// Anonymous viewers get the same comparison, its times from the report's
	// track rather than the activity's streams.
	checkpoints = public.GetComparison().GetCheckpoints()
	if public.GetComparison().GetActualTimesSource() != racesv1.ActualTimesSource_ACTUAL_TIMES_SOURCE_ACTIVITY || len(checkpoints) != 2 ||
		checkpoints[0].GetPlannedArrivalSeconds() != 1700 || checkpoints[0].GetActualArrivalSeconds() != 1800 ||
		checkpoints[1].GetActualArrivalSeconds() != 3600 || public.GetComparison().GetPlannedFinishSeconds() != 3400 {
		t.Errorf("unexpected public comparison %v", public.GetComparison())
	}
	list, err := e.anonRaceReports.ListPublicRaceReports(ctx, racesv1.ListPublicRaceReportsRequest_builder{Handle: "jane-doe"}.Build())
	if err != nil || len(list.GetReports()) != 1 {
		t.Fatalf("ListPublicRaceReports = %v, %v", list, err)
	}
	// The public page has the report's title.
	resp, body := e.get("/u/jane-doe/races/ridge-10k", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<title>Ridge 10K · Jane Doe</title>") {
		t.Errorf("got public page %d %q", resp.StatusCode, body)
	}

	// Unpublishing makes it private again, and deleting removes it.
	if _, err := e.raceReports.UnpublishRaceReport(ctx, racesv1.UnpublishRaceReportRequest_builder{Id: report.GetId()}.Build()); err != nil {
		t.Fatalf("UnpublishRaceReport: %v", err)
	}
	_, err = e.anonRaceReports.GetPublicRaceReport(ctx, publicReq)
	wantCode(t, err, connect.CodeNotFound)
	// Marking the report as a DNF moves the finish time to when the runner
	// stopped, at the activity's distance unless set, and meets no goal.
	dnf, err := e.raceReports.UpdateRaceReport(ctx, racesv1.UpdateRaceReportRequest_builder{
		Id:             report.GetId(),
		DidNotFinish:   new(true),
		StopCheckpoint: new(" Halfway "),
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRaceReport: %v", err)
	}
	if r := dnf.GetReport(); !r.GetDidNotFinish() || r.GetFinishSeconds() != 0 || r.GetStopSeconds() != 3590 ||
		r.GetStopDistanceMeters() != 10_000 || r.GetStopCheckpoint() != "Halfway" {
		t.Errorf("unexpected DNF report %v", r)
	}
	for _, goal := range dnf.GetComparison().GetGoals() {
		if goal.GetMet() {
			t.Errorf("goal %v is met by a DNF", goal)
		}
	}
	// With the stop halfway, the finish isn't reached, and the activity isn't
	// scaled to the 5 km up to the stop.
	dnf, err = e.raceReports.UpdateRaceReport(ctx, racesv1.UpdateRaceReportRequest_builder{
		Id:                 report.GetId(),
		StopDistanceMeters: new(5000.0),
		StopSeconds:        new(int32(1900)),
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRaceReport: %v", err)
	}
	checkpoints = dnf.GetComparison().GetCheckpoints()
	if r := dnf.GetReport(); r.GetStopDistanceMeters() != 5000 || r.GetStopSeconds() != 1900 || r.GetStopCheckpoint() != "Halfway" ||
		checkpoints[0].GetActualArrivalSeconds() != 1800 || checkpoints[0].GetNotReached() ||
		!checkpoints[1].GetNotReached() || checkpoints[1].HasActualArrivalSeconds() {
		t.Errorf("unexpected DNF report %v, comparison %v", r, dnf.GetComparison())
	}
	// A DNF has no finish time, and stops within the race.
	_, err = e.raceReports.UpdateRaceReport(ctx, racesv1.UpdateRaceReportRequest_builder{Id: report.GetId(), FinishSeconds: new(int32(3600))}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = e.raceReports.UpdateRaceReport(ctx, racesv1.UpdateRaceReportRequest_builder{Id: report.GetId(), StopDistanceMeters: new(10_001.0)}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
	// Marking it as finished clears the stop, which can't then be set.
	finished, err := e.raceReports.UpdateRaceReport(ctx, racesv1.UpdateRaceReportRequest_builder{
		Id:            report.GetId(),
		DidNotFinish:  new(false),
		FinishSeconds: new(int32(3590)),
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRaceReport: %v", err)
	}
	if r := finished.GetReport(); r.GetDidNotFinish() || r.GetFinishSeconds() != 3590 || r.GetStopSeconds() != 0 ||
		r.GetStopDistanceMeters() != 0 || r.GetStopCheckpoint() != "" || !finished.GetComparison().GetGoals()[1].GetMet() {
		t.Errorf("unexpected finished report %v", finished)
	}
	_, err = e.raceReports.UpdateRaceReport(ctx, racesv1.UpdateRaceReportRequest_builder{Id: report.GetId(), StopSeconds: new(int32(1))}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	if _, err := e.raceReports.DeleteRaceReport(ctx, racesv1.DeleteRaceReportRequest_builder{Id: report.GetId()}.Build()); err != nil {
		t.Fatalf("DeleteRaceReport: %v", err)
	}
	reports, err := e.raceReports.ListRaceReports(ctx, racesv1.ListRaceReportsRequest_builder{}.Build())
	if err != nil || len(reports.GetReports()) != 0 {
		t.Errorf("ListRaceReports = %v, %v", reports, err)
	}
}
