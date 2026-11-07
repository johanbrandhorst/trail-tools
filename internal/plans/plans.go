// Package plans stores training plans, their month and week periods, and
// workouts.
package plans

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/plans/internal/gen"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// Repository stores training plans.
type Repository struct {
	log *slog.Logger
	db  *store.DB
	q   *gen.Queries
}

// NewRepository creates a new Repository from the provided logger and database.
func NewRepository(log *slog.Logger, db *store.DB) (*Repository, error) {
	return &Repository{
		log: log,
		db:  db,
		q:   gen.New(db),
	}, nil
}

// TrainingPlan is a training plan.
type TrainingPlan struct {
	ID          pgtype.UUID
	UserID      pgtype.UUID
	Name        string
	Description string
	StartDate   time.Time
	EndDate     time.Time
	GoalRaceIDs []pgtype.UUID
	CreateTime  time.Time
	UpdateTime  time.Time
}

// Granularity is the length of a plan period.
type Granularity string

// Period granularities.
const (
	GranularityMonth Granularity = "month"
	GranularityWeek  Granularity = "week"
)

// Period is a month or week in a training plan.
type Period struct {
	ID                        pgtype.UUID
	PlanID                    pgtype.UUID
	Granularity               Granularity
	StartDate                 time.Time
	Title                     string
	Notes                     string
	TargetDistanceMeters      float64
	TargetElevationGainMeters float64
	TargetDurationSeconds     int32
}

// WorkoutType is the type of a workout.
type WorkoutType string

// Workout types.
const (
	WorkoutTypeBase          WorkoutType = "base"
	WorkoutTypeIntensity     WorkoutType = "intensity"
	WorkoutTypeCrossTraining WorkoutType = "cross_training"
	WorkoutTypeStrength      WorkoutType = "strength"
	WorkoutTypeRace          WorkoutType = "race"
	WorkoutTypeRest          WorkoutType = "rest"
)

// Workout is a workout planned for a day.
type Workout struct {
	ID                        pgtype.UUID
	PlanID                    pgtype.UUID
	Date                      time.Time
	Type                      WorkoutType
	Title                     string
	Description               string
	TargetDistanceMeters      float64
	TargetElevationGainMeters float64
	TargetDurationSeconds     int32
	// Unknown if there is no target terrain.
	TargetTerrain geo.Terrain
	// Invalid if the workout has no route.
	RouteID pgtype.UUID
	// Invalid if the workout has not been completed.
	CompletedActivityID pgtype.UUID
	// The earliest local time to start, "HH:MM". Empty for any time of the
	// day.
	StartTime string
	// How long the start window is, 0 if there's no start time.
	StartWindowMinutes int32
	// Where the workout starts, if set, like a group run's meeting point,
	// without elevation.
	Location     *geo.Point
	LocationName string
	// The structure of a run, in order. Nil for runs without structure and
	// other workouts.
	RunBlocks []RunBlock
	// Whether a run is a lactate test, run in stages of rising heart rate.
	LactateTest bool
	// What to eat and drink during a run. Nil if it has no fueling plan.
	RunFueling *RunFueling
	// The user's sweat profile, read with the workout, for the sodium of
	// runs. Nil if they haven't set one.
	Sweat *SweatProfile
	// The token of the workout's share link, empty if it isn't shared.
	ShareToken string
	// The exercises of a strength workout, in order. Nil for other types.
	Exercises []*PlannedExercise
	// The exercises the user lacks equipment for, computed when reading.
	EquipmentIssues []EquipmentIssue
	// The log of a completed strength workout. Nil if it has none.
	Log *StrengthLog
}

// CreateTrainingPlan stores a new training plan.
func (r *Repository) CreateTrainingPlan(ctx context.Context, plan *TrainingPlan) (*TrainingPlan, error) {
	var created *TrainingPlan
	err := r.withGoalRacesOnCalendar(ctx, plan, func(q *gen.Queries) error {
		dbPlan, err := q.CreateTrainingPlan(ctx, &gen.CreateTrainingPlanParams{
			UserID:      plan.UserID,
			Name:        plan.Name,
			Description: plan.Description,
			StartDate:   store.DateToPG(plan.StartDate),
			EndDate:     store.DateToPG(plan.EndDate),
			GoalRaceIds: nonNil(plan.GoalRaceIDs),
		})
		if err != nil {
			return fmt.Errorf("failed to create training plan: %w", err)
		}
		created = planFromDB(dbPlan)
		return nil
	})
	return created, err
}

