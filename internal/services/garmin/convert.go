package garmin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/fit"
	"github.com/johanbrandhorst/trail-tools/internal/garmin"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/plans"
)

// sportTypes are the sport types activities are stored with, which are
// Strava's, by Garmin's activity types, so activities from both are told
// apart the same way, like runs and strength training. Garmin's types are
// from Appendix A of the Activity API's documentation.
var sportTypes = map[string]string{
	"RUNNING":                       "Run",
	"STREET_RUNNING":                "Run",
	"TRACK_RUNNING":                 "Run",
	"OBSTACLE_RUN":                  "Run",
	"INDOOR_RUNNING":                "VirtualRun",
	"TREADMILL_RUNNING":             "VirtualRun",
	"VIRTUAL_RUN":                   "VirtualRun",
	"TRAIL_RUNNING":                 "TrailRun",
	"ULTRA_RUN":                     "TrailRun",
	"WALKING":                       "Walk",
	"CASUAL_WALKING":                "Walk",
	"SPEED_WALKING":                 "Walk",
	"HIKING":                        "Hike",
	"RUCKING":                       "Hike",
	"MOUNTAINEERING":                "Hike",
	"CYCLING":                       "Ride",
	"ROAD_BIKING":                   "Ride",
	"TRACK_CYCLING":                 "Ride",
	"RECUMBENT_CYCLING":             "Ride",
	"CYCLOCROSS":                    "Ride",
	"MOUNTAIN_BIKING":               "MountainBikeRide",
	"DOWNHILL_BIKING":               "MountainBikeRide",
	"ENDURO_MTB":                    "MountainBikeRide",
	"E_BIKE_MOUNTAIN":               "EMountainBikeRide",
	"E_ENDURO_MTB":                  "EMountainBikeRide",
	"E_BIKE_FITNESS":                "EBikeRide",
	"GRAVEL_CYCLING":                "GravelRide",
	"INDOOR_CYCLING":                "VirtualRide",
	"VIRTUAL_RIDE":                  "VirtualRide",
	"LAP_SWIMMING":                  "Swim",
	"OPEN_WATER_SWIMMING":           "Swim",
	"STRENGTH_TRAINING":             "WeightTraining",
	"INDOOR_CARDIO":                 "Workout",
	"HIIT":                          "HighIntensityIntervalTraining",
	"YOGA":                          "Yoga",
	"PILATES":                       "Pilates",
	"BACKCOUNTRY_SKIING":            "BackcountrySki",
	"BACKCOUNTRY_SNOWBOARDING":      "BackcountrySki",
	"RESORT_SKIING":                 "AlpineSki",
	"RESORT_SKIING_SNOWBOARDING_WS": "AlpineSki",
	"SNOWBOARDING_WS":               "Snowboard",
	"CROSS_COUNTRY_SKIING_WS":       "NordicSki",
	"SKATE_SKIING_WS":               "NordicSki",
	"CROSS_COUNTRY_INDOOR_SKIING":   "NordicSki",
	"SNOW_SHOE_WS":                  "Snowshoe",
	"ROCK_CLIMBING":                 "RockClimbing",
	"INDOOR_CLIMBING":               "RockClimbing",
	"BOULDERING":                    "RockClimbing",
	"ELLIPTICAL":                    "Elliptical",
	"STAIR_CLIMBING":                "StairStepper",
	"INDOOR_ROWING":                 "Rowing",
	"ROWING":                        "Rowing",
	"ROWING_V2":                     "Rowing",
	"KAYAKING":                      "Kayaking",
	"KAYAKING_V2":                   "Kayaking",
	"STAND_UP_PADDLEBOARDING":       "StandUpPaddling",
	"STAND_UP_PADDLEBOARDING_V2":    "StandUpPaddling",
	"SURFING":                       "Surfing",
	"SURFING_V2":                    "Surfing",
}

// sportType returns the sport type of a Garmin activity type, "Workout" for
// ones without a match.
func sportType(activityType string) string {
	if t, ok := sportTypes[strings.ToUpper(activityType)]; ok {
		return t
	}
	return "Workout"
}

