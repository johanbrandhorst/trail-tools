package services_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestExerciseGroups(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	library, err := e.exercises.CreateExercises(ctx, plansv1.CreateExercisesRequest_builder{
		Exercises: []*plansv1.Exercise{
			plansv1.Exercise_builder{Name: "Leg swings"}.Build(),
			plansv1.Exercise_builder{Name: "Hip raise"}.Build(),
			plansv1.Exercise_builder{Name: "Dead bug"}.Build(),
			plansv1.Exercise_builder{Name: "Side plank"}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(library.GetExercises()))
	for i, ex := range library.GetExercises() {
		ids[i] = ex.GetId()
	}
	plan, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{Name: "Strength", StartDate: "2026-11-02", EndDate: "2026-11-30"}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	circuit := plansv1.ExerciseGroup_builder{
		Name: "Core circuit", Kind: plansv1.ExerciseGroupKind_EXERCISE_GROUP_KIND_CIRCUIT, Rounds: proto.Int32(2), RestSeconds: proto.Int32(45),
	}.Build()
	warmUpGroup := plansv1.ExerciseGroup_builder{Name: "Warm-up", Kind: plansv1.ExerciseGroupKind_EXERCISE_GROUP_KIND_WARM_UP}.Build()
	warmUp := func() *plansv1.PlannedExercise {
		// Done until the user is done, with neither reps nor a time.
		return plansv1.PlannedExercise_builder{ExerciseId: ids[0], Group: "Warm-up", Sets: 1}.Build()
	}
	inCircuit := func(i int, rest int32) *plansv1.PlannedExercise {
		return plansv1.PlannedExercise_builder{ExerciseId: ids[i], Group: "Core circuit", Reps: 10, RestSeconds: rest}.Build()
	}
	create := func(groups []*plansv1.ExerciseGroup, exercises ...*plansv1.PlannedExercise) (*plansv1.Workout, error) {
		resp, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
			PlanId: plan.GetPlan().GetId(),
			Workouts: []*plansv1.Workout{plansv1.Workout_builder{
				Date: "2026-11-04", Title: "Core", Strength: plansv1.StrengthWorkout_builder{Groups: groups, Exercises: exercises}.Build(),
			}.Build()},
		}.Build())
		if err != nil {
			return nil, err
		}
		return resp.GetWorkouts()[0], nil
	}

	// A circuit's exercises are done once each round: its rounds and the
	// rest between rounds are only on the group.
	w, err := create([]*plansv1.ExerciseGroup{warmUpGroup, circuit}, warmUp(), inCircuit(1, 0), inCircuit(2, 15), inCircuit(3, 0))
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	st := w.GetStrength()
	groups := st.GetGroups()
	if len(groups) != 2 || groups[0].GetName() != "Warm-up" || groups[0].GetKind() != plansv1.ExerciseGroupKind_EXERCISE_GROUP_KIND_WARM_UP ||
		groups[0].HasRounds() || groups[0].HasRestSeconds() ||
		groups[1].GetKind() != plansv1.ExerciseGroupKind_EXERCISE_GROUP_KIND_CIRCUIT || groups[1].GetRounds() != 2 || groups[1].GetRestSeconds() != 45 {
		t.Errorf("got groups %v, want the warm-up and the circuit of 2 rounds resting 45 s", groups)
	}
	ex := st.GetExercises()
	if ex[0].GetSets() != 1 || ex[0].GetReps() != 0 || ex[0].GetHoldSeconds() != 0 {
		t.Errorf("the warm-up is %v, want one set until done", ex[0])
	}
	for i := 1; i < 4; i++ {
		if ex[i].GetSets() != 0 {
			t.Errorf("exercise %d of the circuit has %d sets, want none, as its rounds are on the group", i, ex[i].GetSets())
		}
	}
	if ex[2].GetRestSeconds() != 15 || ex[3].GetRestSeconds() != 0 {
		t.Errorf("got rests %d and %d, want 15 s between exercises and the round's rest on the group", ex[2].GetRestSeconds(), ex[3].GetRestSeconds())
	}
	// It reads back the same, so clients can send it back as they got it.
	updated, err := e.plans.UpdateWorkout(ctx, plansv1.UpdateWorkoutRequest_builder{
		Workout:    plansv1.Workout_builder{Id: w.GetId(), Strength: st}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"strength"}},
	}.Build())
	if err != nil {
		t.Fatalf("UpdateWorkout with the workout as read: %v", err)
	}
	if got := updated.GetWorkout().GetStrength().GetGroups(); len(got) != 2 || got[1].GetRestSeconds() != 45 {
		t.Errorf("after updating, got groups %v", got)
	}
	// Watches get the circuit as rounds.
	if resp, _ := e.get("/workouts/"+w.GetId()+".fit", e.token); resp.StatusCode != http.StatusOK {
		t.Errorf("downloading the circuit: %d", resp.StatusCode)
	}

	// A warm-up exercise is rated by how stiff it felt.
	logged, err := e.plans.SetStrengthLog(ctx, plansv1.SetStrengthLogRequest_builder{
		WorkoutId: w.GetId(),
		Log: plansv1.StrengthLog_builder{Exercises: []*plansv1.LoggedExercise{plansv1.LoggedExercise_builder{
			ExerciseId: ids[0],
			Sets:       []*plansv1.LoggedSet{{}},
			Stiffness:  plansv1.Stiffness_STIFFNESS_SORE,
			Notes:      "Left hip",
		}.Build()}}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("SetStrengthLog: %v", err)
	}
	if got := logged.GetWorkout().GetStrength().GetLog().GetExercises(); len(got) != 1 || got[0].GetStiffness() != plansv1.Stiffness_STIFFNESS_SORE {
		t.Errorf("got logged exercises %v, want the warm-up rated sore", got)
	}

	// Ambiguous workouts are refused, saying what to do.
	for name, tc := range map[string]struct {
		groups    []*plansv1.ExerciseGroup
		exercises []*plansv1.PlannedExercise
		want      string
	}{
		"circuit not listed": {
			exercises: []*plansv1.PlannedExercise{inCircuit(1, 0)},
			want:      "list it in groups",
		},
		"sets other than the rounds": {
			groups: []*plansv1.ExerciseGroup{circuit},
			exercises: []*plansv1.PlannedExercise{
				inCircuit(1, 0), plansv1.PlannedExercise_builder{ExerciseId: ids[2], Group: "Core circuit", Sets: 3, Reps: 10}.Build(),
			},
			want: "leave sets 0",
		},
		"rest on the last exercise": {
			groups:    []*plansv1.ExerciseGroup{circuit},
			exercises: []*plansv1.PlannedExercise{inCircuit(1, 0), inCircuit(2, 45)},
			want:      "group's rest_seconds",
		},
		"not consecutive": {
			groups:    []*plansv1.ExerciseGroup{warmUpGroup, circuit},
			exercises: []*plansv1.PlannedExercise{inCircuit(1, 0), warmUp(), inCircuit(2, 0)},
			want:      "consecutive",
		},
		"no rounds": {
			groups: []*plansv1.ExerciseGroup{plansv1.ExerciseGroup_builder{
				Name: "Core circuit", Kind: plansv1.ExerciseGroupKind_EXERCISE_GROUP_KIND_CIRCUIT,
			}.Build()},
			exercises: []*plansv1.PlannedExercise{inCircuit(1, 0)},
			want:      "rounds",
		},
		"unused group": {
			groups:    []*plansv1.ExerciseGroup{warmUpGroup, circuit},
			exercises: []*plansv1.PlannedExercise{warmUp()},
			want:      "no exercise is in group",
		},
		"straight sets with rounds": {
			groups: []*plansv1.ExerciseGroup{plansv1.ExerciseGroup_builder{
				Name: "Warm-up", Kind: plansv1.ExerciseGroupKind_EXERCISE_GROUP_KIND_STRAIGHT_SETS, Rounds: proto.Int32(2),
			}.Build()},
			exercises: []*plansv1.PlannedExercise{warmUp()},
			want:      "leave them unset",
		},
		"warm-up with rounds": {
			groups: []*plansv1.ExerciseGroup{plansv1.ExerciseGroup_builder{
				Name: "Warm-up", Kind: plansv1.ExerciseGroupKind_EXERCISE_GROUP_KIND_WARM_UP, Rounds: proto.Int32(2),
			}.Build()},
			exercises: []*plansv1.PlannedExercise{warmUp()},
			want:      "only supersets and circuits have rounds",
		},
		"warm-up not listed": {
			exercises: []*plansv1.PlannedExercise{warmUp()},
			want:      "reads like a warm-up",
		},
		"warm-up with a target effort": {
			groups:    []*plansv1.ExerciseGroup{warmUpGroup},
			exercises: []*plansv1.PlannedExercise{plansv1.PlannedExercise_builder{ExerciseId: ids[0], Group: "Warm-up", Sets: 1, TargetEffort: 5}.Build()},
			want:      "no target_effort",
		},
		"straight sets without sets": {
			exercises: []*plansv1.PlannedExercise{plansv1.PlannedExercise_builder{ExerciseId: ids[0], Group: "Main", Reps: 10}.Build()},
			want:      "sets must be between",
		},
	} {
		_, err := create(tc.groups, tc.exercises...)
		wantCode(t, err, connect.CodeInvalidArgument)
		if err != nil && !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %q, want it to say %q", name, err, tc.want)
		}
	}
}
