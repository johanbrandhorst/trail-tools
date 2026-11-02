package lactate

import (
	"math"
	"testing"
)

func TestDmax(t *testing.T) {
	t.Parallel()
	stages := func(heartRates []int32, lactates []float64) []Stage {
		var s []Stage
		for i := range heartRates {
			s = append(s, Stage{HeartRateBPM: heartRates[i], LactateMmolPerLiter: lactates[i]})
		}
		return s
	}
	// A typical curve: flat, then bending up after about 150 bpm.
	test := &Test{Stages: stages([]int32{120, 130, 140, 150, 160, 170}, []float64{1.0, 1.1, 1.3, 1.8, 2.8, 4.5})}
	heartRate, mmol, ok := test.Dmax()
	if !ok || heartRate < 145 || heartRate > 158 {
		t.Errorf("got Dmax %d bpm (%v), want between 145 and 158", heartRate, ok)
	}
	if mmol < 1.3 || mmol > 2.8 {
		t.Errorf("got Dmax lactate %.2f mmol/L, want on the curve between the stages around it", mmol)
	}
	// The 4 mmol/L threshold is interpolated between 160 and 170 bpm.
	if got := test.HeartRateAt(AnaerobicThresholdMmolPerLiter); got != 167 {
		t.Errorf("got 4 mmol/L at %d bpm, want 167", got)
	}

	for name, test := range map[string]*Test{
		"too few stages": {Stages: stages([]int32{120, 140, 160}, []float64{1, 2, 4})},
		// Lactate that falls, or rises in a straight line, doesn't bend.
		"falling":  {Stages: stages([]int32{120, 130, 140, 150}, []float64{4, 3, 2, 1})},
		"straight": {Stages: stages([]int32{120, 130, 140, 150}, []float64{1, 2, 3, 4})},
	} {
		if heartRate, _, ok := test.Dmax(); ok {
			t.Errorf("%s: got Dmax %d bpm, want none", name, heartRate)
		}
	}
}

func TestFitPolynomial(t *testing.T) {
	t.Parallel()
	// Points on 1 + 2x - x^2 + 0.5x^3 are fitted exactly.
	var xs, ys []float64
	for i := range 6 {
		x := float64(i) / 5
		xs, ys = append(xs, x), append(ys, 1+2*x-x*x+0.5*x*x*x)
	}
	got, ok := fitPolynomial(xs, ys, 3)
	if !ok {
		t.Fatal("no fit")
	}
	for i, want := range []float64{1, 2, -1, 0.5} {
		if math.Abs(got[i]-want) > 1e-9 {
			t.Errorf("coefficient %d = %v, want %v", i, got[i], want)
		}
	}
}
