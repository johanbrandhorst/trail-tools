package garmin_test

import (
	"testing"

	"github.com/bs-tools/trailrunn.ing/internal/garmin"
)

func TestExerciseCatalog(t *testing.T) {
	t.Parallel()
	e, ok := garmin.LookupExercise("squat/goblet_squat")
	if !ok || e.Category != "SQUAT" || e.Name != "GOBLET_SQUAT" {
		t.Fatalf("LookupExercise: got %+v, %t", e, ok)
	}
	if _, ok := garmin.LookupExercise("UNKNOWN/COPENHAGEN_PLANK"); ok {
		t.Error("looked up an exercise that isn't in the catalog")
	}
	// Names starting with a digit start with an underscore.
	if _, ok := garmin.LookupExercise("CALF_RAISE/_3_WAY_CALF_RAISE"); !ok {
		t.Error("didn't look up a name starting with a digit")
	}
	for name, want := range map[string]string{
		"Goblet squat":                     "SQUAT/GOBLET_SQUAT",
		"Goblet squats":                    "SQUAT/GOBLET_SQUAT",
		"Dumbbell Bulgarian split squat":   "LUNGE/DUMBBELL_BULGARIAN_SPLIT_SQUAT",
		"Copenhagen plank":                 "",
		"Plank":                            "", // In several categories.
		"  dumbbell bulgarian-split squat": "LUNGE/DUMBBELL_BULGARIAN_SPLIT_SQUAT",
	} {
		got, ok := garmin.MatchExercise(name)
		if (want == "") == ok || ok && got.String() != want {
			t.Errorf("MatchExercise(%q): got %v, %t, want %q", name, got, ok, want)
		}
	}
	found := garmin.SearchExercises("bulgarian split squat", 3)
	if len(found) == 0 || found[0].Category != "LUNGE" {
		t.Errorf("SearchExercises: got %v", found)
	}
}