// withGoalRacesOnCalendar runs fn, which stores the plan, in a transaction
// that also adds the plan's goal races to the user's calendar, as the user
// is training to run them.
func (r *Repository) withGoalRacesOnCalendar(ctx context.Context, plan *TrainingPlan, fn func(q *gen.Queries) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := r.q.WithTx(tx)
	if err := fn(q); err != nil {
		return err
	}
	if len(plan.GoalRaceIDs) > 0 {
		if err := q.AddRacesToCalendar(ctx, &gen.AddRacesToCalendarParams{UserID: plan.UserID, Ids: plan.GoalRaceIDs}); err != nil {
			return fmt.Errorf("failed to add goal races to the calendar: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// GetTrainingPlan gets one of the user's training plans.
func (r *Repository) GetTrainingPlan(ctx context.Context, userID, planID pgtype.UUID) (*TrainingPlan, error) {
	dbPlan, err := r.q.GetTrainingPlan(ctx, &gen.GetTrainingPlanParams{ID: planID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get training plan: %w", err)
	}
	return planFromDB(dbPlan), nil
}

// ListTrainingPlans lists the user's training plans, most recent start date
// first.
func (r *Repository) ListTrainingPlans(ctx context.Context, userID pgtype.UUID) ([]*TrainingPlan, error) {
	dbPlans, err := r.q.ListTrainingPlans(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list training plans: %w", err)
	}
	result := make([]*TrainingPlan, len(dbPlans))
	for i, p := range dbPlans {
		result[i] = planFromDB(p)
	}
	return result, nil
}

// OutsideDatesError is returned when changing a plan's dates would leave some
// of its workouts or periods outside them.
type OutsideDatesError struct {
	// The dates of the workouts outside, in date order.
	WorkoutDates []time.Time
	// The start dates of the weeks and months outside, in date order.
	WeekStartDates, MonthStartDates []time.Time
}

// maxOutsideDates is how many dates of each kind an OutsideDatesError
// names, to keep the message short when a plan is shortened a lot.
const maxOutsideDates = 5

func (e *OutsideDatesError) Error() string {
	var parts []string
	for _, kind := range []struct {
		one, many string
		dates     []time.Time
	}{
		{"workout", "workouts", e.WorkoutDates},
		{"week", "weeks", e.WeekStartDates},
		{"month", "months", e.MonthStartDates},
	} {
		if len(kind.dates) == 0 {
			continue
		}
		var dates []string
		for _, date := range kind.dates[:min(len(kind.dates), maxOutsideDates)] {
			dates = append(dates, store.FormatDate(date))
		}
		if len(kind.dates) > maxOutsideDates {
			dates = append(dates, "...")
		}
		name := kind.many
		if len(kind.dates) == 1 {
			name = kind.one
		}
		parts = append(parts, fmt.Sprintf("%d %s (%s)", len(kind.dates), name, strings.Join(dates, ", ")))
	}
	return fmt.Sprintf("the plan has %s outside the new dates; move or delete them first, or choose dates that include them", strings.Join(parts, " and "))
}

// UpdateTrainingPlan replaces a training plan's details. If the plan's
// workouts or periods would be outside its new dates, it returns an
// *OutsideDatesError and leaves the plan unchanged: they could no longer be
// updated, as their dates must be within the plan.
func (r *Repository) UpdateTrainingPlan(ctx context.Context, plan *TrainingPlan) (*TrainingPlan, error) {
	var updated *TrainingPlan
	err := r.withGoalRacesOnCalendar(ctx, plan, func(q *gen.Queries) error {
		dbPlan, err := q.UpdateTrainingPlan(ctx, &gen.UpdateTrainingPlanParams{
			ID:          plan.ID,
			UserID:      plan.UserID,
			Name:        plan.Name,
			Description: plan.Description,
			StartDate:   store.DateToPG(plan.StartDate),
			EndDate:     store.DateToPG(plan.EndDate),
			GoalRaceIds: nonNil(plan.GoalRaceIDs),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return store.ErrNotFound
			}
			return fmt.Errorf("failed to update training plan: %w", err)
		}
		// Updating the plan locks it, so workouts and periods can't be added
		// outside the new dates after checking.
		outside, err := q.ListDatesOutsidePlan(ctx, &gen.ListDatesOutsidePlanParams{
			PlanID:    plan.ID,
			StartDate: store.DateToPG(plan.StartDate),
			EndDate:   store.DateToPG(plan.EndDate),
		})
		if err != nil {
			return fmt.Errorf("failed to check workouts and periods: %w", err)
		}
		if len(outside) > 0 {
			var outsideErr OutsideDatesError
			for _, row := range outside {
				date := store.DateFromPG(row.Date)
				switch Granularity(row.Kind) {
				case GranularityWeek:
					outsideErr.WeekStartDates = append(outsideErr.WeekStartDates, date)
				case GranularityMonth:
					outsideErr.MonthStartDates = append(outsideErr.MonthStartDates, date)
				default:
					outsideErr.WorkoutDates = append(outsideErr.WorkoutDates, date)
				}
			}
			return &outsideErr
		}
		updated = planFromDB(dbPlan)
		return nil
	})
	return updated, err
}

// DeleteTrainingPlan deletes a training plan with its periods and workouts.
func (r *Repository) DeleteTrainingPlan(ctx context.Context, userID, planID pgtype.UUID) error {
	n, err := r.q.DeleteTrainingPlan(ctx, &gen.DeleteTrainingPlanParams{ID: planID, UserID: userID})
	if err != nil {
		return fmt.Errorf("failed to delete training plan: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// SetPeriods creates or replaces periods of a plan, identified by their
// granularity and start date. The caller must check the plan belongs to the
// user.
func (r *Repository) SetPeriods(ctx context.Context, planID pgtype.UUID, periods []*Period) ([]*Period, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := r.q.WithTx(tx)
	result := make([]*Period, len(periods))
	for i, p := range periods {
		dbPeriod, err := q.UpsertPlanPeriod(ctx, &gen.UpsertPlanPeriodParams{
			PlanID:                    planID,
			Granularity:               gen.PeriodGranularity(p.Granularity),
			StartDate:                 store.DateToPG(p.StartDate),
			Title:                     p.Title,
			Notes:                     p.Notes,
			TargetDistanceMeters:      p.TargetDistanceMeters,
			TargetElevationGainMeters: p.TargetElevationGainMeters,
			TargetDurationSeconds:     p.TargetDurationSeconds,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to set period: %w", err)
		}
		result[i] = periodFromDB(dbPeriod)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return result, nil
}

// ListPeriods lists a plan's periods in date order. The caller must check the
// plan belongs to the user.
func (r *Repository) ListPeriods(ctx context.Context, planID pgtype.UUID) ([]*Period, error) {
	dbPeriods, err := r.q.ListPlanPeriods(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("failed to list periods: %w", err)
	}
	result := make([]*Period, len(dbPeriods))
	for i, p := range dbPeriods {
		result[i] = periodFromDB(p)
	}
	return result, nil
}

// DeletePeriods deletes periods of a plan, and returns how many it deleted.
// The caller must check the plan belongs to the user.
func (r *Repository) DeletePeriods(ctx context.Context, planID pgtype.UUID, ids []pgtype.UUID) (int, error) {
	n, err := r.q.DeletePlanPeriods(ctx, &gen.DeletePlanPeriodsParams{PlanID: planID, Ids: ids})
	if err != nil {
		return 0, fmt.Errorf("failed to delete periods: %w", err)
	}
	return int(n), nil
}

// CreateWorkouts creates workouts in a plan with their exercises, in a
// single round trip. The caller must check the plan belongs to the user.
func (r *Repository) CreateWorkouts(ctx context.Context, userID, planID pgtype.UUID, workouts []*Workout) ([]*Workout, error) {
	params := &gen.CreateWorkoutsParams{PlanID: planID}
	for _, w := range workouts {
		// The IDs are assigned here to return the workouts in the order
		// given, which returning doesn't promise, and to insert their
		// exercises in the same batch.
		w.ID = store.NewUUID()
		params.Ids = append(params.Ids, w.ID)
		params.Dates = append(params.Dates, store.DateToPG(w.Date))
		params.Types = append(params.Types, string(w.Type))
		params.Titles = append(params.Titles, w.Title)
		params.Descriptions = append(params.Descriptions, w.Description)
		params.TargetDistancesMeters = append(params.TargetDistancesMeters, w.TargetDistanceMeters)
		params.TargetElevationGainsMeters = append(params.TargetElevationGainsMeters, w.TargetElevationGainMeters)
		params.TargetDurationsSeconds = append(params.TargetDurationsSeconds, w.TargetDurationSeconds)
		params.TargetTerrains = append(params.TargetTerrains, string(terrainToDB(w.TargetTerrain).Terrain))
		params.RouteIds = append(params.RouteIds, w.RouteID)
		params.CompletedActivityIds = append(params.CompletedActivityIds, w.CompletedActivityID)
		params.StartTimes = append(params.StartTimes, startTimeToDB(w.StartTime))
		params.StartWindowsMinutes = append(params.StartWindowsMinutes, w.StartWindowMinutes)
		var location geo.Point
		if w.Location != nil {
			location = *w.Location
		}
		params.HasLocations = append(params.HasLocations, w.Location != nil)
		params.LocationLats = append(params.LocationLats, location.Lat)
		params.LocationLngs = append(params.LocationLngs, location.Lng)
		params.LocationNames = append(params.LocationNames, w.LocationName)
		params.RunBlocks = append(params.RunBlocks, runBlocksToDB(w.RunBlocks))
		params.LactateTests = append(params.LactateTests, w.LactateTest)
		params.RunFuelings = append(params.RunFuelings, runFuelingToDB(w.RunFueling))
	}
	var wr workoutReader
	err := r.db.Transact(ctx, func(b *store.Batch) error {
		b.Read(func(db store.Querier) error {
			var err error
			wr.rows, err = gen.New(db).CreateWorkouts(ctx, params)
			return err
		})
		if err := queueReplaceExercises(ctx, gen.New(b), workouts, false); err != nil {
			return err
		}
		wr.queue(ctx, b, workoutFilter{userID: userID, planID: planID, workoutIDs: params.Ids})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create workouts: %w", err)
	}
	byID := map[pgtype.UUID]*Workout{}
	for _, w := range wr.workouts() {
		byID[w.ID] = w
	}
	result := make([]*Workout, len(params.Ids))
	for i, id := range params.Ids {
		w, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("created workout %s wasn't returned", store.UUIDToString(id))
		}
		result[i] = w
	}
	return result, nil
}

// ListWorkouts lists a plan's workouts with their exercises and logs in an
// inclusive date range, in date order, in a single round trip. Zero dates
// are unbounded. The caller must check the plan belongs to the user.
func (r *Repository) ListWorkouts(ctx context.Context, userID, planID pgtype.UUID, startDate, endDate time.Time) ([]*Workout, error) {
	return r.readWorkouts(ctx, workoutFilter{userID: userID, planID: planID, startDate: startDate, endDate: endDate}, func(q *gen.Queries) ([]*gen.Workout, error) {
		return q.ListWorkouts(ctx, &gen.ListWorkoutsParams{
			PlanID:    planID,
			StartDate: store.DateToPG(startDate),
			EndDate:   store.DateToPG(endDate),
		})
	})
}

// ListUserWorkouts lists the workouts of all the user's plans with their
// exercises and logs in date order, optionally limited to a date range, in a
// single round trip.
func (r *Repository) ListUserWorkouts(ctx context.Context, userID pgtype.UUID, startDate, endDate time.Time) ([]*Workout, error) {
	return r.readWorkouts(ctx, workoutFilter{userID: userID, startDate: startDate, endDate: endDate}, func(q *gen.Queries) ([]*gen.Workout, error) {
		return q.ListUserWorkouts(ctx, &gen.ListUserWorkoutsParams{
			UserID:    userID,
			StartDate: store.DateToPG(startDate),
			EndDate:   store.DateToPG(endDate),
		})
	})
}

// GetWorkout gets one of the user's workouts with its exercises and log.
func (r *Repository) GetWorkout(ctx context.Context, userID, workoutID pgtype.UUID) (*Workout, error) {
	workouts, err := r.readWorkouts(ctx, workoutFilter{userID: userID, workoutIDs: []pgtype.UUID{workoutID}}, func(q *gen.Queries) ([]*gen.Workout, error) {
		w, err := q.GetWorkout(ctx, &gen.GetWorkoutParams{ID: workoutID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return []*gen.Workout{w}, err
	})
	if err != nil {
		return nil, err
	}
	if len(workouts) == 0 {
		return nil, store.ErrNotFound
	}
	return workouts[0], nil
}

// readWorkouts reads workouts, with the exercises and logs of those f
// selects, in a single round trip.
func (r *Repository) readWorkouts(ctx context.Context, f workoutFilter, query func(q *gen.Queries) ([]*gen.Workout, error)) ([]*Workout, error) {
	var wr workoutReader
	err := r.db.ReadSnapshot(ctx, func(b *store.Batch) error {
		b.Read(func(db store.Querier) error {
			var err error
			wr.rows, err = query(gen.New(db))
			return err
		})
		wr.queue(ctx, b, f)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list workouts: %w", err)
	}
	return wr.workouts(), nil
}

// UpdateWorkout replaces one of the user's workouts and its exercises,
// keeping its log, in a single round trip. The caller must check the
// workout belongs to the user.
func (r *Repository) UpdateWorkout(ctx context.Context, userID pgtype.UUID, w *Workout) (*Workout, error) {
	var wr workoutReader
	err := r.db.Transact(ctx, func(b *store.Batch) error {
		b.Read(func(db store.Querier) error {
			row, err := gen.New(db).UpdateWorkout(ctx, &gen.UpdateWorkoutParams{
				ID:                        w.ID,
				Date:                      store.DateToPG(w.Date),
				Type:                      gen.WorkoutType(w.Type),
				Title:                     w.Title,
				Description:               w.Description,
				TargetDistanceMeters:      w.TargetDistanceMeters,
				TargetElevationGainMeters: w.TargetElevationGainMeters,
				TargetDurationSeconds:     w.TargetDurationSeconds,
				TargetTerrain:             terrainToDB(w.TargetTerrain),
				RouteID:                   w.RouteID,
				CompletedActivityID:       w.CompletedActivityID,
				StartTime:                 startTimeToDB(w.StartTime),
				StartWindowMinutes:        w.StartWindowMinutes,
				LocationLat:               locationToDB(w.Location, func(p geo.Point) float64 { return p.Lat }),
				LocationLng:               locationToDB(w.Location, func(p geo.Point) float64 { return p.Lng }),
				LocationName:              w.LocationName,
				RunBlocks:                 runBlocksToDB(w.RunBlocks),
				LactateTest:               w.LactateTest,
				RunFueling:                runFuelingToDB(w.RunFueling),
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return store.ErrNotFound
			}
			wr.rows = []*gen.Workout{row}
			return err
		})
		if err := queueReplaceExercises(ctx, gen.New(b), []*Workout{w}, true); err != nil {
			return err
		}
		wr.queue(ctx, b, workoutFilter{userID: userID, workoutIDs: []pgtype.UUID{w.ID}})
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update workout: %w", err)
	}
	return wr.workouts()[0], nil
}

// DeleteWorkouts deletes workouts of a plan, with their exercises. Their
// logs are kept as the history of their exercises. The caller must check the
// plan belongs to the user.
func (r *Repository) DeleteWorkouts(ctx context.Context, planID pgtype.UUID, ids []pgtype.UUID) error {
	if _, err := r.q.DeleteWorkouts(ctx, &gen.DeleteWorkoutsParams{PlanID: planID, Ids: ids}); err != nil {
		return fmt.Errorf("failed to delete workouts: %w", err)
	}
	return nil
}

// FindReferences returns which of the routes and activities exist and belong
// to the user, in a single query.
func (r *Repository) FindReferences(ctx context.Context, userID pgtype.UUID, routeIDs, activityIDs []pgtype.UUID) (routes, activities map[pgtype.UUID]bool, err error) {
	routes = map[pgtype.UUID]bool{}
	activities = map[pgtype.UUID]bool{}
	if len(routeIDs) == 0 && len(activityIDs) == 0 {
		return routes, activities, nil
	}
	found, err := r.q.FindReferences(ctx, &gen.FindReferencesParams{
		UserID:      userID,
		RouteIds:    nonNil(routeIDs),
		ActivityIds: nonNil(activityIDs),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find routes and activities: %w", err)
	}
	for _, id := range found.RouteIds {
		routes[id] = true
	}
	for _, id := range found.ActivityIds {
		activities[id] = true
	}
	return routes, activities, nil
}

// RacesExist checks whether all the races exist and belong to the user.
func (r *Repository) RacesExist(ctx context.Context, userID pgtype.UUID, raceIDs []pgtype.UUID) (bool, error) {
	if len(raceIDs) == 0 {
		return true, nil
	}
	n, err := r.q.CountRaces(ctx, &gen.CountRacesParams{UserID: userID, Ids: raceIDs})
	if err != nil {
		return false, fmt.Errorf("failed to check races: %w", err)
	}
	unique := map[pgtype.UUID]bool{}
	for _, id := range raceIDs {
		unique[id] = true
	}
	return int(n) == len(unique), nil
}

func nonNil(ids []pgtype.UUID) []pgtype.UUID {
	if ids == nil {
		return []pgtype.UUID{}
	}
	return ids
}

func terrainToDB(t geo.Terrain) gen.NullTerrain {
	if t == geo.TerrainUnknown {
		return gen.NullTerrain{}
	}
	return gen.NullTerrain{Terrain: gen.Terrain(t), Valid: true}
}

func planFromDB(p *gen.TrainingPlan) *TrainingPlan {
	return &TrainingPlan{
		ID:          p.ID,
		UserID:      p.UserID,
		Name:        p.Name,
		Description: p.Description,
		StartDate:   store.DateFromPG(p.StartDate),
		EndDate:     store.DateFromPG(p.EndDate),
		GoalRaceIDs: p.GoalRaceIds,
		CreateTime:  p.CreateTime.Time,
		UpdateTime:  p.UpdateTime.Time,
	}
}

func periodFromDB(p *gen.PlanPeriod) *Period {
	return &Period{
		ID:                        p.ID,
		PlanID:                    p.PlanID,
		Granularity:               Granularity(p.Granularity),
		StartDate:                 store.DateFromPG(p.StartDate),
		Title:                     p.Title,
		Notes:                     p.Notes,
		TargetDistanceMeters:      p.TargetDistanceMeters,
		TargetElevationGainMeters: p.TargetElevationGainMeters,
		TargetDurationSeconds:     p.TargetDurationSeconds,
	}
}

func workoutFromDB(w *gen.Workout) *Workout {
	workout := &Workout{
		ID:                        w.ID,
		PlanID:                    w.PlanID,
		Date:                      store.DateFromPG(w.Date),
		Type:                      WorkoutType(w.Type),
		Title:                     w.Title,
		Description:               w.Description,
		TargetDistanceMeters:      w.TargetDistanceMeters,
		TargetElevationGainMeters: w.TargetElevationGainMeters,
		TargetDurationSeconds:     w.TargetDurationSeconds,
		RouteID:                   w.RouteID,
		CompletedActivityID:       w.CompletedActivityID,
		StartTime:                 startTimeFromDB(w.StartTime),
		StartWindowMinutes:        w.StartWindowMinutes,
		LocationName:              w.LocationName,
		RunBlocks:                 runBlocksFromDB(w.RunBlocks),
		LactateTest:               w.LactateTest,
		RunFueling:                runFuelingFromDB(w.RunFueling),
		ShareToken:                w.ShareToken.String,
	}
	if w.LocationLat.Valid && w.LocationLng.Valid {
		workout.Location = &geo.Point{Lat: w.LocationLat.Float64, Lng: w.LocationLng.Float64}
	}
	if w.TargetTerrain.Valid {
		workout.TargetTerrain = geo.Terrain(w.TargetTerrain.Terrain)
	}
	return workout
}

// locationToDB returns a coordinate of a workout's location, invalid if it
// has none.
func locationToDB(location *geo.Point, coordinate func(geo.Point) float64) pgtype.Float8 {
	if location == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: coordinate(*location), Valid: true}
}

// startTimeToDB converts a local time "HH:MM", which the caller has
// validated, to the database's, invalid if it's empty.
func startTimeToDB(t string) pgtype.Time {
	parsed, err := time.Parse("15:04", t)
	if err != nil {
		return pgtype.Time{}
	}
	return pgtype.Time{Microseconds: int64(parsed.Hour()*60+parsed.Minute()) * int64(time.Minute/time.Microsecond), Valid: true}
}

func startTimeFromDB(t pgtype.Time) string {
	if !t.Valid {
		return ""
	}
	minutes := t.Microseconds / int64(time.Minute/time.Microsecond)
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// ShareWorkout returns the token of the share link of one of the user's
// workouts, creating it if the workout isn't shared yet.
func (r *Repository) ShareWorkout(ctx context.Context, userID, workoutID pgtype.UUID) (string, error) {
	token, err := r.q.ShareWorkout(ctx, &gen.ShareWorkoutParams{ID: workoutID, UserID: userID, NewToken: rand.Text()})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", store.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("failed to share workout: %w", err)
	}
	return token.String, nil
}

// UnshareWorkout removes the share link of one of the user's workouts.
func (r *Repository) UnshareWorkout(ctx context.Context, userID, workoutID pgtype.UUID) error {
	n, err := r.q.UnshareWorkout(ctx, &gen.UnshareWorkoutParams{ID: workoutID, UserID: userID})
	if err != nil {
		return fmt.Errorf("failed to unshare workout: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// GetSharedWorkout returns the workout shared with a token, with the ID of
// the user it belongs to.
func (r *Repository) GetSharedWorkout(ctx context.Context, token string) (*Workout, pgtype.UUID, error) {
	if token == "" {
		return nil, pgtype.UUID{}, store.ErrNotFound
	}
	share, err := r.q.GetWorkoutShare(ctx, pgtype.Text{String: token, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pgtype.UUID{}, store.ErrNotFound
	}
	if err != nil {
		return nil, pgtype.UUID{}, fmt.Errorf("failed to get shared workout: %w", err)
	}
	w, err := r.GetWorkout(ctx, share.UserID, share.ID)
	if err != nil {
		return nil, pgtype.UUID{}, err
	}
	return w, share.UserID, nil
}

// GetCalendarToken returns the secret of the user's calendar feed, empty if
// they have none.
func (r *Repository) GetCalendarToken(ctx context.Context, userID pgtype.UUID) (string, error) {
	token, err := r.q.GetCalendarToken(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("failed to get calendar token: %w", err)
	}
	return token.String, nil
}

// SetCalendarToken sets the secret of the user's calendar feed, replacing
// any earlier one, or removes the feed if token is empty.
func (r *Repository) SetCalendarToken(ctx context.Context, userID pgtype.UUID, token string) error {
	if err := r.q.SetCalendarToken(ctx, &gen.SetCalendarTokenParams{
		UserID: userID,
		Token:  pgtype.Text{String: token, Valid: token != ""},
	}); err != nil {
		return fmt.Errorf("failed to set calendar token: %w", err)
	}
	return nil
}

// GetCalendarFeedUser returns the user of a calendar feed, and the time zone
// of their latest activity, empty if they have none.
func (r *Repository) GetCalendarFeedUser(ctx context.Context, token string) (pgtype.UUID, string, error) {
	if token == "" {
		return pgtype.UUID{}, "", store.ErrNotFound
	}
	row, err := r.q.GetCalendarFeedUser(ctx, pgtype.Text{String: token, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, "", store.ErrNotFound
	}
	if err != nil {
		return pgtype.UUID{}, "", fmt.Errorf("failed to get calendar feed: %w", err)
	}
	return row.ID, row.Timezone, nil
}
