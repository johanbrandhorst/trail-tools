package plans

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/plans/internal/gen"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// Muscle is a muscle or muscle group an exercise works, e.g. "glutes".
type Muscle string

// Muscles are the muscles exercises can work, in the order of the API's
// enum.
var Muscles = []Muscle{
	"neck", "upper_back", "lats", "lower_back", "shoulders", "chest", "biceps",
	"triceps", "forearms", "abs", "obliques", "hip_flexors", "glutes",
	"hip_abductors", "adductors", "quads", "hamstrings", "calves", "shins",
	"feet",
}

// Equipment is a kind of equipment, e.g. "dumbbells".
type Equipment string

// Kinds of equipment whose weights matter, as they come in fixed weights.
const (
	EquipmentDumbbells    Equipment = "dumbbells"
	EquipmentKettlebell   Equipment = "kettlebell"
	EquipmentMedicineBall Equipment = "medicine_ball"
)

// Equipments are the kinds of equipment, in the order of the API's enum.
var Equipments = []Equipment{
	"dumbbells", "kettlebell", "barbell", "squat_rack", "medicine_ball",
	"bench", "step", "mat", "resistance_band", "mini_band", "pull_up_bar",
	"suspension_trainer", "foam_roller", "sliders", "balance_board",
	"jump_rope",
}

// Weighted reports whether equipment comes in fixed weights, which
// planned weights must match.
func (e Equipment) Weighted() bool {
	return e == EquipmentDumbbells || e == EquipmentKettlebell || e == EquipmentMedicineBall
}

// WeightUnit is the unit of a weight.
type WeightUnit string

// Weight units.
const (
	Kilograms WeightUnit = "kilograms"
	Pounds    WeightUnit = "pounds"
)

// kilogramsPerPound is the exact definition of the pound.
const kilogramsPerPound = 0.45359237

// Weight is a weight in the unit it's marked in. The zero value is none.
type Weight struct {
	Value float64
	// Empty if the value is 0.
	Unit WeightUnit
}

// Kilograms returns the weight in kilograms.
func (w Weight) Kilograms() float64 {
	if w.Unit == Pounds {
		return w.Value * kilogramsPerPound
	}
	return w.Value
}

// Same reports whether two weights are the same, within the rounding of
// converting between units.
func (w Weight) Same(other Weight) bool {
	return math.Abs(w.Kilograms()-other.Kilograms()) < 0.05
}

// String formats the weight, e.g. "10 kg" or "25 lb".
func (w Weight) String() string {
	unit := "kg"
	if w.Unit == Pounds {
		unit = "lb"
	}
	return strconv.FormatFloat(w.Value, 'f', -1, 64) + " " + unit
}

func weightFromDB(value float64, unit gen.NullWeightUnit) Weight {
	if value == 0 || !unit.Valid {
		return Weight{}
	}
	return Weight{Value: value, Unit: WeightUnit(unit.WeightUnit)}
}

// OwnedEquipment is equipment a user has at home.
type OwnedEquipment struct {
	Equipment Equipment `json:"equipment"`
	// The weights of weighted equipment, in increasing order.
	Weights []Weight `json:"weights,omitempty"`
	Notes   string   `json:"notes,omitempty"`
}

// HomeEquipment is the equipment a user has at home.
type HomeEquipment struct {
	Equipment []OwnedEquipment
	// When the list was last set. Zero if the user hasn't said what they
	// have.
	UpdateTime time.Time
}

// Exercise is an exercise in a user's library.
type Exercise struct {
	ID     pgtype.UUID
	UserID pgtype.UUID
	Name   string
	// The steps of how to do the exercise.
	Instructions []string
	// How the exercise should feel and where. Empty if not set.
	Feel             string
	PrimaryMuscles   []Muscle
	SecondaryMuscles []Muscle
	// Empty for bodyweight exercises.
	Equipment      []Equipment
	EquipmentNotes string
	// Whether reps and hold times are per side.
	EachSide bool
	// The animations as a JSON array of plans.v1.ExerciseAnimation
	// messages, which the service validates.
	Animations json.RawMessage
	CreateTime time.Time
	UpdateTime time.Time
}

