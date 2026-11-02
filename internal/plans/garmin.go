package plans

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/plans/internal/gen"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// GarminWorkout is a workout on the user's Garmin calendar.
type GarminWorkout struct {
	ID pgtype.UUID
	// Invalid once the workout was deleted, so it's removed from Garmin
	// too.
	WorkoutID pgtype.UUID
	// The workout's date, zero if it was deleted.
	WorkoutDate      time.Time
	GarminWorkoutID  int64
	GarminScheduleID int64
	// A hash of what was sent, to send it again only when it changes.
	ContentHash string
}

// ListGarminWorkouts lists the workouts on the user's Garmin calendar.
func (r *Repository) ListGarminWorkouts(ctx context.Context, userID pgtype.UUID) ([]*GarminWorkout, error) {
	rows, err := r.q.ListGarminWorkouts(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list Garmin workouts: %w", err)
	}
	result := make([]*GarminWorkout, len(rows))
	for i, row := range rows {
		result[i] = &GarminWorkout{
			ID:               row.ID,
			WorkoutID:        row.WorkoutID,
			WorkoutDate:      store.DateFromPG(row.WorkoutDate),
			GarminWorkoutID:  row.GarminWorkoutID,
			GarminScheduleID: row.GarminScheduleID.Int64,
			ContentHash:      row.ContentHash,
		}
	}
	return result, nil
}

// AddGarminWorkout records a workout put on the user's Garmin calendar.
func (r *Repository) AddGarminWorkout(ctx context.Context, userID pgtype.UUID, w *GarminWorkout) error {
	if err := r.q.InsertGarminWorkout(ctx, &gen.InsertGarminWorkoutParams{
		UserID:           userID,
		WorkoutID:        w.WorkoutID,
		GarminWorkoutID:  w.GarminWorkoutID,
		GarminScheduleID: pgtype.Int8{Int64: w.GarminScheduleID, Valid: w.GarminScheduleID != 0},
		ContentHash:      w.ContentHash,
	}); err != nil {
		return fmt.Errorf("failed to record Garmin workout: %w", err)
	}
	return nil
}

// RemoveGarminWorkout forgets a workout removed from the user's Garmin
// calendar.
func (r *Repository) RemoveGarminWorkout(ctx context.Context, userID, id pgtype.UUID) error {
	if err := r.q.DeleteGarminWorkout(ctx, &gen.DeleteGarminWorkoutParams{ID: id, UserID: userID}); err != nil {
		return fmt.Errorf("failed to remove Garmin workout: %w", err)
	}
	return nil
}

// RemoveGarminWorkouts forgets all the user's workouts on Garmin, once
// they disconnect it.
func (r *Repository) RemoveGarminWorkouts(ctx context.Context, userID pgtype.UUID) error {
	if err := r.q.DeleteGarminWorkouts(ctx, userID); err != nil {
		return fmt.Errorf("failed to remove Garmin workouts: %w", err)
	}
	return nil
}

// GetWorkoutByActivity gets the user's workout an activity completed. It
// returns store.ErrNotFound if none did.
func (r *Repository) GetWorkoutByActivity(ctx context.Context, userID, activityID pgtype.UUID) (*Workout, error) {
	id, err := r.q.GetWorkoutByActivity(ctx, &gen.GetWorkoutByActivityParams{UserID: userID, CompletedActivityID: activityID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get workout: %w", err)
	}
	return r.GetWorkout(ctx, userID, id)
}

// GarminCourse is a course on the user's Garmin account, of a route.
type GarminCourse struct {
	ID pgtype.UUID
	// Invalid once the route was deleted, so its course is removed from
	// Garmin too.
	RouteID        pgtype.UUID
	GarminCourseID int64
	// A hash of what was sent, to send it again only when it changes.
	ContentHash string
}

// ListGarminCourses lists the courses on the user's Garmin account.
func (r *Repository) ListGarminCourses(ctx context.Context, userID pgtype.UUID) ([]*GarminCourse, error) {
	rows, err := r.q.ListGarminCourses(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list Garmin courses: %w", err)
	}
	result := make([]*GarminCourse, len(rows))
	for i, row := range rows {
		result[i] = &GarminCourse{ID: row.ID, RouteID: row.RouteID, GarminCourseID: row.GarminCourseID, ContentHash: row.ContentHash}
	}
	return result, nil
}

// AddGarminCourse records a course put on the user's Garmin account.
func (r *Repository) AddGarminCourse(ctx context.Context, userID pgtype.UUID, c *GarminCourse) error {
	if err := r.q.InsertGarminCourse(ctx, &gen.InsertGarminCourseParams{
		UserID:         userID,
		RouteID:        c.RouteID,
		GarminCourseID: c.GarminCourseID,
		ContentHash:    c.ContentHash,
	}); err != nil {
		return fmt.Errorf("failed to record Garmin course: %w", err)
	}
	return nil
}

// RemoveGarminCourse forgets a course removed from the user's Garmin
// account.
func (r *Repository) RemoveGarminCourse(ctx context.Context, userID, id pgtype.UUID) error {
	if err := r.q.DeleteGarminCourse(ctx, &gen.DeleteGarminCourseParams{ID: id, UserID: userID}); err != nil {
		return fmt.Errorf("failed to remove Garmin course: %w", err)
	}
	return nil
}

// RemoveGarminCourses forgets all the user's courses on Garmin, once they
// disconnect it.
func (r *Repository) RemoveGarminCourses(ctx context.Context, userID pgtype.UUID) error {
	if err := r.q.DeleteGarminCourses(ctx, userID); err != nil {
		return fmt.Errorf("failed to remove Garmin courses: %w", err)
	}
	return nil
}
