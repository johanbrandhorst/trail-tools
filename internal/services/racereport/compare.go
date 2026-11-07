package racereport

import (
	"math"
	"sort"
	"strings"

	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/races"
	"github.com/johanbrandhorst/trail-tools/internal/services/raceplan"
	"github.com/johanbrandhorst/trail-tools/internal/track"
)

// recording is an activity's elapsed time by distance.
type recording struct {
	times     []int32
	distances []float64
}

// newRecording returns the recording of an activity's streams, or nil if
// they have no times or distances.
func newRecording(streams *activitiesv1.ActivityStreams) *recording {
	times, distances := streams.GetTimeSeconds(), streams.GetDistanceMeters()
	if len(times) < 2 || len(times) != len(distances) {
		return nil
	}
	return &recording{times: times, distances: distances}
}

// trackRecording returns the recording of a report's track, or nil if it
// has no times.
func trackRecording(t *track.Track) *recording {
	if len(t.Times) < 2 || len(t.Times) != len(t.Dists) {
		return nil
	}
	return &recording{times: t.Times, distances: t.Dists}
}

// totalMeters returns the recorded distance.
func (r *recording) totalMeters() float64 {
	return r.distances[len(r.distances)-1]
}

// endMargin is how far beyond the end of a recording a distance may be and
// still be taken as its end, for rounding errors of scaled distances.
const endMargin = 0.01

// elapsedAt returns the elapsed time in seconds when the recording first
// reached a distance, interpolated between samples. ok is false beyond the
// recorded distance: it never extrapolates.
func (r *recording) elapsedAt(meters float64) (seconds int32, ok bool) {
	if meters > r.totalMeters() {
		if meters-r.totalMeters() > endMargin {
			return 0, false
		}
		meters = r.totalMeters()
	}
	i := sort.SearchFloat64s(r.distances, meters)
	if i == 0 {
		return r.times[0], true
	}
	before, after := r.distances[i-1], r.distances[i]
	f := 0.0
	if after > before {
		f = (meters - before) / (after - before)
	}
	elapsed := float64(r.times[i-1]) + f*float64(r.times[i]-r.times[i-1])
	return int32(math.Round(elapsed)), true
}

// maxDistanceError is the largest difference between the distance of a
// recording and that of the stretch of course it covers, as a fraction of
// the latter, that is put down to GPS error.
const maxDistanceError = 0.1

// scaleTo returns the factor from course distances to the recording's
// distances, if the recording covers a stretch of course from the start of
// the given length. ok is false if it doesn't: a recording much shorter than
// the course, e.g. of a runner who stopped, would otherwise be stretched
// over all of it, giving arrival times at checkpoints the runner never
// reached, and one much longer has more than the race in it.
func (r *recording) scaleTo(courseMeters float64) (scale float64, ok bool) {
	if courseMeters <= 0 {
		return 0, false
	}
	scale = r.totalMeters() / courseMeters
	if math.Abs(scale-1) > maxDistanceError {
		return 0, false
	}
	return scale, true
}

// stopMeters returns where a runner who did not finish stopped, as the
// distance along the course, 0 if unknown: the stop's distance, or that of
// the plan's checkpoint where they stopped, whichever is further.
func stopMeters(checkpoints []raceplan.PlannedCheckpoint, stop races.RaceStop) float64 {
	meters := stop.DistanceMeters
	name := strings.TrimSpace(stop.Checkpoint)
	if name == "" {
		return meters
	}
	for _, cp := range checkpoints {
		if strings.EqualFold(strings.TrimSpace(cp.Name), name) {
			return max(meters, cp.DistanceMeters)
		}
	}
	return meters
}

// compare compares a race plan with a report of what happened: the finish
// time against the goals, and the arrival times at the checkpoints, from the
// recording if any, or else from the race day splits. The recording's
// distances are scaled to the course's, as GPS distances differ from course
// distances, if it covers the course, or for a DNF the course up to where
// the runner stopped. A DNF meets no goal, and has no arrival times at the
// finish or beyond where the runner stopped.
func compare(outcome *raceplan.PlanOutcome, report *races.RaceReport, rec *recording) *racesv1.RaceComparison {
	b := racesv1.RaceComparison_builder{}
	if outcome == nil {
		return b.Build()
	}
	b.HasPlan = true
	b.PlannedFinishSeconds = outcome.PlannedFinishSeconds
	finishSeconds := report.FinishSeconds
	if report.DidNotFinish {
		finishSeconds = 0
	}
	for _, goal := range []struct {
		label  string
		target int32
	}{
		{"A", outcome.Goals.AFinishSeconds},
		{"B", outcome.Goals.BFinishSeconds},
		{"C", outcome.Goals.CFinishSeconds},
	} {
		if goal.target <= 0 {
			continue
		}
		result := racesv1.GoalResult_builder{Goal: goal.label, TargetSeconds: goal.target}
		if finishSeconds > 0 {
			result.Met = finishSeconds <= goal.target
			result.DifferenceSeconds = finishSeconds - goal.target
		}
		b.Goals = append(b.Goals, result.Build())
	}

	raceMeters := report.DistanceMeters
	if raceMeters <= 0 && len(outcome.Checkpoints) > 0 {
		raceMeters = outcome.Checkpoints[len(outcome.Checkpoints)-1].DistanceMeters
	}
	// The course the recording covers: all of it, or up to the stop.
	courseMeters := raceMeters
	var stop float64
	if report.DidNotFinish {
		stop = stopMeters(outcome.Checkpoints, report.Stop)
		courseMeters = stop
	}
	scale := 1.0
	if rec != nil {
		if s, ok := rec.scaleTo(courseMeters); ok {
			scale = s
			b.ActualTimesScaled = true
		}
	}
	hasRaceDay := !outcome.RaceDayStart.IsZero()
	for _, cp := range outcome.Checkpoints {
		notReached := report.DidNotFinish &&
			((stop > 0 && cp.DistanceMeters > stop) || (raceMeters > 0 && cp.DistanceMeters >= raceMeters))
		result := racesv1.CheckpointResult_builder{
			Name:                  cp.Name,
			DistanceMeters:        cp.DistanceMeters,
			PlannedArrivalSeconds: cp.PlannedArrivalSeconds,
			CutoffSeconds:         cp.CutoffSeconds,
			NotReached:            notReached,
		}
		var actual int32
		found := false
		switch {
		case rec != nil:
			b.ActualTimesSource = racesv1.ActualTimesSource_ACTUAL_TIMES_SOURCE_ACTIVITY
			if !notReached {
				actual, found = rec.elapsedAt(cp.DistanceMeters * scale)
			}
		case hasRaceDay && !cp.RecordedArrival.IsZero():
			b.ActualTimesSource = racesv1.ActualTimesSource_ACTUAL_TIMES_SOURCE_RACE_DAY
			if !notReached {
				actual = int32(cp.RecordedArrival.Sub(outcome.RaceDayStart).Seconds())
				found = true
			}
		}
		if found {
			difference := actual - cp.PlannedArrivalSeconds
			result.ActualArrivalSeconds = &actual
			result.DifferenceSeconds = &difference
		}
		b.Checkpoints = append(b.Checkpoints, result.Build())
	}
	return b.Build()
}
