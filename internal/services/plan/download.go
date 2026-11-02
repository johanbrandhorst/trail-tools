package plan

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/fit"
	"github.com/johanbrandhorst/trail-tools/internal/plans"
	"github.com/johanbrandhorst/trail-tools/internal/store"
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
		if workout.Type != plans.WorkoutTypeStrength {
			http.Error(w, "only strength workouts can be downloaded as Garmin workouts", http.StatusConflict)
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
	})
}

// isRoundGroup reports whether a group of exercises is done back to back for
// rounds, rather than each exercise for its sets: a superset or circuit.
func isRoundGroup(group string) bool {
	group = strings.ToLower(group)
	return strings.Contains(group, "superset") || strings.Contains(group, "circuit")
}

// strengthWorkoutFIT returns a strength workout as a FIT workout: each
// exercise repeated for its sets, and supersets and circuits repeated
// together for their rounds.
func strengthWorkoutFIT(w *plans.Workout) *fit.StrengthWorkout {
	workout := &fit.StrengthWorkout{Name: w.Title}
	if workout.Name == "" {
		workout.Name = "Strength"
	}
	for i := 0; i < len(w.Exercises); {
		e := w.Exercises[i]
		// A superset is the consecutive exercises of its group.
		end := i + 1
		if e.Group != "" && isRoundGroup(e.Group) {
			for end < len(w.Exercises) && w.Exercises[end].Group == e.Group {
				end++
			}
		}
		block := fit.StrengthBlock{}
		for _, planned := range w.Exercises[i:end] {
			block.Rounds = max(block.Rounds, int(planned.Sets))
			block.Exercises = append(block.Exercises, strengthExerciseFIT(planned))
		}
		workout.Blocks = append(workout.Blocks, block)
		i = end
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
	}
}
