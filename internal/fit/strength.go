package fit

import (
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

// exerciseTitleMessage is the global number of exercise titles, which name
// the exercises of a strength workout's steps.
const exerciseTitleMessage = 264

// Sports of strength workouts: training, with the strength training sub
// sport.
const (
	SportTraining            = 10
	SubSportStrengthTraining = 20
)

// More step duration types.
const (
	durationRepeatUntilStepsComplete = 6
	durationReps                     = 29
)

// Weight display units of exercise weights.
const (
	WeightUnitKilograms = 1
	WeightUnitPounds    = 2
)

// unknownExerciseCategory is the exercise category of exercises that aren't
// in Garmin's catalog. Watches then show the step's exercise title, which
// matches the step by its exercise name number.
const unknownExerciseCategory = 65534

// stepWeightScale is the scale of exercise weights: hundredths of a
// kilogram.
const stepWeightScale = 100

// StrengthWorkout is a strength workout: exercises done for sets, with rest
// between them, which watches guide set by set and record the reps and
// weights of.
type StrengthWorkout struct {
	Name   string
	Blocks []StrengthBlock
}

// StrengthBlock is exercises done together for a number of rounds: a single
// exercise for its sets, or a superset or circuit of several, done back to
// back.
type StrengthBlock struct {
	Exercises []StrengthExercise
	// How many sets, or rounds of a superset. At least 1.
	Rounds int
}

// StrengthExercise is an exercise of a block.
type StrengthExercise struct {
	// The name watches show, cut to 15 bytes on the step and 31 on the
	// title.
	Name string
	// Notes on the exercise, which some devices show.
	Notes string
	// The reps of each set; 0 for an exercise held for a time.
	Reps int
	// How long each set is held, for an exercise without reps.
	Hold time.Duration
	// The weight in kilograms, 0 for bodyweight, shown in pounds if
	// Pounds is set.
	WeightKilograms float64
	Pounds          bool
	// The rest after the exercise, 0 for none.
	Rest time.Duration
	// The step's intensity: IntensityActive, the zero value, or
	// IntensityWarmUp or IntensityCoolDown for a warm-up or cool-down.
	Intensity Intensity
}

// strengthStep is a step of a strength workout as written.
type strengthStep struct {
	name          string
	notes         string
	durationType  uint8
	durationValue uint32
	targetValue   uint32
	intensity     Intensity
	// The exercise name number of exercise steps, matching their title;
	// 0xFFFF for rest and repeat steps.
	exercise   uint16
	weight     uint16
	weightUnit uint8
}

// invalidUint16 marks an unset 16 bit field.
const invalidUint16 = 0xFFFF

// strengthSteps returns the steps of a strength workout: each exercise of a
// block, with its rest, and a step repeating the block for its rounds.
func strengthSteps(workout *StrengthWorkout) ([]strengthStep, []string, error) {
	var steps []strengthStep
	var titles []string
	for _, block := range workout.Blocks {
		if len(block.Exercises) == 0 || block.Rounds < 1 {
			return nil, nil, errors.New("a block needs exercises and at least one round")
		}
		first := len(steps)
		for _, e := range block.Exercises {
			step := strengthStep{
				name:      e.Name,
				notes:     e.Notes,
				intensity: e.Intensity,
				exercise:  uint16(len(titles)),
				weight:    invalidUint16,
			}
			titles = append(titles, e.Name)
			switch {
			case e.Reps > 0:
				step.durationType, step.durationValue = durationReps, uint32(e.Reps)
			case e.Hold > 0:
				step.durationType, step.durationValue = durationTime, uint32(e.Hold/time.Millisecond)
			default:
				step.durationType = durationOpen
			}
			if e.WeightKilograms > 0 {
				step.weight = uint16(min(math.Round(e.WeightKilograms*stepWeightScale), invalidUint16-1))
				step.weightUnit = WeightUnitKilograms
				if e.Pounds {
					step.weightUnit = WeightUnitPounds
				}
			}
			steps = append(steps, step)
			if e.Rest > 0 {
				steps = append(steps, strengthStep{
					name:          "Rest",
					durationType:  durationTime,
					durationValue: uint32(e.Rest / time.Millisecond),
					intensity:     IntensityRest,
					exercise:      invalidUint16,
					weight:        invalidUint16,
				})
			}
		}
		if block.Rounds > 1 {
			steps = append(steps, strengthStep{
				durationType:  durationRepeatUntilStepsComplete,
				durationValue: uint32(first),
				targetValue:   uint32(block.Rounds),
				intensity:     IntensityActive,
				exercise:      invalidUint16,
				weight:        invalidUint16,
			})
		}
	}
	if len(steps) == 0 {
		return nil, nil, errors.New("a workout needs at least one exercise")
	}
	if len(steps) > MaxWorkoutSteps {
		return nil, nil, fmt.Errorf("the workout needs %d steps, and watches take at most %d", len(steps), MaxWorkoutSteps)
	}
	return steps, titles, nil
}

// exerciseTitleSize is the size of exercise titles, including the
// terminating zero.
const exerciseTitleSize = 32

// WriteStrengthWorkout encodes a strength workout as a FIT workout file,
// created at a time. Exercises are written as exercises outside Garmin's
// catalog, named by their titles, so watches show their names.
func WriteStrengthWorkout(w io.Writer, workout *StrengthWorkout, created time.Time) error {
	steps, titles, err := strengthSteps(workout)
	if err != nil {
		return err
	}
	var f Writer
	f.Define(0, FileIDMessage,
		EnumField(0),   // Type.
		Uint16Field(1), // Manufacturer.
		Uint16Field(2), // Product.
		Uint32Field(4), // Time created.
	)
	f.Data(0, uint8(FileWorkout), uint16(DevelopmentManufacturer), uint16(0), Time(created))
	f.Define(1, workoutMessage,
		EnumField(4),                    // Sport.
		Uint16Field(6),                  // Number of valid steps.
		StringField(8, workoutNameSize), // Name.
		EnumField(11),                   // Sub sport.
	)
	f.Data(1, uint8(SportTraining), uint16(len(steps)), StringValue(workout.Name, workoutNameSize), uint8(SubSportStrengthTraining))
	f.Define(2, workoutStepMessage,
		Uint16Field(254),             // Message index.
		StringField(0, stepNameSize), // Name.
		EnumField(1),                 // Duration type.
		Uint32Field(2),               // Duration value.
		EnumField(3),                 // Target type.
		Uint32Field(4),               // Target value: the rounds of repeat steps.
		EnumField(7),                 // Intensity.
		StringField(8, notesSize),    // Notes.
		Uint16Field(10),              // Exercise category.
		Uint16Field(11),              // Exercise name.
		Uint16Field(12),              // Exercise weight, in hundredths of a kilogram.
		EnumField(13),                // Weight display unit.
	)
	for i, s := range steps {
		category := uint16(invalidUint16)
		if s.exercise != invalidUint16 {
			category = unknownExerciseCategory
		}
		weightUnit := s.weightUnit
		if s.weight == invalidUint16 {
			weightUnit = 0xFF
		}
		f.Data(2, uint16(i), StringValue(s.name, stepNameSize), s.durationType, s.durationValue,
			uint8(targetOpen), s.targetValue, uint8(s.intensity), StringValue(s.notes, notesSize),
			category, s.exercise, s.weight, weightUnit)
	}
	// Watches look the names of the steps' exercises up in the titles,
	// which must come after the steps.
	f.Define(3, exerciseTitleMessage,
		Uint16Field(254),                  // Message index.
		Uint16Field(0),                    // Exercise category.
		Uint16Field(1),                    // Exercise name.
		StringField(2, exerciseTitleSize), // Workout step name.
	)
	for i, title := range titles {
		f.Data(3, uint16(i), uint16(unknownExerciseCategory), uint16(i), StringValue(title, exerciseTitleSize))
	}
	return f.WriteFile(w)
}
