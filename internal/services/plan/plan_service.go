// Package plan implements the TrainingPlanService.
package plan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	"github.com/johanbrandhorst/trail-tools/internal/services/activity"
	"github.com/johanbrandhorst/trail-tools/internal/services/route"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ plansv1connect.TrainingPlanServiceHandler = (*Service)(nil)

type PlanRepository interface {
	CreateTrainingPlan(ctx context.Context, plan *plans.TrainingPlan) (*plans.TrainingPlan, error)
	GetTrainingPlan(ctx context.Context, userID, planID pgtype.UUID) (*plans.TrainingPlan, error)
	ListTrainingPlans(ctx context.Context, userID pgtype.UUID) ([]*plans.TrainingPlan, error)
	UpdateTrainingPlan(ctx context.Context, plan *plans.TrainingPlan) (*plans.TrainingPlan, error)
	DeleteTrainingPlan(ctx context.Context, userID, planID pgtype.UUID) error
	SetPeriods(ctx context.Context, planID pgtype.UUID, periods []*plans.Period) ([]*plans.Period, error)
	ListPeriods(ctx context.Context, planID pgtype.UUID) ([]*plans.Period, error)
	DeletePeriods(ctx context.Context, planID pgtype.UUID, ids []pgtype.UUID) error
	CreateWorkouts(ctx context.Context, planID pgtype.UUID, workouts []*plans.Workout) ([]*plans.Workout, error)
	ListWorkouts(ctx context.Context, planID pgtype.UUID, startDate, endDate time.Time) ([]*plans.Workout, error)
	ListUserWorkouts(ctx context.Context, userID pgtype.UUID, startDate, endDate time.Time) ([]*plans.Workout, error)
	GetWorkout(ctx context.Context, userID, workoutID pgtype.UUID) (*plans.Workout, error)
	UpdateWorkout(ctx context.Context, w *plans.Workout) (*plans.Workout, error)
	DeleteWorkouts(ctx context.Context, planID pgtype.UUID, ids []pgtype.UUID) error
	FindReferences(ctx context.Context, userID pgtype.UUID, routeIDs, activityIDs []pgtype.UUID) (routes, activities map[pgtype.UUID]bool, err error)
	RacesExist(ctx context.Context, userID pgtype.UUID, raceIDs []pgtype.UUID) (bool, error)
}

// Service implements API handlers for the training plan service.
type Service struct {
	log        *slog.Logger
	report     *errreport.Reporter
	plans      PlanRepository
	routes     RouteSource
	activities ActivitySource
	weather    WeatherSource
}

// NewService creates a new Service. The routes, activities and weather
// forecast the weather of workouts.
func NewService(log *slog.Logger, report *errreport.Reporter, plans PlanRepository, routes RouteSource, activities ActivitySource, weather WeatherSource) *Service {
	return &Service{
		log:        log,
		report:     report,
		plans:      plans,
		routes:     routes,
		activities: activities,
		weather:    weather,
	}
}

const (
	maxPlanDays        = 366
	maxWorkoutsPerCall = 400
)

// invalidArgument is a convenience for returning InvalidArgument errors.
func invalidArgument(format string, args ...any) error {
	return connect.Errorf(connect.CodeInvalidArgument, format, args...)
}

func (s *Service) CreateTrainingPlan(ctx context.Context, req *plansv1.CreateTrainingPlanRequest) (*plansv1.CreateTrainingPlanResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	plan, err := s.planFromProto(ctx, userID, req.GetPlan())
	if err != nil {
		return nil, err
	}
	created, err := s.plans.CreateTrainingPlan(ctx, plan)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to create training plan")
	}
	return plansv1.CreateTrainingPlanResponse_builder{Plan: planToProto(created)}.Build(), nil
}

