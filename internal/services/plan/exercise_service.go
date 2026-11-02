package plan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1/plansv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/plans"
	"github.com/johanbrandhorst/trail-tools/internal/protomask"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ plansv1connect.ExerciseServiceHandler = (*ExerciseService)(nil)

// ExerciseRepository stores the users' exercise libraries and their history.
type ExerciseRepository interface {
	CreateExercises(ctx context.Context, userID pgtype.UUID, exercises []*plans.Exercise) ([]*plans.Exercise, error)
	ListExercises(ctx context.Context, userID pgtype.UUID) ([]*plans.Exercise, error)
	GetExercise(ctx context.Context, userID, id pgtype.UUID) (*plans.Exercise, error)
	UpdateExercise(ctx context.Context, e *plans.Exercise) (*plans.Exercise, error)
	DeleteExercise(ctx context.Context, userID, id pgtype.UUID) error
	ListExerciseHistory(ctx context.Context, userID pgtype.UUID, exerciseIDs []pgtype.UUID, startDate, endDate time.Time) ([]*plans.HistoryEntry, error)
}

// ExerciseService implements API handlers for the exercise service.
type ExerciseService struct {
	log       *slog.Logger
	report    *errreport.Reporter
	exercises ExerciseRepository
}

// NewExerciseService creates a new ExerciseService.
func NewExerciseService(log *slog.Logger, report *errreport.Reporter, exercises ExerciseRepository) *ExerciseService {
	return &ExerciseService{log: log, report: report, exercises: exercises}
}

const (
	maxExercisesPerCall  = 100
	maxInstructionSteps  = 10
	maxExerciseNameRunes = 100
)

func (s *ExerciseService) CreateExercises(ctx context.Context, req *plansv1.CreateExercisesRequest) (*plansv1.CreateExercisesResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	n := len(req.GetExercises())
	if n == 0 || n > maxExercisesPerCall {
		return nil, invalidArgument("between 1 and %d exercises are required", maxExercisesPerCall)
	}
	exercises := make([]*plans.Exercise, n)
	names := map[string]bool{}
	for i, e := range req.GetExercises() {
		exercise, err := exerciseFromProto(e)
		if err != nil {
			return nil, prefixError(fmt.Sprintf("exercise %d", i+1), err)
		}
		key := strings.ToLower(exercise.Name)
		if names[key] {
			return nil, invalidArgument("exercise %d: the name %q is given twice", i+1, exercise.Name)
		}
		names[key] = true
		exercises[i] = exercise
	}
	created, err := s.exercises.CreateExercises(ctx, userID, exercises)
	if errors.Is(err, plans.ErrDuplicateExerciseName) {
		return nil, s.duplicateNameError(ctx, userID, exercises)
	}
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to create exercises")
	}
	result := make([]*plansv1.Exercise, len(created))
	for i, e := range created {
		result[i] = exerciseToProto(e)
	}
	return plansv1.CreateExercisesResponse_builder{Exercises: result}.Build(), nil
}

// duplicateNameError returns the error for creating exercises when one of
// their names is taken, naming the existing exercise so agents can use it
// instead.
func (s *ExerciseService) duplicateNameError(ctx context.Context, userID pgtype.UUID, exercises []*plans.Exercise) error {
	existing, err := s.exercises.ListExercises(ctx, userID)
	if err != nil {
		return s.toConnectError(ctx, err, "failed to list exercises")
	}
	for _, e := range exercises {
		for _, x := range existing {
			if strings.EqualFold(e.Name, x.Name) {
				return connect.NewError(connect.CodeAlreadyExists, fmt.Sprintf("an exercise named %q already exists with ID %s; use it, or choose another name", x.Name, store.UUIDToString(x.ID)))
			}
		}
	}
	return connect.NewError(connect.CodeAlreadyExists, plans.ErrDuplicateExerciseName.Error())
}

