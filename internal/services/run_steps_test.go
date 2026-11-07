package services_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/bs-tools/trailrunn.ing/internal/garmin"
	garminv1 "github.com/bs-tools/trailrunn.ing/internal/gen/garmin/v1"
	lactatev1 "github.com/bs-tools/trailrunn.ing/internal/gen/lactate/v1"
	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
)

func TestStructuredRun(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	day := func(days int) string { return today.AddDate(0, 0, days).Format(time.DateOnly) }
	plan, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{Name: "Build", StartDate: day(0), EndDate: day(30)}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	step := func(kind plansv1.RunStepKind, seconds int32) *plansv1.RunStep {
		return plansv1.RunStep_builder{Kind: kind, DurationSeconds: seconds}.Build()
	}
	hard := step(plansv1.RunStepKind_RUN_STEP_KIND_RUN, 180)
	hard.SetHeartRate(plansv1.HeartRateTarget_builder{
		Reference: plansv1.HeartRateReference_HEART_RATE_REFERENCE_ANAEROBIC_THRESHOLD, LowBpm: -5, HighBpm: 3,
	}.Build())
	hard.SetNotes("Uphill")
	easy := step(plansv1.RunStepKind_RUN_STEP_KIND_RECOVERY, 120)
	easy.SetPace(plansv1.PaceTarget_builder{FastSecondsPerKilometer: 330, SlowSecondsPerKilometer: 420}.Build())
	blocks := []*plansv1.RunBlock{
		plansv1.RunBlock_builder{Steps: []*plansv1.RunStep{step(plansv1.RunStepKind_RUN_STEP_KIND_WARM_UP, 900)}}.Build(),
		plansv1.RunBlock_builder{Repeats: 6, Steps: []*plansv1.RunStep{hard, easy}}.Build(),
		plansv1.RunBlock_builder{Steps: []*plansv1.RunStep{step(plansv1.RunStepKind_RUN_STEP_KIND_COOL_DOWN, 0)}}.Build(),
	}
	created, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
		PlanId: plan.GetPlan().GetId(),
		Workouts: []*plansv1.Workout{plansv1.Workout_builder{Date: day(2), Title: "Hill repeats", Run: plansv1.RunWorkout_builder{
			Intensity: plansv1.RunIntensity_RUN_INTENSITY_HARD, Blocks: blocks,
		}.Build()}.Build()},
	}.Build())
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	got, err := e.plans.GetWorkout(ctx, plansv1.GetWorkoutRequest_builder{Id: created.GetWorkouts()[0].GetId()}.Build())
	if err != nil {
		t.Fatalf("GetWorkout: %v", err)
	}
	gotBlocks := got.GetWorkout().GetRun().GetBlocks()
	if len(gotBlocks) != 3 || gotBlocks[0].GetRepeats() != 1 || gotBlocks[1].GetRepeats() != 6 ||
		gotBlocks[1].GetSteps()[0].GetHeartRate().GetLowBpm() != -5 || gotBlocks[1].GetSteps()[0].GetNotes() != "Uphill" ||
		gotBlocks[1].GetSteps()[1].GetPace().GetSlowSecondsPerKilometer() != 420 {
		t.Errorf("got blocks %v, want them as created", gotBlocks)
	}

	// Invalid steps are rejected.
	for name, bad := range map[string]*plansv1.RunStep{
		"no kind":             plansv1.RunStep_builder{DurationSeconds: 60}.Build(),
		"time and distance":   plansv1.RunStep_builder{Kind: plansv1.RunStepKind_RUN_STEP_KIND_RUN, DurationSeconds: 60, DistanceMeters: 400}.Build(),
		"inverted heart rate": plansv1.RunStep_builder{Kind: plansv1.RunStepKind_RUN_STEP_KIND_RUN, HeartRate: plansv1.HeartRateTarget_builder{LowBpm: 160, HighBpm: 150}.Build()}.Build(),
		"inverted pace":       plansv1.RunStep_builder{Kind: plansv1.RunStepKind_RUN_STEP_KIND_RUN, Pace: plansv1.PaceTarget_builder{FastSecondsPerKilometer: 400, SlowSecondsPerKilometer: 300}.Build()}.Build(),
	} {
		_, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
			PlanId: plan.GetPlan().GetId(),
			Workouts: []*plansv1.Workout{plansv1.Workout_builder{Date: day(3), Run: plansv1.RunWorkout_builder{
				Intensity: plansv1.RunIntensity_RUN_INTENSITY_HARD,
				Blocks:    []*plansv1.RunBlock{plansv1.RunBlock_builder{Steps: []*plansv1.RunStep{bad}}.Build()},
			}.Build()}.Build()},
		}.Build())
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: got %v, want InvalidArgument", name, err)
		}
	}

	// On Garmin, the threshold targets are in beats per minute from the
	// latest lactate test: 4 mmol/L at 180 bpm.
	var stages []*lactatev1.LactateStage
	for i, mmol := range []float64{1, 1.2, 1.6, 2, 3, 4} {
		stages = append(stages, lactatev1.LactateStage_builder{HeartRateBpm: int32(130 + 10*i), LactateMmolPerLiter: mmol}.Build())
	}
	if _, err := e.lactate.CreateLactateTest(ctx, lactatev1.CreateLactateTestRequest_builder{
		Test: lactatev1.LactateTest_builder{Date: day(0), Stages: stages}.Build(),
	}.Build()); err != nil {
		t.Fatal(err)
	}
	e.connectGarmin(garmin.PermissionWorkoutImport)
	if _, err := e.garminAPI.SyncGarmin(ctx, garminv1.SyncGarminRequest_builder{}.Build()); err != nil {
		t.Fatalf("SyncGarmin: %v", err)
	}
	e.garmin.mu.Lock()
	defer e.garmin.mu.Unlock()
	if len(e.garmin.workouts) != 1 {
		t.Fatalf("got %d workouts on Garmin, want 1", len(e.garmin.workouts))
	}
	for _, w := range e.garmin.workouts {
		steps := w.Segments[0].Steps
		if len(steps) != 3 || steps[1].RepeatValue != 6 {
			t.Fatalf("got steps %+v, want a warm-up, a repeat and a cool-down", steps)
		}
		if h := steps[1].Steps[0]; h.TargetType != "HEART_RATE" || h.TargetValueLow != 175 || h.TargetValueHigh != 183 {
			t.Errorf("got the hard step %+v, want 175 to 183 bpm", h)
		}
	}
}