// PlannedExercise is an exercise of a strength workout, with what to do.
type PlannedExercise struct {
	ExerciseID pgtype.UUID
	// The name of the exercise, set when reading.
	ExerciseName string
	// The equipment of the exercise, set when reading.
	ExerciseEquipment []Equipment
	// Whether the exercise is done on each side, set when reading.
	ExerciseEachSide bool
	// The part of the workout, e.g. "Warm-up". Empty if none.
	Group string
	Sets  int32
	// Per side for exercises done on each side. 0 for exercises held for a
	// time.
	Reps int32
	// 0 for exercises done for reps.
	HoldSeconds int32
	// Of each dumbbell for exercises with two. Zero for bodyweight.
	Weight      Weight
	RestSeconds int32
	Tempo       string
	// From 1 to 10, 0 if not set.
	TargetEffort int32
	Notes        string
}

// SessionEffort is how hard a strength session felt. Empty if not rated.
type SessionEffort string

// ExerciseEffort is how hard an exercise felt compared to the plan. Empty
// if not rated.
type ExerciseEffort string

// ExerciseVolume is how the number of reps of an exercise felt. Empty if
// not rated.
type ExerciseVolume string

// StrengthLog is what the user did in a strength workout.
type StrengthLog struct {
	ID pgtype.UUID
	// Invalid once the workout is deleted.
	WorkoutID pgtype.UUID
	Date      time.Time
	Effort    SessionEffort
	Notes     string
	// When the log was first saved, which completed the workout.
	CompleteTime time.Time
	Exercises    []*LoggedExercise
}

// LoggedExercise is an exercise as it was done.
type LoggedExercise struct {
	ExerciseID pgtype.UUID
	// The name of the exercise, set when reading.
	ExerciseName string
	Sets         []LoggedSet
	Effort       ExerciseEffort
	Volume       ExerciseVolume
	Notes        string
	// What was planned for the exercise when the log was saved. Nil if it
	// wasn't planned.
	Planned *PlannedExercise
}

// LoggedSet is a set as it was done.
type LoggedSet struct {
	// Per side for exercises done on each side. 0 for exercises held for a
	// time.
	Reps int32
	// 0 for exercises done for reps.
	HoldSeconds int32
	// Zero for bodyweight.
	Weight Weight
}

// HistoryEntry is what the user did in an exercise in one strength workout.
type HistoryEntry struct {
	Date time.Time
	// Invalid if the workout was deleted.
	WorkoutID pgtype.UUID
	// Empty if the workout was deleted.
	WorkoutTitle  string
	SessionEffort SessionEffort
	Exercise      *LoggedExercise
}

// ErrDuplicateExerciseName is returned when the user already has an
// exercise with the name, ignoring case.
var ErrDuplicateExerciseName = errors.New("an exercise with this name already exists")

// ErrExerciseInUse is returned when deleting an exercise a workout or log
// refers to.
var ErrExerciseInUse = errors.New("the exercise is used by workouts or logs; remove it from those workouts first, or keep it")

// CreateExercises stores new exercises of a user, in a single transaction.
// It returns ErrDuplicateExerciseName if the user has an exercise with one
// of the names.
func (r *Repository) CreateExercises(ctx context.Context, userID pgtype.UUID, exercises []*Exercise) ([]*Exercise, error) {
	created := make([]*gen.Exercise, len(exercises))
	err := r.db.Transact(ctx, func(b *store.Batch) error {
		for i, e := range exercises {
			b.Read(func(db store.Querier) error {
				var err error
				created[i], err = gen.New(db).CreateExercise(ctx, &gen.CreateExerciseParams{
					UserID:           userID,
					Name:             e.Name,
					Instructions:     nonNilStrings(e.Instructions),
					Feel:             e.Feel,
					PrimaryMuscles:   musclesToDB(e.PrimaryMuscles),
					SecondaryMuscles: musclesToDB(e.SecondaryMuscles),
					Equipment:        equipmentToDB(e.Equipment),
					EquipmentNotes:   e.EquipmentNotes,
					EachSide:         e.EachSide,
					Animations:       nonNilJSON(e.Animations),
				})
				return err
			})
		}
		return nil
	})
	if err != nil {
		return nil, exerciseWriteError("create exercises", err)
	}
	result := make([]*Exercise, len(created))
	for i, e := range created {
		result[i] = exerciseFromDB(e)
	}
	return result, nil
}