// isStrength reports whether an activity is strength training, whose file
// has the sets.
func isStrength(sport string) bool {
	return sport == "WeightTraining"
}

// isFootSport reports whether cadence is counted in steps, which FIT files
// record as strides.
func isFootSport(sport string) bool {
	switch sport {
	case "Run", "TrailRun", "VirtualRun", "Walk", "Hike", "Snowshoe":
		return true
	default:
		return false
	}
}

// activityFromSummary converts an activity's summary from Garmin.
func activityFromSummary(s *garmin.ActivitySummary) *activities.GarminActivity {
	start := s.StartTime()
	// Shifting by the offset gives a time whose UTC fields are the local
	// ones.
	local := start.Add(time.Duration(s.StartTimeOffsetInSeconds) * time.Second)
	moving := s.MovingDurationInSeconds
	if moving == 0 {
		moving = s.DurationInSeconds
	}
	name := strings.TrimSpace(s.ActivityName)
	sport := sportType(s.ActivityType)
	if name == "" {
		name = strings.ReplaceAll(sport, "Training", " training")
	}
	return &activities.GarminActivity{
		GarminActivityID:    int64(s.ActivityID),
		Name:                name,
		SportType:           sport,
		StartTime:           start,
		StartDateLocal:      time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC),
		DistanceMeters:      s.DistanceInMeters,
		MovingTimeSeconds:   moving,
		ElapsedTimeSeconds:  s.DurationInSeconds,
		ElevationGainMeters: s.TotalElevationGainInMeters,
		AverageSpeedMps:     s.AverageSpeedInMetersPerSecond,
		MaxSpeedMps:         s.MaxSpeedInMetersPerSecond,
		AverageHeartrateBpm: s.AverageHeartRateInBeatsPerMinute,
		MaxHeartrateBpm:     s.MaxHeartRateInBeatsPerMinute,
		AverageCadenceSpm:   s.AverageRunCadenceInStepsPerMinute,
		DeviceName:          s.DeviceName,
		RawJSON:             s.RawJSON,
	}
}

// streamsFromFIT returns an activity's samples from its FIT file, and the
// bounds of its track, if it has one. Streams are as long as the records;
// a value a record lacks is the previous record's, so streams stay aligned.
func streamsFromFIT(a *fit.Activity, footSport bool) (*activitiesv1.ActivityStreams, *geo.Bounds) {
	if len(a.Records) == 0 {
		return nil, nil
	}
	var has struct{ position, altitude, heartRate, cadence, distance, speed bool }
	for _, r := range a.Records {
		has.position = has.position || r.HasPosition
		has.altitude = has.altitude || r.HasAltitude
		has.heartRate = has.heartRate || r.HasHeartRate
		has.cadence = has.cadence || r.HasCadence
		has.distance = has.distance || r.HasDistance
		has.speed = has.speed || r.HasSpeed
	}
	start := a.Records[0].Time
	n := len(a.Records)
	msg := activitiesv1.ActivityStreams_builder{TimeSeconds: make([]int32, n)}
	stream := func(on bool) []float64 {
		if !on {
			return nil
		}
		return make([]float64, n)
	}
	msg.DistanceMeters = stream(has.distance)
	msg.AltitudeMeters = stream(has.altitude)
	msg.HeartrateBpm = stream(has.heartRate)
	msg.CadenceSpm = stream(has.cadence)
	msg.VelocityMps = stream(has.speed)
	msg.Lat, msg.Lng = stream(has.position), stream(has.position)
	var points []geo.Point
	var last fit.Record
	for i, r := range a.Records {
		msg.TimeSeconds[i] = int32(r.Time.Sub(start) / time.Second)
		if r.HasPosition {
			last.Lat, last.Lng = r.Lat, r.Lng
			points = append(points, geo.Point{Lat: r.Lat, Lng: r.Lng})
		}
		if r.HasAltitude {
			last.AltitudeMeters = r.AltitudeMeters
		}
		if r.HasHeartRate {
			last.HeartRateBPM = r.HeartRateBPM
		}
		if r.HasCadence {
			last.Cadence = r.Cadence
			if footSport {
				last.Cadence *= 2
			}
		}
		if r.HasDistance {
			last.DistanceMeters = r.DistanceMeters
		}
		if r.HasSpeed {
			last.SpeedMPS = r.SpeedMPS
		}
		if has.position {
			msg.Lat[i], msg.Lng[i] = last.Lat, last.Lng
		}
		if has.altitude {
			msg.AltitudeMeters[i] = last.AltitudeMeters
		}
		if has.heartRate {
			msg.HeartrateBpm[i] = last.HeartRateBPM
		}
		if has.cadence {
			msg.CadenceSpm[i] = last.Cadence
		}
		if has.distance {
			msg.DistanceMeters[i] = last.DistanceMeters
		}
		if has.speed {
			msg.VelocityMps[i] = last.SpeedMPS
		}
	}
	bounds, ok := geo.BoundsOf(points)
	if !ok {
		return msg.Build(), nil
	}
	return msg.Build(), &bounds
}

