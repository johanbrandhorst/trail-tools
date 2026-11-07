package raceplan

import (
	"math"
	"sort"
	"time"

	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// minPacePlannedMoving is how much planned moving time must be covered
	// before the pace factor is used, as early pace is noisy.
	minPacePlannedMoving = 15 * time.Minute
	// minPaceFactor and maxPaceFactor bound the pace factor to filter out
	// bad data.
	minPaceFactor = 0.5
	maxPaceFactor = 3.0
	// passedMarginMeters is how far past a checkpoint the runner must be to
	// have passed it, to allow for GPS error.
	passedMarginMeters = 200
	// liveMarginMeters is how much further along a LiveTrack position must
	// be than the last recorded checkpoint to be used instead.
	liveMarginMeters = 100
)

// LiveFix is the runner's latest known position from LiveTrack.
type LiveFix struct {
	Time time.Time
	// The position matched to the course, as a distance from the start.
	CourseDistanceMeters float64
}

// progress is the runner's latest known position and time.
type progress struct {
	source   racesv1.EstimateSource
	distance float64
	time     time.Time
	// The checkpoint the runner is at, -1 if between checkpoints.
	atCheckpoint int
}

// EstimateRaceDay estimates the arrival and departure times at each
// checkpoint from the recorded race day data. The last checkpoint is the
// finish, which has no departure: the race is over once it's reached. Recorded checkpoint times are
// used as is. The runner's progress is the most recent recorded checkpoint
// time or LiveTrack position, and the remaining checkpoints are estimated by
// scaling the planned moving time by the runner's pace relative to the plan
// so far, keeping the planned stop times.
func EstimateRaceDay(plan *racesv1.RacePlan, start time.Time, live *LiveFix, now time.Time) *racesv1.RaceDayStatus {
	cps := plan.GetCheckpoints()
	status := &racesv1.RaceDayStatus{}
	status.SetRaceDay(plan.GetRaceDay())
	status.SetStatusTime(timestamppb.New(now))
	status.SetPaceFactor(1)
	status.SetEstimateSource(racesv1.EstimateSource_ESTIMATE_SOURCE_PLAN)

	splits := map[int]*racesv1.CheckpointSplit{}
	for _, s := range plan.GetRaceDay().GetSplits() {
		splits[int(s.GetCheckpointIndex())] = s
	}
	if start.IsZero() || len(cps) == 0 {
		// Without a start time, only the recorded times are known.
		for i, cp := range cps {
			e := racesv1.CheckpointEstimate_builder{
				CheckpointIndex: int32(i),
				Name:            cp.GetName(),
				DistanceMeters:  cp.GetDistanceMeters(),
				Status:          racesv1.CheckpointStatus_CHECKPOINT_STATUS_UPCOMING,
			}
			if s := splits[i]; s != nil {
				e.ArrivalTime, e.ArrivalRecorded = s.GetArrivalTime(), s.HasArrivalTime()
				if i < len(cps)-1 {
					e.DepartureTime, e.DepartureRecorded = s.GetDepartureTime(), s.HasDepartureTime()
				}
			}
			status.SetCheckpoints(append(status.GetCheckpoints(), e.Build()))
		}
		status.SetFinished(splits[len(cps)-1].HasArrivalTime())
		return status
	}
	status.SetStartTime(timestamppb.New(start))

	times := newPlanTimes(cps)
	recordedStop := func(i int) time.Duration {
		if s := splits[i]; s.HasArrivalTime() && s.HasDepartureTime() {
			return s.GetDepartureTime().AsTime().Sub(s.GetArrivalTime().AsTime())
		}
		return times.stop[i]
	}

	// Find the runner's latest known progress.
	known := progress{source: racesv1.EstimateSource_ESTIMATE_SOURCE_PLAN, atCheckpoint: -1, time: start}
	for i := len(cps) - 1; i >= 0; i-- {
		s := splits[i]
		if s.HasDepartureTime() {
			known = progress{source: racesv1.EstimateSource_ESTIMATE_SOURCE_CHECKPOINT, distance: times.dist[i], time: s.GetDepartureTime().AsTime(), atCheckpoint: -1}
			break
		}
		if s.HasArrivalTime() {
			known = progress{source: racesv1.EstimateSource_ESTIMATE_SOURCE_CHECKPOINT, distance: times.dist[i], time: s.GetArrivalTime().AsTime(), atCheckpoint: i}
			break
		}
	}
	if live != nil && live.CourseDistanceMeters > known.distance+liveMarginMeters && live.Time.After(known.time) {
		known = progress{source: racesv1.EstimateSource_ESTIMATE_SOURCE_LIVETRACK, distance: live.CourseDistanceMeters, time: live.Time, atCheckpoint: -1}
	}
	status.SetEstimateSource(known.source)
	status.SetDistanceMeters(known.distance)

	// The pace factor compares the actual moving time with the planned
	// moving time to the known position.
	if known.source != racesv1.EstimateSource_ESTIMATE_SOURCE_PLAN {
		var stops time.Duration
		for i := range cps {
			passed := times.dist[i] < known.distance || (times.dist[i] == known.distance && known.atCheckpoint != i)
			if passed {
				stops += recordedStop(i)
			}
		}
		plannedMoving := times.movingAt(known.distance)
		actualMoving := known.time.Sub(start) - stops
		if plannedMoving >= minPacePlannedMoving && actualMoving > 0 {
			factor := actualMoving.Seconds() / plannedMoving.Seconds()
			status.SetPaceFactor(math.Round(math.Min(maxPaceFactor, math.Max(minPaceFactor, factor))*1000) / 1000)
		}
	}
	factor := status.GetPaceFactor()

	// Walk the checkpoints, estimating from the known position onwards.
	cursorTime, cursorDist := known.time, known.distance
	if known.source == racesv1.EstimateSource_ESTIMATE_SOURCE_PLAN {
		cursorTime, cursorDist = start, 0
	}
	var estimates []*racesv1.CheckpointEstimate
	for i, cp := range cps {
		s := splits[i]
		finish := i == len(cps)-1
		e := racesv1.CheckpointEstimate_builder{
			CheckpointIndex:    int32(i),
			Name:               cp.GetName(),
			DistanceMeters:     cp.GetDistanceMeters(),
			PlannedArrivalTime: timestamppb.New(start.Add(times.arrival[i])),
		}
		if cp.GetCutoffSeconds() > 0 {
			e.CutoffTime = timestamppb.New(start.Add(time.Duration(cp.GetCutoffSeconds()) * time.Second))
		}
		passed := known.distance > times.dist[i]+passedMarginMeters ||
			(known.source == racesv1.EstimateSource_ESTIMATE_SOURCE_CHECKPOINT && known.distance > times.dist[i])
		var arrival, departure time.Time
		switch {
		case s.HasArrivalTime():
			arrival = s.GetArrivalTime().AsTime()
			e.ArrivalRecorded = true
		case passed:
			// Passed without a recorded time: assume the plan, scaled by
			// the pace.
			arrival = start.Add(time.Duration(float64(times.moving[i])*factor) + times.stopsBefore(i))
		default:
			arrival = cursorTime.Add(time.Duration(float64(times.moving[i]-times.movingAt(cursorDist)) * factor))
			if arrival.Before(now) {
				// The runner should have arrived by now.
				e.Overdue = true
				arrival = now
			}
		}
		switch {
		case finish:
			// The race ends at the finish.
			departure = arrival
		case s.HasDepartureTime():
			departure = s.GetDepartureTime().AsTime()
			e.DepartureRecorded = true
		case e.ArrivalRecorded && !passed:
			// At the checkpoint: leaving after the planned stop.
			departure = arrival.Add(times.stop[i])
			if departure.Before(now) {
				departure = now
			}
		default:
			departure = arrival.Add(times.stop[i])
		}
		switch {
		case e.DepartureRecorded || passed:
			e.Status = racesv1.CheckpointStatus_CHECKPOINT_STATUS_DEPARTED
		case e.ArrivalRecorded:
			e.Status = racesv1.CheckpointStatus_CHECKPOINT_STATUS_ARRIVED
		default:
			e.Status = racesv1.CheckpointStatus_CHECKPOINT_STATUS_UPCOMING
		}
		if e.Status != racesv1.CheckpointStatus_CHECKPOINT_STATUS_DEPARTED || times.dist[i] >= cursorDist {
			cursorTime, cursorDist = departure, times.dist[i]
		}
		e.ArrivalTime = timestamppb.New(arrival)
		if !finish {
			e.DepartureTime = timestamppb.New(departure)
		}
		e.DelaySeconds = int32(arrival.Sub(start.Add(times.arrival[i])).Seconds())
		if e.CutoffTime != nil {
			e.CutoffBufferSeconds = int32(e.CutoffTime.AsTime().Sub(arrival).Seconds())
		}
		estimates = append(estimates, e.Build())
	}
	status.SetCheckpoints(estimates)
	status.SetFinished(estimates[len(estimates)-1].GetArrivalRecorded())
	status.SetEstimatedFinishTime(estimates[len(estimates)-1].GetArrivalTime())
	status.SetEstimatedDistanceMeters(estimatedDistance(estimates, known, start, now))
	return status
}

