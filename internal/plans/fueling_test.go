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