// strengthLogFromSets returns what the user did in a strength workout from
// the sets their watch recorded. A set of a workout step belongs to that
// step's exercise, when the watch followed the workout as it was sent or
// downloaded. The other sets go, in order, to the planned exercises after
// the last one matched, each exercise's run of sets to the next exercise.
// It returns nil if the watch recorded no sets.
func strengthLogFromSets(w *plans.Workout, a *fit.Activity) *plans.StrengthLog {
	steps := plans.StrengthStepExercises(w.Exercises)
	sets := make([][]plans.LoggedSet, len(w.Exercises))
	next := 0
	var lastKey [2]uint16
	lastExercise := -1
	recorded := 0
	for _, s := range a.Sets {
		if !s.Active {
			continue
		}
		exercise := -1
		if s.HasWorkoutStep && s.WorkoutStep < len(steps) {
			exercise = steps[s.WorkoutStep]
			// The second side of a hold is the same set as the first.
			if exercise == plans.StepSecondSide {
				continue
			}
		}
		key := [2]uint16{s.Category, s.Name}
		if exercise < 0 {
			// A new run of sets moves on to the next exercise.
			if lastExercise >= 0 && key == lastKey {
				exercise = lastExercise
			} else if next < len(w.Exercises) {
				exercise = next
			}
		}
		if exercise < 0 {
			continue
		}
		sets[exercise] = append(sets[exercise], loggedSet(w.Exercises[exercise], s))
		lastKey, lastExercise = key, exercise
		next = max(next, exercise+1)
		recorded++
	}
	if recorded == 0 {
		return nil
	}
	log := &plans.StrengthLog{Notes: "Sets recorded by the watch."}
	for i, e := range w.Exercises {
		if len(sets[i]) == 0 {
			continue
		}
		log.Exercises = append(log.Exercises, &plans.LoggedExercise{ExerciseID: e.ExerciseID, Sets: sets[i]})
	}
	return log
}

// loggedSet converts a set from a watch, in the unit of the planned weight,
// or as the watch showed it.
func loggedSet(planned *plans.PlannedExercise, s fit.Set) plans.LoggedSet {
	set := plans.LoggedSet{Reps: int32(s.Reps)}
	if planned.HoldSeconds > 0 || s.Reps == 0 {
		set.Reps = 0
		set.HoldSeconds = int32(math.Round(s.Duration.Seconds()))
	}
	if s.WeightKilograms > 0 {
		unit := plans.Kilograms
		if planned.Weight.Value > 0 {
			unit = planned.Weight.Unit
		} else if s.Pounds {
			unit = plans.Pounds
		}
		set.Weight = weightIn(s.WeightKilograms, unit)
		// Watches store weights in sixteenths of a kilogram, so a planned
		// weight that's that close is the one used.
		if planned.Weight.Value > 0 && math.Abs(planned.Weight.Kilograms()-s.WeightKilograms) < 0.07 {
			set.Weight = planned.Weight
		}
	}
	return set
}

// weightIn returns a weight in kilograms in a unit, rounded to a tenth.
func weightIn(kilograms float64, unit plans.WeightUnit) plans.Weight {
	value := kilograms
	if unit == plans.Pounds {
		value = kilograms / 0.45359237
	}
	return plans.Weight{Value: math.Round(value*10) / 10, Unit: unit}
}