func (s *ExerciseService) ListExercises(ctx context.Context, _ *plansv1.ListExercisesRequest) (*plansv1.ListExercisesResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	exercises, err := s.exercises.ListExercises(ctx, userID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list exercises")
	}
	result := make([]*plansv1.Exercise, len(exercises))
	for i, e := range exercises {
		result[i] = exerciseToProto(e)
	}
	return plansv1.ListExercisesResponse_builder{Exercises: result}.Build(), nil
}

func (s *ExerciseService) GetExercise(ctx context.Context, req *plansv1.GetExerciseRequest) (*plansv1.GetExerciseResponse, error) {
	userID, id, err := exerciseID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	exercise, err := s.exercises.GetExercise(ctx, userID, id)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get exercise")
	}
	return plansv1.GetExerciseResponse_builder{Exercise: exerciseToProto(exercise)}.Build(), nil
}

func (s *ExerciseService) UpdateExercise(ctx context.Context, req *plansv1.UpdateExerciseRequest) (*plansv1.UpdateExerciseResponse, error) {
	userID, id, err := exerciseID(ctx, req.GetExercise().GetId())
	if err != nil {
		return nil, err
	}
	existing, err := s.exercises.GetExercise(ctx, userID, id)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get exercise")
	}
	merged := exerciseToProto(existing)
	if err := protomask.Apply(merged, req.GetExercise(), req.GetUpdateMask(), "id", "create_time", "update_time"); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	exercise, err := exerciseFromProto(merged)
	if err != nil {
		return nil, err
	}
	exercise.ID = existing.ID
	exercise.UserID = userID
	updated, err := s.exercises.UpdateExercise(ctx, exercise)
	if errors.Is(err, plans.ErrDuplicateExerciseName) {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Sprintf("another exercise is named %q; choose another name", exercise.Name))
	}
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to update exercise")
	}
	return plansv1.UpdateExerciseResponse_builder{Exercise: exerciseToProto(updated)}.Build(), nil
}

func (s *ExerciseService) DeleteExercise(ctx context.Context, req *plansv1.DeleteExerciseRequest) (*plansv1.DeleteExerciseResponse, error) {
	userID, id, err := exerciseID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.exercises.DeleteExercise(ctx, userID, id); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete exercise")
	}
	return plansv1.DeleteExerciseResponse_builder{}.Build(), nil
}

func (s *ExerciseService) ListExerciseHistory(ctx context.Context, req *plansv1.ListExerciseHistoryRequest) (*plansv1.ListExerciseHistoryResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	ids, err := store.UUIDsToPG(req.GetExerciseIds())
	if err != nil {
		return nil, invalidArgument("exercise_ids: %v", err)
	}
	var startDate, endDate time.Time
	if req.GetStartDate() != "" {
		if startDate, err = store.ParseDate(req.GetStartDate()); err != nil {
			return nil, invalidArgument("start_date: %v", err)
		}
	}
	if req.GetEndDate() != "" {
		if endDate, err = store.ParseDate(req.GetEndDate()); err != nil {
			return nil, invalidArgument("end_date: %v", err)
		}
	}
	entries, err := s.exercises.ListExerciseHistory(ctx, userID, ids, startDate, endDate)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list exercise history")
	}
	result := make([]*plansv1.ExerciseHistoryEntry, len(entries))
	for i, e := range entries {
		b := plansv1.ExerciseHistoryEntry_builder{
			Date:          store.FormatDate(e.Date),
			WorkoutId:     store.UUIDToString(e.WorkoutID),
			WorkoutTitle:  e.WorkoutTitle,
			Logged:        loggedExerciseToProto(e.Exercise),
			SessionEffort: sessionEffortsToProto[e.SessionEffort],
		}
		if e.Exercise.Planned != nil {
			b.Planned = plannedExerciseToProto(e.Exercise.Planned)
		}
		result[i] = b.Build()
	}
	return plansv1.ListExerciseHistoryResponse_builder{Entries: result}.Build(), nil
}

// exerciseID returns the authenticated user and the parsed exercise ID.
func exerciseID(ctx context.Context, id string) (pgtype.UUID, pgtype.UUID, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return pgtype.UUID{}, pgtype.UUID{}, connect.NewError(connect.CodeUnauthenticated, "")
	}
	parsed, err := store.StringToUUID(id)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, invalidArgument("invalid exercise ID: %v", err)
	}
	return userID, parsed, nil
}

