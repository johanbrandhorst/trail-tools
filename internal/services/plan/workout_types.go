package plan

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/plans"
	"github.com/johanbrandhorst/trail-tools/internal/services/route"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/proto"
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
		blocks, err := runBlocksFromProto(run.GetBlocks())
		if err != nil {
			return invalidArgument("run.%v", err)
		}
		workout.RunBlocks = blocks
		workout.LactateTest = run.GetLactateTest()
		if run.HasFueling() {
			f := run.GetFueling()
			workout.RunFueling = &plans.RunFueling{
				CarbsGramsPerHour: f.GetCarbsGramsPerHour(),
				FluidMLPerHour:    f.GetFluidMlPerHour(),
				SodiumMGPerHour:   f.GetSodiumMgPerHour(),
				Notes:             strings.TrimSpace(f.GetNotes()),
			}
			if err := workout.RunFueling.Validate(); err != nil {
				return invalidArgument("run.fueling: %v", err)
			}
		}
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
		exercises, err := strengthExercisesFromProto(st)
		if err != nil {
			return invalidArgument("strength.%v", err)
		}
		workout.Exercises = exercises
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
	if e.GetSets() < 0 || e.GetSets() > maxSets {
		return nil, fmt.Errorf("sets must be between 1 and %d", maxSets)
	}
	if e.GetReps() < 0 || e.GetHoldSeconds() < 0 || e.GetRestSeconds() < 0 {
		return nil, errors.New("reps, hold_seconds and rest_seconds must not be negative")
	}
	if e.GetReps() > 0 && e.GetHoldSeconds() > 0 {
		return nil, errors.New("set one of reps and hold_seconds, or neither for an exercise done until the user is done")
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

// groupKinds are the API's kinds of exercise groups.
var groupKinds = map[plansv1.ExerciseGroupKind]plans.GroupKind{
	plansv1.ExerciseGroupKind_EXERCISE_GROUP_KIND_STRAIGHT_SETS: plans.GroupStraightSets,
	plansv1.ExerciseGroupKind_EXERCISE_GROUP_KIND_SUPERSET:      plans.GroupSuperset,
	plansv1.ExerciseGroupKind_EXERCISE_GROUP_KIND_CIRCUIT:       plans.GroupCircuit,
	plansv1.ExerciseGroupKind_EXERCISE_GROUP_KIND_WARM_UP:       plans.GroupWarmUp,
	plansv1.ExerciseGroupKind_EXERCISE_GROUP_KIND_COOL_DOWN:     plans.GroupCoolDown,
}

// strengthExercisesFromProto converts and validates the exercises of a
// strength workout with its groups. The exercises of a superset or circuit
// are stored with its rounds as their sets, and its rest between rounds as
// the rest of its last exercise, which is where watches put it.
func strengthExercisesFromProto(st *plansv1.StrengthWorkout) ([]*plans.PlannedExercise, error) {
	groups := map[string]*plansv1.ExerciseGroup{}
	for i, g := range st.GetGroups() {
		name := strings.TrimSpace(g.GetName())
		switch kind, ok := groupKinds[g.GetKind()]; {
		case name == "":
			return nil, fmt.Errorf("groups[%d].name is required: the group of its exercises", i)
		case groups[name] != nil:
			return nil, fmt.Errorf("groups[%d]: group %q is listed twice", i, name)
		case !ok:
			return nil, fmt.Errorf("groups[%d].kind is required: STRAIGHT_SETS, SUPERSET, CIRCUIT, WARM_UP or COOL_DOWN", i)
		case !kind.Rounds() && (g.HasRounds() || g.HasRestSeconds()):
			return nil, fmt.Errorf("groups[%d]: only supersets and circuits have rounds and rest_seconds; leave them unset, and set each exercise's sets and rest_seconds", i)
		case kind.Rounds() && (g.GetRounds() < 1 || g.GetRounds() > maxSets):
			return nil, fmt.Errorf("groups[%d].rounds must be between 1 and %d", i, maxSets)
		case g.GetRestSeconds() < 0:
			return nil, fmt.Errorf("groups[%d].rest_seconds must not be negative", i)
		}
		groups[name] = g
	}
	// A strength workout always has a list of exercises, possibly empty, so
	// it's told apart from other kinds.
	exercises := []*plans.PlannedExercise{}
	used := map[string]bool{}
	for i, e := range st.GetExercises() {
		planned, err := plannedExerciseFromProto(e)
		if err != nil {
			return nil, fmt.Errorf("exercises[%d]: %w", i, err)
		}
		g := groups[planned.Group]
		if planned.Group != "" {
			if used[planned.Group] && exercises[i-1].Group != planned.Group {
				return nil, fmt.Errorf("exercises[%d]: the exercises of group %q must be consecutive", i, planned.Group)
			}
			used[planned.Group] = true
		}
		kind := plans.GroupStraightSets
		if g != nil {
			kind = groupKinds[g.GetKind()]
		}
		if !kind.Rounds() {
			if g == nil && looksLikeRounds(planned.Group) {
				return nil, fmt.Errorf("exercises[%d]: group %q reads like a superset or circuit; list it in groups with its kind, rounds and rest_seconds, or with kind STRAIGHT_SETS", i, planned.Group)
			}
			if g == nil && looksLikeEasy(planned.Group) {
				return nil, fmt.Errorf("exercises[%d]: group %q reads like a warm-up or cool-down; list it in groups with kind WARM_UP or COOL_DOWN, so the user rates it by stiffness rather than effort, or with kind STRAIGHT_SETS", i, planned.Group)
			}
			if planned.Sets < 1 {
				return nil, fmt.Errorf("exercises[%d].sets must be between 1 and %d", i, maxSets)
			}
			if kind.Easy() && planned.TargetEffort != 0 {
				return nil, fmt.Errorf("exercises[%d]: warm-up and cool-down exercises have no target_effort; leave it 0", i)
			}
			planned.GroupKind = kind
			exercises = append(exercises, planned)
			continue
		}
		if planned.Sets != 0 && planned.Sets != g.GetRounds() {
			return nil, fmt.Errorf("exercises[%d]: %d sets in a %s of %d rounds; leave sets 0, as the exercise is done once each round", i, planned.Sets, kind, g.GetRounds())
		}
		last := i+1 == len(st.GetExercises()) || strings.TrimSpace(st.GetExercises()[i+1].GetGroup()) != planned.Group
		if last && planned.RestSeconds != 0 {
			return nil, fmt.Errorf("exercises[%d]: the last exercise of %s %q has rest_seconds; set the rest between rounds in the group's rest_seconds", i, kind, planned.Group)
		}
		planned.GroupKind = kind
		planned.Sets = g.GetRounds()
		if last {
			planned.RestSeconds = g.GetRestSeconds()
		}
		exercises = append(exercises, planned)
	}
	for name := range groups {
		if !used[name] {
			return nil, fmt.Errorf("groups: no exercise is in group %q", name)
		}
	}
	return exercises, nil
}

// looksLikeRounds reports whether a group's name says its exercises are
// done back to back, as it did before groups had kinds, so a client that
// names a group "Circuit" without listing it isn't silently given straight
// sets.
func looksLikeRounds(group string) bool {
	group = strings.ToLower(group)
	return strings.Contains(group, "superset") || strings.Contains(group, "circuit")
}

// looksLikeEasy reports whether a group's name says it's a warm-up or
// cool-down, so a client that names a group "Warm-up" without listing it
// isn't silently given straight sets, rated by effort.
func looksLikeEasy(group string) bool {
	group = strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(group))
	return strings.Contains(group, "warmup") || strings.Contains(group, "cooldown")
}