// workoutProvider is the app's name on Garmin Connect's workouts, which
// Garmin also takes as the source ID.
const workoutProvider = "trailrunn.ing"

// thresholds are the heart rates of the user's aerobic and anaerobic
// thresholds, 0 if unknown, and of the first stage of their last lactate
// test, which the next one starts at, 0 if they have none.
type thresholds struct {
	aerobic, anaerobic int32
	lactateStart       int32
}

// trainingWorkout returns a workout as a Training API workout, if it's a
// kind Garmin takes: a run, or a strength workout with exercises, of at most
// garmin.MaxWorkoutSteps steps. Heart rate targets relative to a threshold
// are converted with t.
func trainingWorkout(w *plans.Workout, t thresholds) (*garmin.Workout, bool) {
	gw := &garmin.Workout{
		WorkoutName:     w.Title,
		Description:     truncate(strings.TrimSpace(fuelingLine(w)+"\n\n"+w.Description), 1024),
		WorkoutProvider: workoutProvider,
		WorkoutSourceID: workoutProvider,
	}
	var steps []garmin.WorkoutStep
	switch w.Type {
	case plans.WorkoutTypeBase, plans.WorkoutTypeIntensity:
		gw.Sport = garmin.SportRunning
		if gw.WorkoutName == "" {
			gw.WorkoutName = "Run"
		}
		if len(w.RunBlocks) > 0 {
			steps = runSteps(w.RunBlocks, t)
			break
		}
		// A lactate test is run in its stages, starting where the last test
		// did, so the two are comparable.
		if w.LactateTest {
			steps = runSteps(plans.LactateTestStagesFrom(t.lactateStart).Blocks(), t)
			break
		}
		step := garmin.WorkoutStep{Type: "WorkoutStep", StepOrder: 1, Intensity: "ACTIVE", DurationType: "OPEN", TargetType: "OPEN"}
		switch {
		case w.TargetDistanceMeters > 0:
			step.DurationType, step.DurationValue = "DISTANCE", w.TargetDistanceMeters
		case w.TargetDurationSeconds > 0:
			step.DurationType, step.DurationValue = "TIME", float64(w.TargetDurationSeconds)
		}
		steps = []garmin.WorkoutStep{step}
	case plans.WorkoutTypeStrength:
		if len(w.Exercises) == 0 {
			return nil, false
		}
		gw.Sport = garmin.SportStrengthTraining
		if gw.WorkoutName == "" {
			gw.WorkoutName = "Strength"
		}
		order := 0
		nextOrder := func() int {
			order++
			return order
		}
		for _, block := range plans.StrengthBlocks(w.Exercises) {
			var blockSteps []garmin.WorkoutStep
			var repeat *garmin.WorkoutStep
			if block.Rounds > 1 {
				repeat = &garmin.WorkoutStep{Type: "WorkoutRepeatStep", StepOrder: nextOrder(), RepeatType: "REPEAT_UNTIL_STEPS_CMPLT", RepeatValue: block.Rounds}
			}
			for _, i := range block.Exercises {
				blockSteps = append(blockSteps, exerciseSteps(w.Exercises[i], nextOrder)...)
			}
			if repeat != nil {
				repeat.Steps = blockSteps
				steps = append(steps, *repeat)
			} else {
				steps = append(steps, blockSteps...)
			}
		}
	default:
		return nil, false
	}
	gw.Segments = []garmin.WorkoutSegment{{SegmentOrder: 1, Sport: gw.Sport, Steps: steps}}
	if gw.CountSteps() > garmin.MaxWorkoutSteps {
		return nil, false
	}
	return gw, true
}

// runIntensities are the Garmin intensities of run steps.
var runIntensities = map[plans.RunStepKind]string{
	plans.RunStepWarmUp:   "WARMUP",
	plans.RunStepRun:      "ACTIVE",
	plans.RunStepRecovery: "RECOVERY",
	plans.RunStepRest:     "REST",
	plans.RunStepCoolDown: "COOLDOWN",
}

