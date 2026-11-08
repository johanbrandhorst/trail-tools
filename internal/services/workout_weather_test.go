package services_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	geov1 "github.com/bs-tools/trailrunn.ing/internal/gen/geo/v1"
	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
	routesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/routes/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
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
			// A group run without a route, from a meeting point.
			plansv1.Workout_builder{
				Date: day(6), Run: baseRun(), Title: "Group run",
				Location: geov1.LatLng_builder{Lat: 0.3, Lng: 0.01}.Build(), LocationName: " Café ",
			}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	easy, hills, rest, strength, later, early, group := created.GetWorkouts()[0], created.GetWorkouts()[1], created.GetWorkouts()[2], created.GetWorkouts()[3], created.GetWorkouts()[4], created.GetWorkouts()[5], created.GetWorkouts()[6]
	if group.GetLocation().GetLat() != 0.3 || group.GetLocationName() != "Café" {
		t.Errorf("got group run at %v, %q, want its location and trimmed name", group.GetLocation(), group.GetLocationName())
	}
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

	// Without activities, only workouts with a location or a route have
	// one.
	weather := list()
	if len(weather) != 2 || weather[hills.GetId()] == nil || weather[group.GetId()] == nil {
		t.Fatalf("weather without activities %v, want only the workouts with a location or route", weather)
	}
	if w := weather[group.GetId()]; w.GetLocationSource() != plansv1.WeatherLocationSource_WEATHER_LOCATION_SOURCE_WORKOUT_LOCATION ||
		w.GetLocation().GetLat() != 0.3 || w.GetLocation().GetLng() != 0.01 {
		t.Errorf("unexpected group run weather %v", w)
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
	if len(weather) != 4 || weather[rest.GetId()] != nil || weather[strength.GetId()] != nil || weather[later.GetId()] != nil {
		t.Fatalf("got weather for %d workouts, want 4: %v", len(weather), weather)
	}
	if n := len(e.mapData.hourlyForecasts) - calls; n != 3 {
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
	// The lowest freezing level of the daytime is at 06:00, and the snow.
	if d := w.GetDay(); d.GetMinFreezingLevelMeters() != 2060 || d.GetMaxSnowDepthCm() != 25 || d.GetSnowfallPast_72HoursCm() != 12 {
		t.Errorf("got freezing level %v, snow depth %v and new snow %v, want 2060, 25 and 12",
			d.GetMinFreezingLevelMeters(), d.GetMaxSnowDepthCm(), d.GetSnowfallPast_72HoursCm())
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

	// A location takes the place of the route's start, and can be cleared.
	updated, err := e.plans.UpdateWorkout(ctx, plansv1.UpdateWorkoutRequest_builder{
		Workout:    plansv1.Workout_builder{Id: hills.GetId(), Location: geov1.LatLng_builder{Lat: 0.5, Lng: 0}.Build()}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"location"}},
	}.Build())
	if err != nil {
		t.Fatalf("UpdateWorkout: %v", err)
	}
	if w := list()[hills.GetId()]; w.GetLocationSource() != plansv1.WeatherLocationSource_WEATHER_LOCATION_SOURCE_WORKOUT_LOCATION || w.GetLocation().GetLat() != 0.5 {
		t.Errorf("got weather %v after setting the location, want it there", w)
	}
	if updated.GetWorkout().GetRun().GetRouteId() != route.GetRoute().GetId() {
		t.Errorf("setting the location changed the route to %q", updated.GetWorkout().GetRun().GetRouteId())
	}
	if _, err := e.plans.UpdateWorkout(ctx, plansv1.UpdateWorkoutRequest_builder{
		Workout:    plansv1.Workout_builder{Id: hills.GetId()}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"location"}},
	}.Build()); err != nil {
		t.Fatalf("UpdateWorkout: %v", err)
	}
	if w := list()[hills.GetId()]; w.GetLocationSource() != plansv1.WeatherLocationSource_WEATHER_LOCATION_SOURCE_ROUTE_START {
		t.Errorf("got weather %v after clearing the location, want the route's start", w)
	}
	_, err = e.plans.UpdateWorkout(ctx, plansv1.UpdateWorkoutRequest_builder{
		Workout:    plansv1.Workout_builder{Id: hills.GetId(), Location: geov1.LatLng_builder{Lat: 91}.Build()}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"location"}},
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	_, err = e.plans.ListWorkoutWeather(ctx, plansv1.ListWorkoutWeatherRequest_builder{StartDate: day(2), EndDate: day(1)}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = e.plans.GetWorkoutWeather(ctx, plansv1.GetWorkoutWeatherRequest_builder{WorkoutId: "00000000-0000-0000-0000-000000000000"}.Build())
	wantCode(t, err, connect.CodeNotFound)
}
