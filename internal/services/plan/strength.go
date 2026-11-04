package plan

import (
	"context"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
	"github.com/johanbrandhorst/trail-tools/internal/plans"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

func (s *Service) SetStrengthLog(ctx context.Context, req *plansv1.SetStrengthLogRequest) (*plansv1.SetStrengthLogResponse, error) {
	userID, workout, err := s.getStrengthWorkout(ctx, req.GetWorkoutId())
	if err != nil {
		return nil, err
	}
	log, err := strengthLogFromProto(req.GetLog())
	if err != nil {
		return nil, err
	}
	ids := make([]pgtype.UUID, len(log.Exercises))
	for i, e := range log.Exercises {
		ids[i] = e.ExerciseID
	}
	exercises, err := s.plans.FindExercises(ctx, userID, ids)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to check exercises")
	}
	for i, e := range log.Exercises {
		if exercises[e.ExerciseID] == nil {
			return nil, invalidArgument("exercises[%d]: exercise %s does not exist", i, store.UUIDToString(e.ExerciseID))
		}
	}
	if err := s.plans.SetStrengthLog(ctx, userID, workout, log); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to save strength log")
	}
	// The activity may have synced before the workout was done, e.g. when
	// the log is saved after the watch's activity.
	if !workout.CompletedActivityID.Valid {
		if err := s.activities.LinkStrengthWorkouts(ctx, userID); err != nil {
			s.report.Report(ctx, "Failed to link a strength workout to its activity, leaving it to the next sync", err)
		}
	}
	updated, err := s.plans.GetWorkout(ctx, userID, workout.ID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get workout")
	}
	return plansv1.SetStrengthLogResponse_builder{Workout: s.workoutProto(updated)}.Build(), nil
}

func (s *Service) DeleteStrengthLog(ctx context.Context, req *plansv1.DeleteStrengthLogRequest) (*plansv1.DeleteStrengthLogResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	workoutID, err := store.StringToUUID(req.GetWorkoutId())
	if err != nil {
		return nil, invalidArgument("invalid workout ID: %v", err)
	}
	if err := s.plans.DeleteStrengthLog(ctx, userID, workoutID); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete strength log")
	}
	return plansv1.DeleteStrengthLogResponse_builder{}.Build(), nil
}

// getStrengthWorkout gets one of the authenticated user's strength workouts
// by ID.
func (s *Service) getStrengthWorkout(ctx context.Context, id string) (pgtype.UUID, *plans.Workout, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return pgtype.UUID{}, nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	workoutID, err := store.StringToUUID(id)
	if err != nil {
		return pgtype.UUID{}, nil, invalidArgument("invalid workout ID: %v", err)
	}
	workout, err := s.plans.GetWorkout(ctx, userID, workoutID)
	if err != nil {
		return pgtype.UUID{}, nil, s.toConnectError(ctx, err, "failed to get workout")
	}
	if workout.Type != plans.WorkoutTypeStrength {
		return pgtype.UUID{}, nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Sprintf("workout %s is not a strength workout; only strength workouts have logs", id))
	}
	return userID, workout, nil
}
