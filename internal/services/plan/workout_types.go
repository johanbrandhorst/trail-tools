package plan

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
	"github.com/johanbrandhorst/trail-tools/internal/plans"
	"github.com/johanbrandhorst/trail-tools/internal/services/route"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// maxPlannedExercises is how many exercises a strength workout or log
	// can have: a long session has a dozen or two.
	maxPlannedExercises = 40
	// maxSets is how many sets an exercise can have.
	maxSets = 20
	// maxTargetEffort is the top of the effort scale, maximal.
	maxTargetEffort = 10
)

var workoutTypes = map[plansv1.WorkoutType]plans.WorkoutType{
	plansv1.WorkoutType_WORKOUT_TYPE_BASE:           plans.WorkoutTypeBase,
	plansv1.WorkoutType_WORKOUT_TYPE_INTENSITY:      plans.WorkoutTypeIntensity,
	plansv1.WorkoutType_WORKOUT_TYPE_CROSS_TRAINING: plans.WorkoutTypeCrossTraining,
	plansv1.WorkoutType_WORKOUT_TYPE_STRENGTH:       plans.WorkoutTypeStrength,
	plansv1.WorkoutType_WORKOUT_TYPE_RACE:           plans.WorkoutTypeRace,
	plansv1.WorkoutType_WORKOUT_TYPE_REST:           plans.WorkoutTypeRest,
}

func workoutTypeToProto(t plans.WorkoutType) plansv1.WorkoutType {
	for k, v := range workoutTypes {
		if v == t {
			return k
		}
	}
	return plansv1.WorkoutType_WORKOUT_TYPE_UNSPECIFIED
}

// workoutDetailsFromProto sets the type and the fields of the kind of
// workout w is on workout, leaving the fields of other kinds zero, so
// changing the kind clears them.
func workoutDetailsFromProto(w *plansv1.Workout, workout *plans.Workout) error {
	switch w.WhichDetails() {
	case plansv1.Workout_Run_case:
		run := w.GetRun()
		switch run.GetIntensity() {
		case plansv1.RunIntensity_RUN_INTENSITY_BASE:
			workout.Type = plans.WorkoutTypeBase
		case plansv1.RunIntensity_RUN_INTENSITY_HARD:
			workout.Type = plans.WorkoutTypeIntensity
		default:
			return invalidArgument("run.intensity must be BASE or HARD")
		}
		if err := validateTargets(run.GetTargetDistanceMeters(), run.GetTargetElevationGainMeters(), run.GetTargetDurationSeconds()); err != nil {
			return invalidArgument("run: %v", err)
		}
		workout.TargetDistanceMeters = run.GetTargetDistanceMeters()
		workout.TargetElevationGainMeters = run.GetTargetElevationGainMeters()
		workout.TargetDurationSeconds = run.GetTargetDurationSeconds()
		workout.TargetTerrain = route.TerrainFromProto(run.GetTargetTerrain())
		return parseRouteID("run.route_id", run.GetRouteId(), workout)
	case plansv1.Workout_CrossTraining_case:
		ct := w.GetCrossTraining()
		workout.Type = plans.WorkoutTypeCrossTraining
		if err := validateTargets(ct.GetTargetDistanceMeters(), ct.GetTargetElevationGainMeters(), ct.GetTargetDurationSeconds()); err != nil {
			return invalidArgument("cross_training: %v", err)
		}
		workout.TargetDistanceMeters = ct.GetTargetDistanceMeters()
		workout.TargetElevationGainMeters = ct.GetTargetElevationGainMeters()
		workout.TargetDurationSeconds = ct.GetTargetDurationSeconds()
		return nil
	case plansv1.Workout_Strength_case:
		st := w.GetStrength()
		workout.Type = plans.WorkoutTypeStrength
		if err := validateTargets(0, 0, st.GetTargetDurationSeconds()); err != nil {
			return invalidArgument("strength: %v", err)
		}
		workout.TargetDurationSeconds = st.GetTargetDurationSeconds()
		if len(st.GetExercises()) > maxPlannedExercises {
			return invalidArgument("strength.exercises: at most %d exercises are allowed", maxPlannedExercises)
		}
		// A strength workout always has a list of exercises, possibly
		// empty, so it's told apart from other kinds.
		workout.Exercises = []*plans.PlannedExercise{}
		for i, e := range st.GetExercises() {
			planned, err := plannedExerciseFromProto(e)
			if err != nil {
				return invalidArgument("strength.exercises[%d]: %v", i, err)
			}
			workout.Exercises = append(workout.Exercises, planned)
		}
		return nil
	case plansv1.Workout_Race_case:
		race := w.GetRace()
		workout.Type = plans.WorkoutTypeRace
		if err := validateTargets(race.GetTargetDistanceMeters(), race.GetTargetElevationGainMeters(), race.GetTargetDurationSeconds()); err != nil {
			return invalidArgument("race: %v", err)
		}
		workout.TargetDistanceMeters = race.GetTargetDistanceMeters()
		workout.TargetElevationGainMeters = race.GetTargetElevationGainMeters()
		workout.TargetDurationSeconds = race.GetTargetDurationSeconds()
		return parseRouteID("race.route_id", race.GetRouteId(), workout)
	case plansv1.Workout_Rest_case:
		workout.Type = plans.WorkoutTypeRest
		return nil
	default:
		return invalidArgument("one of run, cross_training, strength, race or rest is required, saying what kind of workout it is")
	}
}

