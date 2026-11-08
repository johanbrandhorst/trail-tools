package services_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	geov1 "github.com/bs-tools/trailrunn.ing/internal/gen/geo/v1"
	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
	routesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/routes/v1"
	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/bs-tools/trailrunn.ing/internal/routing"
)

func TestWindWarnings(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	day := func(days int) string {
		return today.AddDate(0, 0, days).Format(time.DateOnly)
	}

	// The map has a trail north through a forest at longitude 10, and one
	// through open ground at longitude 11, each about 2.2 km long.
	trail := func(source int64, lng float64) routing.Edge {
		return routing.Edge{
			Source: source, Target: source + 1, Highway: "path", Terrain: geo.TerrainTrail,
			Points: []geo.Point{{Lat: 10, Lng: lng}, {Lat: 10.01, Lng: lng}, {Lat: 10.02, Lng: lng}},
		}
	}
	forest := routing.Forest{ID: 1, Ring: []geo.Point{
		{Lat: 9.99, Lng: 9.99}, {Lat: 9.99, Lng: 10.01}, {Lat: 10.03, Lng: 10.01}, {Lat: 10.03, Lng: 9.99}, {Lat: 9.99, Lng: 9.99},
	}}
	bounds := geo.Bounds{MinLat: 9.9, MinLng: 9.9, MaxLat: 10.1, MaxLng: 11.1}
	if _, err := routing.SaveRegion(ctx, e.db, "forest", bounds, slices.Values([]routing.Edge{trail(1, 10), trail(3, 11)}), nil, []routing.Forest{forest}); err != nil {
		t.Fatalf("SaveRegion: %v", err)
	}

	plan, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{Name: "Build", StartDate: day(0), EndDate: day(10)}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("CreateTrainingPlan: %v", err)
	}
	newRoute := func(name string, lng float64) string {
		t.Helper()
		route, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
			Name:      name,
			Waypoints: routesv1.Waypoints_builder{Waypoints: latLngs(10, lng, 10.02, lng)}.Build(),
		}.Build())
		if err != nil {
			t.Fatalf("CreateRoute: %v", err)
		}
		return route.GetRoute().GetId()
	}
	woods, open := newRoute("Through the woods", 10), newRoute("In the open", 11)
	created, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
		PlanId: plan.GetPlan().GetId(),
		Workouts: []*plansv1.Workout{
			plansv1.Workout_builder{Date: day(1), Title: "Woods", Run: plansv1.RunWorkout_builder{
				Intensity: plansv1.RunIntensity_RUN_INTENSITY_BASE, RouteId: woods,
			}.Build()}.Build(),
			plansv1.Workout_builder{Date: day(1), Title: "Open", Run: plansv1.RunWorkout_builder{
				Intensity: plansv1.RunIntensity_RUN_INTENSITY_BASE, RouteId: open,
			}.Build()}.Build(),
			// From a meeting point in the forest, without a route, at a time.
			plansv1.Workout_builder{
				Date: day(2), StartTime: "08:00", Run: baseRun(), Title: "Group run",
				Location: geov1.LatLng_builder{Lat: 10.01, Lng: 10}.Build(),
			}.Build(),
			// From a meeting point in the open.
			plansv1.Workout_builder{
				Date: day(2), Run: baseRun(), Title: "Open group run",
				Location: geov1.LatLng_builder{Lat: 10.01, Lng: 11}.Build(),
			}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	woodsRun, openRun, groupRun, openGroupRun := created.GetWorkouts()[0], created.GetWorkouts()[1], created.GetWorkouts()[2], created.GetWorkouts()[3]
	warnings := func() map[string][]*plansv1.WeatherWarning {
		t.Helper()
		resp, err := e.plans.ListWorkoutWeather(ctx, plansv1.ListWorkoutWeatherRequest_builder{StartDate: day(0), EndDate: day(5)}.Build())
		if err != nil {
			t.Fatalf("ListWorkoutWeather: %v", err)
		}
		if len(resp.GetWeather()) != 4 {
			t.Fatalf("got %d forecasts, want 4", len(resp.GetWeather()))
		}
		byWorkout := map[string][]*plansv1.WeatherWarning{}
		for _, w := range resp.GetWeather() {
			if len(w.GetWarnings()) > 0 {
				byWorkout[w.GetWorkoutId()] = w.GetWarnings()
			}
		}
		return byWorkout
	}

	// Strong breezes are no danger.
	e.mapData.setGusts(45)
	if got := warnings(); len(got) != 0 {
		t.Errorf("got warnings %v for gusts of 45 km/h, want none", got)
	}

	// Gales are, in the forest.
	e.mapData.setGusts(75)
	got := warnings()
	if len(got) != 2 || len(got[woodsRun.GetId()]) != 1 || len(got[groupRun.GetId()]) != 1 {
		t.Fatalf("got warnings %v, want them for the run through the woods and the group run in the forest", got)
	}
	if got[openRun.GetId()] != nil || got[openGroupRun.GetId()] != nil {
		t.Errorf("got warnings for runs in the open")
	}
	w := got[woodsRun.GetId()][0]
	if w.GetKind() != plansv1.WeatherWarningKind_WEATHER_WARNING_KIND_WIND_IN_FOREST || w.GetMaxWindGustsKmh() != 75 ||
		!approx(w.GetForestDistanceMeters(), 2200, 150) || !strings.Contains(w.GetMessage(), "Gusts up to 75 km/h are forecast during the day") ||
		!strings.Contains(w.GetMessage(), "2.2 km of its route is in forest") {
		t.Errorf("unexpected warning for the run through the woods: %v", w)
	}
	w = got[groupRun.GetId()][0]
	if w.GetForestDistanceMeters() != 0 || !strings.Contains(w.GetMessage(), "during the workout") ||
		!strings.Contains(w.GetMessage(), "the trails around its location are mostly in forest") {
		t.Errorf("unexpected warning for the group run: %v", w)
	}

	// A workout's forecast has its warnings too.
	one, err := e.plans.GetWorkoutWeather(ctx, plansv1.GetWorkoutWeatherRequest_builder{WorkoutId: woodsRun.GetId()}.Build())
	if err != nil {
		t.Fatalf("GetWorkoutWeather: %v", err)
	}
	if len(one.GetWeather().GetWarnings()) != 1 {
		t.Errorf("got warnings %v, want the wind warning", one.GetWeather().GetWarnings())
	}
}
