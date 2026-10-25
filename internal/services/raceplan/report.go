package raceplan

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/races"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// PlanOutcome is what a race plan planned, and what was recorded on race
// day, for comparing with what happened, e.g. in a race report.
type PlanOutcome struct {
	Goals races.RaceGoals
	// The planned finish time in seconds, 0 if the plan has no checkpoints.
	PlannedFinishSeconds int32
	Checkpoints          []PlannedCheckpoint
	// When the race started on race day, zero if it wasn't recorded.
	RaceDayStart time.Time
}

// PlannedCheckpoint is a checkpoint of a race plan.
type PlannedCheckpoint struct {
	Name           string
	DistanceMeters float64
	// The planned elapsed time at arrival in seconds.
	PlannedArrivalSeconds int32
	// The cutoff as elapsed time in seconds, 0 if none.
	CutoffSeconds int32
	// The arrival time recorded on race day, zero if it wasn't recorded.
	RecordedArrival time.Time
}

// PlanOutcome returns the plan of one of the user's races, analysed, with
// what was recorded on race day. It returns nil if the race has no plan.
//
// Public race reports call this on every view, so it only reads the plan:
// the planned times don't depend on the race or its course.
func (s *Service) PlanOutcome(ctx context.Context, userID, raceID pgtype.UUID) (*PlanOutcome, error) {
	stored, err := s.repo.GetRacePlanForRace(ctx, userID, raceID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	analysis := Analyze(planToProto(stored, ""), RaceInfo{})
	outcome := &PlanOutcome{
		Goals:                stored.Goals,
		PlannedFinishSeconds: analysis.GetPlannedFinishSeconds(),
	}
	recorded := map[int32]time.Time{}
	outcome.RaceDayStart = stored.RaceDay.StartTime
	for _, split := range stored.RaceDay.Splits {
		recorded[split.CheckpointIndex] = split.ArrivalTime
	}
	analysed := analysis.GetCheckpoints()
	for i, cp := range stored.Checkpoints {
		planned := PlannedCheckpoint{
			Name:            cp.Name,
			DistanceMeters:  cp.DistanceMeters,
			CutoffSeconds:   cp.CutoffSeconds,
			RecordedArrival: recorded[int32(i)],
		}
		if i < len(analysed) {
			planned.PlannedArrivalSeconds = analysed[i].GetArrivalSeconds()
		}
		outcome.Checkpoints = append(outcome.Checkpoints, planned)
	}
	return outcome, nil
}
