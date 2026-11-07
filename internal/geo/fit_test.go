package geo

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/fit"
	"github.com/johanbrandhorst/trail-tools/internal/fit/fittest"
)

func TestWriteFITCourse(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 3, 7, 0, 0, 0, time.UTC)
	points := []Point{
		{Lat: 49.7, Lng: -123.15, Ele: 10},
		{Lat: 49.71, Lng: -123.15, Ele: 510},
		{Lat: 49.72, Lng: -123.15, Ele: math.NaN()},
	}
	var buf bytes.Buffer
	err := WriteFITCourse(&buf, &FITCourse{
		Name:           "Squamish 50K",
		Points:         points,
		ElapsedSeconds: []float64{0, 1800, 3000},
		CoursePoints: []CoursePoint{
			{Name: "Alice Lake aid station", DistanceMeters: 1000, ElapsedSeconds: 1500, Type: CoursePointFood},
			{Name: "Finish", DistanceMeters: 99999, ElapsedSeconds: 3000},
		},
		Start: start,
	})
	if err != nil {
		t.Fatalf("WriteFITCourse: %v", err)
	}
	byGlobal := map[uint16][]fittest.Message{}
	for _, m := range fittest.Decode(t, buf.Bytes()) {
		byGlobal[m.Global] = append(byGlobal[m.Global], m)
	}
	fitStart := uint64(fit.Time(start))

	if fileID := byGlobal[fit.FileIDMessage]; len(fileID) != 1 || fileID[0].Uint(t, 0) != 6 {
		t.Errorf("file ID %v, want a course file", fileID)
	}
	if course := byGlobal[fitCourse]; len(course) != 1 || course[0].String(5) != "Squamish 50K" || course[0].Uint(t, 4) != 1 {
		t.Errorf("course %v, want a running course named Squamish 50K", course)
	}
	records := byGlobal[fitRecord]
	if len(records) != 3 {
		t.Fatalf("got %d records, want 3", len(records))
	}
	dists := CumulativeDistances(points)
	for i, r := range records {
		if got := r.Uint(t, 253); got != fitStart+[]uint64{0, 1800, 3000}[i] {
			t.Errorf("record %d at %d, want %d", i, got, fitStart)
		}
		if got := float64(r.Sint32(t, 0)) * 180 / (1 << 31); math.Abs(got-points[i].Lat) > 1e-6 {
			t.Errorf("record %d latitude %v, want %v", i, got, points[i].Lat)
		}
		if got := float64(r.Uint(t, 5)) / 100; math.Abs(got-dists[i]) > 0.01 {
			t.Errorf("record %d distance %v, want %v", i, got, dists[i])
		}
	}
	if alt := records[1].Uint(t, 2); alt != (510+500)*5 {
		t.Errorf("altitude %d, want %d", alt, (510+500)*5)
	}
	if alt := records[2].Uint(t, 2); alt != fitInvalidUint16 {
		t.Errorf("unknown altitude %d, want invalid", alt)
	}
	laps := byGlobal[fitLap]
	if len(laps) != 1 || laps[0].Uint(t, 7) != 3000*1000 || laps[0].Uint(t, 21) != 500 {
		t.Errorf("lap %v, want 3000s and 500m up", laps)
	}
	events := byGlobal[fitEvent]
	if len(events) != 2 || events[0].Uint(t, 1) != 0 || events[1].Uint(t, 1) != 9 || events[1].Uint(t, 253) != fitStart+3000 {
		t.Errorf("events %v, want the timer to start and stop", events)
	}

	// Course points are placed along the course, with names cut to fit.
	cps := byGlobal[fitCoursePoint]
	if len(cps) != 2 {
		t.Fatalf("got %d course points, want 2", len(cps))
	}
	if name := cps[0].String(6); name != "Alice Lake aid " || cps[0].Uint(t, 5) != uint64(CoursePointFood) {
		t.Errorf("course point %q type %d", name, cps[0].Uint(t, 5))
	}
	if d := cps[0].Uint(t, 4); d != 100000 {
		t.Errorf("course point at %d cm, want 100000", d)
	}
	if at := cps[0].Uint(t, 1) - fitStart; at != 1500 {
		t.Errorf("course point at %d s, want 1500", at)
	}
	if lat := float64(cps[0].Sint32(t, 2)) * 180 / (1 << 31); math.Abs(lat-(49.7+0.01*1000/dists[1])) > 1e-6 {
		t.Errorf("course point latitude %v", lat)
	}
	// Course points past the end are at the finish.
	if d, name := float64(cps[1].Uint(t, 4))/100, cps[1].String(6); math.Abs(d-dists[2]) > 0.01 || name != "Finish" {
		t.Errorf("finish course point %q at %v, want %v", name, d, dists[2])
	}

	for name, c := range map[string]*FITCourse{
		"one point":     {Points: points[:1], ElapsedSeconds: []float64{0}},
		"missing times": {Points: points, ElapsedSeconds: []float64{0}},
	} {
		if err := WriteFITCourse(&buf, c); err == nil {
			t.Errorf("WriteFITCourse with %s succeeded", name)
		}
	}
}

func TestElapsedSeconds(t *testing.T) {
	t.Parallel()
	// 1 km north climbing 100 m at 10 km/h and 600 m/h takes 6 + 10
	// minutes; unknown elevations add no climbing.
	points := []Point{{Lat: 0, Ele: 0}, {Lat: 0.008993, Ele: 100}}
	elapsed := ElapsedSeconds(points, 10, 600)
	if math.Abs(elapsed[1]-960) > 1 {
		t.Errorf("got %v seconds, want 960", elapsed[1])
	}
	unknown := []Point{{Lat: 0, Ele: math.NaN()}, {Lat: 0.008993, Ele: 100}}
	if elapsed := ElapsedSeconds(unknown, 10, 600); math.Abs(elapsed[1]-360) > 1 {
		t.Errorf("got %v seconds with an unknown elevation, want 360", elapsed[1])
	}
}
