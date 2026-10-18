package services_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
	"github.com/johanbrandhorst/trail-tools/internal/livetrack"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRaceDay(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// A 22km course north along a meridian, with a checkpoint half way.
	course, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Name:      "Course",
		Waypoints: routesv1.Waypoints_builder{Waypoints: latLngs(0, 0, 0.1, 0, 0.2, 0)}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	race, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
		Race: racesv1.Race_builder{Name: "Race", Date: "2026-10-11", DistanceMeters: 22239, CourseRouteId: course.GetRoute().GetId(), OnCalendar: true}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.racePlans.CreateRacePlan(ctx, racesv1.CreateRacePlanRequest_builder{RaceId: race.GetRace().GetId()}.Build())
	if err != nil {
		t.Fatal(err)
	}
	planID := created.GetPlan().GetId()
	if _, err := e.racePlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{
		Plan: racesv1.RacePlan_builder{
			Id: planID,
			Checkpoints: []*racesv1.Checkpoint{
				racesv1.Checkpoint_builder{Name: "Half", DistanceMeters: 11119, PlannedSegmentSeconds: 3600, PlannedStopSeconds: 300}.Build(),
				racesv1.Checkpoint_builder{Name: "Finish", DistanceMeters: 22239, PlannedSegmentSeconds: 3600}.Build(),
			},
		}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"checkpoints"}},
	}.Build()); err != nil {
		t.Fatal(err)
	}
	shared, err := e.racePlans.ShareRacePlan(ctx, racesv1.ShareRacePlanRequest_builder{Id: planID}.Build())
	if err != nil {
		t.Fatal(err)
	}
	token := shared.GetShareUrl()[len("https://trail.example/share/race-plans#"):]

	// Crew record the start through the share link, without an account.
	start := time.Now().Add(-90 * time.Minute).Truncate(time.Second)
	_, err = e.anonPlans.UpdateRaceDay(ctx, racesv1.UpdateRaceDayRequest_builder{
		ShareToken: new(token),
		RaceDay:    racesv1.RaceDay_builder{StartTime: timestamppb.New(start)}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"start_time"}},
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRaceDay with share token: %v", err)
	}
	// The plan ID can't be used without authentication.
	_, err = e.anonPlans.GetRaceDayStatus(ctx, racesv1.GetRaceDayStatusRequest_builder{PlanId: new(planID)}.Build())
	wantCode(t, err, connect.CodeUnauthenticated)

	// Invalid LiveTrack links and checkpoints are rejected.
	_, err = e.anonPlans.UpdateRaceDay(ctx, racesv1.UpdateRaceDayRequest_builder{
		ShareToken: new(token),
		RaceDay:    racesv1.RaceDay_builder{LivetrackUrl: "https://example.com/live"}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"livetrack_url"}},
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = e.anonPlans.UpdateRaceDay(ctx, racesv1.UpdateRaceDayRequest_builder{
		ShareToken: new(token),
		RaceDay: racesv1.RaceDay_builder{Splits: []*racesv1.CheckpointSplit{
			racesv1.CheckpointSplit_builder{CheckpointIndex: 5, ArrivalTime: timestamppb.Now()}.Build(),
		}}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"splits"}},
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	// Link LiveTrack: 15km after 1:25 on the watch, the course distance
	// comes from matching the position to the course.
	e.liveTrack.track = livetrack.Track{
		Start: start,
		Latest: &livetrack.Point{
			Time: start.Add(85 * time.Minute), Lat: 0.1349, Lng: 0, DistanceMeters: 15300, ElapsedSeconds: 85 * 60,
		},
	}
	updated, err := e.anonPlans.UpdateRaceDay(ctx, racesv1.UpdateRaceDayRequest_builder{
		ShareToken: new(token),
		RaceDay: racesv1.RaceDay_builder{
			LivetrackUrl: "https://livetrack.garmin.com/session/aaaaaaaa-bbbb/token/TOKEN",
			Splits: []*racesv1.CheckpointSplit{
				racesv1.CheckpointSplit_builder{CheckpointIndex: 0, ArrivalTime: timestamppb.New(start.Add(55 * time.Minute)), DepartureTime: timestamppb.New(start.Add(60 * time.Minute))}.Build(),
			},
		}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"livetrack_url", "splits"}},
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRaceDay: %v", err)
	}
	status := updated.GetStatus()
	if status.GetLivetrackError() != "" || status.GetEstimateSource() != racesv1.EstimateSource_ESTIMATE_SOURCE_LIVETRACK {
		t.Fatalf("unexpected status %v", status)
	}
	if got := status.GetLivetrack().GetCourseDistanceMeters(); !approx(got, 15012, 120) {
		t.Errorf("got course distance %v, want ~15km", got)
	}
	// The runner is ahead of the plan: 1:20 moving for 15km planned at 1:21.
	if f := status.GetPaceFactor(); f >= 1 || f < 0.9 {
		t.Errorf("unexpected pace factor %v", f)
	}
	cps := status.GetCheckpoints()
	if cps[0].GetStatus() != racesv1.CheckpointStatus_CHECKPOINT_STATUS_DEPARTED || !cps[0].GetArrivalRecorded() ||
		cps[1].GetStatus() != racesv1.CheckpointStatus_CHECKPOINT_STATUS_UPCOMING || cps[1].GetDelaySeconds() >= 0 {
		t.Errorf("unexpected checkpoints %v", cps)
	}

	if status.GetStartTimeSource() != racesv1.StartTimeSource_START_TIME_SOURCE_RECORDED || !status.HasEstimatedLocation() {
		t.Errorf("unexpected start source or location %v", status)
	}

	// Crew record the finish arrival for one checkpoint without touching
	// the others, then clear it.
	finish := start.Add(80 * time.Minute)
	split, err := e.anonPlans.SetCheckpointSplit(ctx, racesv1.SetCheckpointSplitRequest_builder{
		ShareToken: new(token),
		Split:      racesv1.CheckpointSplit_builder{CheckpointIndex: 1, ArrivalTime: timestamppb.New(finish)}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("SetCheckpointSplit: %v", err)
	}
	if splits := split.GetStatus().GetRaceDay().GetSplits(); len(splits) != 2 || !split.GetStatus().GetCheckpoints()[1].GetArrivalRecorded() {
		t.Errorf("unexpected splits %v", splits)
	}
	cleared, err := e.anonPlans.SetCheckpointSplit(ctx, racesv1.SetCheckpointSplitRequest_builder{
		ShareToken: new(token),
		Split:      racesv1.CheckpointSplit_builder{CheckpointIndex: 1}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("SetCheckpointSplit: %v", err)
	}
	if splits := cleared.GetStatus().GetRaceDay().GetSplits(); len(splits) != 1 || splits[0].GetCheckpointIndex() != 0 {
		t.Errorf("unexpected splits after clearing %v", splits)
	}

	// The owner sees the same status, and the race day in the plan.
	owner, err := e.racePlans.GetRaceDayStatus(ctx, racesv1.GetRaceDayStatusRequest_builder{PlanId: new(planID)}.Build())
	if err != nil {
		t.Fatalf("GetRaceDayStatus: %v", err)
	}
	if !owner.GetStatus().GetEstimatedFinishTime().AsTime().Equal(status.GetEstimatedFinishTime().AsTime()) {
		t.Errorf("owner and crew status differ")
	}
	plan, err := e.racePlans.GetRacePlan(ctx, racesv1.GetRacePlanRequest_builder{Id: planID}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if plan.GetPlan().GetRaceDay().GetLivetrackUrl() == "" || len(plan.GetPlan().GetRaceDay().GetSplits()) != 1 {
		t.Errorf("unexpected race day in plan %v", plan.GetPlan().GetRaceDay())
	}

	// A broken LiveTrack link is reported without failing the status.
	_, err = e.racePlans.UpdateRaceDay(ctx, racesv1.UpdateRaceDayRequest_builder{
		PlanId:     new(planID),
		RaceDay:    racesv1.RaceDay_builder{LivetrackUrl: "https://livetrack.garmin.com/session/aaaaaaaa-bbbb/token/WRONG"}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"livetrack_url"}},
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	broken, err := e.anonPlans.GetRaceDayStatus(ctx, racesv1.GetRaceDayStatusRequest_builder{ShareToken: new(token)}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if broken.GetStatus().GetLivetrackError() == "" || broken.GetStatus().GetEstimateSource() != racesv1.EstimateSource_ESTIMATE_SOURCE_CHECKPOINT {
		t.Errorf("unexpected status with a broken link %v", broken.GetStatus())
	}
}

func TestRaceDaySplits(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	race, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
		Race: racesv1.Race_builder{Name: "Race", Date: "2026-10-11", DistanceMeters: 40000, OnCalendar: true}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.racePlans.CreateRacePlan(ctx, racesv1.CreateRacePlanRequest_builder{RaceId: race.GetRace().GetId()}.Build())
	if err != nil {
		t.Fatal(err)
	}
	planID := created.GetPlan().GetId()
	setCheckpoints := func(names ...string) {
		t.Helper()
		var checkpoints []*racesv1.Checkpoint
		for i, name := range names {
			checkpoints = append(checkpoints, racesv1.Checkpoint_builder{Name: name, DistanceMeters: float64(i+1) * 10000, PlannedSegmentSeconds: 3600}.Build())
		}
		if _, err := e.racePlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{
			Plan:       racesv1.RacePlan_builder{Id: planID, Checkpoints: checkpoints}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"checkpoints"}},
		}.Build()); err != nil {
			t.Fatal(err)
		}
	}
	splits := func() map[int32]*racesv1.CheckpointSplit {
		t.Helper()
		resp, err := e.racePlans.GetRaceDayStatus(ctx, racesv1.GetRaceDayStatusRequest_builder{PlanId: new(planID)}.Build())
		if err != nil {
			t.Fatal(err)
		}
		result := map[int32]*racesv1.CheckpointSplit{}
		for _, split := range resp.GetStatus().GetRaceDay().GetSplits() {
			result[split.GetCheckpointIndex()] = split
		}
		return result
	}
	setCheckpoints("CP1", "CP2", "CP3", "Finish")

	// Crew at different checkpoints record times at the same time.
	start := time.Now().Add(-4 * time.Hour).Truncate(time.Second)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range 4 {
		wg.Go(func() {
			_, errs[i] = e.racePlans.SetCheckpointSplit(ctx, racesv1.SetCheckpointSplitRequest_builder{
				PlanId: new(planID),
				Split: racesv1.CheckpointSplit_builder{
					CheckpointIndex: int32(i),
					ArrivalTime:     timestamppb.New(start.Add(time.Duration(i+1) * time.Hour)),
				}.Build(),
			}.Build())
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("SetCheckpointSplit: %v", err)
		}
	}
	if got := splits(); len(got) != 4 || !got[2].GetArrivalTime().AsTime().Equal(start.Add(3*time.Hour)) || got[2].HasDepartureTime() {
		t.Errorf("unexpected splits after concurrent updates %v", got)
	}

	// Updating the start time leaves the splits alone.
	if _, err := e.racePlans.UpdateRaceDay(ctx, racesv1.UpdateRaceDayRequest_builder{
		PlanId:     new(planID),
		RaceDay:    racesv1.RaceDay_builder{StartTime: timestamppb.New(start)}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"start_time"}},
	}.Build()); err != nil {
		t.Fatal(err)
	}
	if got := splits(); len(got) != 4 {
		t.Errorf("updating the start time changed the splits %v", got)
	}

	// Editing the checkpoints keeps the splits with their checkpoints, by ID:
	// removing CP2 drops its split, and inserting a checkpoint before CP1
	// moves the others along with their splits.
	plan, err := e.racePlans.GetRacePlan(ctx, racesv1.GetRacePlanRequest_builder{Id: planID}.Build())
	if err != nil {
		t.Fatal(err)
	}
	cp := plan.GetPlan().GetCheckpoints()
	cp[0].SetName("CP1 renamed")
	updateCheckpoints := func(checkpoints ...*racesv1.Checkpoint) {
		t.Helper()
		if _, err := e.racePlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{
			Plan:       racesv1.RacePlan_builder{Id: planID, Checkpoints: checkpoints}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"checkpoints"}},
		}.Build()); err != nil {
			t.Fatal(err)
		}
	}
	extra := racesv1.Checkpoint_builder{Name: "Extra", DistanceMeters: 5000, PlannedSegmentSeconds: 1800}.Build()
	updateCheckpoints(extra, cp[0], cp[2], cp[3])
	arrivals := func() []time.Duration {
		t.Helper()
		got := splits()
		arrivals := make([]time.Duration, 4)
		for i, split := range got {
			arrivals[i] = split.GetArrivalTime().AsTime().Sub(start)
		}
		if len(got) > 4 {
			t.Errorf("got %d splits, want at most 4", len(got))
		}
		return arrivals
	}
	if got, want := arrivals(), []time.Duration{0, time.Hour, 3 * time.Hour, 4 * time.Hour}; !slices.Equal(got, want) {
		t.Errorf("got arrivals %v after inserting and removing checkpoints, want %v", got, want)
	}
	plan, err = e.racePlans.GetRacePlan(ctx, racesv1.GetRacePlanRequest_builder{Id: planID}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if rd := plan.GetPlan().GetRaceDay(); !rd.GetStartTime().AsTime().Equal(start) || len(rd.GetSplits()) != 3 {
		t.Errorf("unexpected race day in plan %v", rd)
	}
	var names []string
	for _, c := range plan.GetPlan().GetCheckpoints() {
		names = append(names, c.GetName())
	}
	if !slices.Equal(names, []string{"Extra", "CP1 renamed", "CP3", "Finish"}) {
		t.Errorf("got checkpoints %v", names)
	}

	// Swapping checkpoints swaps their splits too.
	cp = plan.GetPlan().GetCheckpoints()
	updateCheckpoints(cp[2], cp[0], cp[1], cp[3])
	if got, want := arrivals(), []time.Duration{3 * time.Hour, 0, time.Hour, 4 * time.Hour}; !slices.Equal(got, want) {
		t.Errorf("got arrivals %v after reordering checkpoints, want %v", got, want)
	}

	// A checkpoint without an ID, or with another plan's, is new.
	other, err := e.racePlans.CreateRacePlan(ctx, racesv1.CreateRacePlanRequest_builder{RaceId: race.GetRace().GetId()}.Build())
	if err != nil {
		t.Fatal(err)
	}
	copied, err := e.racePlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{
		Plan:       racesv1.RacePlan_builder{Id: other.GetPlan().GetId(), Checkpoints: cp}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"checkpoints"}},
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRacePlan with another plan's checkpoints: %v", err)
	}
	for i, c := range copied.GetPlan().GetCheckpoints() {
		if c.GetId() == "" || c.GetId() == cp[i].GetId() {
			t.Errorf("checkpoint %d copied from another plan has ID %q", i, c.GetId())
		}
	}
	if got, want := arrivals(), []time.Duration{3 * time.Hour, 0, time.Hour, 4 * time.Hour}; !slices.Equal(got, want) {
		t.Errorf("got arrivals %v after copying the checkpoints to another plan, want %v", got, want)
	}
	_, err = e.racePlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{
		Plan: racesv1.RacePlan_builder{Id: planID, Checkpoints: []*racesv1.Checkpoint{
			racesv1.Checkpoint_builder{Id: "not-an-id", Name: "Finish"}.Build(),
		}}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"checkpoints"}},
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	// Removing checkpoints keeps the splits of the remaining ones.
	updateCheckpoints(cp[2], cp[3])
	if got, want := arrivals(), []time.Duration{3 * time.Hour, 4 * time.Hour, 0, 0}; !slices.Equal(got, want) {
		t.Errorf("got arrivals %v after removing checkpoints, want %v", got, want)
	}

	// Splits for checkpoints the plan doesn't have are rejected.
	_, err = e.racePlans.SetCheckpointSplit(ctx, racesv1.SetCheckpointSplitRequest_builder{
		PlanId: new(planID),
		Split:  racesv1.CheckpointSplit_builder{CheckpointIndex: 2, ArrivalTime: timestamppb.Now()}.Build(),
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	// The finish has no departure.
	_, err = e.racePlans.SetCheckpointSplit(ctx, racesv1.SetCheckpointSplitRequest_builder{
		PlanId: new(planID),
		Split:  racesv1.CheckpointSplit_builder{CheckpointIndex: 1, DepartureTime: timestamppb.Now()}.Build(),
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	// Replacing the splits removes the ones not given.
	if _, err := e.racePlans.UpdateRaceDay(ctx, racesv1.UpdateRaceDayRequest_builder{
		PlanId: new(planID),
		RaceDay: racesv1.RaceDay_builder{Splits: []*racesv1.CheckpointSplit{
			racesv1.CheckpointSplit_builder{CheckpointIndex: 0, DepartureTime: timestamppb.New(start.Add(5 * time.Hour))}.Build(),
		}}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"splits"}},
	}.Build()); err != nil {
		t.Fatal(err)
	}
	if got := splits(); len(got) != 1 || got[0].HasArrivalTime() || !got[0].GetDepartureTime().AsTime().Equal(start.Add(5*time.Hour)) {
		t.Errorf("unexpected splits after replacing them %v", got)
	}
}
