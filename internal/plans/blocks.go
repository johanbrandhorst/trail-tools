package plans

import "strings"

// StrengthBlock is exercises of a strength workout done together for a
// number of rounds: a single exercise for its sets, or a superset or
// circuit of several, done back to back.
type StrengthBlock struct {
	// The indices of the exercises in the workout's exercises.
	Exercises []int
	// How many sets, or rounds of a superset. At least 1.
	Rounds int
}

// isRoundGroup reports whether a group of exercises is done back to back for
// rounds, rather than each exercise for its sets: a superset or circuit.
func isRoundGroup(group string) bool {
	group = strings.ToLower(group)
	return strings.Contains(group, "superset") || strings.Contains(group, "circuit")
}

// StrengthBlocks returns the blocks of a strength workout's exercises:
// each exercise for its sets, and the consecutive exercises of a superset
// or circuit group together, for the most sets of them.
func StrengthBlocks(exercises []*PlannedExercise) []StrengthBlock {
	var blocks []StrengthBlock
	for i := 0; i < len(exercises); {
		end := i + 1
		if group := exercises[i].Group; group != "" && isRoundGroup(group) {
			for end < len(exercises) && exercises[end].Group == group {
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

// StrengthStepExercises returns the exercise of each step of a strength
// workout as watches lay it out, from StrengthBlocks: each exercise of a
// block, followed by its rest if it has one, then a step repeating the
// block if it has more than one round. Rest and repeat steps are -1. The
// sets a watch records refer to these steps.
func StrengthStepExercises(exercises []*PlannedExercise) []int {
	var steps []int
	for _, block := range StrengthBlocks(exercises) {
		for _, i := range block.Exercises {
			steps = append(steps, i)
			if exercises[i].RestSeconds > 0 {
				steps = append(steps, -1)
			}
		}
		if block.Rounds > 1 {
			steps = append(steps, -1)
		}
	}
	return steps
}
