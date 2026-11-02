package garmin

import (
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
