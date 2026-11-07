package plan

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/authn"
	"github.com/bs-tools/trailrunn.ing/internal/fit"
	"github.com/bs-tools/trailrunn.ing/internal/plans"
	"github.com/bs-tools/trailrunn.ing/internal/store"
)

// unsafeFilenameChars matches characters that are replaced in download file
// names.
var unsafeFilenameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// DownloadHandler serves strength workouts as FIT workout files for Garmin
// watches at /workouts/{id}.fit. It must be mounted at /workouts/ behind the
// authn middleware. Other paths are passed to next, so the frontend can
// serve its own /workouts pages.
func (s *Service) DownloadHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, isFIT := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/workouts/"), ".fit")
		if !isFIT || strings.Contains(id, "/") || r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		userID, ok := authn.GetUserID(r.Context())
		if !ok {
			http.Error(w, "sign in to download workouts", http.StatusUnauthorized)
			return
		}
		workoutID, err := store.StringToUUID(id)
		if err != nil {
			http.Error(w, "invalid workout ID", http.StatusBadRequest)
			return
		}
		workout, err := s.plans.GetWorkout(r.Context(), userID, workoutID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			s.report.Report(r.Context(), "Failed to get workout", err)
			http.Error(w, "failed to get workout", http.StatusInternalServerError)
			return
		}
		writeStrengthFIT(w, workout)
	})
}

// writeStrengthFIT writes a strength workout as a FIT workout download.
func writeStrengthFIT(w http.ResponseWriter, workout *plans.Workout) {
	if workout.Type != plans.WorkoutTypeStrength {
		http.Error(w, "only strength workouts can be downloaded as watch workouts", http.StatusConflict)
		return
	}
	if len(workout.Exercises) == 0 {
		http.Error(w, "the workout has no exercises yet", http.StatusConflict)
		return
	}
	var buf bytes.Buffer
	if err := fit.WriteStrengthWorkout(&buf, strengthWorkoutFIT(workout), time.Now()); err != nil {
		// Too many steps for a watch is the only way it fails.
		http.Error(w, fmt.Sprintf("the workout doesn't fit on a watch: %v", err), http.StatusConflict)
		return
	}
	name := strings.Trim(unsafeFilenameChars.ReplaceAllString(workout.Title, "-"), "-")
	if name == "" {
		name = "strength-workout"
	}
	w.Header().Set("Content-Type", "application/vnd.ant.fit")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.fit"`, name))
	_, _ = w.Write(buf.Bytes())
}

// strengthWorkoutFIT returns a strength workout as a FIT workout: each
// exercise repeated for its sets, and supersets and circuits repeated
// together for their rounds.
func strengthWorkoutFIT(w *plans.Workout) *fit.StrengthWorkout {
	workout := &fit.StrengthWorkout{Name: w.Title}
	if workout.Name == "" {
		workout.Name = "Strength"
	}
	for _, block := range plans.StrengthBlocks(w.Exercises) {
		b := fit.StrengthBlock{Rounds: block.Rounds}
		for _, i := range block.Exercises {
			b.Exercises = append(b.Exercises, strengthExerciseFIT(w.Exercises[i]))
		}
		workout.Blocks = append(workout.Blocks, b)
	}
	return workout
}

func strengthExerciseFIT(e *plans.PlannedExercise) fit.StrengthExercise {
	var notes []string
	if e.ExerciseEachSide {
		notes = append(notes, "Each side")
	}
	if e.Tempo != "" {
		notes = append(notes, e.Tempo)
	}
	if e.TargetEffort > 0 {
		notes = append(notes, fmt.Sprintf("Effort %d of 10", e.TargetEffort))
	}
	return fit.StrengthExercise{
		Name:            e.ExerciseName,
		Notes:           strings.Join(notes, " · "),
		Reps:            int(e.Reps),
		Hold:            time.Duration(e.HoldSeconds) * time.Second,
		WeightKilograms: e.Weight.Kilograms(),
		Pounds:          e.Weight.Unit == plans.Pounds,
		Rest:            time.Duration(e.RestSeconds) * time.Second,
		Intensity:       exerciseIntensities[e.GroupKind],
		EachSide:        e.ExerciseEachSide,
	}
}

// exerciseIntensities are the intensities of the steps of warm-up and
// cool-down exercises; other exercises are active.
var exerciseIntensities = map[plans.GroupKind]fit.Intensity{
	plans.GroupWarmUp:   fit.IntensityWarmUp,
	plans.GroupCoolDown: fit.IntensityCoolDown,
}
