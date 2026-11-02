package services_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestStrengthWorkouts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// Build an exercise library.
	created, err := e.exercises.CreateExercises(ctx, plansv1.CreateExercisesRequest_builder{
		Exercises: []*plansv1.Exercise{
			plansv1.Exercise_builder{
				Name:             "Bulgarian split squat",
				Instructions:     []string{"Rest the back foot on a bench.", " ", "Lower for three seconds."},
				Feel:             "In the front leg's glute.",
				PrimaryMuscles:   []plansv1.Muscle{plansv1.Muscle_MUSCLE_GLUTES, plansv1.Muscle_MUSCLE_QUADS},
				SecondaryMuscles: []plansv1.Muscle{plansv1.Muscle_MUSCLE_ADDUCTORS, plansv1.Muscle_MUSCLE_GLUTES},
				Equipment:        "Two dumbbells and a bench",
				EachSide:         true,
			}.Build(),
			plansv1.Exercise_builder{Name: "Copenhagen plank", PrimaryMuscles: []plansv1.Muscle{plansv1.Muscle_MUSCLE_ADDUCTORS}}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateExercises: %v", err)
	}
	squat, plank := created.GetExercises()[0], created.GetExercises()[1]
	// Blank steps are dropped, and a muscle is either primary or secondary.
	if got := squat.GetInstructions(); len(got) != 2 {
		t.Errorf("got instructions %q, want 2 steps", got)
	}
	if got := squat.GetSecondaryMuscles(); len(got) != 1 || got[0] != plansv1.Muscle_MUSCLE_ADDUCTORS {
		t.Errorf("got secondary muscles %v, want adductors", got)
	}
	// Names are unique, ignoring case, and the error names the existing
	// exercise.
	_, err = e.exercises.CreateExercises(ctx, plansv1.CreateExercisesRequest_builder{
		Exercises: []*plansv1.Exercise{plansv1.Exercise_builder{Name: "bulgarian SPLIT squat"}.Build()},
	}.Build())
	wantCode(t, err, connect.CodeAlreadyExists)
	if err != nil && !strings.Contains(err.Error(), squat.GetId()) {
		t.Errorf("error %q doesn't name the existing exercise", err)
	}
	// Other users have their own library.
	others, err := e.otherExercises.ListExercises(ctx, plansv1.ListExercisesRequest_builder{}.Build())
	if err != nil || len(others.GetExercises()) != 0 {
		t.Errorf("another user listed %d exercises (%v), want none", len(others.GetExercises()), err)
	}
	otherExercise, err := e.otherExercises.CreateExercises(ctx, plansv1.CreateExercisesRequest_builder{
		Exercises: []*plansv1.Exercise{plansv1.Exercise_builder{Name: "Bulgarian split squat"}.Build()},
	}.Build())
	if err != nil {
		t.Fatalf("another user's CreateExercises: %v", err)
	}

	// Plan a strength workout of the exercises.
	plan, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{Name: "Strength", StartDate: "2026-11-02", EndDate: "2026-11-30"}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	planID := plan.GetPlan().GetId()
	strength := func(exercises ...*plansv1.PlannedExercise) *plansv1.StrengthWorkout {
		return plansv1.StrengthWorkout_builder{Exercises: exercises, TargetDurationSeconds: 2700}.Build()
	}
	squats := plansv1.PlannedExercise_builder{
		ExerciseId: squat.GetId(), Group: "Main", Sets: 3, Reps: 8, WeightKilograms: 10, RestSeconds: 90, TargetEffort: 7,
	}.Build()
	planks := plansv1.PlannedExercise_builder{ExerciseId: plank.GetId(), Group: "Superset A", Sets: 3, HoldSeconds: 20}.Build()
	workouts, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
		PlanId: planID,
		Workouts: []*plansv1.Workout{
			plansv1.Workout_builder{Date: "2026-11-05", Title: "Hips and legs", Strength: strength(squats, planks)}.Build(),
			plansv1.Workout_builder{Date: "2026-11-12", Title: "Hips and legs again", Strength: strength(squats)}.Build(),
			plansv1.Workout_builder{Date: "2026-11-06", Title: "Easy", Run: baseRun()}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	first, second, run := workouts.GetWorkouts()[0], workouts.GetWorkouts()[1], workouts.GetWorkouts()[2]
	if first.GetType() != plansv1.WorkoutType_WORKOUT_TYPE_STRENGTH {
		t.Errorf("got type %v, want strength", first.GetType())
	}
	if got := first.GetStrength().GetExercises(); len(got) != 2 || got[0].GetExerciseName() != "Bulgarian split squat" || got[1].GetHoldSeconds() != 20 {
		t.Errorf("unexpected exercises %v", got)
	}
	// Exercises must be in the user's library, and be done for reps or for
	// a time.
	for name, planned := range map[string]*plansv1.PlannedExercise{
		"missing":        plansv1.PlannedExercise_builder{ExerciseId: "00000000-0000-0000-0000-000000000000", Sets: 1, Reps: 5}.Build(),
		"another user's": plansv1.PlannedExercise_builder{ExerciseId: otherExercise.GetExercises()[0].GetId(), Sets: 1, Reps: 5}.Build(),
		"no reps":        plansv1.PlannedExercise_builder{ExerciseId: squat.GetId(), Sets: 3}.Build(),
		"no sets":        plansv1.PlannedExercise_builder{ExerciseId: squat.GetId(), Reps: 5}.Build(),
	} {
		_, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
			PlanId:   planID,
			Workouts: []*plansv1.Workout{plansv1.Workout_builder{Date: "2026-11-05", Strength: strength(planned)}.Build()},
		}.Build())
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s exercise: got %v, want InvalidArgument", name, err)
		}
	}

	// Changing the title keeps the exercises.
	renamed, err := e.plans.UpdateWorkout(ctx, plansv1.UpdateWorkoutRequest_builder{
		Workout:    plansv1.Workout_builder{Id: first.GetId(), Title: "Hips, hamstrings and downhill legs"}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"title"}},
	}.Build())
	if err != nil {
		t.Fatalf("UpdateWorkout: %v", err)
	}
	if got := renamed.GetWorkout().GetStrength().GetExercises(); len(got) != 2 {
		t.Errorf("renaming left %d exercises, want 2", len(got))
	}

	// The strength activity of the day syncs, and completes the first
	// workout, but not the run of the next day.
	activityID, err := e.activities.UpsertActivity(ctx, e.userID, &activities.ActivitySummary{
		StravaID:       9001,
		Name:           "Evening weight training",
		SportType:      "WeightTraining",
		StartTime:      time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC),
		StartDateLocal: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC),
		Timezone:       "Europe/Zurich",
		RawJSON:        []byte("{}"),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.plans.GetWorkout(ctx, plansv1.GetWorkoutRequest_builder{Id: first.GetId()}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if got.GetWorkout().GetCompletedActivityId() != store.UUIDToString(activityID) {
		t.Errorf("workout completed by %q, want the strength activity %s", got.GetWorkout().GetCompletedActivityId(), store.UUIDToString(activityID))
	}

	// Log the first workout: all the sets of the squats, heavier than
	// planned on the last one, and two sets of planks.
	logged, err := e.plans.SetStrengthLog(ctx, plansv1.SetStrengthLogRequest_builder{
		WorkoutId: first.GetId(),
		Log: plansv1.StrengthLog_builder{
			Effort: plansv1.SessionEffort_SESSION_EFFORT_HARD,
			Notes:  "Left knee felt the step-downs.",
			Exercises: []*plansv1.LoggedExercise{
				plansv1.LoggedExercise_builder{
					ExerciseId: squat.GetId(),
					Sets: []*plansv1.LoggedSet{
						plansv1.LoggedSet_builder{Reps: 8, WeightKilograms: 10}.Build(),
						plansv1.LoggedSet_builder{Reps: 8, WeightKilograms: 10}.Build(),
						plansv1.LoggedSet_builder{Reps: 6, WeightKilograms: 12}.Build(),
					},
					Effort: plansv1.ExerciseEffort_EXERCISE_EFFORT_TOO_HARD,
					Volume: plansv1.ExerciseVolume_EXERCISE_VOLUME_TOO_MANY,
					Notes:  "Last set wobbly.",
				}.Build(),
				plansv1.LoggedExercise_builder{
					ExerciseId: plank.GetId(),
					Sets: []*plansv1.LoggedSet{
						plansv1.LoggedSet_builder{HoldSeconds: 20}.Build(),
						plansv1.LoggedSet_builder{HoldSeconds: 15}.Build(),
					},
					Effort: plansv1.ExerciseEffort_EXERCISE_EFFORT_JUST_RIGHT,
				}.Build(),
			},
		}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("SetStrengthLog: %v", err)
	}
	log := logged.GetWorkout().GetStrength().GetLog()
	if log == nil || log.GetCompleteTime() == nil || log.GetEffort() != plansv1.SessionEffort_SESSION_EFFORT_HARD {
		t.Fatalf("unexpected log %v", log)
	}
	if got := log.GetExercises(); len(got) != 2 || len(got[0].GetSets()) != 3 || got[0].GetSets()[2].GetWeightKilograms() != 12 || got[1].GetExerciseName() != "Copenhagen plank" {
		t.Errorf("unexpected logged exercises %v", got)
	}
	// Replanning the workout keeps its log.
	replanned, err := e.plans.UpdateWorkout(ctx, plansv1.UpdateWorkoutRequest_builder{
		Workout:    plansv1.Workout_builder{Id: first.GetId(), Strength: strength(planks)}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"strength"}},
	}.Build())
	if err != nil {
		t.Fatalf("UpdateWorkout: %v", err)
	}
	if w := replanned.GetWorkout().GetStrength(); len(w.GetExercises()) != 1 || len(w.GetLog().GetExercises()) != 2 {
		t.Errorf("replanning gave exercises %v and log %v", w.GetExercises(), w.GetLog())
	}

	// The second workout is done on a day whose strength activity synced
	// before the log was saved, and saving the log links it.
	earlier, err := e.activities.UpsertActivity(ctx, e.userID, &activities.ActivitySummary{
		StravaID:       9002,
		Name:           "Gym",
		SportType:      "Workout",
		StartTime:      time.Date(2026, 11, 13, 7, 0, 0, 0, time.UTC),
		StartDateLocal: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC),
		Timezone:       "Europe/Zurich",
		RawJSON:        []byte("{}"),
	})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := e.plans.UpdateWorkout(ctx, plansv1.UpdateWorkoutRequest_builder{
		Workout:    plansv1.Workout_builder{Id: second.GetId(), Date: "2026-11-13"}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"date"}},
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if moved.GetWorkout().GetCompletedActivityId() != "" {
		t.Errorf("moving the workout linked activity %q before it was logged", moved.GetWorkout().GetCompletedActivityId())
	}
	logged, err = e.plans.SetStrengthLog(ctx, plansv1.SetStrengthLogRequest_builder{
		WorkoutId: second.GetId(),
		Log: plansv1.StrengthLog_builder{Exercises: []*plansv1.LoggedExercise{
			plansv1.LoggedExercise_builder{ExerciseId: squat.GetId(), Sets: []*plansv1.LoggedSet{
				plansv1.LoggedSet_builder{Reps: 8, WeightKilograms: 12}.Build(),
			}}.Build(),
		}}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("SetStrengthLog: %v", err)
	}
	if got := logged.GetWorkout().GetCompletedActivityId(); got != store.UUIDToString(earlier) {
		t.Errorf("logged workout completed by %q, want %s", got, store.UUIDToString(earlier))
	}

	// Only strength workouts have logs, and only the user's own.
	_, err = e.plans.SetStrengthLog(ctx, plansv1.SetStrengthLogRequest_builder{WorkoutId: run.GetId(), Log: plansv1.StrengthLog_builder{}.Build()}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)
	_, err = e.otherTraining.SetStrengthLog(ctx, plansv1.SetStrengthLogRequest_builder{WorkoutId: first.GetId(), Log: plansv1.StrengthLog_builder{}.Build()}.Build())
	wantCode(t, err, connect.CodeNotFound)

	// An exercise in use can't be deleted.
	_, err = e.exercises.DeleteExercise(ctx, plansv1.DeleteExerciseRequest_builder{Id: squat.GetId()}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)

	// Deleting the first workout keeps its log in the history, which
	// compares what was done with what was planned when it was logged.
	if _, err := e.plans.DeleteWorkouts(ctx, plansv1.DeleteWorkoutsRequest_builder{PlanId: planID, Ids: []string{first.GetId()}}.Build()); err != nil {
		t.Fatal(err)
	}
	history, err := e.exercises.ListExerciseHistory(ctx, plansv1.ListExerciseHistoryRequest_builder{ExerciseIds: []string{squat.GetId()}}.Build())
	if err != nil {
		t.Fatalf("ListExerciseHistory: %v", err)
	}
	entries := history.GetEntries()
	if len(entries) != 2 {
		t.Fatalf("got %d history entries, want 2", len(entries))
	}
	if got := entries[0]; got.GetDate() != "2026-11-05" || got.GetWorkoutId() != "" || got.GetSessionEffort() != plansv1.SessionEffort_SESSION_EFFORT_HARD ||
		got.GetPlanned().GetReps() != 8 || got.GetPlanned().GetWeightKilograms() != 10 || len(got.GetLogged().GetSets()) != 3 ||
		got.GetLogged().GetVolume() != plansv1.ExerciseVolume_EXERCISE_VOLUME_TOO_MANY {
		t.Errorf("unexpected first entry %v", got)
	}
	if got := entries[1]; got.GetDate() != "2026-11-13" || got.GetWorkoutTitle() != "Hips and legs again" || got.GetLogged().GetSets()[0].GetWeightKilograms() != 12 {
		t.Errorf("unexpected second entry %v", got)
	}
	all, err := e.exercises.ListExerciseHistory(ctx, plansv1.ListExerciseHistoryRequest_builder{EndDate: "2026-11-05"}.Build())
	if err != nil || len(all.GetEntries()) != 2 {
		t.Errorf("got %d entries up to the first workout (%v), want 2", len(all.GetEntries()), err)
	}
	otherHistory, err := e.otherExercises.ListExerciseHistory(ctx, plansv1.ListExerciseHistoryRequest_builder{}.Build())
	if err != nil || len(otherHistory.GetEntries()) != 0 {
		t.Errorf("another user listed %d history entries (%v), want none", len(otherHistory.GetEntries()), err)
	}

	// Deleting the second workout's log makes it incomplete again, and
	// removes it from the history.
	if _, err := e.plans.DeleteStrengthLog(ctx, plansv1.DeleteStrengthLogRequest_builder{WorkoutId: second.GetId()}.Build()); err != nil {
		t.Fatalf("DeleteStrengthLog: %v", err)
	}
	got, err = e.plans.GetWorkout(ctx, plansv1.GetWorkoutRequest_builder{Id: second.GetId()}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if got.GetWorkout().GetStrength().HasLog() {
		t.Errorf("workout still has log %v", got.GetWorkout().GetStrength().GetLog())
	}

	// An exercise's details can be updated, and other users can't.
	updated, err := e.exercises.UpdateExercise(ctx, plansv1.UpdateExerciseRequest_builder{
		Exercise:   plansv1.Exercise_builder{Id: plank.GetId(), Feel: "In the inner thigh of the top leg."}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"feel"}},
	}.Build())
	if err != nil {
		t.Fatalf("UpdateExercise: %v", err)
	}
	if got := updated.GetExercise(); got.GetName() != "Copenhagen plank" || got.GetFeel() != "In the inner thigh of the top leg." {
		t.Errorf("unexpected updated exercise %v", got)
	}
	_, err = e.otherExercises.GetExercise(ctx, plansv1.GetExerciseRequest_builder{Id: plank.GetId()}.Build())
	wantCode(t, err, connect.CodeNotFound)
}
