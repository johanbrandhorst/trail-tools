package garmin

import (
	"strings"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/plans"
)

func TestStepDescription(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		exercise *plans.PlannedExercise
		withName bool
		want     string
	}{
		{"nothing", &plans.PlannedExercise{ExerciseName: "Push-up"}, false, ""},
		{
			"everything",
			&plans.PlannedExercise{ExerciseName: "Split squat", Tempo: "3 s down", TargetEffort: 6, Notes: "Front foot flat."},
			true,
			"Split squat. Tempo: 3 s down. Effort 6/10. Front foot flat",
		},
		{"catalog exercise", &plans.PlannedExercise{ExerciseName: "Push-up", Tempo: "2 s down", Notes: "Elbows in"}, false, "Tempo: 2 s down. Elbows in"},
	} {
		if got := stepDescription(tc.exercise, tc.withName); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRunSteps(t *testing.T) {
	t.Parallel()
	w := &plans.Workout{Type: plans.WorkoutTypeIntensity, Title: "Hill repeats", RunBlocks: []plans.RunBlock{
		{Repeats: 1, Steps: []plans.RunStep{{Kind: plans.RunStepWarmUp, DurationSeconds: 900, HeartRate: &plans.HeartRateTarget{Reference: plans.HeartRateAerobicThreshold, LowBPM: -20, HighBPM: 0}}}},
		{Repeats: 6, Steps: []plans.RunStep{
			{Kind: plans.RunStepRun, DurationSeconds: 180, HeartRate: &plans.HeartRateTarget{Reference: plans.HeartRateAnaerobicThreshold, LowBPM: -5, HighBPM: 3}, Notes: "Uphill"},
			{Kind: plans.RunStepRecovery, DistanceMeters: 400, Pace: &plans.PaceTarget{FastSecondsPerKilometer: 300, SlowSecondsPerKilometer: 400}},
		}},
		{Repeats: 1, Steps: []plans.RunStep{{Kind: plans.RunStepCoolDown}}},
	}}
	gw, ok := trainingWorkout(w, thresholds{aerobic: 150})
	if !ok {
		t.Fatal("not a Garmin workout")
	}
	steps := gw.Segments[0].Steps
	if len(steps) != 3 || gw.CountSteps() != 5 {
		t.Fatalf("got %d top-level steps and %d in all, want 3 and 5: %+v", len(steps), gw.CountSteps(), steps)
	}
	warmUp, repeat, coolDown := steps[0], steps[1], steps[2]
	if warmUp.Intensity != "WARMUP" || warmUp.DurationType != "TIME" || warmUp.DurationValue != 900 ||
		warmUp.TargetType != "HEART_RATE" || warmUp.TargetValueLow != 130 || warmUp.TargetValueHigh != 150 {
		t.Errorf("unexpected warm-up %+v", warmUp)
	}
	if repeat.Type != "WorkoutRepeatStep" || repeat.RepeatValue != 6 || len(repeat.Steps) != 2 {
		t.Fatalf("unexpected repeat %+v", repeat)
	}
	// Without an anaerobic threshold, its target is left open and described.
	if hard := repeat.Steps[0]; hard.TargetType != "OPEN" || hard.Intensity != "ACTIVE" ||
		!strings.Contains(hard.Description, "Uphill. Heart rate -5 to +3 bpm from your anaerobic threshold") {
		t.Errorf("unexpected hard step %+v", hard)
	}
	// Paces are speeds, the slow pace the low one.
	if easy := repeat.Steps[1]; easy.DurationType != "DISTANCE" || easy.DurationValue != 400 || easy.TargetType != "PACE" ||
		easy.TargetValueLow != 2.5 || easy.TargetValueHigh != 3.333 {
		t.Errorf("unexpected recovery %+v", easy)
	}
	if coolDown.Intensity != "COOLDOWN" || coolDown.DurationType != "OPEN" {
		t.Errorf("unexpected cool-down %+v", coolDown)
	}
	// Step orders count every step, repeats included.
	if warmUp.StepOrder != 1 || repeat.StepOrder != 2 || repeat.Steps[0].StepOrder != 3 || coolDown.StepOrder != 5 {
		t.Errorf("unexpected step orders %d %d %d %d", warmUp.StepOrder, repeat.StepOrder, repeat.Steps[0].StepOrder, coolDown.StepOrder)
	}
}
