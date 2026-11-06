package plans

// StrengthBlock is exercises of a strength workout done together for a
// number of rounds: a single exercise for its sets, or a superset or
// circuit of several, done back to back.
type StrengthBlock struct {
	// The indices of the exercises in the workout's exercises.
	Exercises []int
	// How many sets, or rounds of a superset. At least 1.
	Rounds int
}

// StrengthBlocks returns the blocks of a strength workout's exercises:
// each exercise for its sets, and the consecutive exercises of a superset
// or circuit group together, for the most sets of them.
func StrengthBlocks(exercises []*PlannedExercise) []StrengthBlock {
	var blocks []StrengthBlock
	for i := 0; i < len(exercises); {
		end := i + 1
		if e := exercises[i]; e.Group != "" && e.GroupKind.Rounds() {
			for end < len(exercises) && exercises[end].Group == e.Group {
				end++
			}
		}
		block := StrengthBlock{}
		for j := i; j < end; j++ {
			block.Rounds = max(block.Rounds, int(exercises[j].Sets))
			block.Exercises = append(block.Exercises, j)
		}
		blocks = append(blocks, block)
		i = end
	}
	return blocks
}

// StepKind is the kind of a step of a strength workout on a watch.
type StepKind int

const (
	// StepExercise does the exercise: its reps, its hold or until the lap
	// button.
	StepExercise StepKind = iota
	// StepGetReady waits for the lap button before a hold, so its timer
	// starts once the user is in position rather than as the last step
	// ends.
	StepGetReady
	// StepRest is the rest after the exercise.
	StepRest
)

// ExerciseStep is a step of an exercise on a watch.
type ExerciseStep struct {
	Kind StepKind
	// For an exercise held on each side, the side, 1 or 2, of an exercise
	// or get ready step. 0 otherwise.
	Side int
}

// ExerciseSteps returns the steps of a set of an exercise on a watch: a
// hold waits for the lap button before its timer, and a hold on each side
// is done for each side in turn, then the rest. Exercises done for reps or
// until done are a single step, with reps per side.
func ExerciseSteps(e *PlannedExercise) []ExerciseStep {
	var steps []ExerciseStep
	switch {
	case e.HoldSeconds > 0 && e.ExerciseEachSide:
		for side := 1; side <= 2; side++ {
			steps = append(steps, ExerciseStep{Kind: StepGetReady, Side: side}, ExerciseStep{Kind: StepExercise, Side: side})
		}
	case e.HoldSeconds > 0:
		steps = append(steps, ExerciseStep{Kind: StepGetReady}, ExerciseStep{Kind: StepExercise})
	default:
		steps = append(steps, ExerciseStep{Kind: StepExercise})
	}
	if e.RestSeconds > 0 {
		steps = append(steps, ExerciseStep{Kind: StepRest})
	}
	return steps
}

const (
	// StepNoExercise marks a step without an exercise in
	// StrengthStepExercises: a get ready, rest or repeat step.
	StepNoExercise = -1
	// StepSecondSide marks the second side of a hold on each side in
	// StrengthStepExercises, whose set is the same set as the first side.
	StepSecondSide = -2
)

// StrengthStepExercises returns the exercise of each step of a strength
// workout as watches lay it out, from StrengthBlocks: the ExerciseSteps of
// each exercise of a block, then a step repeating the block if it has more
// than one round. Steps without an exercise are StepNoExercise, and the
// second sides of holds on each side StepSecondSide. The sets a watch
// records refer to these steps.
func StrengthStepExercises(exercises []*PlannedExercise) []int {
	var steps []int
	for _, block := range StrengthBlocks(exercises) {
		for _, i := range block.Exercises {
			for _, step := range ExerciseSteps(exercises[i]) {
				switch {
				case step.Kind != StepExercise:
					steps = append(steps, StepNoExercise)
				case step.Side == 2:
					steps = append(steps, StepSecondSide)
				default:
					steps = append(steps, i)
				}
			}
		}
		if block.Rounds > 1 {
			steps = append(steps, StepNoExercise)
		}
	}
	return steps
}
