package garmin

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/fit"
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
		if got := stepDescription(tc.exercise, tc.withName, 0); got != tc.want {
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

func TestStrengthWarmUpSteps(t *testing.T) {
	t.Parallel()
	w := &plans.Workout{Type: plans.WorkoutTypeStrength, Title: "Legs", Exercises: []*plans.PlannedExercise{
		{ExerciseName: "Leg swings", Group: "Warm-up", GroupKind: plans.GroupWarmUp, Sets: 1},
		{ExerciseName: "Split squat", Group: "Main", Sets: 1, Reps: 8},
		{ExerciseName: "Hip flexor stretch", Group: "Cool-down", GroupKind: plans.GroupCoolDown, Sets: 1, HoldSeconds: 30},
	}}
	gw, ok := trainingWorkout(w, thresholds{})
	if !ok {
		t.Fatal("not a Garmin workout")
	}
	steps := gw.Segments[0].Steps
	// The stretch's hold waits for the lap button.
	if len(steps) != 4 {
		t.Fatalf("got %d steps, want 4: %+v", len(steps), steps)
	}
	for i, want := range []string{"WARMUP", "ACTIVE", "REST", "COOLDOWN"} {
		if steps[i].Intensity != want {
			t.Errorf("step %d has intensity %q, want %q", i, steps[i].Intensity, want)
		}
	}
	if steps[0].DurationType != "OPEN" {
		t.Errorf("the warm-up until done is %q, want OPEN", steps[0].DurationType)
	}
}

func TestStrengthHoldSteps(t *testing.T) {
	t.Parallel()
	w := &plans.Workout{Type: plans.WorkoutTypeStrength, Title: "Core", Exercises: []*plans.PlannedExercise{
		{ExerciseName: "Side plank", ExerciseEachSide: true, Sets: 1, HoldSeconds: 30, RestSeconds: 45},
		{ExerciseName: "Dead bug", ExerciseEachSide: true, Sets: 1, Reps: 8},
	}}
	gw, ok := trainingWorkout(w, thresholds{})
	if !ok {
		t.Fatal("not a Garmin workout")
	}
	steps := gw.Segments[0].Steps
	type step struct {
		intensity, duration string
		value               float64
	}
	var got []step
	for _, s := range steps {
		got = append(got, step{s.Intensity, s.DurationType, s.DurationValue})
	}
	// Each side's hold waits for the lap button, then the rest, then the
	// dead bugs with reps per side.
	want := []step{
		{"REST", "OPEN", 0},
		{"ACTIVE", "TIME", 30},
		{"REST", "OPEN", 0},
		{"ACTIVE", "TIME", 30},
		{"REST", "TIME", 45},
		{"ACTIVE", "REPS", 8},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got steps %v, want %v", got, want)
	}
	if !strings.Contains(steps[3].Description, "Side 2 of 2") || !strings.Contains(steps[5].Description, "Each side") {
		t.Errorf("got descriptions %q and %q", steps[3].Description, steps[5].Description)
	}
	// The steps the watch records sets of are the same as those sent.
	if layout := plans.StrengthStepExercises(w.Exercises); len(layout) != len(steps) || layout[1] != 0 || layout[3] != plans.StepSecondSide || layout[5] != 1 {
		t.Errorf("got step exercises %v for %d steps", layout, len(steps))
	}

	// A watch records a set for each side: they're one set.
	log := strengthLogFromSets(w, &fit.Activity{Sets: []fit.Set{
		{Active: true, HasWorkoutStep: true, WorkoutStep: 1, Duration: 30 * time.Second},
		{Active: true, HasWorkoutStep: true, WorkoutStep: 3, Duration: 31 * time.Second},
		{Active: true, HasWorkoutStep: true, WorkoutStep: 5, Reps: 8},
	}})
	if log == nil || len(log.Exercises) != 2 || len(log.Exercises[0].Sets) != 1 || log.Exercises[0].Sets[0].HoldSeconds != 30 || len(log.Exercises[1].Sets) != 1 {
		t.Errorf("got log %+v, want one side plank set and one set of dead bugs", log)
	}
}

func TestLactateTestSteps(t *testing.T) {
	t.Parallel()
	w := &plans.Workout{Type: plans.WorkoutTypeBase, Title: "Lactate test", LactateTest: true}
	for _, tc := range []struct {
		name              string
		start             int32
		firstBPM, lastBPM float64
		wantStages        int
	}{
		{name: "first test", firstBPM: 100, lastBPM: 180, wantStages: 9},
		// A later test starts where the last did, with enough stages to get
		// past the threshold.
		{name: "from the last test", start: 150, firstBPM: 150, lastBPM: 190, wantStages: 5},
	} {
		gw, ok := trainingWorkout(w, thresholds{lactateStart: tc.start})
		if !ok {
			t.Fatalf("%s: not a Garmin workout", tc.name)
		}
		steps := gw.Segments[0].Steps
		if len(steps) != 2*tc.wantStages+2 {
			t.Fatalf("%s: got %d steps, want a warm-up, %d stages with a sample each, and a cool-down: %+v", tc.name, len(steps), tc.wantStages, steps)
		}
		if warmUp := steps[0]; warmUp.Intensity != "WARMUP" || warmUp.DurationType != "TIME" || warmUp.DurationValue != 900 {
			t.Errorf("%s: unexpected warm-up %+v", tc.name, warmUp)
		}
		first, sample, last := steps[1], steps[2], steps[len(steps)-3]
		if first.Intensity != "ACTIVE" || first.DurationType != "TIME" || first.DurationValue != 240 ||
			first.TargetType != "HEART_RATE" || first.TargetValueLow != tc.firstBPM-5 || first.TargetValueHigh != tc.firstBPM+5 {
			t.Errorf("%s: unexpected first stage %+v", tc.name, first)
		}
		if sample.Intensity != "REST" || sample.DurationType != "OPEN" || !strings.Contains(sample.Description, "lactate") {
			t.Errorf("%s: unexpected sample step %+v", tc.name, sample)
		}
		if last.TargetValueLow != tc.lastBPM-5 || last.TargetValueHigh != tc.lastBPM+5 {
			t.Errorf("%s: the last stage is %v-%v bpm, want around %v", tc.name, last.TargetValueLow, last.TargetValueHigh, tc.lastBPM)
		}
		if coolDown := steps[len(steps)-1]; coolDown.Intensity != "COOLDOWN" || coolDown.DurationType != "OPEN" {
			t.Errorf("%s: unexpected cool-down %+v", tc.name, coolDown)
		}
	}
}