func parseRouteID(field, id string, workout *plans.Workout) error {
	if id == "" {
		return nil
	}
	var err error
	if workout.RouteID, err = store.StringToUUID(id); err != nil {
		return invalidArgument("invalid %s: %v", field, err)
	}
	return nil
}

func plannedExerciseFromProto(e *plansv1.PlannedExercise) (*plans.PlannedExercise, error) {
	if e.GetExerciseId() == "" {
		return nil, errors.New("exercise_id is required; create the exercise with CreateExercises first")
	}
	id, err := store.StringToUUID(e.GetExerciseId())
	if err != nil {
		return nil, fmt.Errorf("invalid exercise_id: %w", err)
	}
	if e.GetSets() < 1 || e.GetSets() > maxSets {
		return nil, fmt.Errorf("sets must be between 1 and %d", maxSets)
	}
	if e.GetReps() < 0 || e.GetHoldSeconds() < 0 || e.GetRestSeconds() < 0 {
		return nil, errors.New("reps, hold_seconds and rest_seconds must not be negative")
	}
	if (e.GetReps() > 0) == (e.GetHoldSeconds() > 0) {
		return nil, errors.New("set one of reps and hold_seconds")
	}
	weight, err := weightFromProto("weight", e.GetWeight())
	if err != nil {
		return nil, err
	}
	if e.GetTargetEffort() < 0 || e.GetTargetEffort() > maxTargetEffort {
		return nil, fmt.Errorf("target_effort must be between 1 and %d, or 0 if not set", maxTargetEffort)
	}
	return &plans.PlannedExercise{
		ExerciseID:   id,
		Group:        strings.TrimSpace(e.GetGroup()),
		Sets:         e.GetSets(),
		Reps:         e.GetReps(),
		HoldSeconds:  e.GetHoldSeconds(),
		Weight:       weight,
		RestSeconds:  e.GetRestSeconds(),
		Tempo:        strings.TrimSpace(e.GetTempo()),
		TargetEffort: e.GetTargetEffort(),
		Notes:        strings.TrimSpace(e.GetNotes()),
	}, nil
}

func plannedExerciseToProto(e *plans.PlannedExercise) *plansv1.PlannedExercise {
	return plansv1.PlannedExercise_builder{
		ExerciseId:   store.UUIDToString(e.ExerciseID),
		ExerciseName: e.ExerciseName,
		Group:        e.Group,
		Sets:         e.Sets,
		Reps:         e.Reps,
		HoldSeconds:  e.HoldSeconds,
		Weight:       weightToProto(e.Weight),
		RestSeconds:  e.RestSeconds,
		Tempo:        e.Tempo,
		TargetEffort: e.TargetEffort,
		Notes:        e.Notes,
	}.Build()
}