// runSteps returns a structured run's blocks as workout steps, each
// repeated block a repeat step.
func runSteps(blocks []plans.RunBlock, t thresholds) []garmin.WorkoutStep {
	order := 0
	nextOrder := func() int {
		order++
		return order
	}
	var steps []garmin.WorkoutStep
	for _, b := range blocks {
		var repeat *garmin.WorkoutStep
		if b.Repeats > 1 {
			repeat = &garmin.WorkoutStep{Type: "WorkoutRepeatStep", StepOrder: nextOrder(), RepeatType: "REPEAT_UNTIL_STEPS_CMPLT", RepeatValue: b.Repeats}
		}
		var blockSteps []garmin.WorkoutStep
		for _, st := range b.Steps {
			blockSteps = append(blockSteps, runStep(st, nextOrder(), t))
		}
		if repeat != nil {
			repeat.Steps = blockSteps
			steps = append(steps, *repeat)
		} else {
			steps = append(steps, blockSteps...)
		}
	}
	return steps
}

// runStep returns a step of a structured run as a workout step. A target
// relative to an unknown threshold is left open, and described instead.
func runStep(st plans.RunStep, order int, t thresholds) garmin.WorkoutStep {
	step := garmin.WorkoutStep{
		Type:         "WorkoutStep",
		StepOrder:    order,
		Intensity:    runIntensities[st.Kind],
		DurationType: "OPEN",
		TargetType:   "OPEN",
		Description:  truncate(strings.TrimSpace(st.Notes), 512),
	}
	switch {
	case st.DurationSeconds > 0:
		step.DurationType, step.DurationValue = "TIME", float64(st.DurationSeconds)
	case st.DistanceMeters > 0:
		step.DurationType, step.DurationValue = "DISTANCE", st.DistanceMeters
	}
	switch {
	case st.HeartRate != nil:
		low, high, ok := st.HeartRate.Resolve(t.aerobic, t.anaerobic)
		if !ok {
			missing := "aerobic"
			if st.HeartRate.Reference == plans.HeartRateAnaerobicThreshold {
				missing = "anaerobic"
			}
			note := fmt.Sprintf("Heart rate %+d to %+d bpm from your %s threshold; do a lactate test to set it", st.HeartRate.LowBPM, st.HeartRate.HighBPM, missing)
			step.Description = truncate(strings.TrimPrefix(step.Description+". "+note, ". "), 512)
			break
		}
		step.TargetType, step.TargetValueLow, step.TargetValueHigh = "HEART_RATE", float64(low), float64(high)
	case st.Pace != nil:
		// Garmin takes paces as speeds in meters per second; the slow pace is
		// the low speed.
		step.TargetType = "PACE"
		step.TargetValueLow = math.Round(1000/float64(st.Pace.SlowSecondsPerKilometer)*1000) / 1000
		step.TargetValueHigh = math.Round(1000/float64(st.Pace.FastSecondsPerKilometer)*1000) / 1000
	}
	return step
}

// garminExercise returns the exercise of Garmin's catalog a planned
// exercise is: the one set on the exercise, or else the one named like it,
// if any.
func garminExercise(e *plans.PlannedExercise) (garmin.Exercise, bool) {
	if e.ExerciseGarmin != "" {
		return garmin.LookupExercise(e.ExerciseGarmin)
	}
	return garmin.MatchExercise(e.ExerciseName)
}