// estimatedDistance estimates the distance covered now by interpolating in
// time between the latest known progress and the estimated checkpoint times.
func estimatedDistance(estimates []*racesv1.CheckpointEstimate, known progress, start, now time.Time) float64 {
	if now.Before(start) {
		return 0
	}
	prevTime, prevDist := start, 0.0
	if known.source != racesv1.EstimateSource_ESTIMATE_SOURCE_PLAN && !known.time.After(now) {
		prevTime, prevDist = known.time, known.distance
	}
	for _, e := range estimates {
		d := e.GetDistanceMeters()
		departed := e.GetStatus() == racesv1.CheckpointStatus_CHECKPOINT_STATUS_DEPARTED
		if d < prevDist || (d == prevDist && departed) {
			continue
		}
		arrival, departure := e.GetArrivalTime().AsTime(), e.GetDepartureTime().AsTime()
		if !e.HasDepartureTime() {
			// The finish.
			departure = arrival
		}
		if now.Before(arrival) {
			if !arrival.After(prevTime) {
				return d
			}
			f := now.Sub(prevTime).Seconds() / arrival.Sub(prevTime).Seconds()
			return prevDist + math.Max(0, math.Min(1, f))*(d-prevDist)
		}
		if now.Before(departure) {
			return d
		}
		prevTime, prevDist = departure, d
	}
	return prevDist
}