func (s *Service) GetTrainingPlan(ctx context.Context, req *plansv1.GetTrainingPlanRequest) (*plansv1.GetTrainingPlanResponse, error) {
	plan, err := s.getPlan(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	periods, err := s.plans.ListPeriods(ctx, plan.ID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list periods")
	}
	protoPeriods := make([]*plansv1.PlanPeriod, len(periods))
	for i, p := range periods {
		protoPeriods[i] = periodToProto(p)
	}
	return plansv1.GetTrainingPlanResponse_builder{
		Plan:    planToProto(plan),
		Periods: protoPeriods,
	}.Build(), nil
}

func (s *Service) ListTrainingPlans(ctx context.Context, req *plansv1.ListTrainingPlansRequest) (*plansv1.ListTrainingPlansResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	list, err := s.plans.ListTrainingPlans(ctx, userID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list training plans")
	}
	protoPlans := make([]*plansv1.TrainingPlan, len(list))
	for i, p := range list {
		protoPlans[i] = planToProto(p)
	}
	return plansv1.ListTrainingPlansResponse_builder{Plans: protoPlans}.Build(), nil
}

func (s *Service) UpdateTrainingPlan(ctx context.Context, req *plansv1.UpdateTrainingPlanRequest) (*plansv1.UpdateTrainingPlanResponse, error) {
	existing, err := s.getPlan(ctx, req.GetPlan().GetId())
	if err != nil {
		return nil, err
	}
	merged := planToProto(existing)
	if err := protomask.Apply(merged, req.GetPlan(), req.GetUpdateMask(), "id", "create_time", "update_time"); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	plan, err := s.planFromProto(ctx, existing.UserID, merged)
	if err != nil {
		return nil, err
	}
	plan.ID = existing.ID
	updated, err := s.plans.UpdateTrainingPlan(ctx, plan)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to update training plan")
	}
	return plansv1.UpdateTrainingPlanResponse_builder{Plan: planToProto(updated)}.Build(), nil
}

func (s *Service) DeleteTrainingPlan(ctx context.Context, req *plansv1.DeleteTrainingPlanRequest) (*plansv1.DeleteTrainingPlanResponse, error) {
	plan, err := s.getPlan(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.plans.DeleteTrainingPlan(ctx, plan.UserID, plan.ID); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete training plan")
	}
	return plansv1.DeleteTrainingPlanResponse_builder{}.Build(), nil
}

func (s *Service) SetPlanPeriods(ctx context.Context, req *plansv1.SetPlanPeriodsRequest) (*plansv1.SetPlanPeriodsResponse, error) {
	plan, err := s.getPlan(ctx, req.GetPlanId())
	if err != nil {
		return nil, err
	}
	if len(req.GetPeriods()) == 0 {
		return nil, invalidArgument("at least one period is required")
	}
	periods := make([]*plans.Period, len(req.GetPeriods()))
	for i, p := range req.GetPeriods() {
		period, err := periodFromProto(p, plan)
		if err != nil {
			return nil, invalidArgument("period %d: %v", i+1, err)
		}
		periods[i] = period
	}
	set, err := s.plans.SetPeriods(ctx, plan.ID, periods)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to set periods")
	}
	protoPeriods := make([]*plansv1.PlanPeriod, len(set))
	for i, p := range set {
		protoPeriods[i] = periodToProto(p)
	}
	return plansv1.SetPlanPeriodsResponse_builder{Periods: protoPeriods}.Build(), nil
}

func (s *Service) DeletePlanPeriods(ctx context.Context, req *plansv1.DeletePlanPeriodsRequest) (*plansv1.DeletePlanPeriodsResponse, error) {
	plan, err := s.getPlan(ctx, req.GetPlanId())
	if err != nil {
		return nil, err
	}
	ids, err := store.UUIDsToPG(req.GetIds())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if err := s.plans.DeletePeriods(ctx, plan.ID, ids); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete periods")
	}
	return plansv1.DeletePlanPeriodsResponse_builder{}.Build(), nil
}

func (s *Service) ListWorkouts(ctx context.Context, req *plansv1.ListWorkoutsRequest) (*plansv1.ListWorkoutsResponse, error) {
	var (
		startDate, endDate time.Time
		err                error
	)
	if req.GetStartDate() != "" {
		if startDate, err = store.ParseDate(req.GetStartDate()); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
	}
	if req.GetEndDate() != "" {
		if endDate, err = store.ParseDate(req.GetEndDate()); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
	}
	var workouts []*plans.Workout
	if req.GetPlanId() == "" {
		userID, ok := authn.GetUserID(ctx)
		if !ok {
			return nil, connect.NewError(connect.CodeUnauthenticated, "")
		}
		workouts, err = s.plans.ListUserWorkouts(ctx, userID, startDate, endDate)
	} else {
		plan, planErr := s.getPlan(ctx, req.GetPlanId())
		if planErr != nil {
			return nil, planErr
		}
		workouts, err = s.plans.ListWorkouts(ctx, plan.ID, startDate, endDate)
	}
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list workouts")
	}
	protoWorkouts := make([]*plansv1.Workout, len(workouts))
	for i, w := range workouts {
		protoWorkouts[i] = workoutToProto(w)
	}
	return plansv1.ListWorkoutsResponse_builder{Workouts: protoWorkouts}.Build(), nil
}