func workoutToProto(w *plans.Workout) *plansv1.Workout {
	b := plansv1.Workout_builder{
		Id:                  store.UUIDToString(w.ID),
		PlanId:              store.UUIDToString(w.PlanID),
		Date:                store.FormatDate(w.Date),
		Type:                workoutTypeToProto(w.Type),
		Title:               w.Title,
		Description:         w.Description,
		CompletedActivityId: store.UUIDToString(w.CompletedActivityID),
		StartTime:           w.StartTime,
		StartWindowMinutes:  w.StartWindowMinutes,
	}
	switch w.Type {
	case plans.WorkoutTypeBase, plans.WorkoutTypeIntensity:
		intensity := plansv1.RunIntensity_RUN_INTENSITY_BASE
		if w.Type == plans.WorkoutTypeIntensity {
			intensity = plansv1.RunIntensity_RUN_INTENSITY_HARD
		}
		b.Run = plansv1.RunWorkout_builder{
			Intensity:                 intensity,
			TargetDistanceMeters:      w.TargetDistanceMeters,
			TargetElevationGainMeters: w.TargetElevationGainMeters,
			TargetDurationSeconds:     w.TargetDurationSeconds,
			TargetTerrain:             route.TerrainToProto(w.TargetTerrain),
			RouteId:                   store.UUIDToString(w.RouteID),
		}.Build()
	case plans.WorkoutTypeCrossTraining:
		b.CrossTraining = plansv1.CrossTrainingWorkout_builder{
			TargetDistanceMeters:      w.TargetDistanceMeters,
			TargetElevationGainMeters: w.TargetElevationGainMeters,
			TargetDurationSeconds:     w.TargetDurationSeconds,
		}.Build()
	case plans.WorkoutTypeStrength:
		exercises := make([]*plansv1.PlannedExercise, len(w.Exercises))
		for i, e := range w.Exercises {
			exercises[i] = plannedExerciseToProto(e)
		}
		st := plansv1.StrengthWorkout_builder{
			Exercises:             exercises,
			TargetDurationSeconds: w.TargetDurationSeconds,
			EquipmentIssues:       equipmentIssuesToProto(w.EquipmentIssues),
		}
		if w.Log != nil {
			st.Log = strengthLogToProto(w.Log)
		}
		b.Strength = st.Build()
	case plans.WorkoutTypeRace:
		b.Race = plansv1.RaceWorkout_builder{
			TargetDistanceMeters:      w.TargetDistanceMeters,
			TargetElevationGainMeters: w.TargetElevationGainMeters,
			TargetDurationSeconds:     w.TargetDurationSeconds,
			RouteId:                   store.UUIDToString(w.RouteID),
		}.Build()
	case plans.WorkoutTypeRest:
		b.Rest = plansv1.RestDay_builder{}.Build()
	}
	return b.Build()
}

var sessionEfforts = map[plansv1.SessionEffort]plans.SessionEffort{
	plansv1.SessionEffort_SESSION_EFFORT_UNSPECIFIED: "",
	plansv1.SessionEffort_SESSION_EFFORT_VERY_EASY:   "very_easy",
	plansv1.SessionEffort_SESSION_EFFORT_EASY:        "easy",
	plansv1.SessionEffort_SESSION_EFFORT_ABOUT_RIGHT: "about_right",
	plansv1.SessionEffort_SESSION_EFFORT_HARD:        "hard",
	plansv1.SessionEffort_SESSION_EFFORT_VERY_HARD:   "very_hard",
}

var exerciseEfforts = map[plansv1.ExerciseEffort]plans.ExerciseEffort{
	plansv1.ExerciseEffort_EXERCISE_EFFORT_UNSPECIFIED: "",
	plansv1.ExerciseEffort_EXERCISE_EFFORT_TOO_EASY:    "too_easy",
	plansv1.ExerciseEffort_EXERCISE_EFFORT_JUST_RIGHT:  "just_right",
	plansv1.ExerciseEffort_EXERCISE_EFFORT_TOO_HARD:    "too_hard",
}

var exerciseVolumes = map[plansv1.ExerciseVolume]plans.ExerciseVolume{
	plansv1.ExerciseVolume_EXERCISE_VOLUME_UNSPECIFIED: "",
	plansv1.ExerciseVolume_EXERCISE_VOLUME_TOO_FEW:     "too_few",
	plansv1.ExerciseVolume_EXERCISE_VOLUME_ABOUT_RIGHT: "about_right",
	plansv1.ExerciseVolume_EXERCISE_VOLUME_TOO_MANY:    "too_many",
}

// reverse returns the keys of m by its values.
func reverse[K, V comparable](m map[K]V) map[V]K {
	result := make(map[V]K, len(m))
	for k, v := range m {
		result[v] = k
	}
	return result
}

var (
	sessionEffortsToProto  = reverse(sessionEfforts)
	exerciseEffortsToProto = reverse(exerciseEfforts)
	exerciseVolumesToProto = reverse(exerciseVolumes)
)

// strengthLogFromProto converts and validates a strength log. The caller
// must check its exercises exist.
func strengthLogFromProto(l *plansv1.StrengthLog) (*plans.StrengthLog, error) {
	effort, ok := sessionEfforts[l.GetEffort()]
	if !ok {
		return nil, invalidArgument("invalid effort %v", l.GetEffort())
	}
	if len(l.GetExercises()) > maxPlannedExercises {
		return nil, invalidArgument("exercises: at most %d exercises are allowed", maxPlannedExercises)
	}
	log := &plans.StrengthLog{Effort: effort, Notes: strings.TrimSpace(l.GetNotes())}
	for i, e := range l.GetExercises() {
		logged, err := loggedExerciseFromProto(e)
		if err != nil {
			return nil, invalidArgument("exercises[%d]: %v", i, err)
		}
		log.Exercises = append(log.Exercises, logged)
	}
	return log, nil
}

