package fit_test

import (
	"bytes"
	"math"
	"os"
	"testing"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/fit"
)

// The test files are written by Garmin's FIT SDK for JavaScript, as watches
// write them.

func TestDecodeActivityRun(t *testing.T) {
	t.Parallel()
	file, err := os.ReadFile("testdata/run.fit")
	if err != nil {
		t.Fatal(err)
	}
	a, err := fit.DecodeActivity(file)
	if err != nil {
		t.Fatalf("DecodeActivity: %v", err)
	}
	start := time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC)
	if a.Sport != 1 || !a.StartTime.Equal(start) || a.ElapsedTime != 10*time.Minute || a.DistanceMeters != 1500 ||
		a.ElevationGainMeters != 42 || a.AverageHeartRateBPM != 140 || a.AverageSpeedMPS != 2.5 {
		t.Errorf("unexpected summary %+v", a)
	}
	if len(a.Records) != 11 {
		t.Fatalf("got %d records, want 11", len(a.Records))
	}
	last := a.Records[10]
	if !last.Time.Equal(start.Add(10*time.Minute)) || !last.HasPosition || math.Abs(last.Lat-49.706) > 1e-6 || math.Abs(last.Lng+123.15) > 1e-6 ||
		math.Abs(last.AltitudeMeters-160) > 0.2 || last.HeartRateBPM != 160 || last.DistanceMeters != 1500 || last.SpeedMPS != 2.5 || last.Cadence != 85 {
		t.Errorf("unexpected last record %+v", last)
	}
}

func TestDecodeActivityStrength(t *testing.T) {
	t.Parallel()
	file, err := os.ReadFile("testdata/strength.fit")
	if err != nil {
		t.Fatal(err)
	}
	a, err := fit.DecodeActivity(file)
	if err != nil {
		t.Fatalf("DecodeActivity: %v", err)
	}
	if a.Sport != fit.SportTraining || a.SubSport != fit.SubSportStrengthTraining || len(a.Sets) != 4 {
		t.Fatalf("got sport %d/%d and %d sets, want strength training with 4", a.Sport, a.SubSport, len(a.Sets))
	}
	first, rest, catalog := a.Sets[0], a.Sets[1], a.Sets[3]
	// Set weights are stored in sixteenths of a kilogram.
	if !first.Active || first.Reps != 8 || math.Abs(first.WeightKilograms-11.34) > 1.0/16 || !first.Pounds ||
		!first.HasExercise || first.Category != 65534 || first.Name != 0 || !first.HasWorkoutStep || first.WorkoutStep != 0 || first.Duration != 40*time.Second {
		t.Errorf("unexpected first set %+v", first)
	}
	if rest.Active || rest.Duration != 90*time.Second {
		t.Errorf("unexpected rest %+v", rest)
	}
	if catalog.HasWorkoutStep || catalog.Category != 6 || catalog.Name != 1 || catalog.WeightKilograms != 10 || catalog.Pounds {
		t.Errorf("unexpected catalog set %+v", catalog)
	}
	if got := a.ExerciseTitles[[2]uint16{65534, 0}]; got != "Bulgarian split squat" {
		t.Errorf("got title %q", got)
	}
}

func TestDecodeRejects(t *testing.T) {
	t.Parallel()
	file, err := os.ReadFile("testdata/run.fit")
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), file...)
	corrupt[40] ^= 0xFF
	for name, data := range map[string][]byte{
		"not FIT":   []byte("hello, this is not a FIT file"),
		"cut short": file[:len(file)/2],
		"corrupt":   corrupt,
	} {
		if _, err := fit.DecodeActivity(data); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}

func TestDecodeActivityUTCOffset(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 11, 1, 42, 0, 0, time.UTC)
	write := func(offset time.Duration) []byte {
		t.Helper()
		var f fit.Writer
		f.Define(0, fit.FileIDMessage, fit.EnumField(0))
		f.Data(0, uint8(4)) // An activity file.
		// The activity message, with the device's local time.
		f.Define(1, 34, fit.Uint32Field(253), fit.Uint32Field(5))
		f.Data(1, fit.Time(start), fit.Time(start.Add(offset)))
		f.Define(2, 18, fit.Uint32Field(2))
		f.Data(2, fit.Time(start))
		var buf bytes.Buffer
		if err := f.WriteFile(&buf); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	// 8 hours behind UTC, as in Vancouver in November.
	a, err := fit.DecodeActivity(write(-8 * time.Hour))
	if err != nil {
		t.Fatalf("DecodeActivity: %v", err)
	}
	if !a.HasUTCOffset || a.UTCOffset != -8*time.Hour {
		t.Errorf("got offset %v (%t), want -8h", a.UTCOffset, a.HasUTCOffset)
	}
	// A clock that wasn't set isn't a time zone.
	a, err = fit.DecodeActivity(write(-40 * time.Hour))
	if err != nil {
		t.Fatalf("DecodeActivity: %v", err)
	}
	if a.HasUTCOffset {
		t.Errorf("got offset %v from an unset clock, want none", a.UTCOffset)
	}
}