func (s *Service) CreateWorkouts(ctx context.Context, req *plansv1.CreateWorkoutsRequest) (*plansv1.CreateWorkoutsResponse, error) {
	plan, err := s.getPlan(ctx, req.GetPlanId())
	if err != nil {
		return nil, err
	}
	n := len(req.GetWorkouts())
	if n == 0 || n > maxWorkoutsPerCall {
		return nil, invalidArgument("between 1 and %d workouts are required", maxWorkoutsPerCall)
	}
	workouts := make([]*plans.Workout, n)
	for i, w := range req.GetWorkouts() {
		workout, err := workoutFromProto(w, plan)
		if err != nil {
			return nil, prefixError(fmt.Sprintf("workout %d", i+1), err)
		}
		workouts[i] = workout
	}
	if err := s.checkReferences(ctx, plan.UserID, workouts); err != nil {
		return nil, err
	}
	created, err := s.plans.CreateWorkouts(ctx, plan.ID, workouts)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to create workouts")
	}
	protoWorkouts := make([]*plansv1.Workout, len(created))
	for i, w := range created {
		protoWorkouts[i] = workoutToProto(w)
	}
	return plansv1.CreateWorkoutsResponse_builder{Workouts: protoWorkouts}.Build(), nil
}

func (s *Service) GetWorkout(ctx context.Context, req *plansv1.GetWorkoutRequest) (*plansv1.GetWorkoutResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	workoutID, err := store.StringToUUID(req.GetId())
	if err != nil {
		return nil, invalidArgument("invalid workout ID: %v", err)
	}
	workout, err := s.plans.GetWorkout(ctx, userID, workoutID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get workout")
	}
	return plansv1.GetWorkoutResponse_builder{Workout: workoutToProto(workout)}.Build(), nil
}

func (s *Service) UpdateWorkout(ctx context.Context, req *plansv1.UpdateWorkoutRequest) (*plansv1.UpdateWorkoutResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	workoutID, err := store.StringToUUID(req.GetWorkout().GetId())
	if err != nil {
		return nil, invalidArgument("invalid workout ID: %v", err)
	}
	existing, err := s.plans.GetWorkout(ctx, userID, workoutID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get workout")
	}
	plan, err := s.plans.GetTrainingPlan(ctx, userID, existing.PlanID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get training plan")
	}
	merged := workoutToProto(existing)
	if err := protomask.Apply(merged, req.GetWorkout(), req.GetUpdateMask(), "id", "plan_id"); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	workout, err := workoutFromProto(merged, plan)
	if err != nil {
		return nil, err
	}
	if err := s.checkReferences(ctx, plan.UserID, []*plans.Workout{workout}); err != nil {
		return nil, err
	}
	workout.ID = existing.ID
	updated, err := s.plans.UpdateWorkout(ctx, workout)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to update workout")
	}
	return plansv1.UpdateWorkoutResponse_builder{Workout: workoutToProto(updated)}.Build(), nil
}

func (s *Service) DeleteWorkouts(ctx context.Context, req *plansv1.DeleteWorkoutsRequest) (*plansv1.DeleteWorkoutsResponse, error) {
	plan, err := s.getPlan(ctx, req.GetPlanId())
	if err != nil {
		return nil, err
	}
	ids, err := store.UUIDsToPG(req.GetIds())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if err := s.plans.DeleteWorkouts(ctx, plan.ID, ids); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete workouts")
	}
	return plansv1.DeleteWorkoutsResponse_builder{}.Build(), nil
}

// getPlan gets one of the authenticated user's plans by ID.
func (s *Service) getPlan(ctx context.Context, id string) (*plans.TrainingPlan, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	planID, err := store.StringToUUID(id)
	if err != nil {
		return nil, invalidArgument("invalid plan ID: %v", err)
	}
	plan, err := s.plans.GetTrainingPlan(ctx, userID, planID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get training plan")
	}
	return plan, nil
}