// muscles are the stored muscles by their API values, whose names are the
// stored names in upper case, prefixed with MUSCLE_.
var muscles = func() map[plansv1.Muscle]plans.Muscle {
	m := map[plansv1.Muscle]plans.Muscle{}
	for _, muscle := range plans.Muscles {
		value, ok := plansv1.Muscle_value["MUSCLE_"+strings.ToUpper(string(muscle))]
		if !ok {
			panic(fmt.Sprintf("muscle %q has no API value", muscle))
		}
		m[plansv1.Muscle(value)] = muscle
	}
	return m
}()

var musclesToProto = reverse(muscles)

func musclesFromProto(field string, ms []plansv1.Muscle) ([]plans.Muscle, error) {
	var result []plans.Muscle
	for _, m := range ms {
		muscle, ok := muscles[m]
		if !ok {
			return nil, invalidArgument("%s: invalid muscle %v", field, m)
		}
		if !slices.Contains(result, muscle) {
			result = append(result, muscle)
		}
	}
	return result, nil
}

func exerciseFromProto(e *plansv1.Exercise) (*plans.Exercise, error) {
	name := strings.TrimSpace(e.GetName())
	if name == "" {
		return nil, invalidArgument("name is required")
	}
	if len([]rune(name)) > maxExerciseNameRunes {
		return nil, invalidArgument("name must be at most %d characters", maxExerciseNameRunes)
	}
	var instructions []string
	for _, step := range e.GetInstructions() {
		if step = strings.TrimSpace(step); step != "" {
			instructions = append(instructions, step)
		}
	}
	if len(instructions) > maxInstructionSteps {
		return nil, invalidArgument("instructions: at most %d steps are allowed", maxInstructionSteps)
	}
	primary, err := musclesFromProto("primary_muscles", e.GetPrimaryMuscles())
	if err != nil {
		return nil, err
	}
	secondary, err := musclesFromProto("secondary_muscles", e.GetSecondaryMuscles())
	if err != nil {
		return nil, err
	}
	// A muscle is either worked hardest or also worked, not both.
	secondary = slices.DeleteFunc(secondary, func(m plans.Muscle) bool { return slices.Contains(primary, m) })
	return &plans.Exercise{
		Name:             name,
		Instructions:     instructions,
		Feel:             strings.TrimSpace(e.GetFeel()),
		PrimaryMuscles:   primary,
		SecondaryMuscles: secondary,
		Equipment:        strings.TrimSpace(e.GetEquipment()),
		EachSide:         e.GetEachSide(),
	}, nil
}

func exerciseToProto(e *plans.Exercise) *plansv1.Exercise {
	toProto := func(ms []plans.Muscle) []plansv1.Muscle {
		result := make([]plansv1.Muscle, len(ms))
		for i, m := range ms {
			result[i] = musclesToProto[m]
		}
		return result
	}
	return plansv1.Exercise_builder{
		Id:               store.UUIDToString(e.ID),
		Name:             e.Name,
		Instructions:     e.Instructions,
		Feel:             e.Feel,
		PrimaryMuscles:   toProto(e.PrimaryMuscles),
		SecondaryMuscles: toProto(e.SecondaryMuscles),
		Equipment:        e.Equipment,
		EachSide:         e.EachSide,
		CreateTime:       timestamppb.New(e.CreateTime),
		UpdateTime:       timestamppb.New(e.UpdateTime),
	}.Build()
}

func (s *ExerciseService) toConnectError(ctx context.Context, err error, msg string) error {
	var connectErr *connect.Error
	switch {
	case errors.As(err, &connectErr):
		return err
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, "not found")
	case errors.Is(err, plans.ErrExerciseInUse):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	default:
		s.report.Report(ctx, msg, err)
		return connect.NewError(connect.CodeInternal, msg).WithCause(err)
	}
}
