package services_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	geov1 "github.com/bs-tools/trailrunn.ing/internal/gen/geo/v1"
	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
	routesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/routes/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestAvalancheForecasts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	day := func(days int) string {
		return today.AddDate(0, 0, days).Format(time.DateOnly)
	}
	location := func(lat, lng float64) *geov1.LatLng {
		return geov1.LatLng_builder{Lat: lat, Lng: lng}.Build()
	}

	// Avalanche forecasts are looked up by point.
	resp, err := e.geo.GetAvalancheForecast(ctx, geov1.GetAvalancheForecastRequest_builder{Location: location(51.3, -117.5)}.Build())
	if err != nil {
		t.Fatalf("GetAvalancheForecast: %v", err)
	}
	f := resp.GetForecast()
	if f.GetTitle() != "Glacier" || f.GetForecaster() != "Parks Canada" || f.GetUrl() == "" {
		t.Errorf("got %q by %q at %q, want Glacier by Parks Canada with a link", f.GetTitle(), f.GetForecaster(), f.GetUrl())
	}
	if len(f.GetDays()) != 3 || f.GetDays()[1].GetDate() != day(1) || f.GetDays()[1].GetAlpine().GetDanger() != geov1.AvalancheDanger_AVALANCHE_DANGER_HIGH {
		t.Errorf("got days %v, want 3 with High in the alpine tomorrow", f.GetDays())
	}
	if problems := f.GetProblems(); len(problems) != 1 || problems[0].GetName() != "Wind Slab" || problems[0].GetMaxSize() != 2.5 {
		t.Errorf("got problems %v, want a wind slab up to size 2.5", problems)
	}
	// Elsewhere there is none.
	resp, err = e.geo.GetAvalancheForecast(ctx, geov1.GetAvalancheForecastRequest_builder{Location: location(45.9, 6.9)}.Build())
	if err != nil {
		t.Fatalf("GetAvalancheForecast: %v", err)
	}
	if resp.HasForecast() {
		t.Errorf("got forecast %v outside the forecast areas, want none", resp.GetForecast())
	}
	// When forecasts are down, it says so.
	_, err = e.geo.GetAvalancheForecast(ctx, geov1.GetAvalancheForecastRequest_builder{Location: location(51, -100)}.Build())
	wantCode(t, err, connect.CodeUnavailable)
	// The location is required, and must be valid.
	_, err = e.geo.GetAvalancheForecast(ctx, geov1.GetAvalancheForecastRequest_builder{}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = e.geo.GetAvalancheForecast(ctx, geov1.GetAvalancheForecastRequest_builder{Location: location(91, 0)}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	// A route forecast for tomorrow has the forecast of its highest point,
	// its end, as the fake's elevations rise with the latitude, and warns
	// about the High danger.
	forecastRoute := func(lng float64) *routesv1.GetRouteForecastResponse {
		t.Helper()
		resp, err := e.routes.GetRouteForecast(ctx, routesv1.GetRouteForecastRequest_builder{
			Path:      routesv1.Path_builder{Points: latLngs(51.3, lng, 51.32, lng)}.Build(),
			StartTime: timestamppb.New(today.AddDate(0, 0, 1).Add(9 * time.Hour)),
			SpeedKmh:  4,
		}.Build())
		if err != nil {
			t.Fatalf("GetRouteForecast: %v", err)
		}
		return resp
	}
	avalancheWarning := func(warnings []string) string {
		for _, w := range warnings {
			if strings.HasPrefix(w, "Avalanche danger") {
				return w
			}
		}
		return ""
	}
	route := forecastRoute(-117.5)
	if got := route.GetAvalancheForecast().GetTitle(); got != "Glacier" {
		t.Errorf("got avalanche forecast %q, want Glacier", got)
	}
	if points := e.avalanches.requestedPoints(); points[len(points)-1].Lat != 51.32 {
		t.Errorf("got the forecast at %v, want it at the highest point", points[len(points)-1])
	}
	if got := avalancheWarning(route.GetWarnings()); !strings.HasPrefix(got, "Avalanche danger is High in the alpine on ") {
		t.Errorf("got warnings %q, want one about High danger in the alpine", route.GetWarnings())
	}
	// Elsewhere, and when forecasts are down, routes are forecast without.
	for _, lng := range []float64{6.9, -100} {
		route := forecastRoute(lng)
		if route.HasAvalancheForecast() || avalancheWarning(route.GetWarnings()) != "" {
			t.Errorf("at longitude %v got avalanche forecast %v and warnings %q, want none", lng, route.GetAvalancheForecast(), route.GetWarnings())
		}
	}

	// Ski tours get the forecast too, with a warning on days of High
	// danger, while runs don't.
	plan, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{Name: "Winter", StartDate: day(0), EndDate: day(10)}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("CreateTrainingPlan: %v", err)
	}
	skiTour := plansv1.CrossTrainingWorkout_builder{Sport: plansv1.CrossTrainingSport_CROSS_TRAINING_SPORT_SKI_TOURING}.Build()
	created, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
		PlanId: plan.GetPlan().GetId(),
		Workouts: []*plansv1.Workout{
			plansv1.Workout_builder{Date: day(1), Title: "Asulkan", CrossTraining: skiTour, Location: location(51.3, -117.5)}.Build(),
			plansv1.Workout_builder{Date: day(2), Title: "Balu Pass", CrossTraining: skiTour, Location: location(51.3, -117.5)}.Build(),
			plansv1.Workout_builder{Date: day(1), Title: "Valley run", Run: baseRun(), Location: location(51.3, -117.5)}.Build(),
			plansv1.Workout_builder{Date: day(1), Title: "Alps", CrossTraining: skiTour, Location: location(45.9, 6.9)}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	ids := make([]string, len(created.GetWorkouts()))
	for i, w := range created.GetWorkouts() {
		ids[i] = w.GetId()
	}
	list, err := e.plans.ListWorkoutWeather(ctx, plansv1.ListWorkoutWeatherRequest_builder{StartDate: day(0), EndDate: day(3)}.Build())
	if err != nil {
		t.Fatalf("ListWorkoutWeather: %v", err)
	}
	byID := make(map[string]*plansv1.WorkoutWeather)
	for _, w := range list.GetWeather() {
		byID[w.GetWorkoutId()] = w
	}
	avalancheWarnings := func(w *plansv1.WorkoutWeather) []*plansv1.WeatherWarning {
		var warnings []*plansv1.WeatherWarning
		for _, warning := range w.GetWarnings() {
			if warning.GetKind() == plansv1.WeatherWarningKind_WEATHER_WARNING_KIND_AVALANCHE_DANGER {
				warnings = append(warnings, warning)
			}
		}
		return warnings
	}
	high := byID[ids[0]]
	if high.GetAvalancheForecast().GetTitle() != "Glacier" {
		t.Errorf("got avalanche forecast %v for the ski tour, want Glacier", high.GetAvalancheForecast())
	}
	if warnings := avalancheWarnings(high); len(warnings) != 1 ||
		warnings[0].GetMaxAvalancheDanger() != geov1.AvalancheDanger_AVALANCHE_DANGER_HIGH ||
		!strings.Contains(warnings[0].GetMessage(), "not recommended") {
		t.Errorf("got warnings %v, want one about High danger", warnings)
	}
	moderate := byID[ids[1]]
	if !moderate.HasAvalancheForecast() || len(avalancheWarnings(moderate)) != 0 {
		t.Errorf("got forecast %v and warnings %v on a Moderate day, want a forecast without warnings", moderate.GetAvalancheForecast(), moderate.GetWarnings())
	}
	for _, id := range ids[2:] {
		if w := byID[id]; w == nil || w.HasAvalancheForecast() || len(avalancheWarnings(w)) != 0 {
			t.Errorf("got weather %v for a run or a tour outside the forecast areas, want it without an avalanche forecast", w)
		}
	}
	// A single workout's weather has it too.
	got, err := e.plans.GetWorkoutWeather(ctx, plansv1.GetWorkoutWeatherRequest_builder{WorkoutId: ids[0]}.Build())
	if err != nil {
		t.Fatalf("GetWorkoutWeather: %v", err)
	}
	if len(avalancheWarnings(got.GetWeather())) != 1 {
		t.Errorf("got warnings %v, want one about High danger", got.GetWeather().GetWarnings())
	}
}
