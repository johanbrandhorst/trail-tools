package lactate

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/fit"
	"github.com/bs-tools/trailrunn.ing/internal/plans"
)

// WorkoutPath is the path of a lactate test as a Garmin workout.
const WorkoutPath = "/lactate-tests/workout.fit"

// workoutWarmUp is how long the workout's warm-up lasts.
const workoutWarmUp = plans.LactateTestWarmUpMinutes * time.Minute

// workoutParam is a query parameter of the workout, with its default and
// bounds.
type workoutParam struct {
	name               string
	fallback, min, max int
}

// The workout's query parameters, matching the guided test's settings, and
// by default the stages of planned tests on the Garmin calendar.
var (
	startParam   = workoutParam{"start", int(plans.DefaultLactateTestStages.StartBPM), 60, 200}
	stepParam    = workoutParam{"step", int(plans.DefaultLactateTestStages.StepBPM), 2, 30}
	endParam     = workoutParam{"end", int(plans.DefaultLactateTestStages.EndBPM), 60, 220}
	minutesParam = workoutParam{"minutes", int(plans.DefaultLactateTestStages.StageMinutes), 1, 20}
	rangeParam   = workoutParam{"range", int(plans.DefaultLactateTestStages.RangeBPM), 1, 15}
)

// get returns the parameter's value in the query, or its default if unset.
func (p workoutParam) get(r *http.Request) (int, error) {
	text := r.URL.Query().Get(p.name)
	if text == "" {
		return p.fallback, nil
	}
	v, err := strconv.Atoi(text)
	if err != nil || v < p.min || v > p.max {
		return 0, fmt.Errorf("%s must be a whole number from %d to %d", p.name, p.min, p.max)
	}
	return v, nil
}

// WorkoutHandler serves a lactate test as a FIT workout file, which Garmin
// watches guide stage by stage, alerting when the heart rate leaves each
// stage's range, and waiting for the lap button while the user samples. The
// query sets the stages like the guided test: the start heart rate, the step
// between stages, the end heart rate, the minutes of each stage, and the
// range, how far the heart rate may stray from each stage's.
func (s *Service) WorkoutHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := make([]int, 5)
		for i, p := range []workoutParam{startParam, stepParam, endParam, minutesParam, rangeParam} {
			v, err := p.get(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			values[i] = v
		}
		start, step, end, minutes, spread := values[0], values[1], values[2], values[3], values[4]
		if end < start {
			http.Error(w, "end must be at least start", http.StatusBadRequest)
			return
		}
		workout := testWorkout(start, step, end, time.Duration(minutes)*time.Minute, spread)
		if len(workout.Steps) > fit.MaxWorkoutSteps {
			http.Error(w, fmt.Sprintf("too many stages for a watch: at most %d; raise the step or narrow the heart rates", (fit.MaxWorkoutSteps-2)/2), http.StatusBadRequest)
			return
		}
		var buf bytes.Buffer
		if err := fit.WriteWorkout(&buf, workout, time.Now()); err != nil {
			s.report.Report(r.Context(), "Failed to write a lactate test workout", err)
			http.Error(w, "failed to write the workout", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.ant.fit")
		w.Header().Set("Content-Disposition", `attachment; filename="lactate-test.fit"`)
		_, _ = w.Write(buf.Bytes())
	})
}

// testWorkout returns the steps of a lactate test: a warm-up, then each
// stage, held in its heart rate range, followed by an open step to sample
// lactate in, ending at the lap button, and a cool-down.
func testWorkout(start, step, end int, stage time.Duration, spread int) *fit.Workout {
	steps := []fit.WorkoutStep{{
		Name:      "Warm up",
		Notes:     fmt.Sprintf("Very easy, below %d bpm", start),
		Duration:  workoutWarmUp,
		Intensity: fit.IntensityWarmUp,
	}}
	for heartRate := start; heartRate <= end; heartRate += step {
		steps = append(steps,
			fit.WorkoutStep{
				Name:            fmt.Sprintf("%d bpm", heartRate),
				Notes:           fmt.Sprintf("Hold %d-%d bpm, then take a sample", heartRate-spread, heartRate+spread),
				Duration:        stage,
				MinHeartRateBPM: heartRate - spread,
				MaxHeartRateBPM: heartRate + spread,
				Intensity:       fit.IntensityActive,
			},
			fit.WorkoutStep{
				Name:      "Sample",
				Notes:     "Measure your lactate, then press lap",
				Intensity: fit.IntensityRest,
			},
		)
	}
	steps = append(steps, fit.WorkoutStep{
		Name:      "Cool down",
		Notes:     "Easy, until you press lap",
		Intensity: fit.IntensityCoolDown,
	})
	return &fit.Workout{Name: "Lactate test", Sport: fit.SportRunning, Steps: steps}
}
