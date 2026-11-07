package plans

import (
	"testing"
	"time"
)

func TestRunFuelingAmounts(t *testing.T) {
	t.Parallel()
	f := &RunFueling{CarbsGramsPerHour: 60, FluidMLPerHour: 500, SodiumMGPerHour: 400}
	hot, mild := 25.0, 10.0
	for _, tc := range []struct {
		name     string
		workout  Workout
		apparent *float64
		want     FuelAmounts
		wantOK   bool
	}{
		{
			name:    "target duration",
			workout: Workout{TargetDurationSeconds: 5400, TargetDistanceMeters: 20000},
			want:    FuelAmounts{ExpectedDuration: 90 * time.Minute, CarbsGrams: 90, FluidML: 750, SodiumMG: 600},
			wantOK:  true,
		},
		{
			// 16 km at 8 km/h and 600 m at 600 m/h: three hours.
			name:    "distance and climb",
			workout: Workout{TargetDistanceMeters: 16000, TargetElevationGainMeters: 600},
			want:    FuelAmounts{ExpectedDuration: 3 * time.Hour, CarbsGrams: 180, FluidML: 1500, SodiumMG: 1200},
			wantOK:  true,
		},
		{
			// 10 degrees above 15 raises fluid and sodium by 50%.
			name:     "hot",
			workout:  Workout{TargetDurationSeconds: 3600},
			apparent: &hot,
			want:     FuelAmounts{ExpectedDuration: time.Hour, CarbsGrams: 60, FluidML: 750, SodiumMG: 600, ApparentTemperatureCelsius: &hot, HeatIncrease: 0.5},
			wantOK:   true,
		},
		{
			name:     "mild",
			workout:  Workout{TargetDurationSeconds: 3600},
			apparent: &mild,
			want:     FuelAmounts{ExpectedDuration: time.Hour, CarbsGrams: 60, FluidML: 500, SodiumMG: 400, ApparentTemperatureCelsius: &mild},
			wantOK:   true,
		},
		{name: "no time or distance", workout: Workout{TargetElevationGainMeters: 500}},
	} {
		got, ok := f.Amounts(tc.workout.ExpectedDuration(), tc.apparent)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("%s: got %+v, %t; want %+v, %t", tc.name, got, ok, tc.want, tc.wantOK)
		}
	}
	if got := HeatIncrease(40); got != maxHeatIncrease {
		t.Errorf("at 40 degrees got an increase of %g, want the most, %g", got, maxHeatIncrease)
	}
}

func TestRunFuelingValidate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fueling RunFueling
		wantErr bool
	}{
		{fueling: RunFueling{CarbsGramsPerHour: 60}},
		{fueling: RunFueling{}, wantErr: true},
		{fueling: RunFueling{FluidMLPerHour: 2000}, wantErr: true},
		{fueling: RunFueling{CarbsGramsPerHour: -1, FluidMLPerHour: 500}, wantErr: true},
	} {
		if err := tc.fueling.Validate(); (err != nil) != tc.wantErr {
			t.Errorf("%+v: got %v, want an error: %t", tc.fueling, err, tc.wantErr)
		}
	}
}

func TestFuelAmountsFromSweat(t *testing.T) {
	t.Parallel()
	hot, cold := 25.0, 5.0
	salty := &SweatProfile{SodiumMGPerLiter: 1000}
	for _, tc := range []struct {
		name     string
		workout  Workout
		apparent *float64
		want     FuelAmounts
	}{
		{
			// The default 800 ml an hour for two hours, with 1 g of sodium
			// a liter, replacing the run's missing sodium rate.
			name:    "mild",
			workout: Workout{Type: WorkoutTypeBase, TargetDurationSeconds: 7200, RunFueling: &RunFueling{CarbsGramsPerHour: 60}, Sweat: salty},
			want:    FuelAmounts{ExpectedDuration: 2 * time.Hour, CarbsGrams: 120, SweatML: 1600, SodiumLossMG: 1600, SodiumMG: 1600, SodiumFromSweat: true},
		},
		{
			// 10 degrees above mild is 50% more sweat, and hard running
			// 20% more on top: 1440 ml an hour.
			name:     "hot and hard",
			workout:  Workout{Type: WorkoutTypeIntensity, TargetDurationSeconds: 3600, RunFueling: &RunFueling{CarbsGramsPerHour: 60}, Sweat: salty},
			apparent: &hot,
			want: FuelAmounts{
				ExpectedDuration: time.Hour, CarbsGrams: 60, ApparentTemperatureCelsius: &hot, HeatIncrease: 0.5,
				SweatML: 1450, SodiumLossMG: 1450, SodiumMG: 1450, SodiumFromSweat: true,
			},
		},
		{
			// A measured rate, 20% less in the cold, and the run's own
			// sodium rate kept, with the loss for comparison.
			name: "own rate",
			workout: Workout{
				Type: WorkoutTypeBase, TargetDurationSeconds: 3600,
				RunFueling: &RunFueling{SodiumMGPerHour: 300}, Sweat: &SweatProfile{SodiumMGPerLiter: 500, RateMLPerHour: 1000},
			},
			apparent: &cold,
			want:     FuelAmounts{ExpectedDuration: time.Hour, SodiumMG: 300, ApparentTemperatureCelsius: &cold, SweatML: 800, SodiumLossMG: 400},
		},
		{
			// A sweat rate alone gives the sweat, but no sodium loss.
			name:    "rate only",
			workout: Workout{Type: WorkoutTypeBase, TargetDurationSeconds: 3600, RunFueling: &RunFueling{FluidMLPerHour: 500}, Sweat: &SweatProfile{RateMLPerHour: 1200}},
			want:    FuelAmounts{ExpectedDuration: time.Hour, FluidML: 500, SweatML: 1200},
		},
	} {
		got, ok := tc.workout.FuelAmounts(tc.apparent)
		if !ok || got != tc.want {
			t.Errorf("%s: got %+v, %t; want %+v", tc.name, got, ok, tc.want)
		}
	}
	if _, ok := (&Workout{TargetDurationSeconds: 3600, Sweat: salty}).FuelAmounts(nil); ok {
		t.Error("a run without fueling got amounts")
	}
}

func TestSweatProfileValidate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		profile SweatProfile
		wantErr bool
	}{
		{profile: SweatProfile{}},
		{profile: SweatProfile{SodiumMGPerLiter: 900, RateMLPerHour: 1100}},
		// In mmol/L rather than mg/L.
		{profile: SweatProfile{SodiumMGPerLiter: 40}, wantErr: true},
		// In liters rather than milliliters.
		{profile: SweatProfile{RateMLPerHour: 1.2}, wantErr: true},
	} {
		if err := tc.profile.Validate(); (err != nil) != tc.wantErr {
			t.Errorf("%+v: got %v, want an error: %t", tc.profile, err, tc.wantErr)
		}
	}
}
