package garmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Sports of Training API workouts.
const (
	SportRunning          = "RUNNING"
	SportStrengthTraining = "STRENGTH_TRAINING"
)

// Workout is a structured workout of the Training API V2: one segment for a
// single sport.
type Workout struct {
	WorkoutName string `json:"workoutName"`
	// At most 1024 characters, which Garmin truncates to.
	Description string `json:"description,omitempty"`
	Sport       string `json:"sport"`
	// The app's name, shown on Garmin Connect, and the same for tracking,
	// at most 20 characters.
	WorkoutProvider string           `json:"workoutProvider"`
	WorkoutSourceID string           `json:"workoutSourceId"`
	Segments        []WorkoutSegment `json:"segments"`
}

// WorkoutSegment is a sport of a workout and its steps.
type WorkoutSegment struct {
	SegmentOrder int           `json:"segmentOrder"`
	Sport        string        `json:"sport"`
	Steps        []WorkoutStep `json:"steps"`
}

// MaxWorkoutSteps is the most steps, counting those within repeats, of a
// single sport workout.
const MaxWorkoutSteps = 100

// CountSteps counts the steps of a workout, with those within repeats.
func (w *Workout) CountSteps() int {
	var count func(steps []WorkoutStep) int
	count = func(steps []WorkoutStep) int {
		n := 0
		for _, s := range steps {
			n += 1 + count(s.Steps)
		}
		return n
	}
	n := 0
	for _, segment := range w.Segments {
		n += count(segment.Steps)
	}
	return n
}

// WorkoutStep is a step of a workout: an exercise or interval, with Type
// "WorkoutStep", or a repeat of steps, with Type "WorkoutRepeatStep".
type WorkoutStep struct {
	Type      string `json:"type"`
	StepOrder int    `json:"stepOrder"`
	Intensity string `json:"intensity,omitempty"`
	// At most 512 characters, which Garmin truncates to.
	Description string `json:"description,omitempty"`
	// "TIME" in seconds, "DISTANCE" in meters, "REPS" or "OPEN".
	DurationType  string  `json:"durationType,omitempty"`
	DurationValue float64 `json:"durationValue,omitempty"`
	// "OPEN", "HEART_RATE" in beats per minute, or "PACE" in meters per
	// second, between TargetValueLow and TargetValueHigh.
	TargetType      string  `json:"targetType,omitempty"`
	TargetValueLow  float64 `json:"targetValueLow,omitempty"`
	TargetValueHigh float64 `json:"targetValueHigh,omitempty"`
	// The exercise of strength steps, from Garmin's catalog: see
	// LookupExercise. Empty for exercises outside the catalog.
	ExerciseCategory string `json:"exerciseCategory,omitempty"`
	ExerciseName     string `json:"exerciseName,omitempty"`
	// The weight in kilograms, shown in WeightDisplayUnit, "KILOGRAM" or
	// "POUND".
	WeightValue       float64 `json:"weightValue,omitempty"`
	WeightDisplayUnit string  `json:"weightDisplayUnit,omitempty"`
	// Repeat steps repeat their steps until they're done RepeatValue
	// times.
	RepeatType  string        `json:"repeatType,omitempty"`
	RepeatValue int           `json:"repeatValue,omitempty"`
	Steps       []WorkoutStep `json:"steps,omitempty"`
}

// CreateWorkout adds a workout to the user's Garmin Connect workouts, and
// returns its ID.
func (c *Client) CreateWorkout(ctx context.Context, accessToken string, w *Workout) (int64, error) {
	var out struct {
		WorkoutID int64 `json:"workoutId"`
	}
	if err := c.do(ctx, accessToken, http.MethodPost, c.apiURL+"/workoutportal/workout/v2", w, &out); err != nil {
		return 0, fmt.Errorf("failed to create workout: %w", err)
	}
	if out.WorkoutID == 0 {
		return 0, errors.New("garmin returned no workout ID")
	}
	return out.WorkoutID, nil
}

// DeleteWorkout deletes a workout, with its schedules. A workout that no
// longer exists isn't an error.
func (c *Client) DeleteWorkout(ctx context.Context, accessToken string, id int64) error {
	err := c.do(ctx, accessToken, http.MethodDelete, c.apiURL+"/training-api/workout/v2/"+strconv.FormatInt(id, 10), nil, nil)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("failed to delete workout: %w", err)
	}
	return nil
}

// ScheduleWorkout puts a workout on the user's calendar on a date, and
// returns the schedule's ID, or 0 if Garmin didn't return it. The watch gets
// the workouts scheduled in the coming days when it syncs.
func (c *Client) ScheduleWorkout(ctx context.Context, accessToken string, workoutID int64, date time.Time) (int64, error) {
	body := map[string]any{"workoutId": workoutID, "date": date.Format(time.DateOnly)}
	// Garmin's documentation doesn't say what the response is: the
	// schedule, or its ID.
	var out json.RawMessage
	if err := c.do(ctx, accessToken, http.MethodPost, c.apiURL+"/training-api/schedule/", body, &out); err != nil {
		return 0, fmt.Errorf("failed to schedule workout: %w", err)
	}
	var id int64
	if json.Unmarshal(out, &id) == nil {
		return id, nil
	}
	var schedule struct {
		ScheduleID int64 `json:"scheduleId"`
	}
	_ = json.Unmarshal(out, &schedule)
	return schedule.ScheduleID, nil
}