func (s *Service) planFromProto(ctx context.Context, userID pgtype.UUID, p *plansv1.TrainingPlan) (*plans.TrainingPlan, error) {
	name := strings.TrimSpace(p.GetName())
	if name == "" {
		return nil, invalidArgument("name is required")
	}
	startDate, err := store.ParseDate(p.GetStartDate())
	if err != nil {
		return nil, invalidArgument("start_date: %v", err)
	}
	endDate, err := store.ParseDate(p.GetEndDate())
	if err != nil {
		return nil, invalidArgument("end_date: %v", err)
	}
	if endDate.Before(startDate) {
		return nil, invalidArgument("end_date must not be before start_date")
	}
	if endDate.Sub(startDate) > maxPlanDays*24*time.Hour {
		return nil, invalidArgument("a plan can be at most %d days long", maxPlanDays)
	}
	raceIDs, err := store.UUIDsToPG(p.GetGoalRaceIds())
	if err != nil {
		return nil, invalidArgument("goal_race_ids: %v", err)
	}
	exist, err := s.plans.RacesExist(ctx, userID, raceIDs)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to check races")
	}
	if !exist {
		return nil, invalidArgument("goal_race_ids: not all races exist; create them with CreateRace first")
	}
	return &plans.TrainingPlan{
		UserID:      userID,
		Name:        name,
		Description: p.GetDescription(),
		StartDate:   startDate,
		EndDate:     endDate,
		GoalRaceIDs: raceIDs,
	}, nil
}

func periodFromProto(p *plansv1.PlanPeriod, plan *plans.TrainingPlan) (*plans.Period, error) {
	date, err := store.ParseDate(p.GetStartDate())
	if err != nil {
		return nil, err
	}
	var granularity plans.Granularity
	switch p.GetGranularity() {
	case plansv1.PeriodGranularity_PERIOD_GRANULARITY_MONTH:
		granularity = plans.GranularityMonth
		date = time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, time.UTC)
		if date.AddDate(0, 1, -1).Before(plan.StartDate) || date.After(plan.EndDate) {
			return nil, fmt.Errorf("month %s is outside the plan", date.Format("2006-01"))
		}
	case plansv1.PeriodGranularity_PERIOD_GRANULARITY_WEEK:
		granularity = plans.GranularityWeek
		date = activity.WeekStart(date)
		if date.AddDate(0, 0, 6).Before(plan.StartDate) || date.After(plan.EndDate) {
			return nil, fmt.Errorf("week of %s is outside the plan", store.FormatDate(date))
		}
	default:
		return nil, errors.New("granularity must be MONTH or WEEK")
	}
	if err := validateTargets(p.GetTargetDistanceMeters(), p.GetTargetElevationGainMeters(), p.GetTargetDurationSeconds()); err != nil {
		return nil, err
	}
	return &plans.Period{
		Granularity:               granularity,
		StartDate:                 date,
		Title:                     strings.TrimSpace(p.GetTitle()),
		Notes:                     p.GetNotes(),
		TargetDistanceMeters:      p.GetTargetDistanceMeters(),
		TargetElevationGainMeters: p.GetTargetElevationGainMeters(),
		TargetDurationSeconds:     p.GetTargetDurationSeconds(),
	}, nil
}

// workoutFromProto converts and validates a workout of a plan. The caller
// must check its references with checkReferences.
func workoutFromProto(w *plansv1.Workout, plan *plans.TrainingPlan) (*plans.Workout, error) {
	date, err := store.ParseDate(w.GetDate())
	if err != nil {
		return nil, invalidArgument("%v", err)
	}
	if date.Before(plan.StartDate) || date.After(plan.EndDate) {
		return nil, invalidArgument("date %s is outside the plan (%s to %s)", store.FormatDate(date), store.FormatDate(plan.StartDate), store.FormatDate(plan.EndDate))
	}
	workoutType, ok := workoutTypeFromProto(w.GetType())
	if !ok {
		return nil, invalidArgument("type is required")
	}
	if err := validateTargets(w.GetTargetDistanceMeters(), w.GetTargetElevationGainMeters(), w.GetTargetDurationSeconds()); err != nil {
		return nil, invalidArgument("%v", err)
	}
	workout := &plans.Workout{
		Date:                      date,
		Type:                      workoutType,
		Title:                     strings.TrimSpace(w.GetTitle()),
		Description:               w.GetDescription(),
		TargetDistanceMeters:      w.GetTargetDistanceMeters(),
		TargetElevationGainMeters: w.GetTargetElevationGainMeters(),
		TargetDurationSeconds:     w.GetTargetDurationSeconds(),
		TargetTerrain:             route.TerrainFromProto(w.GetTargetTerrain()),
	}
	if w.GetRouteId() != "" {
		if workout.RouteID, err = store.StringToUUID(w.GetRouteId()); err != nil {
			return nil, invalidArgument("invalid route_id: %v", err)
		}
	}
	if w.GetCompletedActivityId() != "" {
		if workout.CompletedActivityID, err = store.StringToUUID(w.GetCompletedActivityId()); err != nil {
			return nil, invalidArgument("invalid completed_activity_id: %v", err)
		}
	}
	return workout, nil
}

