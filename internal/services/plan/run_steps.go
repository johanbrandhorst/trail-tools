package plan

import (
	"fmt"

	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
	"github.com/bs-tools/trailrunn.ing/internal/plans"
)

const (
	// maxRunBlocks and maxRunSteps bound a structured run. Garmin takes 100
	// steps, counting each repeat as one too, which 50 steps in 20 blocks
	// stay within.
	maxRunBlocks = 20
	maxRunSteps  = 50
	maxRepeats   = 50
	// maxStepSeconds and maxStepMeters bound a step's duration.
	maxStepSeconds = 6 * 3600
	maxStepMeters  = 100_000
	// The heart rates and paces a target may have.
	minHeartRateBPM, maxHeartRateBPM = 40, 230
	maxThresholdOffsetBPM            = 80
	minSecondsPerKilometer           = 120
	maxSecondsPerKilometer           = 1200
	maxRunStepNotesLength            = 200
)

var runStepKinds = map[plansv1.RunStepKind]plans.RunStepKind{
	plansv1.RunStepKind_RUN_STEP_KIND_WARM_UP:   plans.RunStepWarmUp,
	plansv1.RunStepKind_RUN_STEP_KIND_RUN:       plans.RunStepRun,
	plansv1.RunStepKind_RUN_STEP_KIND_RECOVERY:  plans.RunStepRecovery,
	plansv1.RunStepKind_RUN_STEP_KIND_REST:      plans.RunStepRest,
	plansv1.RunStepKind_RUN_STEP_KIND_COOL_DOWN: plans.RunStepCoolDown,
}

var heartRateReferences = map[plansv1.HeartRateReference]plans.HeartRateReference{
	plansv1.HeartRateReference_HEART_RATE_REFERENCE_UNSPECIFIED:         plans.HeartRateBPM,
	plansv1.HeartRateReference_HEART_RATE_REFERENCE_AEROBIC_THRESHOLD:   plans.HeartRateAerobicThreshold,
	plansv1.HeartRateReference_HEART_RATE_REFERENCE_ANAEROBIC_THRESHOLD: plans.HeartRateAnaerobicThreshold,
}

// runBlocksFromProto validates a run's blocks.
func runBlocksFromProto(blocks []*plansv1.RunBlock) ([]plans.RunBlock, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	if len(blocks) > maxRunBlocks {
		return nil, fmt.Errorf("blocks: at most %d are allowed", maxRunBlocks)
	}
	steps := 0
	result := make([]plans.RunBlock, len(blocks))
	for i, b := range blocks {
		repeats := int(b.GetRepeats())
		if repeats == 0 {
			repeats = 1
		}
		if repeats < 1 || repeats > maxRepeats {
			return nil, fmt.Errorf("blocks[%d]: repeats must be from 1 to %d", i, maxRepeats)
		}
		if len(b.GetSteps()) == 0 {
			return nil, fmt.Errorf("blocks[%d]: a block needs at least one step", i)
		}
		steps += len(b.GetSteps())
		if steps > maxRunSteps {
			return nil, fmt.Errorf("blocks: at most %d steps are allowed in all", maxRunSteps)
		}
		result[i].Repeats = repeats
		for j, st := range b.GetSteps() {
			step, err := runStepFromProto(st)
			if err != nil {
				return nil, fmt.Errorf("blocks[%d].steps[%d]: %w", i, j, err)
			}
			result[i].Steps = append(result[i].Steps, step)
		}
	}
	return result, nil
}