func loggedExerciseFromProto(e *plansv1.LoggedExercise) (*plans.LoggedExercise, error) {
	if e.GetExerciseId() == "" {
		return nil, errors.New("exercise_id is required")
	}
	id, err := store.StringToUUID(e.GetExerciseId())
	if err != nil {
		return nil, fmt.Errorf("invalid exercise_id: %w", err)
	}
	effort, ok := exerciseEfforts[e.GetEffort()]
	if !ok {
		return nil, fmt.Errorf("invalid effort %v", e.GetEffort())
	}
	volume, ok := exerciseVolumes[e.GetVolume()]
	if !ok {
		return nil, fmt.Errorf("invalid volume %v", e.GetVolume())
	}
	if len(e.GetSets()) > maxSets {
		return nil, fmt.Errorf("at most %d sets are allowed", maxSets)
	}
	logged := &plans.LoggedExercise{ExerciseID: id, Effort: effort, Volume: volume, Notes: strings.TrimSpace(e.GetNotes())}
	for j, s := range e.GetSets() {
		if s.GetReps() < 0 || s.GetHoldSeconds() < 0 {
			return nil, fmt.Errorf("sets[%d]: reps and hold_seconds must not be negative", j)
		}
		weight, err := weightFromProto(fmt.Sprintf("sets[%d].weight", j), s.GetWeight())
		if err != nil {
			return nil, err
		}
		logged.Sets = append(logged.Sets, plans.LoggedSet{Reps: s.GetReps(), HoldSeconds: s.GetHoldSeconds(), Weight: weight})
	}
	return logged, nil
}

func strengthLogToProto(l *plans.StrengthLog) *plansv1.StrengthLog {
	exercises := make([]*plansv1.LoggedExercise, len(l.Exercises))
	for i, e := range l.Exercises {
		exercises[i] = loggedExerciseToProto(e)
	}
	return plansv1.StrengthLog_builder{
		CompleteTime: timestamppb.New(l.CompleteTime),
		Effort:       sessionEffortsToProto[l.Effort],
		Notes:        l.Notes,
		Exercises:    exercises,
	}.Build()
}

func loggedExerciseToProto(e *plans.LoggedExercise) *plansv1.LoggedExercise {
	sets := make([]*plansv1.LoggedSet, len(e.Sets))
	for i, s := range e.Sets {
		sets[i] = plansv1.LoggedSet_builder{Reps: s.Reps, HoldSeconds: s.HoldSeconds, Weight: weightToProto(s.Weight)}.Build()
	}
	return plansv1.LoggedExercise_builder{
		ExerciseId:   store.UUIDToString(e.ExerciseID),
		ExerciseName: e.ExerciseName,
		Sets:         sets,
		Effort:       exerciseEffortsToProto[e.Effort],
		Volume:       exerciseVolumesToProto[e.Volume],
		Notes:        e.Notes,
	}.Build()
}

// exerciseIDs returns the IDs of the exercises workouts plan.
func exerciseIDs(workouts []*plans.Workout) []pgtype.UUID {
	var ids []pgtype.UUID
	for _, w := range workouts {
		for _, e := range w.Exercises {
			ids = append(ids, e.ExerciseID)
		}
	}
	return ids
}

const (
	// defaultStartWindowMinutes is the start window of workouts with a start
	// time but no window.
	defaultStartWindowMinutes = 60
	minStartWindowMinutes     = 15
	maxStartWindowMinutes     = 360
)

// startWindowFromProto sets when a workout can start.
func startWindowFromProto(w *plansv1.Workout, workout *plans.Workout) error {
	start := strings.TrimSpace(w.GetStartTime())
	if start == "" {
		if w.GetStartWindowMinutes() != 0 {
			return invalidArgument("start_window_minutes needs a start_time")
		}
		return nil
	}
	parsed, err := time.Parse("15:04", start)
	if err != nil {
		return invalidArgument("start_time must be HH:MM, e.g. 07:00")
	}
	window := w.GetStartWindowMinutes()
	if window == 0 {
		window = defaultStartWindowMinutes
	}
	if window < minStartWindowMinutes || window > maxStartWindowMinutes {
		return invalidArgument("start_window_minutes must be between %d and %d", minStartWindowMinutes, maxStartWindowMinutes)
	}
	workout.StartTime = parsed.Format("15:04")
	workout.StartWindowMinutes = window
	return nil
}
