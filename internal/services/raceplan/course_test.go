package raceplan

import (
	"math"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/races"
)

func TestPlannedSeconds(t *testing.T) {
	t.Parallel()
	// A flat 3 km course north from the equator, a point every 500 m. The
	// pace model runs it at 8 km/h: 450 seconds a kilometer.
	var points []geo.Point
	for i := range 7 {
		points = append(points, geo.Point{Lat: float64(i) * 500 / 111195})
	}
	checkpoints := []*races.Checkpoint{
		{Name: "CP1", DistanceMeters: 1000, PlannedSegmentSeconds: 600, PlannedStopSeconds: 60},
		// Without a planned time, the segment follows the pace model.
		{Name: "CP2", DistanceMeters: 2000},
	}
	got, arrivals := plannedSeconds(points, checkpoints)
	// The first segment takes the planned 600 seconds, then 60 seconds at
	// the checkpoint, 450 seconds to CP2, and 450 to the finish.
	want := []float64{0, 300, 600, 885, 1110, 1335, 1560}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1 {
			t.Errorf("planned seconds %v, want %v", got, want)
			break
		}
	}
	// The checkpoints are reached at the end of their segments, before the
	// stops.
	if len(arrivals) != 2 || math.Abs(arrivals[0]-600) > 1 || math.Abs(arrivals[1]-1110) > 1 {
		t.Errorf("arrivals %v, want [600 1110]", arrivals)
	}
	// Checkpoints past the finish are reached too.
	_, arrivals = plannedSeconds(points, append(checkpoints, &races.Checkpoint{Name: "Far", DistanceMeters: 9000}))
	if len(arrivals) != 3 || math.Abs(arrivals[2]-1560) > 1 {
		t.Errorf("arrivals %v, want the last at the finish", arrivals)
	}
}