// ListExercises lists the user's exercises by name.
func (r *Repository) ListExercises(ctx context.Context, userID pgtype.UUID) ([]*Exercise, error) {
	rows, err := r.q.ListExercises(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list exercises: %w", err)
	}
	result := make([]*Exercise, len(rows))
	for i, e := range rows {
		result[i] = exerciseFromDB(e)
	}
	return result, nil
}

// GetExercise gets one of the user's exercises.
func (r *Repository) GetExercise(ctx context.Context, userID, id pgtype.UUID) (*Exercise, error) {
	e, err := r.q.GetExercise(ctx, &gen.GetExerciseParams{ID: id, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get exercise: %w", err)
	}
	return exerciseFromDB(e), nil
}

// FindExercises returns which of the exercises exist and belong to the
// user, by ID.
func (r *Repository) FindExercises(ctx context.Context, userID pgtype.UUID, ids []pgtype.UUID) (map[pgtype.UUID]*Exercise, error) {
	found := map[pgtype.UUID]*Exercise{}
	if len(ids) == 0 {
		return found, nil
	}
	rows, err := r.q.FindExercises(ctx, &gen.FindExercisesParams{UserID: userID, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("failed to find exercises: %w", err)
	}
	for _, e := range rows {
		found[e.ID] = exerciseFromDB(e)
	}
	return found, nil
}

// UpdateExercise replaces one of the user's exercises. It returns
// ErrDuplicateExerciseName if the user has another exercise with its name.
func (r *Repository) UpdateExercise(ctx context.Context, e *Exercise) (*Exercise, error) {
	updated, err := r.q.UpdateExercise(ctx, &gen.UpdateExerciseParams{
		ID:               e.ID,
		UserID:           e.UserID,
		Name:             e.Name,
		Instructions:     nonNilStrings(e.Instructions),
		Feel:             e.Feel,
		PrimaryMuscles:   musclesToDB(e.PrimaryMuscles),
		SecondaryMuscles: musclesToDB(e.SecondaryMuscles),
		Equipment:        equipmentToDB(e.Equipment),
		EquipmentNotes:   e.EquipmentNotes,
		EachSide:         e.EachSide,
		Animations:       nonNilJSON(e.Animations),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, exerciseWriteError("update exercise", err)
	}
	return exerciseFromDB(updated), nil
}

// DeleteExercise deletes one of the user's exercises. It returns
// ErrExerciseInUse if a workout or log refers to it.
func (r *Repository) DeleteExercise(ctx context.Context, userID, id pgtype.UUID) error {
	n, err := r.q.DeleteExercise(ctx, &gen.DeleteExerciseParams{ID: id, UserID: userID})
	if err != nil {
		return exerciseWriteError("delete exercise", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// exerciseWriteError translates the errors of writing exercises.
func exerciseWriteError(does string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "23505" && pgErr.ConstraintName == "exercises_user_id_name":
			return ErrDuplicateExerciseName
		case pgErr.Code == "23503":
			return ErrExerciseInUse
		}
	}
	return fmt.Errorf("failed to %s: %w", does, err)
}

// SetStrengthLog creates or replaces the log of one of the user's strength
// workouts, in a transaction. What was planned for each logged exercise is
// taken from the workout's exercises: the nth time an exercise is logged
// matches the nth time it's planned.
func (r *Repository) SetStrengthLog(ctx context.Context, userID pgtype.UUID, w *Workout, log *StrengthLog) error {
	var logID pgtype.UUID
	return r.db.Transact(ctx, func(b *store.Batch) error {
		b.Read(func(db store.Querier) error {
			row, err := gen.New(db).UpsertStrengthLog(ctx, &gen.UpsertStrengthLogParams{
				UserID:    userID,
				WorkoutID: w.ID,
				Date:      store.DateToPG(w.Date),
				Effort:    gen.NullSessionEffort{SessionEffort: gen.SessionEffort(log.Effort), Valid: log.Effort != ""},
				Notes:     log.Notes,
			})
			if err != nil {
				return fmt.Errorf("failed to save strength log: %w", err)
			}
			logID = row.ID
			return nil
		})
		return nil
	}, func(b *store.Batch) error {
		q := gen.New(b)
		if err := q.DeleteStrengthLogExercises(ctx, logID); err != nil {
			return err
		}
		exercises := &gen.InsertStrengthLogExercisesParams{LogID: logID}
		sets := &gen.InsertStrengthLogSetsParams{LogID: logID}
		planned := plannedMatches(w.Exercises, log.Exercises)
		for i, e := range log.Exercises {
			position := int32(i)
			exercises.Positions = append(exercises.Positions, position)
			exercises.ExerciseIds = append(exercises.ExerciseIds, e.ExerciseID)
			exercises.Efforts = append(exercises.Efforts, string(e.Effort))
			exercises.Volumes = append(exercises.Volumes, string(e.Volume))
			exercises.Notes = append(exercises.Notes, e.Notes)
			p := planned[i]
			exercises.Planned = append(exercises.Planned, p != nil)
			if p == nil {
				p = &PlannedExercise{}
			}
			exercises.PlannedGroupNames = append(exercises.PlannedGroupNames, p.Group)
			exercises.PlannedSets = append(exercises.PlannedSets, p.Sets)
			exercises.PlannedReps = append(exercises.PlannedReps, p.Reps)
			exercises.PlannedHoldSeconds = append(exercises.PlannedHoldSeconds, p.HoldSeconds)
			exercises.PlannedWeightValues = append(exercises.PlannedWeightValues, p.Weight.Value)
			exercises.PlannedWeightUnits = append(exercises.PlannedWeightUnits, string(p.Weight.Unit))
			exercises.PlannedRestSeconds = append(exercises.PlannedRestSeconds, p.RestSeconds)
			exercises.PlannedTempos = append(exercises.PlannedTempos, p.Tempo)
			exercises.PlannedTargetEfforts = append(exercises.PlannedTargetEfforts, p.TargetEffort)
			exercises.PlannedNotes = append(exercises.PlannedNotes, p.Notes)
			for n, s := range e.Sets {
				sets.Positions = append(sets.Positions, position)
				sets.SetNumbers = append(sets.SetNumbers, int32(n+1))
				sets.Reps = append(sets.Reps, s.Reps)
				sets.HoldSeconds = append(sets.HoldSeconds, s.HoldSeconds)
				sets.WeightValues = append(sets.WeightValues, s.Weight.Value)
				sets.WeightUnits = append(sets.WeightUnits, string(s.Weight.Unit))
			}
		}
		if len(log.Exercises) > 0 {
			if err := q.InsertStrengthLogExercises(ctx, exercises); err != nil {
				return err
			}
		}
		if len(sets.Positions) > 0 {
			if err := q.InsertStrengthLogSets(ctx, sets); err != nil {
				return err
			}
		}
		return nil
	})
}

// plannedMatches returns what was planned for each logged exercise: the
// nth time an exercise is logged matches the nth time it's planned, and
// exercises logged more often than planned match nothing.
func plannedMatches(planned []*PlannedExercise, logged []*LoggedExercise) []*PlannedExercise {
	byExercise := map[pgtype.UUID][]*PlannedExercise{}
	for _, p := range planned {
		byExercise[p.ExerciseID] = append(byExercise[p.ExerciseID], p)
	}
	result := make([]*PlannedExercise, len(logged))
	for i, e := range logged {
		if ps := byExercise[e.ExerciseID]; len(ps) > 0 {
			result[i] = ps[0]
			byExercise[e.ExerciseID] = ps[1:]
		}
	}
	return result
}

// DeleteStrengthLog deletes the log of one of the user's workouts.
func (r *Repository) DeleteStrengthLog(ctx context.Context, userID, workoutID pgtype.UUID) error {
	n, err := r.q.DeleteStrengthLog(ctx, &gen.DeleteStrengthLogParams{WorkoutID: workoutID, UserID: userID})
	if err != nil {
		return fmt.Errorf("failed to delete strength log: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// ListExerciseHistory lists what the user did in exercises, all of them if
// exerciseIDs is empty, in an inclusive date range, in date order. Zero
// dates are unbounded. It takes a single round trip.
func (r *Repository) ListExerciseHistory(ctx context.Context, userID pgtype.UUID, exerciseIDs []pgtype.UUID, startDate, endDate time.Time) ([]*HistoryEntry, error) {
	var rows []*gen.ListExerciseHistoryRow
	var sets []*gen.StrengthLogSet
	err := r.db.ReadSnapshot(ctx, func(b *store.Batch) error {
		b.Read(func(db store.Querier) error {
			var err error
			rows, err = gen.New(db).ListExerciseHistory(ctx, &gen.ListExerciseHistoryParams{
				UserID:      userID,
				ExerciseIds: nonNil(exerciseIDs),
				StartDate:   store.DateToPG(startDate),
				EndDate:     store.DateToPG(endDate),
			})
			return err
		})
		b.Read(func(db store.Querier) error {
			var err error
			sets, err = gen.New(db).ListExerciseHistorySets(ctx, &gen.ListExerciseHistorySetsParams{
				UserID:      userID,
				ExerciseIds: nonNil(exerciseIDs),
				StartDate:   store.DateToPG(startDate),
				EndDate:     store.DateToPG(endDate),
			})
			return err
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list exercise history: %w", err)
	}
	setsByExercise := groupSets(sets)
	result := make([]*HistoryEntry, len(rows))
	for i, row := range rows {
		result[i] = &HistoryEntry{
			Date:          store.DateFromPG(row.Date),
			WorkoutID:     row.WorkoutID,
			WorkoutTitle:  row.WorkoutTitle,
			SessionEffort: SessionEffort(row.SessionEffort.SessionEffort),
			Exercise: loggedExerciseFromDB(&gen.StrengthLogExercise{
				LogID:               row.LogID,
				Position:            row.Position,
				ExerciseID:          row.ExerciseID,
				Effort:              row.Effort,
				Volume:              row.Volume,
				Notes:               row.Notes,
				PlannedGroupName:    row.PlannedGroupName,
				PlannedSets:         row.PlannedSets,
				PlannedReps:         row.PlannedReps,
				PlannedHoldSeconds:  row.PlannedHoldSeconds,
				PlannedWeightValue:  row.PlannedWeightValue,
				PlannedWeightUnit:   row.PlannedWeightUnit,
				PlannedRestSeconds:  row.PlannedRestSeconds,
				PlannedTempo:        row.PlannedTempo,
				PlannedTargetEffort: row.PlannedTargetEffort,
				PlannedNotes:        row.PlannedNotes,
			}, row.ExerciseName, setsByExercise[setKey{row.LogID, row.Position}]),
		}
	}
	return result, nil
}

// workoutFilter selects the user's workouts to read: those of a plan, some
// workouts, or all, optionally in an inclusive date range. Zero dates are
// unbounded.
type workoutFilter struct {
	userID pgtype.UUID
	// Invalid for all plans.
	planID pgtype.UUID
	// Nil for all workouts.
	workoutIDs         []pgtype.UUID
	startDate, endDate time.Time
}

// workoutReader reads workouts with their strength exercises and logs, and
// the user's home equipment to check them against, in the same batch.
type workoutReader struct {
	rows         []*gen.Workout
	exercises    []*gen.ListWorkoutExercisesRow
	logs         []*gen.StrengthLog
	logExercises []*gen.ListStrengthLogExercisesRow
	logSets      []*gen.StrengthLogSet
	home         *HomeEquipment
}

// queue queues the queries of the strength exercises and logs of the
// workouts f selects. The caller queues the query of the workouts.
func (wr *workoutReader) queue(ctx context.Context, b *store.Batch, f workoutFilter) {
	queueHomeEquipment(ctx, b, f.userID, &wr.home)
	b.Read(func(db store.Querier) error {
		var err error
		wr.exercises, err = gen.New(db).ListWorkoutExercises(ctx, &gen.ListWorkoutExercisesParams{
			UserID:     f.userID,
			PlanID:     f.planID,
			WorkoutIds: f.workoutIDs,
			StartDate:  store.DateToPG(f.startDate),
			EndDate:    store.DateToPG(f.endDate),
		})
		return err
	})
	b.Read(func(db store.Querier) error {
		var err error
		wr.logs, err = gen.New(db).ListStrengthLogs(ctx, &gen.ListStrengthLogsParams{
			UserID:     f.userID,
			PlanID:     f.planID,
			WorkoutIds: f.workoutIDs,
			StartDate:  store.DateToPG(f.startDate),
			EndDate:    store.DateToPG(f.endDate),
		})
		return err
	})
	b.Read(func(db store.Querier) error {
		var err error
		wr.logExercises, err = gen.New(db).ListStrengthLogExercises(ctx, &gen.ListStrengthLogExercisesParams{
			UserID:     f.userID,
			PlanID:     f.planID,
			WorkoutIds: f.workoutIDs,
			StartDate:  store.DateToPG(f.startDate),
			EndDate:    store.DateToPG(f.endDate),
		})
		return err
	})
	b.Read(func(db store.Querier) error {
		var err error
		wr.logSets, err = gen.New(db).ListStrengthLogSets(ctx, &gen.ListStrengthLogSetsParams{
			UserID:     f.userID,
			PlanID:     f.planID,
			WorkoutIds: f.workoutIDs,
			StartDate:  store.DateToPG(f.startDate),
			EndDate:    store.DateToPG(f.endDate),
		})
		return err
	})
}

// workouts returns the workouts read, with their exercises and logs.
func (wr *workoutReader) workouts() []*Workout {
	exercises := map[pgtype.UUID][]*PlannedExercise{}
	for _, row := range wr.exercises {
		exercises[row.WorkoutID] = append(exercises[row.WorkoutID], &PlannedExercise{
			ExerciseID:        row.ExerciseID,
			ExerciseName:      row.ExerciseName,
			ExerciseEquipment: equipmentFromDB(row.ExerciseEquipment),
			ExerciseEachSide:  row.ExerciseEachSide,
			Group:             row.GroupName,
			Sets:              row.Sets,
			Reps:              row.Reps,
			HoldSeconds:       row.HoldSeconds,
			Weight:            weightFromDB(row.WeightValue, row.WeightUnit),
			RestSeconds:       row.RestSeconds,
			Tempo:             row.Tempo,
			TargetEffort:      row.TargetEffort,
			Notes:             row.Notes,
		})
	}
	sets := groupSets(wr.logSets)
	logExercises := map[pgtype.UUID][]*LoggedExercise{}
	for _, row := range wr.logExercises {
		logExercises[row.LogID] = append(logExercises[row.LogID], loggedExerciseFromDB(&gen.StrengthLogExercise{
			LogID:               row.LogID,
			Position:            row.Position,
			ExerciseID:          row.ExerciseID,
			Effort:              row.Effort,
			Volume:              row.Volume,
			Notes:               row.Notes,
			PlannedGroupName:    row.PlannedGroupName,
			PlannedSets:         row.PlannedSets,
			PlannedReps:         row.PlannedReps,
			PlannedHoldSeconds:  row.PlannedHoldSeconds,
			PlannedWeightValue:  row.PlannedWeightValue,
			PlannedWeightUnit:   row.PlannedWeightUnit,
			PlannedRestSeconds:  row.PlannedRestSeconds,
			PlannedTempo:        row.PlannedTempo,
			PlannedTargetEffort: row.PlannedTargetEffort,
			PlannedNotes:        row.PlannedNotes,
		}, row.ExerciseName, sets[setKey{row.LogID, row.Position}]))
	}
	logs := map[pgtype.UUID]*StrengthLog{}
	for _, l := range wr.logs {
		logs[l.WorkoutID] = &StrengthLog{
			ID:           l.ID,
			WorkoutID:    l.WorkoutID,
			Date:         store.DateFromPG(l.Date),
			Effort:       SessionEffort(l.Effort.SessionEffort),
			Notes:        l.Notes,
			CompleteTime: l.CompleteTime.Time,
			Exercises:    logExercises[l.ID],
		}
	}
	result := make([]*Workout, len(wr.rows))
	for i, row := range wr.rows {
		w := workoutFromDB(row)
		w.Exercises = exercises[w.ID]
		w.Log = logs[w.ID]
		if w.Type == WorkoutTypeStrength {
			w.EquipmentIssues = EquipmentIssues(w.Exercises, wr.home)
		}
		result[i] = w
	}
	return result
}

// setKey identifies the sets of a logged exercise.
type setKey struct {
	logID    pgtype.UUID
	position int32
}

// groupSets groups sets by their logged exercise, in order.
func groupSets(rows []*gen.StrengthLogSet) map[setKey][]LoggedSet {
	result := map[setKey][]LoggedSet{}
	for _, s := range rows {
		k := setKey{s.LogID, s.Position}
		result[k] = append(result[k], LoggedSet{Reps: s.Reps, HoldSeconds: s.HoldSeconds, Weight: weightFromDB(s.WeightValue, s.WeightUnit)})
	}
	return result
}

// queueReplaceExercises queues replacing the exercises of workouts with
// theirs.
func queueReplaceExercises(ctx context.Context, q *gen.Queries, workouts []*Workout, replace bool) error {
	if replace {
		ids := make([]pgtype.UUID, len(workouts))
		for i, w := range workouts {
			ids[i] = w.ID
		}
		if err := q.DeleteWorkoutExercises(ctx, ids); err != nil {
			return err
		}
	}
	params := &gen.InsertWorkoutExercisesParams{}
	for _, w := range workouts {
		for i, e := range w.Exercises {
			params.WorkoutIds = append(params.WorkoutIds, w.ID)
			params.Positions = append(params.Positions, int32(i))
			params.ExerciseIds = append(params.ExerciseIds, e.ExerciseID)
			params.GroupNames = append(params.GroupNames, e.Group)
			params.Sets = append(params.Sets, e.Sets)
			params.Reps = append(params.Reps, e.Reps)
			params.HoldSeconds = append(params.HoldSeconds, e.HoldSeconds)
			params.WeightValues = append(params.WeightValues, e.Weight.Value)
			params.WeightUnits = append(params.WeightUnits, string(e.Weight.Unit))
			params.RestSeconds = append(params.RestSeconds, e.RestSeconds)
			params.Tempos = append(params.Tempos, e.Tempo)
			params.TargetEfforts = append(params.TargetEfforts, e.TargetEffort)
			params.Notes = append(params.Notes, e.Notes)
		}
	}
	if len(params.WorkoutIds) == 0 {
		return nil
	}
	return q.InsertWorkoutExercises(ctx, params)
}

func loggedExerciseFromDB(row *gen.StrengthLogExercise, name string, sets []LoggedSet) *LoggedExercise {
	e := &LoggedExercise{
		ExerciseID:   row.ExerciseID,
		ExerciseName: name,
		Sets:         sets,
		Effort:       ExerciseEffort(row.Effort.ExerciseEffort),
		Volume:       ExerciseVolume(row.Volume.ExerciseVolume),
		Notes:        row.Notes,
	}
	if row.PlannedSets.Valid {
		e.Planned = &PlannedExercise{
			ExerciseID:   row.ExerciseID,
			ExerciseName: name,
			Group:        row.PlannedGroupName.String,
			Sets:         row.PlannedSets.Int32,
			Reps:         row.PlannedReps.Int32,
			HoldSeconds:  row.PlannedHoldSeconds.Int32,
			Weight:       weightFromDB(row.PlannedWeightValue.Float64, row.PlannedWeightUnit),
			RestSeconds:  row.PlannedRestSeconds.Int32,
			Tempo:        row.PlannedTempo.String,
			TargetEffort: row.PlannedTargetEffort.Int32,
			Notes:        row.PlannedNotes.String,
		}
	}
	return e
}

func exerciseFromDB(e *gen.Exercise) *Exercise {
	return &Exercise{
		ID:               e.ID,
		UserID:           e.UserID,
		Name:             e.Name,
		Instructions:     e.Instructions,
		Feel:             e.Feel,
		PrimaryMuscles:   musclesFromDB(e.PrimaryMuscles),
		SecondaryMuscles: musclesFromDB(e.SecondaryMuscles),
		Equipment:        equipmentFromDB(e.Equipment),
		EquipmentNotes:   e.EquipmentNotes,
		EachSide:         e.EachSide,
		Animations:       e.Animations,
		CreateTime:       e.CreateTime.Time,
		UpdateTime:       e.UpdateTime.Time,
	}
}

// musclesToDB returns muscles as text, as queries send them.
func musclesToDB(muscles []Muscle) []string {
	result := make([]string, len(muscles))
	for i, m := range muscles {
		result[i] = string(m)
	}
	return result
}

func musclesFromDB(muscles []gen.Muscle) []Muscle {
	result := make([]Muscle, len(muscles))
	for i, m := range muscles {
		result[i] = Muscle(m)
	}
	return result
}

// equipmentToDB returns equipment as text, as queries send it.
func equipmentToDB(equipment []Equipment) []string {
	result := make([]string, len(equipment))
	for i, e := range equipment {
		result[i] = string(e)
	}
	return result
}

func equipmentFromDB[E ~string](equipment []E) []Equipment {
	result := make([]Equipment, len(equipment))
	for i, e := range equipment {
		result[i] = Equipment(e)
	}
	return result
}

func nonNilJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("[]")
	}
	return raw
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
