package racereport

import (
	"maps"
	"math"
	"testing"
	"time"

	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/races"
	"github.com/johanbrandhorst/trail-tools/internal/services/raceplan"
)

// testRecording is a recording 1 km longer than a 20 km race, as GPS
// distances often are, at 6 minutes per kilometer.
func testRecording() *recording {
	streams := &activitiesv1.ActivityStreams{}
	for km := 0; km <= 21; km++ {
		streams.SetTimeSeconds(append(streams.GetTimeSeconds(), int32(km*360)))
		streams.SetDistanceMeters(append(streams.GetDistanceMeters(), float64(km*1000)))
	}
	return newRecording(streams)
}

func TestElapsedAt(t *testing.T) {
	t.Parallel()
	rec := testRecording()
	for _, tt := range []struct {
		meters float64
		want   int32
		ok     bool
	}{
		{0, 0, true},
		{500, 180, true},
		{10_000, 3600, true},
		{21_000, 7560, true},
		{21_000.001, 7560, true},
		{21_001, 0, false},
	} {
		got, ok := rec.elapsedAt(tt.meters)
		if got != tt.want || ok != tt.ok {
			t.Errorf("elapsedAt(%v) = %v, %v, want %v, %v", tt.meters, got, ok, tt.want, tt.ok)
		}
	}
	if newRecording(&activitiesv1.ActivityStreams{}) != nil {
		t.Error("a recording without times was created")
	}
}

func TestCompare(t *testing.T) {
	t.Parallel()
	start := time.Date(2027, 6, 26, 5, 0, 0, 0, time.UTC)
	outcome := &raceplan.PlanOutcome{
		Goals:                races.RaceGoals{AFinishSeconds: 7000, BFinishSeconds: 7500, CFinishSeconds: 8000},
		PlannedFinishSeconds: 7200,
		Checkpoints: []raceplan.PlannedCheckpoint{
			{Name: "Halfway", DistanceMeters: 10_000, PlannedArrivalSeconds: 3500, RecordedArrival: start.Add(3700 * time.Second)},
			{Name: "Finish", DistanceMeters: 20_000, PlannedArrivalSeconds: 7200, CutoffSeconds: 9000},
		},
		RaceDayStart: start,
	}

	// The finish time is compared with the goals, and the checkpoints with
	// the recording, scaled to the race distance: halfway is at 10.5 km of
	// the recording.
	got := compare(outcome, &races.RaceReport{FinishSeconds: 7560, DistanceMeters: 20_000}, testRecording())
	goals := got.GetGoals()
	if !got.GetHasPlan() || len(goals) != 3 || goals[0].GetMet() || goals[0].GetDifferenceSeconds() != 560 || goals[1].GetMet() || !goals[2].GetMet() {
		t.Errorf("unexpected goals %v", goals)
	}
	checkpoints := got.GetCheckpoints()
	if got.GetActualTimesSource() != racesv1.ActualTimesSource_ACTUAL_TIMES_SOURCE_ACTIVITY || !got.GetActualTimesScaled() || len(checkpoints) != 2 {
		t.Fatalf("unexpected comparison %v", got)
	}
	if cp := checkpoints[0]; cp.GetActualArrivalSeconds() != 3780 || cp.GetDifferenceSeconds() != 280 {
		t.Errorf("unexpected halfway %v", cp)
	}
	if cp := checkpoints[1]; cp.GetActualArrivalSeconds() != 7560 || cp.GetDifferenceSeconds() != 360 || cp.GetCutoffSeconds() != 9000 {
		t.Errorf("unexpected finish %v", cp)
	}

	// Without a recording, the race day splits are used, where recorded.
	got = compare(outcome, &races.RaceReport{DistanceMeters: 20_000}, nil)
	checkpoints = got.GetCheckpoints()
	if got.GetActualTimesSource() != racesv1.ActualTimesSource_ACTUAL_TIMES_SOURCE_RACE_DAY ||
		checkpoints[0].GetActualArrivalSeconds() != 3700 || checkpoints[1].HasActualArrivalSeconds() {
		t.Errorf("unexpected comparison from race day %v", got)
	}
	// Without a finish time, no goal is met.
	for _, goal := range got.GetGoals() {
		if goal.GetMet() || goal.GetDifferenceSeconds() != 0 {
			t.Errorf("unexpected goal without a finish time %v", goal)
		}
	}

	// Without a plan, there is nothing to compare.
	if got := compare(nil, &races.RaceReport{FinishSeconds: 7560, DistanceMeters: 20_000}, testRecording()); got.GetHasPlan() || len(got.GetCheckpoints()) != 0 {
		t.Errorf("unexpected comparison without a plan %v", got)
	}
}