// planTimes are the planned cumulative times at each checkpoint.
type planTimes struct {
	dist []float64
	// Cumulative planned moving time to each checkpoint.
	moving []time.Duration
	// Planned stop time at each checkpoint.
	stop []time.Duration
	// Planned elapsed time when arriving at each checkpoint.
	arrival []time.Duration
}

func newPlanTimes(cps []*racesv1.Checkpoint) *planTimes {
	p := &planTimes{}
	var moving, elapsed time.Duration
	for _, cp := range cps {
		segment := time.Duration(cp.GetPlannedSegmentSeconds()) * time.Second
		stop := time.Duration(cp.GetPlannedStopSeconds()) * time.Second
		moving += segment
		elapsed += segment
		p.dist = append(p.dist, cp.GetDistanceMeters())
		p.moving = append(p.moving, moving)
		p.stop = append(p.stop, stop)
		p.arrival = append(p.arrival, elapsed)
		elapsed += stop
	}
	return p
}

// movingAt returns the planned moving time to a distance, interpolating
// within segments.
func (p *planTimes) movingAt(d float64) time.Duration {
	i := sort.SearchFloat64s(p.dist, d)
	if i >= len(p.dist) {
		return p.moving[len(p.moving)-1]
	}
	prevDist, prevMoving := 0.0, time.Duration(0)
	if i > 0 {
		prevDist, prevMoving = p.dist[i-1], p.moving[i-1]
	}
	if p.dist[i] <= prevDist {
		return p.moving[i]
	}
	f := (d - prevDist) / (p.dist[i] - prevDist)
	return prevMoving + time.Duration(float64(p.moving[i]-prevMoving)*f)
}

// stopsBefore returns the planned stop time at checkpoints before i.
func (p *planTimes) stopsBefore(i int) time.Duration {
	var total time.Duration
	for j := range i {
		total += p.stop[j]
	}
	return total
}

// PointAtDistance returns the point on the course at a distance from the
// start, interpolating between course points.
func PointAtDistance(course []geo.Point, d float64) (geo.Point, bool) {
	if len(course) == 0 {
		return geo.Point{}, false
	}
	dists := geo.CumulativeDistances(course)
	i := sort.SearchFloat64s(dists, d)
	switch {
	case i == 0:
		return course[0], true
	case i >= len(course):
		return course[len(course)-1], true
	}
	f := (d - dists[i-1]) / (dists[i] - dists[i-1])
	return geo.Interpolate(course[i-1], course[i], f), true
}

// SnapToCourse matches a position to the race course and returns its
// distance from the start, projecting the position onto the nearest segment
// of the course. Courses can pass the same place several times, so only the
// part of the course near the distance recorded by the watch is considered.
func SnapToCourse(course []geo.Point, p geo.Point, watchDistance float64) float64 {
	if len(course) < 2 {
		return watchDistance
	}
	dists := geo.CumulativeDistances(course)
	lo, hi := watchDistance*0.8-3000, watchDistance*1.2+3000
	// Project onto a local plane around the position, which is accurate
	// enough over the length of a segment.
	cosLat := math.Cos(p.Lat * math.Pi / 180)
	toPlane := func(q geo.Point) (x, y float64) {
		return (q.Lng - p.Lng) * cosLat, q.Lat - p.Lat
	}
	best, bestDist := watchDistance, math.Inf(1)
	for i := 0; i+1 < len(course); i++ {
		if dists[i+1] < lo || dists[i] > hi {
			continue
		}
		ax, ay := toPlane(course[i])
		bx, by := toPlane(course[i+1])
		dx, dy := bx-ax, by-ay
		f := 0.0
		if lenSq := dx*dx + dy*dy; lenSq > 0 {
			// The position is the origin of the plane.
			f = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/lenSq))
		}
		x, y := ax+f*dx, ay+f*dy
		if d := x*x + y*y; d < bestDist {
			best, bestDist = dists[i]+f*(dists[i+1]-dists[i]), d
		}
	}
	return best
}