func runStepFromProto(st *plansv1.RunStep) (plans.RunStep, error) {
	kind, ok := runStepKinds[st.GetKind()]
	if !ok {
		return plans.RunStep{}, fmt.Errorf("kind is required: WARM_UP, RUN, RECOVERY, REST or COOL_DOWN")
	}
	step := plans.RunStep{Kind: kind, DurationSeconds: st.GetDurationSeconds(), DistanceMeters: st.GetDistanceMeters(), Notes: st.GetNotes()}
	switch {
	case step.DurationSeconds < 0 || step.DurationSeconds > maxStepSeconds:
		return step, fmt.Errorf("duration_seconds must be from 0 to %d", maxStepSeconds)
	case step.DistanceMeters < 0 || step.DistanceMeters > maxStepMeters:
		return step, fmt.Errorf("distance_meters must be from 0 to %d", maxStepMeters)
	case step.DurationSeconds > 0 && step.DistanceMeters > 0:
		return step, fmt.Errorf("set duration_seconds or distance_meters, not both")
	case len([]rune(step.Notes)) > maxRunStepNotesLength:
		return step, fmt.Errorf("notes must be at most %d characters", maxRunStepNotesLength)
	}
	switch st.WhichTarget() {
	case plansv1.RunStep_HeartRate_case:
		hr := st.GetHeartRate()
		reference, ok := heartRateReferences[hr.GetReference()]
		if !ok {
			return step, fmt.Errorf("heart_rate.reference is unknown")
		}
		low, high := hr.GetLowBpm(), hr.GetHighBpm()
		if high < low {
			return step, fmt.Errorf("heart_rate.high_bpm must be at least low_bpm")
		}
		if reference == plans.HeartRateBPM && (low < minHeartRateBPM || high > maxHeartRateBPM) {
			return step, fmt.Errorf("heart_rate must be from %d to %d bpm", minHeartRateBPM, maxHeartRateBPM)
		}
		if reference != plans.HeartRateBPM && (low < -maxThresholdOffsetBPM || high > maxThresholdOffsetBPM) {
			return step, fmt.Errorf("heart_rate relative to a threshold must be within %d bpm of it", maxThresholdOffsetBPM)
		}
		step.HeartRate = &plans.HeartRateTarget{Reference: reference, LowBPM: low, HighBPM: high}
	case plansv1.RunStep_Pace_case:
		pace := st.GetPace()
		fast, slow := pace.GetFastSecondsPerKilometer(), pace.GetSlowSecondsPerKilometer()
		if fast < minSecondsPerKilometer || slow > maxSecondsPerKilometer || slow < fast {
			return step, fmt.Errorf("pace must be from %d to %d seconds per kilometer, fast_seconds_per_kilometer first", minSecondsPerKilometer, maxSecondsPerKilometer)
		}
		step.Pace = &plans.PaceTarget{FastSecondsPerKilometer: fast, SlowSecondsPerKilometer: slow}
	}
	return step, nil
}

// runBlocksToProto converts a run's blocks.
func runBlocksToProto(blocks []plans.RunBlock) []*plansv1.RunBlock {
	result := make([]*plansv1.RunBlock, len(blocks))
	for i, b := range blocks {
		steps := make([]*plansv1.RunStep, len(b.Steps))
		for j, st := range b.Steps {
			sb := plansv1.RunStep_builder{
				Kind:            runStepKindToProto(st.Kind),
				DurationSeconds: st.DurationSeconds,
				DistanceMeters:  st.DistanceMeters,
				Notes:           st.Notes,
			}
			if st.HeartRate != nil {
				sb.HeartRate = plansv1.HeartRateTarget_builder{
					Reference: heartRateReferenceToProto(st.HeartRate.Reference),
					LowBpm:    st.HeartRate.LowBPM,
					HighBpm:   st.HeartRate.HighBPM,
				}.Build()
			}
			if st.Pace != nil {
				sb.Pace = plansv1.PaceTarget_builder{
					FastSecondsPerKilometer: st.Pace.FastSecondsPerKilometer,
					SlowSecondsPerKilometer: st.Pace.SlowSecondsPerKilometer,
				}.Build()
			}
			steps[j] = sb.Build()
		}
		result[i] = plansv1.RunBlock_builder{Repeats: int32(b.Repeats), Steps: steps}.Build()
	}
	return result
}

func runStepKindToProto(kind plans.RunStepKind) plansv1.RunStepKind {
	for k, v := range runStepKinds {
		if v == kind {
			return k
		}
	}
	return plansv1.RunStepKind_RUN_STEP_KIND_UNSPECIFIED
}

func heartRateReferenceToProto(reference plans.HeartRateReference) plansv1.HeartRateReference {
	for k, v := range heartRateReferences {
		if v == reference {
			return k
		}
	}
	return plansv1.HeartRateReference_HEART_RATE_REFERENCE_UNSPECIFIED
}