// recordingOf returns a recording of a steady run of the distance, at 6
// minutes per kilometer, sampled every 100 meters.
func recordingOf(meters float64) *recording {
	streams := &activitiesv1.ActivityStreams{}
	for d := 0.0; ; d += 100 {
		d = min(d, meters)
		streams.SetTimeSeconds(append(streams.GetTimeSeconds(), int32(math.Round(d*0.36))))
		streams.SetDistanceMeters(append(streams.GetDistanceMeters(), d))
		if d == meters {
			return newRecording(streams)
		}
	}
}

// ultraOutcome is the plan of a 100 km race with checkpoints beyond 72 km,
// at 8 minutes per kilometer.
func ultraOutcome() *raceplan.PlanOutcome {
	outcome := &raceplan.PlanOutcome{
		Goals:                races.RaceGoals{AFinishSeconds: 13 * 3600, BFinishSeconds: 14 * 3600, CFinishSeconds: 16 * 3600},
		PlannedFinishSeconds: 48_000,
	}
	for _, cp := range []struct {
		name   string
		meters float64
	}{
		{"AS3", 30_000},
		{"Function Junction", 72_000},
		{"AS8", 79_600},
		{"HS5", 98_100},
		{"Finish", 100_000},
	} {
		outcome.Checkpoints = append(outcome.Checkpoints, raceplan.PlannedCheckpoint{
			Name:                  cp.name,
			DistanceMeters:        cp.meters,
			PlannedArrivalSeconds: int32(cp.meters * 0.48),
		})
	}
	return outcome
}

// arrivals returns the actual arrival times at the checkpoints by name,
// without those with none.
func arrivals(comparison *racesv1.RaceComparison) map[string]int32 {
	got := map[string]int32{}
	for _, cp := range comparison.GetCheckpoints() {
		if cp.HasActualArrivalSeconds() {
			got[cp.GetName()] = cp.GetActualArrivalSeconds()
		}
	}
	return got
}

func TestCompareNeverExtrapolates(t *testing.T) {
	t.Parallel()
	// A recording of 71.7 km of a 100 km race isn't scaled to the race
	// distance, which would put 98.1 km of the course at 70.3 km of the
	// recording: there are no arrival times beyond the end of the recording.
	report := &races.RaceReport{FinishSeconds: 25_812, DistanceMeters: 100_000}
	got := compare(ultraOutcome(), report, recordingOf(71_700))
	if got.GetActualTimesScaled() {
		t.Error("a partial recording was scaled to the race distance")
	}
	want := map[string]int32{"AS3": 10_800}
	if got := arrivals(got); !maps.Equal(got, want) {
		t.Errorf("got arrivals %v, want %v", got, want)
	}
	for _, cp := range got.GetCheckpoints() {
		if cp.GetNotReached() {
			t.Errorf("checkpoint %v is not reached without a DNF", cp)
		}
	}

	// Nor is one much longer than the race, e.g. with the way back.
	got = compare(ultraOutcome(), report, recordingOf(130_000))
	if got.GetActualTimesScaled() || arrivals(got)["Finish"] != 36_000 {
		t.Errorf("unexpected comparison with a long recording %v", got)
	}

	// A recording within GPS error of the race distance is scaled, and its
	// end is the finish.
	got = compare(ultraOutcome(), report, recordingOf(96_000))
	if !got.GetActualTimesScaled() || arrivals(got)["Finish"] != 34_560 {
		t.Errorf("unexpected comparison with a full recording %v", got)
	}
}

