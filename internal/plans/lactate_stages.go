package plans

import "fmt"

// LactateTestStages is how a lactate test is run: stages of StageMinutes at
// heart rates from StartBPM up to EndBPM, StepBPM apart, each held within
// RangeBPM of its heart rate.
type LactateTestStages struct {
	StartBPM     int32
	StepBPM      int32
	EndBPM       int32
	StageMinutes int32
	RangeBPM     int32
}

// DefaultLactateTestStages are the stages of a test without a previous one,
// the same as the web app's guided test.
var DefaultLactateTestStages = LactateTestStages{
	StartBPM:     100,
	StepBPM:      10,
	EndBPM:       180,
	StageMinutes: 4,
	RangeBPM:     5,
}

// LactateTestWarmUpMinutes is how long a test's warm-up lasts.
const LactateTestWarmUpMinutes = 15

// minLactateTestStages is the fewest stages a test from a high start heart
// rate still gets, so it reaches past the aerobic threshold.
const minLactateTestStages = 5

// LactateTestStagesFrom returns the stages of a test starting at startBPM,
// like the user's last test, or the defaults if it's 0. Tests that start
// where the last one did are comparable.
func LactateTestStagesFrom(startBPM int32) LactateTestStages {
	s := DefaultLactateTestStages
	if startBPM <= 0 {
		return s
	}
	s.StartBPM = startBPM
	if last := startBPM + (minLactateTestStages-1)*s.StepBPM; s.EndBPM < last {
		s.EndBPM = last
	}
	return s
}

// Blocks returns the test as the blocks of a structured run, for watches: a
// warm-up, told to stay below the first stage, then each stage held in its heart rate
// range followed by an open step to sample lactate in, which ends at the lap
// button, and a cool-down.
func (s LactateTestStages) Blocks() []RunBlock {
	steps := []RunStep{{
		Kind:            RunStepWarmUp,
		DurationSeconds: LactateTestWarmUpMinutes * 60,
		Notes:           fmt.Sprintf("Very easy, below %d bpm", s.StartBPM),
	}}
	for bpm := s.StartBPM; bpm <= s.EndBPM; bpm += s.StepBPM {
		steps = append(steps,
			RunStep{
				Kind:            RunStepRun,
				DurationSeconds: s.StageMinutes * 60,
				HeartRate:       &HeartRateTarget{LowBPM: bpm - s.RangeBPM, HighBPM: bpm + s.RangeBPM},
				Notes:           fmt.Sprintf("%d bpm: hold %d-%d bpm, then take a sample", bpm, bpm-s.RangeBPM, bpm+s.RangeBPM),
			},
			RunStep{
				Kind:  RunStepRest,
				Notes: "Measure your lactate, then press lap",
			},
		)
	}
	steps = append(steps, RunStep{Kind: RunStepCoolDown, Notes: "Easy, until you press lap"})
	return []RunBlock{{Repeats: 1, Steps: steps}}
}
