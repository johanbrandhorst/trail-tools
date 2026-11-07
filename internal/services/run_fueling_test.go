package services_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
)

func TestRunFueling(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	tomorrow := today.AddDate(0, 0, 1).Format(time.DateOnly)

	plan, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{Name: "Build", StartDate: today.Format(time.DateOnly), EndDate: today.AddDate(0, 0, 30).Format(time.DateOnly)}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	fueling := func() *plansv1.RunFueling {
		return plansv1.RunFueling_builder{CarbsGramsPerHour: 60, FluidMlPerHour: 500, SodiumMgPerHour: 400, Notes: " Two gels "}.Build()
	}
	run := func(fueling *plansv1.RunFueling) *plansv1.RunWorkout {
		// 16 km and 600 m of climbing: three hours at 8 km/h and 600 m/h.
		return plansv1.RunWorkout_builder{
			Intensity: plansv1.RunIntensity_RUN_INTENSITY_BASE, TargetDistanceMeters: 16000, TargetElevationGainMeters: 600, Fueling: fueling,
		}.Build()
	}
	created, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
		PlanId: plan.GetPlan().GetId(),
		Workouts: []*plansv1.Workout{
			// From 12:00 to 15:00 UTC, when the fake forecast feels like 17
			// to 20 degrees.
			plansv1.Workout_builder{
				Date: tomorrow, StartTime: "12:00", StartWindowMinutes: 15, Title: "Long run", Run: run(fueling()),
				Location: geov1.LatLng_builder{Lat: 0.3, Lng: 0.01}.Build(),
			}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	w := created.GetWorkouts()[0]
	got := w.GetRun().GetFueling()
	amounts := got.GetAmounts()
	if got.GetCarbsGramsPerHour() != 60 || got.GetNotes() != "Two gels" ||
		amounts.GetExpectedDurationSeconds() != 3*3600 || amounts.GetCarbsGrams() != 180 || amounts.GetFluidMl() != 1500 || amounts.GetSodiumMg() != 1200 ||
		amounts.HasApparentTemperatureCelsius() {
		t.Errorf("got fueling %v, want 180 g carbs, 1500 ml fluid and 1200 mg sodium for three hours", got)
	}

	// The forecast raises fluid and sodium for the heat: the hottest hour
	// of the run, 15:00, feels like 20 degrees, 5 above 15, so 25% more.
	weather, err := e.plans.GetWorkoutWeather(ctx, plansv1.GetWorkoutWeatherRequest_builder{WorkoutId: w.GetId()}.Build())
	if err != nil {
		t.Fatalf("GetWorkoutWeather: %v", err)
	}
	hot := weather.GetWeather().GetFueling()
	if hot.GetApparentTemperatureCelsius() != 20 || hot.GetHeatIncreasePercent() != 25 ||
		hot.GetCarbsGrams() != 180 || hot.GetFluidMl() != 1900 || hot.GetSodiumMg() != 1500 {
		t.Errorf("got fueling in the heat %v, want 25%% more fluid and sodium at 20 degrees", hot)
	}
	// The forecast covers the run's expected three hours, not one.
	if end := weather.GetWeather().GetWorkoutEndTime().AsTime(); end.Hour() != 15 || end.Minute() != 15 {
		t.Errorf("the forecast ends at %v, want 15:15, three hours after the start window", end)
	}

	// Rates that can't be right are refused, saying what to do.
	for name, tc := range map[string]struct {
		fueling *plansv1.RunFueling
		want    string
	}{
		"liters":  {plansv1.RunFueling_builder{FluidMlPerHour: 5000}.Build(), "fluid_ml_per_hour must be from 0 to 1500"},
		"nothing": {plansv1.RunFueling_builder{}.Build(), "leave fueling unset"},
	} {
		_, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
			PlanId:   plan.GetPlan().GetId(),
			Workouts: []*plansv1.Workout{plansv1.Workout_builder{Date: tomorrow, Run: run(tc.fueling)}.Build()},
		}.Build())
		wantCode(t, err, connect.CodeInvalidArgument)
		if err != nil && !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %q, want it to say %q", name, err, tc.want)
		}
	}
}
