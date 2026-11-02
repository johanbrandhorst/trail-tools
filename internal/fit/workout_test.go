package fit_test

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/fit"
	"github.com/johanbrandhorst/trail-tools/internal/fit/fittest"
)

func TestWriteWorkout(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 10, 16, 7, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	err := fit.WriteWorkout(&buf, &fit.Workout{
		Name:  "Lactate test",
		Sport: fit.SportRunning,
		Steps: []fit.WorkoutStep{
			{Name: "Warm up", Duration: 15 * time.Minute, Intensity: fit.IntensityWarmUp},
			{Name: "100 bpm", Notes: "Hold 95-105 bpm", Duration: 4 * time.Minute, MinHeartRateBPM: 95, MaxHeartRateBPM: 105},
			{Name: "Sample", Intensity: fit.IntensityRest},
		},
	}, created)
	if err != nil {
		t.Fatalf("WriteWorkout: %v", err)
	}
	messages := fittest.Decode(t, buf.Bytes())
	if len(messages) != 5 {
		t.Fatalf("got %d messages, want a file ID, a workout and 3 steps", len(messages))
	}
	if fileID := messages[0]; fileID.Uint(t, 0) != fit.FileWorkout || fileID.Uint(t, 4) != uint64(fit.Time(created)) {
		t.Errorf("unexpected file ID %v", fileID.Fields)
	}
	if workout := messages[1]; workout.String(8) != "Lactate test" || workout.Uint(t, 4) != fit.SportRunning || workout.Uint(t, 6) != 3 {
		t.Errorf("unexpected workout %v", workout.Fields)
	}
	for i, want := range []struct {
		name                     string
		durationType, durationMS uint64
		targetType, low, high    uint64
		intensity                uint64
	}{
		// Warm up for 15 minutes with no target.
		{"Warm up", 0, 15 * 60 * 1000, 2, 0, 0, 2},
		// Hold 95 to 105 bpm for 4 minutes, as heart rates offset by 100.
		{"100 bpm", 0, 4 * 60 * 1000, 1, 195, 205, 0},
		// Sample until the lap button is pressed.
		{"Sample", 5, 0, 2, 0, 0, 1},
	} {
		step := messages[2+i]
		got := []uint64{step.Uint(t, 1), step.Uint(t, 2), step.Uint(t, 3), step.Uint(t, 5), step.Uint(t, 6), step.Uint(t, 7)}
		wantValues := []uint64{want.durationType, want.durationMS, want.targetType, want.low, want.high, want.intensity}
		if step.String(0) != want.name || step.Uint(t, 254) != uint64(i) || !slices.Equal(got, wantValues) {
			t.Errorf("step %d: got %q %v, want %q %v", i, step.String(0), got, want.name, wantValues)
		}
	}
	if notes := messages[3].String(8); notes != "Hold 95-105 bpm" {
		t.Errorf("got notes %q", notes)
	}

	if err := fit.WriteWorkout(&buf, &fit.Workout{Name: "Too long", Steps: make([]fit.WorkoutStep, fit.MaxWorkoutSteps+1)}, created); err == nil {
		t.Error("wrote a workout with too many steps")
	}
}

func TestWriteStrengthWorkout(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	err := fit.WriteStrengthWorkout(&buf, &fit.StrengthWorkout{
		Name: "Hips and legs",
		Blocks: []fit.StrengthBlock{
			// Three sets of eight split squats with 25 lb dumbbells.
			{Rounds: 3, Exercises: []fit.StrengthExercise{
				{Name: "Bulgarian split squat", Reps: 8, WeightKilograms: 11.34, Pounds: true, Rest: 90 * time.Second},
			}},
			// A superset of two exercises, done twice, resting after both.
			{Rounds: 2, Exercises: []fit.StrengthExercise{
				{Name: "Calf raise", Reps: 15, WeightKilograms: 10},
				{Name: "Copenhagen plank", Hold: 20 * time.Second, Rest: time.Minute},
			}},
		},
	}, created)
	if err != nil {
		t.Fatalf("WriteStrengthWorkout: %v", err)
	}
	messages := fittest.Decode(t, buf.Bytes())
	// A file ID, a workout, 7 steps and 3 exercise titles.
	if len(messages) != 12 {
		t.Fatalf("got %d messages, want 12", len(messages))
	}
	if workout := messages[1]; workout.Uint(t, 4) != fit.SportTraining || workout.Uint(t, 11) != fit.SubSportStrengthTraining || workout.Uint(t, 6) != 7 {
		t.Errorf("unexpected workout %v", workout.Fields)
	}
	for i, want := range []struct {
		name                        string
		durationType, durationValue uint64
		target, intensity           uint64
		exercise, weight            uint64
	}{
		{"Bulgarian split", 29, 8, 0, 0, 0, 1134},
		{"Rest", 0, 90000, 0, 1, 0xFFFF, 0xFFFF},
		// Repeat from the first step for 3 sets.
		{"", 6, 0, 3, 0, 0xFFFF, 0xFFFF},
		{"Calf raise", 29, 15, 0, 0, 1, 1000},
		{"Copenhagen plan", 0, 20000, 0, 0, 2, 0xFFFF},
		{"Rest", 0, 60000, 0, 1, 0xFFFF, 0xFFFF},
		{"", 6, 3, 2, 0, 0xFFFF, 0xFFFF},
	} {
		step := messages[2+i]
		got := []uint64{step.Uint(t, 1), step.Uint(t, 2), step.Uint(t, 4), step.Uint(t, 7), step.Uint(t, 11), step.Uint(t, 12)}
		wantValues := []uint64{want.durationType, want.durationValue, want.target, want.intensity, want.exercise, want.weight}
		if step.String(0) != want.name || !slices.Equal(got, wantValues) {
			t.Errorf("step %d: got %q %v, want %q %v", i, step.String(0), got, want.name, wantValues)
		}
	}
	if unit := messages[2].Uint(t, 13); unit != fit.WeightUnitPounds {
		t.Errorf("got weight unit %d, want pounds", unit)
	}
	// The titles name the exercises in full.
	if title := messages[9]; title.String(2) != "Bulgarian split squat" || title.Uint(t, 1) != 0 || title.Uint(t, 0) != 65534 {
		t.Errorf("unexpected title %v", title.Fields)
	}

	err = fit.WriteStrengthWorkout(&buf, &fit.StrengthWorkout{Name: "Empty"}, created)
	if err == nil {
		t.Error("wrote a workout without exercises")
	}
}