// checkReferences checks that the routes and activities of workouts exist and
// belong to the user. It checks them all in one query, as a plan can be
// created with hundreds of workouts at once.
func (s *Service) checkReferences(ctx context.Context, userID pgtype.UUID, workouts []*plans.Workout) error {
	var routeIDs, activityIDs []pgtype.UUID
	for _, w := range workouts {
		if w.RouteID.Valid {
			routeIDs = append(routeIDs, w.RouteID)
		}
		if w.CompletedActivityID.Valid {
			activityIDs = append(activityIDs, w.CompletedActivityID)
		}
	}
	routes, activities, err := s.plans.FindReferences(ctx, userID, routeIDs, activityIDs)
	if err != nil {
		return s.toConnectError(ctx, err, "failed to check routes and activities")
	}
	for i, w := range workouts {
		prefix := ""
		if len(workouts) > 1 {
			prefix = fmt.Sprintf("workout %d: ", i+1)
		}
		if w.RouteID.Valid && !routes[w.RouteID] {
			return invalidArgument("%sroute %s does not exist", prefix, store.UUIDToString(w.RouteID))
		}
		if w.CompletedActivityID.Valid && !activities[w.CompletedActivityID] {
			return invalidArgument("%sactivity %s does not exist", prefix, store.UUIDToString(w.CompletedActivityID))
		}
	}
	return nil
}

func validateTargets(distance, elevation float64, duration int32) error {
	if distance < 0 || elevation < 0 || duration < 0 {
		return errors.New("targets must not be negative")
	}
	return nil
}

func planToProto(p *plans.TrainingPlan) *plansv1.TrainingPlan {
	raceIDs := make([]string, len(p.GoalRaceIDs))
	for i, id := range p.GoalRaceIDs {
		raceIDs[i] = store.UUIDToString(id)
	}
	return plansv1.TrainingPlan_builder{
		Id:          store.UUIDToString(p.ID),
		Name:        p.Name,
		Description: p.Description,
		StartDate:   store.FormatDate(p.StartDate),
		EndDate:     store.FormatDate(p.EndDate),
		GoalRaceIds: raceIDs,
		CreateTime:  timestamppb.New(p.CreateTime),
		UpdateTime:  timestamppb.New(p.UpdateTime),
	}.Build()
}

func periodToProto(p *plans.Period) *plansv1.PlanPeriod {
	granularity := plansv1.PeriodGranularity_PERIOD_GRANULARITY_WEEK
	if p.Granularity == plans.GranularityMonth {
		granularity = plansv1.PeriodGranularity_PERIOD_GRANULARITY_MONTH
	}
	return plansv1.PlanPeriod_builder{
		Id:                        store.UUIDToString(p.ID),
		Granularity:               granularity,
		StartDate:                 store.FormatDate(p.StartDate),
		Title:                     p.Title,
		Notes:                     p.Notes,
		TargetDistanceMeters:      p.TargetDistanceMeters,
		TargetElevationGainMeters: p.TargetElevationGainMeters,
		TargetDurationSeconds:     p.TargetDurationSeconds,
	}.Build()
}

func workoutToProto(w *plans.Workout) *plansv1.Workout {
	return plansv1.Workout_builder{
		Id:                        store.UUIDToString(w.ID),
		PlanId:                    store.UUIDToString(w.PlanID),
		Date:                      store.FormatDate(w.Date),
		Type:                      workoutTypeToProto(w.Type),
		Title:                     w.Title,
		Description:               w.Description,
		TargetDistanceMeters:      w.TargetDistanceMeters,
		TargetElevationGainMeters: w.TargetElevationGainMeters,
		TargetDurationSeconds:     w.TargetDurationSeconds,
		TargetTerrain:             route.TerrainToProto(w.TargetTerrain),
		RouteId:                   store.UUIDToString(w.RouteID),
		CompletedActivityId:       store.UUIDToString(w.CompletedActivityID),
	}.Build()
}

// prefixError prefixes the message of a connect error.
func prefixError(prefix string, err error) error {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connect.Errorf(connectErr.Code(), "%s: %s", prefix, connectErr.Message())
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func (s *Service) toConnectError(ctx context.Context, err error, msg string) error {
	var connectErr *connect.Error
	switch {
	case errors.As(err, &connectErr):
		return err
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, "not found")
	case errors.As(err, new(*plans.OutsideDatesError)):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	default:
		s.report.Report(ctx, msg, err)
		return connect.NewError(connect.CodeInternal, msg).WithCause(err)
	}
}
