package plans

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// RunFueling is what to eat and drink during a run, as rates per hour. It's
// stored as JSON with the workout, so its field names mustn't change.
type RunFueling struct {
	CarbsGramsPerHour float64 `json:"carbs_grams_per_hour,omitempty"`
	FluidMLPerHour    float64 `json:"fluid_ml_per_hour,omitempty"`
	SodiumMGPerHour   float64 `json:"sodium_mg_per_hour,omitempty"`
	Notes             string  `json:"notes,omitempty"`
}

// The pace runs without a target duration are expected to take, like the
// courses of routes: a speed on the flat, plus a climbing rate.
const (
	expectedSpeedKMH           = 8
	expectedClimbMetersPerHour = 600
)

// ExpectedDuration returns how long a run is expected to take: its target
// duration, or else its target distance at expectedSpeedKMH plus its
// elevation gain at expectedClimbMetersPerHour. It returns 0 if it has
// neither.
func (w *Workout) ExpectedDuration() time.Duration {
	if w.TargetDurationSeconds > 0 {
		return time.Duration(w.TargetDurationSeconds) * time.Second
	}
	if w.TargetDistanceMeters <= 0 {
		return 0
	}
	hours := w.TargetDistanceMeters/1000/expectedSpeedKMH + w.TargetElevationGainMeters/expectedClimbMetersPerHour
	// Rounded to the minute, as an estimate is no more precise.
	return time.Duration(hours * float64(time.Hour)).Round(time.Minute)
}

// Heat raises fluid and sodium by heatIncreasePerDegree for each degree
// Celsius the apparent temperature is above heatThresholdCelsius, up to
// maxHeatIncrease: sweat rates rise with the heat, and so does the sodium
// lost in sweat.
const (
	heatThresholdCelsius  = 15
	heatIncreasePerDegree = 0.05
	maxHeatIncrease       = 0.5
)

// FuelAmounts are the amounts to eat and drink during a run.
type FuelAmounts struct {
	ExpectedDuration time.Duration
	CarbsGrams       float64
	FluidML          float64
	SodiumMG         float64
	// The apparent temperature the amounts are adjusted for, nil without a
	// forecast.
	ApparentTemperatureCelsius *float64
	// How much fluid and sodium are raised for the heat, from 0 to
	// maxHeatIncrease.
	HeatIncrease float64
	// From the user's sweat profile, if they have one: how much they're
	// expected to sweat during the run, and the sodium they lose in it,
	// 0 if their sweat's sodium is unknown.
	SweatML      float64
	SodiumLossMG float64
	// Whether SodiumMG replaces the sodium lost in sweat, as the run's
	// fueling has no sodium rate of its own.
	SodiumFromSweat bool
}

// FuelAmounts returns the amounts to eat and drink during a run, adjusted
// for the apparent temperature in degrees Celsius if it's not nil, with the
// user's sweat losses if they have a sweat profile. Without a sodium rate of
// the run's own, its sodium replaces what the user loses in sweat. It
// reports false if the run has no fueling plan, or its time can't be
// estimated.
func (w *Workout) FuelAmounts(apparentCelsius *float64) (FuelAmounts, bool) {
	if w.RunFueling == nil {
		return FuelAmounts{}, false
	}
	a, ok := w.RunFueling.Amounts(w.ExpectedDuration(), apparentCelsius)
	if !ok || w.Sweat == nil {
		return a, ok
	}
	hours := a.ExpectedDuration.Hours()
	a.SweatML = math.Round(w.Sweat.SweatRate(apparentCelsius, w.Type == WorkoutTypeIntensity)*hours/50) * 50
	if w.Sweat.SodiumMGPerLiter > 0 {
		a.SodiumLossMG = math.Round(a.SweatML/1000*w.Sweat.SodiumMGPerLiter/50) * 50
		if w.RunFueling.SodiumMGPerHour == 0 {
			a.SodiumMG, a.SodiumFromSweat = a.SodiumLossMG, true
		}
	}
	return a, true
}

// HeatIncrease returns how much to raise fluid and sodium for an apparent
// temperature in degrees Celsius.
func HeatIncrease(apparentCelsius float64) float64 {
	return math.Min(maxHeatIncrease, math.Max(0, apparentCelsius-heatThresholdCelsius)*heatIncreasePerDegree)
}

// Amounts returns the amounts to eat and drink during a run of the expected
// duration, adjusted for the apparent temperature in degrees Celsius if it's
// not nil. It reports false if the duration is 0.
func (f *RunFueling) Amounts(expected time.Duration, apparentCelsius *float64) (FuelAmounts, bool) {
	if expected <= 0 {
		return FuelAmounts{}, false
	}
	a := FuelAmounts{ExpectedDuration: expected, ApparentTemperatureCelsius: apparentCelsius}
	if apparentCelsius != nil {
		a.HeatIncrease = HeatIncrease(*apparentCelsius)
	}
	hours := expected.Hours()
	// Carbs to the gram, and fluid and sodium to 50 ml and 50 mg, as
	// they're measured in bottles and tablets rather than precisely.
	a.CarbsGrams = math.Round(f.CarbsGramsPerHour * hours)
	a.FluidML = math.Round(f.FluidMLPerHour*hours*(1+a.HeatIncrease)/50) * 50
	a.SodiumMG = math.Round(f.SodiumMGPerHour*hours*(1+a.HeatIncrease)/50) * 50
	return a, true
}

// The most a fueling plan can take in per hour: well above what's
// recommended, to catch mistaken units like liters for milliliters.
const (
	MaxCarbsGramsPerHour = 150
	MaxFluidMLPerHour    = 1500
	MaxSodiumMGPerHour   = 2000
)

// Validate reports an error if the rates are out of range, or all 0.
func (f *RunFueling) Validate() error {
	for _, r := range []struct {
		name  string
		value float64
		max   float64
	}{
		{"carbs_grams_per_hour", f.CarbsGramsPerHour, MaxCarbsGramsPerHour},
		{"fluid_ml_per_hour", f.FluidMLPerHour, MaxFluidMLPerHour},
		{"sodium_mg_per_hour", f.SodiumMGPerHour, MaxSodiumMGPerHour},
	} {
		if r.value < 0 || r.value > r.max || math.IsNaN(r.value) {
			return fmt.Errorf("%s must be from 0 to %g", r.name, r.max)
		}
	}
	if f.CarbsGramsPerHour == 0 && f.FluidMLPerHour == 0 && f.SodiumMGPerHour == 0 {
		return fmt.Errorf("set at least one of carbs_grams_per_hour, fluid_ml_per_hour and sodium_mg_per_hour, or leave fueling unset")
	}
	return nil
}

// runFuelingToDB returns a run's fueling for the database, empty if it has
// none.
func runFuelingToDB(f *RunFueling) string {
	if f == nil {
		return ""
	}
	data, err := json.Marshal(f)
	if err != nil {
		// The fueling is plain values, which always marshal.
		panic(fmt.Sprintf("marshaling run fueling: %v", err))
	}
	return string(data)
}

// runFuelingFromDB returns a run's fueling from the database.
func runFuelingFromDB(b []byte) *RunFueling {
	if len(b) == 0 {
		return nil
	}
	var f RunFueling
	if err := json.Unmarshal(b, &f); err != nil {
		return nil
	}
	return &f
}