// strengthGroupsToProto returns the groups of a strength workout's
// exercises, in order, and moves the rounds of supersets and circuits from
// their exercises' sets, and the rest between rounds from their last
// exercises, to the groups, so each is in one place.
func strengthGroupsToProto(exercises []*plans.PlannedExercise, out []*plansv1.PlannedExercise) []*plansv1.ExerciseGroup {
	var groups []*plansv1.ExerciseGroup
	for i, e := range exercises {
		if e.Group == "" || (i > 0 && exercises[i-1].Group == e.Group) {
			continue
		}
		g := plansv1.ExerciseGroup_builder{Name: e.Group, Kind: groupKindsToProto[e.GroupKind]}
		if e.GroupKind.Rounds() {
			g.Rounds = proto.Int32(e.Sets)
			last := i
			for last+1 < len(exercises) && exercises[last+1].Group == e.Group {
				last++
			}
			g.RestSeconds = proto.Int32(exercises[last].RestSeconds)
			out[last].SetRestSeconds(0)
			for j := i; j <= last; j++ {
				out[j].SetSets(0)
			}
		}
		groups = append(groups, g.Build())
	}
	return groups
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
		LocationName:        w.LocationName,
	}
	if w.Location != nil {
		b.Location = geov1.LatLng_builder{Lat: w.Location.Lat, Lng: w.Location.Lng}.Build()
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
			Blocks:                    runBlocksToProto(w.RunBlocks),
			LactateTest:               w.LactateTest,
			Fueling:                   runFuelingToProto(w),
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
			Groups:                strengthGroupsToProto(w.Exercises, exercises),
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

var stiffnesses = map[plansv1.Stiffness]plans.Stiffness{
	plansv1.Stiffness_STIFFNESS_UNSPECIFIED: "",
	plansv1.Stiffness_STIFFNESS_LOOSE:       "loose",
	plansv1.Stiffness_STIFFNESS_A_BIT_STIFF: "a_bit_stiff",
	plansv1.Stiffness_STIFFNESS_STIFF:       "stiff",
	plansv1.Stiffness_STIFFNESS_SORE:        "sore",
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
	groupKindsToProto      = reverse(groupKinds)
	sessionEffortsToProto  = reverse(sessionEfforts)
	exerciseEffortsToProto = reverse(exerciseEfforts)
	exerciseVolumesToProto = reverse(exerciseVolumes)
	stiffnessesToProto     = reverse(stiffnesses)
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
	stiffness, ok := stiffnesses[e.GetStiffness()]
	if !ok {
		return nil, fmt.Errorf("invalid stiffness %v", e.GetStiffness())
	}
	if len(e.GetSets()) > maxSets {
		return nil, fmt.Errorf("at most %d sets are allowed", maxSets)
	}
	logged := &plans.LoggedExercise{ExerciseID: id, Effort: effort, Volume: volume, Stiffness: stiffness, Notes: strings.TrimSpace(e.GetNotes())}
	for j, s := range e.GetSets() {
		if s.GetReps() < 0 || s.GetHoldSeconds() < 0 {
			return nil, fmt.Errorf("sets[%d]: reps and hold_seconds must not be negative", j)
		}
		weight, err := weightFromProto(fmt.Sprintf("sets[%d].weight", j), s.GetWeight())
		if err != nil {
			return nil, err
		}
		setEffort, ok := exerciseEfforts[s.GetEffort()]
		if !ok {
			return nil, fmt.Errorf("sets[%d]: invalid effort %v", j, s.GetEffort())
		}
		setStiffness, ok := stiffnesses[s.GetStiffness()]
		if !ok {
			return nil, fmt.Errorf("sets[%d]: invalid stiffness %v", j, s.GetStiffness())
		}
		logged.Sets = append(logged.Sets, plans.LoggedSet{Reps: s.GetReps(), HoldSeconds: s.GetHoldSeconds(), Weight: weight, Effort: setEffort, Stiffness: setStiffness})
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
		sets[i] = plansv1.LoggedSet_builder{
			Reps:        s.Reps,
			HoldSeconds: s.HoldSeconds,
			Weight:      weightToProto(s.Weight),
			Effort:      exerciseEffortsToProto[s.Effort],
			Stiffness:   stiffnessesToProto[s.Stiffness],
		}.Build()
	}
	return plansv1.LoggedExercise_builder{
		ExerciseId:   store.UUIDToString(e.ExerciseID),
		ExerciseName: e.ExerciseName,
		Sets:         sets,
		Effort:       exerciseEffortsToProto[e.Effort],
		Volume:       exerciseVolumesToProto[e.Volume],
		Stiffness:    stiffnessesToProto[e.Stiffness],
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

// maxLocationNameLength is the most characters a workout's location name
// can have.
const maxLocationNameLength = 100

// locationFromProto sets where a workout starts.
func locationFromProto(w *plansv1.Workout, workout *plans.Workout) error {
	name := strings.TrimSpace(w.GetLocationName())
	if len([]rune(name)) > maxLocationNameLength {
		return invalidArgument("location_name must be at most %d characters", maxLocationNameLength)
	}
	workout.LocationName = name
	if !w.HasLocation() {
		return nil
	}
	lat, lng := w.GetLocation().GetLat(), w.GetLocation().GetLng()
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 || (lat == 0 && lng == 0) {
		return invalidArgument("location must be a latitude from -90 to 90 and a longitude from -180 to 180")
	}
	workout.Location = &geo.Point{Lat: lat, Lng: lng}
	return nil
}

// runFuelingToProto returns a run's fueling with its amounts at mild
// temperatures, nil if it has none.
func runFuelingToProto(w *plans.Workout) *plansv1.RunFueling {
	f := w.RunFueling
	if f == nil {
		return nil
	}
	b := plansv1.RunFueling_builder{
		CarbsGramsPerHour: f.CarbsGramsPerHour,
		FluidMlPerHour:    f.FluidMLPerHour,
		SodiumMgPerHour:   f.SodiumMGPerHour,
		Notes:             f.Notes,
	}
	if amounts, ok := w.FuelAmounts(nil); ok {
		b.Amounts = fuelAmountsToProto(amounts)
	}
	return b.Build()
}

func fuelAmountsToProto(a plans.FuelAmounts) *plansv1.FuelAmounts {
	return plansv1.FuelAmounts_builder{
		ExpectedDurationSeconds:    int32(a.ExpectedDuration / time.Second),
		CarbsGrams:                 a.CarbsGrams,
		FluidMl:                    a.FluidML,
		SodiumMg:                   a.SodiumMG,
		ApparentTemperatureCelsius: a.ApparentTemperatureCelsius,
		HeatIncreasePercent:        int32(math.Round(a.HeatIncrease * 100)),
		SweatMl:                    a.SweatML,
		SodiumLossMg:               a.SodiumLossMG,
		SodiumFromSweat:            a.SodiumFromSweat,
	}.Build()
}
