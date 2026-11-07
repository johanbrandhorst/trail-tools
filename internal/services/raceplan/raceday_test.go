package raceplan

import (
	"testing"
	"time"

	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// testPlan has three 10km segments of 1 hour each, with 10 minute stops at
// the first two checkpoints.
func testPlan(splits ...*racesv1.CheckpointSplit) *racesv1.RacePlan {
	cp := func(name string, dist float64, stop, cutoff int32) *racesv1.Checkpoint {
		return racesv1.Checkpoint_builder{
			Name: name, DistanceMeters: dist, PlannedSegmentSeconds: 3600, PlannedStopSeconds: stop, CutoffSeconds: cutoff,
		}.Build()
	}
	return racesv1.RacePlan_builder{
		Checkpoints: []*racesv1.Checkpoint{
			cp("CP1", 10000, 600, 0),
			cp("CP2", 20000, 600, 3*3600),
			cp("Finish", 30000, 0, 0),
		},
		RaceDay: racesv1.RaceDay_builder{Splits: splits}.Build(),
	}.Build()
}

var raceStart = time.Date(2027, 4, 17, 7, 0, 0, 0, time.UTC)

func split(i int32, arrival, departure time.Duration) *racesv1.CheckpointSplit {
	b := racesv1.CheckpointSplit_builder{CheckpointIndex: i}
	if arrival > 0 {
		b.ArrivalTime = timestamppb.New(raceStart.Add(arrival))
	}
	if departure > 0 {
		b.DepartureTime = timestamppb.New(raceStart.Add(departure))
	}
	return b.Build()
}

func clock(ts *timestamppb.Timestamp) time.Duration {
	return ts.AsTime().Sub(raceStart)
}

func TestEstimateRaceDayBeforeStart(t *testing.T) {
	t.Parallel()
	status := EstimateRaceDay(testPlan(), raceStart, nil, raceStart.Add(-time.Hour))
	if status.GetEstimateSource() != racesv1.EstimateSource_ESTIMATE_SOURCE_PLAN || status.GetPaceFactor() != 1 {
		t.Errorf("unexpected status %v", status)
	}
	cps := status.GetCheckpoints()
	// The plan: CP1 at 1:00, CP2 at 2:10, finish at 3:20.
	for i, want := range []time.Duration{time.Hour, 130 * time.Minute, 200 * time.Minute} {
		if got := clock(cps[i].GetArrivalTime()); got != want || cps[i].GetOverdue() || cps[i].GetDelaySeconds() != 0 {
			t.Errorf("checkpoint %d: got arrival %v, want %v (%v)", i, got, want, cps[i])
		}
	}
	if cps[1].GetCutoffBufferSeconds() != 50*60 {
		t.Errorf("got CP2 buffer %d", cps[1].GetCutoffBufferSeconds())
	}

	// Without a start time there are no estimates.
	status = EstimateRaceDay(testPlan(), time.Time{}, nil, raceStart)
	if status.HasStartTime() || status.GetCheckpoints()[0].HasArrivalTime() {
		t.Errorf("unexpected estimates without a start %v", status)
	}
}

func TestEstimateRaceDayFromCheckpoints(t *testing.T) {
	t.Parallel()
	// Arrived at CP1 after 1:12 (20% slower), left after a 15 minute stop.
	plan := testPlan(split(0, 72*time.Minute, 87*time.Minute))
	status := EstimateRaceDay(plan, raceStart, nil, raceStart.Add(90*time.Minute))
	if status.GetEstimateSource() != racesv1.EstimateSource_ESTIMATE_SOURCE_CHECKPOINT || status.GetPaceFactor() != 1.2 {
		t.Fatalf("unexpected status source %v factor %v", status.GetEstimateSource(), status.GetPaceFactor())
	}
	cps := status.GetCheckpoints()
	if cps[0].GetStatus() != racesv1.CheckpointStatus_CHECKPOINT_STATUS_DEPARTED || !cps[0].GetArrivalRecorded() || !cps[0].GetDepartureRecorded() {
		t.Errorf("unexpected CP1 %v", cps[0])
	}
	// CP2: left CP1 at 1:27, plus 1:12 at the slower pace.
	if got := clock(cps[1].GetArrivalTime()); got != 159*time.Minute {
		t.Errorf("got CP2 arrival %v, want 2:39", got)
	}
	if cps[1].GetDelaySeconds() != 29*60 || cps[1].GetCutoffBufferSeconds() != 21*60 || cps[1].GetStatus() != racesv1.CheckpointStatus_CHECKPOINT_STATUS_UPCOMING {
		t.Errorf("unexpected CP2 %v", cps[1])
	}
	// 3 minutes out of CP1 of an estimated 72 minute segment.
	if got := status.GetEstimatedDistanceMeters(); got < 10416 || got > 10417 {
		t.Errorf("got estimated distance %v, want ~10417", got)
	}
	if cps[1].GetDistanceMeters() != 20000 {
		t.Errorf("got CP2 distance %v", cps[1].GetDistanceMeters())
	}
	// Finish: CP2 departure at 2:49 plus 1:12.
	if got := clock(status.GetEstimatedFinishTime()); got != 241*time.Minute {
		t.Errorf("got finish %v, want 4:01", got)
	}

	// At CP2 and still there long after the planned stop: leaving now.
	plan = testPlan(split(0, 72*time.Minute, 87*time.Minute), split(1, 150*time.Minute, 0))
	now := raceStart.Add(170 * time.Minute)
	status = EstimateRaceDay(plan, raceStart, nil, now)
	cp2 := status.GetCheckpoints()[1]
	if got := status.GetEstimatedDistanceMeters(); got != 20000 {
		t.Errorf("at CP2 the estimated distance should be CP2, got %v", got)
	}
	if cp2.GetStatus() != racesv1.CheckpointStatus_CHECKPOINT_STATUS_ARRIVED || !cp2.GetDepartureTime().AsTime().Equal(now) {
		t.Errorf("unexpected CP2 while at the checkpoint %v", cp2)
	}
	// 2:30 to CP2 with a 15 minute stop is 2:15 moving for 2:00 planned.
	if status.GetPaceFactor() != 1.125 {
		t.Errorf("got pace factor %v, want 1.125", status.GetPaceFactor())
	}
	if got := clock(status.GetEstimatedFinishTime()); got != 170*time.Minute+67*time.Minute+30*time.Second {
		t.Errorf("got finish %v", got)
	}
}

func TestEstimateRaceDayFinished(t *testing.T) {
	t.Parallel()
	// Finished after 3:30, checked well after.
	plan := testPlan(split(0, time.Hour, 70*time.Minute), split(1, 130*time.Minute, 140*time.Minute), split(2, 210*time.Minute, 0))
	status := EstimateRaceDay(plan, raceStart, nil, raceStart.Add(5*time.Hour))
	if !status.GetFinished() {
		t.Error("the runner hasn't finished")
	}
	finish := status.GetCheckpoints()[2]
	if finish.HasDepartureTime() || finish.GetDepartureRecorded() {
		t.Errorf("the finish has a departure: %v", finish)
	}
	if got := clock(status.GetEstimatedFinishTime()); got != 210*time.Minute {
		t.Errorf("got finish time %v, want 3h30m", got)
	}
	if got := status.GetEstimatedDistanceMeters(); got != 30000 {
		t.Errorf("got estimated distance %v, want the finish", got)
	}

	// Before the finish, it has no departure either.
	status = EstimateRaceDay(testPlan(), raceStart, nil, raceStart.Add(time.Hour))
	if status.GetFinished() || status.GetCheckpoints()[2].HasDepartureTime() {
		t.Errorf("unexpected finish before reaching it: %v", status)
	}
}

func TestEstimateRaceDayOverdue(t *testing.T) {
	t.Parallel()
	// Nothing recorded 1:30 into the race: CP1 is overdue.
	now := raceStart.Add(90 * time.Minute)
	status := EstimateRaceDay(testPlan(), raceStart, nil, now)
	cp1 := status.GetCheckpoints()[0]
	if !cp1.GetOverdue() || !cp1.GetArrivalTime().AsTime().Equal(now) {
		t.Errorf("unexpected CP1 %v", cp1)
	}
	// Later checkpoints are pushed back.
	if got := clock(status.GetCheckpoints()[1].GetArrivalTime()); got != 160*time.Minute {
		t.Errorf("got CP2 arrival %v, want 2:40", got)
	}
}

func TestEstimateRaceDayFromLiveTrack(t *testing.T) {
	t.Parallel()
	// 15km in after 1:45 including the 10 minute planned stop at CP1: 1:35
	// moving for 1:30 planned.
	live := &LiveFix{Time: raceStart.Add(105 * time.Minute), CourseDistanceMeters: 15000}
	status := EstimateRaceDay(testPlan(), raceStart, live, live.Time)
	if status.GetEstimateSource() != racesv1.EstimateSource_ESTIMATE_SOURCE_LIVETRACK || status.GetDistanceMeters() != 15000 {
		t.Fatalf("unexpected status %v", status)
	}
	if got := status.GetPaceFactor(); got != 1.056 {
		t.Errorf("got pace factor %v, want 1.056", got)
	}
	cps := status.GetCheckpoints()
	if cps[0].GetStatus() != racesv1.CheckpointStatus_CHECKPOINT_STATUS_DEPARTED || cps[0].GetArrivalRecorded() {
		t.Errorf("CP1 should be passed without a recorded time: %v", cps[0])
	}
	// CP2: 30 planned minutes left at the slower pace.
	if got := clock(cps[1].GetArrivalTime()).Round(time.Second); got != 105*time.Minute+31*time.Minute+41*time.Second {
		t.Errorf("got CP2 arrival %v", got)
	}

	// A LiveTrack position behind the last recorded checkpoint is ignored.
	plan := testPlan(split(1, 140*time.Minute, 0))
	status = EstimateRaceDay(plan, raceStart, live, raceStart.Add(141*time.Minute))
	if status.GetEstimateSource() != racesv1.EstimateSource_ESTIMATE_SOURCE_CHECKPOINT {
		t.Errorf("got source %v, want checkpoint", status.GetEstimateSource())
	}
}

func TestSnapToCourse(t *testing.T) {
	t.Parallel()
	// An out and back course: 0 to 0.1 degrees north and back.
	var course []geo.Point
	for i := 0; i <= 100; i++ {
		course = append(course, geo.Point{Lat: float64(i) * 0.001})
	}
	for i := 99; i >= 0; i-- {
		course = append(course, geo.Point{Lat: float64(i) * 0.001})
	}
	p := geo.Point{Lat: 0.02}
	// On the way out the watch reads ~2.2km, on the way back ~20km.
	if got := SnapToCourse(course, p, 2300); got < 2000 || got > 2400 {
		t.Errorf("way out: got %v", got)
	}
	if got := SnapToCourse(course, p, 19800); got < 20000 || got > 20100 {
		t.Errorf("way back: got %v", got)
	}
	if got := SnapToCourse(nil, p, 1234); got != 1234 {
		t.Errorf("no course: got %v", got)
	}
}

func TestPointAtDistance(t *testing.T) {
	t.Parallel()
	course := []geo.Point{{Lat: 0}, {Lat: 0.01}, {Lat: 0.02}}
	p, ok := PointAtDistance(course, 1667.9)
	if !ok || p.Lat < 0.01499 || p.Lat > 0.01501 {
		t.Errorf("got %v", p)
	}
	if p, _ := PointAtDistance(course, 1e9); p.Lat != 0.02 {
		t.Errorf("past the end: got %v", p)
	}
	if _, ok := PointAtDistance(nil, 1); ok {
		t.Error("expected no point without a course")
	}
}