// exerciseSteps returns the steps of an exercise, as plans.ExerciseSteps
// lays them out. An exercise outside Garmin's catalog is a step without an
// exercise, described by its name, as Garmin only takes exercises of its
// catalog.
func exerciseSteps(e *plans.PlannedExercise, nextOrder func() int) []garmin.WorkoutStep {
	// Warm-ups and cool-downs show as such on the watch.
	intensity := "ACTIVE"
	switch e.GroupKind {
	case plans.GroupWarmUp:
		intensity = "WARMUP"
	case plans.GroupCoolDown:
		intensity = "COOLDOWN"
	}
	catalog, inCatalog := garminExercise(e)
	var steps []garmin.WorkoutStep
	for _, s := range plans.ExerciseSteps(e) {
		switch s.Kind {
		case plans.StepGetReady:
			description := "Get into position, then press lap to start the timer"
			if s.Side > 0 {
				description = fmt.Sprintf("Side %d of 2: get into position, then press lap to start the timer", s.Side)
			}
			steps = append(steps, garmin.WorkoutStep{
				Type:         "WorkoutStep",
				StepOrder:    nextOrder(),
				Intensity:    "REST",
				Description:  description,
				DurationType: "OPEN",
				TargetType:   "OPEN",
			})
		case plans.StepRest:
			steps = append(steps, garmin.WorkoutStep{
				Type:          "WorkoutStep",
				StepOrder:     nextOrder(),
				Intensity:     "REST",
				DurationType:  "TIME",
				DurationValue: float64(e.RestSeconds),
				TargetType:    "OPEN",
			})
		default:
			step := garmin.WorkoutStep{
				Type:        "WorkoutStep",
				StepOrder:   nextOrder(),
				Intensity:   intensity,
				TargetType:  "OPEN",
				Description: stepDescription(e, !inCatalog, s.Side),
			}
			if inCatalog {
				step.ExerciseCategory, step.ExerciseName = catalog.Category, catalog.Name
			}
			switch {
			case e.Reps > 0:
				step.DurationType, step.DurationValue = "REPS", float64(e.Reps)
			case e.HoldSeconds > 0:
				step.DurationType, step.DurationValue = "TIME", float64(e.HoldSeconds)
			default:
				step.DurationType = "OPEN"
			}
			if e.Weight.Value > 0 {
				step.WeightValue = math.Round(e.Weight.Kilograms()*100) / 100
				step.WeightDisplayUnit = "KILOGRAM"
				if e.Weight.Unit == plans.Pounds {
					step.WeightDisplayUnit = "POUND"
				}
			}
			steps = append(steps, step)
		}
	}
	return steps
}

// stepDescription describes an exercise's step: its name, if withName, the
// side of a hold on each side (0 for none), and its tempo, target effort
// and notes, which Garmin's steps have no fields for, so the watch shows
// them with the step.
func stepDescription(e *plans.PlannedExercise, withName bool, side int) string {
	var parts []string
	add := func(part string) {
		if part = strings.TrimSuffix(strings.TrimSpace(part), "."); part != "" {
			parts = append(parts, part)
		}
	}
	if withName {
		add(e.ExerciseName)
	}
	switch {
	case side > 0:
		add(fmt.Sprintf("Side %d of 2", side))
	case e.ExerciseEachSide && e.Reps > 0:
		add("Each side")
	}
	if e.Tempo != "" {
		add("Tempo: " + e.Tempo)
	}
	if e.TargetEffort > 0 {
		add(fmt.Sprintf("Effort %d/10", e.TargetEffort))
	}
	add(e.Notes)
	return truncate(strings.Join(parts, ". "), 512)
}

// contentHash returns a hash of a workout as sent and its date, to send it
// again only when either changes.
func contentHash(w *garmin.Workout, date time.Time) string {
	data, _ := json.Marshal(struct {
		Workout *garmin.Workout
		Date    string
	}{w, date.Format(time.DateOnly)})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// fuelingLine describes what to eat and drink during a run, empty if it has
// no fueling plan or its time can't be estimated. Garmin's calendar has no
// forecast, so the amounts are for mild temperatures.
func fuelingLine(w *plans.Workout) string {
	if w.RunFueling == nil {
		return ""
	}
	a, ok := w.FuelAmounts(nil)
	if !ok {
		return ""
	}
	var parts []string
	if a.CarbsGrams > 0 {
		parts = append(parts, fmt.Sprintf("%.0f g carbs", a.CarbsGrams))
	}
	if a.FluidML > 0 {
		parts = append(parts, fmt.Sprintf("%.0f ml fluid", a.FluidML))
	}
	if a.SodiumMG > 0 {
		parts = append(parts, fmt.Sprintf("%.0f mg sodium", a.SodiumMG))
	}
	line := "Fuel: " + strings.Join(parts, ", ")
	if w.RunFueling.Notes != "" {
		line += ". " + w.RunFueling.Notes
	}
	return line
}
