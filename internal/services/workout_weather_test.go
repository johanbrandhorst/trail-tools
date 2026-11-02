package services_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
)

func TestWorkoutWeather(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	day := func(days int) string {
		return today.AddDate(0, 0, days).Format(time.DateOnly)
	}

	plan, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{Name: "Build", StartDate: day(0), EndDate: day(40)}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("CreateTrainingPlan: %v", err)
	}
	route, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Name:      "Up north",
		Waypoints: routesv1.Waypoints_builder{Waypoints: latLngs(0.1, 0, 0.2, 0)}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}
	created, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
		PlanId: plan.GetPlan().GetId(),
		Workouts: []*plansv1.Workout{
			plansv1.Workout_builder{Date: day(1), Run: baseRun(), Title: "Easy run"}.Build(),
			plansv1.Workout_builder{Date: day(2), Title: "Hills", Run: plansv1.RunWorkout_builder{
				Intensity: plansv1.RunIntensity_RUN_INTENSITY_HARD, RouteId: route.GetRoute().GetId(),
			}.Build()}.Build(),
			plansv1.Workout_builder{Date: day(3), Rest: plansv1.RestDay_builder{}.Build()}.Build(),
			plansv1.Workout_builder{Date: day(4), Strength: plansv1.StrengthWorkout_builder{}.Build(), Title: "Gym"}.Build(),
			plansv1.Workout_builder{Date: day(20), Run: baseRun(), Title: "Too far ahead"}.Build(),
			// Started between 05:00 and 06:00, and taking 90 minutes.
			plansv1.Workout_builder{Date: day(5), StartTime: "05:00", Title: "Early", Run: plansv1.RunWorkout_builder{
				Intensity: plansv1.RunIntensity_RUN_INTENSITY_BASE, TargetDurationSeconds: 5400,
			}.Build()}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	easy, hills, rest, strength, later, early := created.GetWorkouts()[0], created.GetWorkouts()[1], created.GetWorkouts()[2], created.GetWorkouts()[3], created.GetWorkouts()[4], created.GetWorkouts()[5]
	list := func() map[string]*plansv1.WorkoutWeather {
		t.Helper()
		resp, err := e.plans.ListWorkoutWeather(ctx, plansv1.ListWorkoutWeatherRequest_builder{StartDate: day(-30), EndDate: day(30)}.Build())
		if err != nil {
			t.Fatalf("ListWorkoutWeather: %v", err)
		}
		byWorkout := map[string]*plansv1.WorkoutWeather{}
		for _, w := range resp.GetWeather() {
			byWorkout[w.GetWorkoutId()] = w
		}
		return byWorkout
	}

	// Without activities, only workouts with a route have a location.
	weather := list()
	if len(weather) != 1 || weather[hills.GetId()] == nil {
		t.Fatalf("weather without activities %v, want only the workout with a route", weather)
	}
	if w := weather[hills.GetId()]; w.GetLocationSource() != plansv1.WeatherLocationSource_WEATHER_LOCATION_SOURCE_ROUTE_START ||
		!approx(w.GetLocation().GetLat(), 0.1, 1e-6) || w.GetDate() != day(2) || len(w.GetHours()) != 0 {
		t.Errorf("unexpected route weather %v", w)
	}

	// Other workouts are forecast where the latest activity started. Rest
	// days, strength training and workouts too far ahead have no forecast.
	e.createActivity()
	calls := len(e.mapData.hourlyForecasts)
	weather = list()
	if len(weather) != 3 || weather[rest.GetId()] != nil || weather[strength.GetId()] != nil || weather[later.GetId()] != nil {
		t.Fatalf("got weather for %d workouts, want 3: %v", len(weather), weather)
	}
	if n := len(e.mapData.hourlyForecasts) - calls; n != 2 {
		t.Errorf("%d forecasts, want one for each location", n)
	}
	w := weather[easy.GetId()]
	if w.GetLocationSource() != plansv1.WeatherLocationSource_WEATHER_LOCATION_SOURCE_LATEST_ACTIVITY || w.GetLocation().GetLat() != 0 {
		t.Errorf("unexpected location %v", w)
	}
	// The daytime is from 06:00 to 21:00, with rain from 10:00 to 12:00.
	if d := w.GetDay(); d.GetMinTemperatureCelsius() != 11 || d.GetMaxTemperatureCelsius() != 25 || d.GetMaxPrecipitationProbability() != 80 ||
		!approx(d.GetPrecipitationMm(), 1, 1e-9) || d.GetWeatherCode() != 61 || d.GetDescription() != "Light rain" || d.GetMaxWindSpeedKmh() != 20 {
		t.Errorf("unexpected day %v", d)
	}

	// A workout's weather has the hours of the daytime.
	got, err := e.plans.GetWorkoutWeather(ctx, plansv1.GetWorkoutWeatherRequest_builder{WorkoutId: easy.GetId()}.Build())
	if err != nil {
		t.Fatalf("GetWorkoutWeather: %v", err)
	}
	hours := got.GetWeather().GetHours()
	if len(hours) != 15 || !hours[0].GetTime().AsTime().Equal(today.AddDate(0, 0, 1).Add(6*time.Hour)) || hours[4].GetWeatherCode() != 61 {
		t.Errorf("unexpected hours %v", hours)
	}
	// Without a start time, there's no weather of the workout itself.
	if got.GetWeather().HasWorkout() {
		t.Errorf("a workout without a start time has the weather of the workout %v", got.GetWeather().GetWorkout())
	}
	// With one, the weather of the workout is from the start of the window
	// to its end plus the workout's duration, and its hours before the
	// daytime are listed too.
	got, err = e.plans.GetWorkoutWeather(ctx, plansv1.GetWorkoutWeatherRequest_builder{WorkoutId: early.GetId()}.Build())
	if err != nil {
		t.Fatalf("GetWorkoutWeather: %v", err)
	}
	start := today.AddDate(0, 0, 5).Add(5 * time.Hour)
	if w := got.GetWeather(); !w.HasWorkout() || !w.GetWorkoutStartTime().AsTime().Equal(start) ||
		!w.GetWorkoutEndTime().AsTime().Equal(start.Add(150*time.Minute)) || len(w.GetHours()) != 16 ||
		!w.GetHours()[0].GetTime().AsTime().Equal(start) {
		t.Errorf("unexpected weather of an early workout: %v", w)
	}
	for _, id := range []string{rest.GetId(), strength.GetId(), later.GetId()} {
		got, err := e.plans.GetWorkoutWeather(ctx, plansv1.GetWorkoutWeatherRequest_builder{WorkoutId: id}.Build())
		if err != nil || got.HasWeather() {
			t.Errorf("GetWorkoutWeather of a workout without a forecast: %v, %v", got, err)
		}
	}

	_, err = e.plans.ListWorkoutWeather(ctx, plansv1.ListWorkoutWeatherRequest_builder{StartDate: day(2), EndDate: day(1)}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = e.plans.GetWorkoutWeather(ctx, plansv1.GetWorkoutWeatherRequest_builder{WorkoutId: "00000000-0000-0000-0000-000000000000"}.Build())
	wantCode(t, err, connect.CodeNotFound)
}