func TestCompareDidNotFinish(t *testing.T) {
	t.Parallel()
	// The runner stopped at Function Junction, at 72 km of the course and
	// 71.7 km of the recording.
	report := &races.RaceReport{
		DistanceMeters: 100_000,
		// A finish time left over from before the report was a DNF.
		FinishSeconds: 25_812,
		DidNotFinish:  true,
		Stop:          races.RaceStop{Checkpoint: " function junction", Seconds: 25_812},
	}
	got := compare(ultraOutcome(), report, recordingOf(71_700))
	goals := got.GetGoals()
	if len(goals) != 3 {
		t.Fatalf("unexpected goals %v", goals)
	}
	for _, goal := range goals {
		if goal.GetMet() || goal.GetDifferenceSeconds() != 0 {
			t.Errorf("goal %v is met by a DNF", goal)
		}
	}
	// The recording is scaled to the course up to the stop.
	if !got.GetActualTimesScaled() {
		t.Error("the recording up to the stop wasn't scaled")
	}
	want := map[string]int32{"AS3": 10_755, "Function Junction": 25_812}
	if got := arrivals(got); !maps.Equal(got, want) {
		t.Errorf("got arrivals %v, want %v", got, want)
	}
	for _, cp := range got.GetCheckpoints() {
		if want := cp.GetDistanceMeters() > 72_000; cp.GetNotReached() != want {
			t.Errorf("checkpoint %v not reached = %v, want %v", cp.GetName(), cp.GetNotReached(), want)
		}
	}

	// Checkpoints beyond the stop distance aren't reached, even if the
	// recording goes on, e.g. on the shuttle back.
	report.Stop = races.RaceStop{DistanceMeters: 75_000}
	got = compare(ultraOutcome(), report, recordingOf(100_000))
	want = map[string]int32{"AS3": 10_800, "Function Junction": 25_920}
	if got.GetActualTimesScaled() || !maps.Equal(arrivals(got), want) {
		t.Errorf("got arrivals %v, want %v, scaled %v", arrivals(got), want, got.GetActualTimesScaled())
	}

	// Without a known stop, the finish still isn't reached, and neither
	// race day splits nor a recording are used for it.
	report.Stop = races.RaceStop{}
	got = compare(ultraOutcome(), report, recordingOf(100_000))
	if cp := got.GetCheckpoints()[4]; !cp.GetNotReached() || cp.HasActualArrivalSeconds() {
		t.Errorf("unexpected finish of a DNF %v", cp)
	}
	if arrivals(got)["HS5"] != 35_316 {
		t.Errorf("unexpected arrivals %v", arrivals(got))
	}
	start := time.Date(2026, 10, 3, 5, 0, 0, 0, time.UTC)
	outcome := ultraOutcome()
	outcome.RaceDayStart = start
	for i := range outcome.Checkpoints {
		outcome.Checkpoints[i].RecordedArrival = start.Add(time.Duration(outcome.Checkpoints[i].PlannedArrivalSeconds) * time.Second)
	}
	report.Stop = races.RaceStop{DistanceMeters: 72_000}
	got = compare(outcome, report, nil)
	want = map[string]int32{"AS3": 14_400, "Function Junction": 34_560}
	if got.GetActualTimesSource() != racesv1.ActualTimesSource_ACTUAL_TIMES_SOURCE_RACE_DAY || !maps.Equal(arrivals(got), want) {
		t.Errorf("unexpected comparison from race day %v", got)
	}
}
