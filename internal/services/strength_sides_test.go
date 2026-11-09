package services_test

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
)

func TestStrengthSides(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// Single-leg squats and side planks are done on each side, glute
	// bridges aren't.
	created, err := e.exercises.CreateExercises(ctx, plansv1.CreateExercisesRequest_builder{
		Exercises: []*plansv1.Exercise{
			plansv1.Exercise_builder{Name: "Single-leg squat", EachSide: true}.Build(),
			plansv1.Exercise_builder{Name: "Side plank", EachSide: true}.Build(),
			plansv1.Exercise_builder{Name: "Glute bridge"}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateExercises: %v", err)
	}
	squat, plank, bridge := created.GetExercises()[0], created.GetExercises()[1], created.GetExercises()[2]
	plan, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{Name: "Rehab", StartDate: "2026-11-02", EndDate: "2026-11-30"}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	create := func(exercises ...*plansv1.PlannedExercise) (*plansv1.CreateWorkoutsResponse, error) {
		return e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
			PlanId: plan.GetPlan().GetId(),
			Workouts: []*plansv1.Workout{plansv1.Workout_builder{
				Date: "2026-11-05", Title: "Rehab", Strength: plansv1.StrengthWorkout_builder{Exercises: exercises}.Build(),
			}.Build()},
		}.Build())
	}

	// The left side's amounts only go with the right side's of the same
	// kind, on exercises done on each side.
	for name, invalid := range map[string]*plansv1.PlannedExercise{
		"left reps of a hold":    plansv1.PlannedExercise_builder{ExerciseId: plank.GetId(), Sets: 2, HoldSeconds: 30, LeftReps: 10}.Build(),
		"left hold without hold": plansv1.PlannedExercise_builder{ExerciseId: squat.GetId(), Sets: 2, Reps: 8, LeftHoldSeconds: 30}.Build(),
		"negative":               plansv1.PlannedExercise_builder{ExerciseId: squat.GetId(), Sets: 2, Reps: 8, LeftReps: -1}.Build(),
		"not done on each side":  plansv1.PlannedExercise_builder{ExerciseId: bridge.GetId(), Sets: 2, Reps: 12, LeftReps: 10}.Build(),
	} {
		if _, err := create(invalid); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: got %v, want InvalidArgument", name, err)
		}
	}

	// Two more reps on the weaker left leg, and a longer hold on the left
	// side.
	workouts, err := create(
		plansv1.PlannedExercise_builder{ExerciseId: squat.GetId(), Sets: 3, Reps: 8, LeftReps: 10}.Build(),
		plansv1.PlannedExercise_builder{ExerciseId: plank.GetId(), Sets: 2, HoldSeconds: 30, LeftHoldSeconds: 45}.Build(),
	)
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	workout := workouts.GetWorkouts()[0]
	planned := workout.GetStrength().GetExercises()
	if planned[0].GetReps() != 8 || planned[0].GetLeftReps() != 10 || planned[1].GetHoldSeconds() != 30 || planned[1].GetLeftHoldSeconds() != 45 {
		t.Errorf("got planned exercises %v, want the left side's amounts kept", planned)
	}

	// The log records each side's reps and holds, and history has them
	// along with what was planned.
	logged, err := e.plans.SetStrengthLog(ctx, plansv1.SetStrengthLogRequest_builder{
		WorkoutId: workout.GetId(),
		Log: plansv1.StrengthLog_builder{Exercises: []*plansv1.LoggedExercise{
			plansv1.LoggedExercise_builder{ExerciseId: squat.GetId(), Sets: []*plansv1.LoggedSet{
				plansv1.LoggedSet_builder{Reps: 8, LeftReps: 10}.Build(),
				plansv1.LoggedSet_builder{Reps: 8, LeftReps: 9}.Build(),
				plansv1.LoggedSet_builder{Reps: 8}.Build(),
			}}.Build(),
			plansv1.LoggedExercise_builder{ExerciseId: plank.GetId(), Sets: []*plansv1.LoggedSet{
				plansv1.LoggedSet_builder{HoldSeconds: 30, LeftHoldSeconds: 40}.Build(),
			}}.Build(),
		}}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("SetStrengthLog: %v", err)
	}
	sets := logged.GetWorkout().GetStrength().GetLog().GetExercises()[0].GetSets()
	if len(sets) != 3 || sets[0].GetLeftReps() != 10 || sets[1].GetLeftReps() != 9 || sets[2].GetLeftReps() != 0 || sets[2].GetReps() != 8 {
		t.Errorf("got squat sets %v, want the left side's reps of each", sets)
	}
	if got := logged.GetWorkout().GetStrength().GetLog().GetExercises()[1].GetSets()[0]; got.GetHoldSeconds() != 30 || got.GetLeftHoldSeconds() != 40 {
		t.Errorf("got plank set %v, want 40 s on the left and 30 s on the right", got)
	}
	history, err := e.exercises.ListExerciseHistory(ctx, plansv1.ListExerciseHistoryRequest_builder{ExerciseIds: []string{squat.GetId()}}.Build())
	if err != nil {
		t.Fatalf("ListExerciseHistory: %v", err)
	}
	if entries := history.GetEntries(); len(entries) != 1 || entries[0].GetPlanned().GetLeftReps() != 10 || entries[0].GetLogged().GetSets()[1].GetLeftReps() != 9 {
		t.Errorf("got history %v, want the planned and logged left side's reps", entries)
	}

	// Sets of an exercise that isn't done on each side have no sides.
	_, err = e.plans.SetStrengthLog(ctx, plansv1.SetStrengthLogRequest_builder{
		WorkoutId: workout.GetId(),
		Log: plansv1.StrengthLog_builder{Exercises: []*plansv1.LoggedExercise{
			plansv1.LoggedExercise_builder{ExerciseId: bridge.GetId(), Sets: []*plansv1.LoggedSet{
				plansv1.LoggedSet_builder{Reps: 12, LeftReps: 10}.Build(),
			}}.Build(),
		}}.Build(),
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
}
