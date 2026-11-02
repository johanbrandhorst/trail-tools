package garmin

import (
	"context"
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

// Workout is a structured workout of the Training API.
type Workout struct {
	WorkoutName string `json:"workoutName"`
	Description string `json:"description,omitempty"`
	Sport       string `json:"sport"`
	// The app's name, shown on Garmin Connect.
	WorkoutProvider string `json:"workoutProvider"`
	// An ID of the workout in the app, for finding it again.
	WorkoutSourceID string        `json:"workoutSourceId"`
	Steps           []WorkoutStep `json:"steps"`
}

// WorkoutStep is a step of a workout: an exercise or interval, with Type
// "WorkoutStep", or a repeat of steps, with Type "WorkoutRepeatStep".
type WorkoutStep struct {
	Type        string `json:"type"`
	StepOrder   int    `json:"stepOrder"`
	Intensity   string `json:"intensity,omitempty"`
	Description string `json:"description,omitempty"`
	// "TIME" in seconds, "DISTANCE" in meters, "REPS" or "OPEN".
	DurationType  string  `json:"durationType,omitempty"`
	DurationValue float64 `json:"durationValue,omitempty"`
	TargetType    string  `json:"targetType,omitempty"`
	// The exercise of strength steps. Exercises outside Garmin's catalog
	// are "UNKNOWN", with their name in ExerciseName.
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
	if err := c.do(ctx, accessToken, http.MethodPost, c.apiURL+"/training-api/workout", w, &out); err != nil {
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
	err := c.do(ctx, accessToken, http.MethodDelete, c.apiURL+"/training-api/workout/"+strconv.FormatInt(id, 10), nil, nil)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("failed to delete workout: %w", err)
	}
	return nil
}

// ScheduleWorkout puts a workout on the user's calendar on a date, and
// returns the schedule's ID. The watch gets the workouts scheduled in the
// coming days when it syncs.
func (c *Client) ScheduleWorkout(ctx context.Context, accessToken string, workoutID int64, date time.Time) (int64, error) {
	body := map[string]any{"workoutId": workoutID, "date": date.Format(time.DateOnly)}
	var id int64
	if err := c.do(ctx, accessToken, http.MethodPost, c.apiURL+"/training-api/schedule", body, &id); err != nil {
		return 0, fmt.Errorf("failed to schedule workout: %w", err)
	}
	return id, nil
}
