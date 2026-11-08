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

func TestTouringLimits(t *testing.T) {
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

	// The user has no limits to start with.
	got, err := e.plans.GetTouringLimits(ctx, plansv1.GetTouringLimitsRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("GetTouringLimits: %v", err)
	}
	if got.GetLimits().HasMaxWindGustsKmh() || got.GetLimits().HasUpdateTime() {
		t.Errorf("got limits %v, want none", got.GetLimits())
	}

	// Limits must be in range, and set limits positive.
	for name, limits := range map[string]*plansv1.TouringLimits{
		"gusts in m/s":   plansv1.TouringLimits_builder{MaxWindGustsKmh: new(15.0 / 3.6)}.Build(),
		"zero gusts":     plansv1.TouringLimits_builder{MaxWindGustsKmh: new(0.0)}.Build(),
		"unknown danger": plansv1.TouringLimits_builder{MaxAvalancheDangerAlpine: geov1.AvalancheDanger(6)}.Build(),
	} {
		_, err := e.plans.SetTouringLimits(ctx, plansv1.SetTouringLimitsRequest_builder{Limits: limits}.Build())
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("SetTouringLimits with %s: got %v, want InvalidArgument", name, err)
		}
	}

	// The user sets their limits: up to Moderate in the alpine, gusts up
	// to 60 km/h, a freezing level up to 2500 m and 30 cm of new snow.
	set, err := e.plans.SetTouringLimits(ctx, plansv1.SetTouringLimitsRequest_builder{Limits: plansv1.TouringLimits_builder{
		MaxAvalancheDangerAlpine:  geov1.AvalancheDanger_AVALANCHE_DANGER_MODERATE,
		MaxWindGustsKmh:           new(60.0),
		MaxFreezingLevelMeters:    new(2500.0),
		MaxSnowfallPast_72HoursCm: new(30.0),
	}.Build()}.Build())
	if err != nil {
		t.Fatalf("SetTouringLimits: %v", err)
	}
	got, err = e.plans.GetTouringLimits(ctx, plansv1.GetTouringLimitsRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("GetTouringLimits: %v", err)
	}
	if l := got.GetLimits(); l.GetMaxAvalancheDangerAlpine() != geov1.AvalancheDanger_AVALANCHE_DANGER_MODERATE ||
		l.GetMaxAvalancheDangerTreeline() != geov1.AvalancheDanger_AVALANCHE_DANGER_UNSPECIFIED ||
		l.GetMaxWindGustsKmh() != 60 || l.GetMaxFreezingLevelMeters() != 2500 || l.GetMaxSnowfallPast_72HoursCm() != 30 ||
		!l.HasUpdateTime() {
		t.Errorf("got limits %v, want those set, %v", l, set.GetLimits())
	}
	// Another user doesn't see them.
	other, err := e.otherTraining.GetTouringLimits(ctx, plansv1.GetTouringLimitsRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("GetTouringLimits: %v", err)
	}
	if other.GetLimits().HasMaxWindGustsKmh() {
		t.Errorf("another user got limits %v, want none", other.GetLimits())
	}

	// Ski tours get a verdict: a no-go tomorrow, when the danger is High in
	// the alpine, a go the day after, when it's Moderate, and a caution
	// outside the forecast areas, where the danger can't be checked. Runs
	// get none.
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
			plansv1.Workout_builder{Date: day(1), Title: "Alps", CrossTraining: skiTour, Location: location(45.9, 6.9)}.Build(),
			plansv1.Workout_builder{Date: day(1), Title: "Valley run", Run: baseRun(), Location: location(51.3, -117.5)}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	list, err := e.plans.ListWorkoutWeather(ctx, plansv1.ListWorkoutWeatherRequest_builder{StartDate: day(0), EndDate: day(3)}.Build())
	if err != nil {
		t.Fatalf("ListWorkoutWeather: %v", err)
	}
	verdicts := make(map[string]*geov1.TouringVerdict)
	for _, w := range list.GetWeather() {
		verdicts[w.GetWorkoutId()] = w.GetTouringVerdict()
	}
	ids := created.GetWorkouts()
	for i, want := range []geov1.TouringVerdictLevel{
		geov1.TouringVerdictLevel_TOURING_VERDICT_LEVEL_NO_GO,
		geov1.TouringVerdictLevel_TOURING_VERDICT_LEVEL_GO,
		geov1.TouringVerdictLevel_TOURING_VERDICT_LEVEL_CAUTION,
		geov1.TouringVerdictLevel_TOURING_VERDICT_LEVEL_UNSPECIFIED,
	} {
		if got := verdicts[ids[i].GetId()].GetLevel(); got != want {
			t.Errorf("%s: got verdict %v, want %v", ids[i].GetTitle(), got, want)
		}
	}
	// The verdict says why, with a check for each limit.
	noGo := verdicts[ids[0].GetId()]
	if checks := noGo.GetChecks(); len(checks) != 4 ||
		checks[0].GetMessage() != "Avalanche danger in the alpine is High, above your limit of Moderate" {
		t.Errorf("got checks %v, want 4 starting with the alpine danger", checks)
	}

	// A route forecast for a ski tour tomorrow gets a verdict too: no-go
	// for the danger, and for the 40 cm of new snow at the finish.
	forecast := func(skiTour bool) *routesv1.GetRouteForecastResponse {
		t.Helper()
		resp, err := e.routes.GetRouteForecast(ctx, routesv1.GetRouteForecastRequest_builder{
			Path:      routesv1.Path_builder{Points: latLngs(51.3, -117.5, 51.32, -117.5)}.Build(),
			StartTime: timestamppb.New(today.AddDate(0, 0, 1).Add(9 * time.Hour)),
			SpeedKmh:  4,
			SkiTour:   skiTour,
		}.Build())
		if err != nil {
			t.Fatalf("GetRouteForecast: %v", err)
		}
		return resp
	}
	route := forecast(true)
	if got := route.GetTouringVerdict().GetLevel(); got != geov1.TouringVerdictLevel_TOURING_VERDICT_LEVEL_NO_GO {
		t.Errorf("got route verdict %v, want no-go", got)
	}
	var snow string
	for _, check := range route.GetTouringVerdict().GetChecks() {
		if strings.Contains(check.GetMessage(), "snow") {
			snow = check.GetMessage()
		}
	}
	if snow != "40 cm of snow in the past 3 days, above your limit of 30 cm" {
		t.Errorf("got snow check %q, want 40 cm above the limit", snow)
	}
	// Other routes don't.
	if route := forecast(false); route.HasTouringVerdict() {
		t.Errorf("got verdict %v for a route that isn't a ski tour, want none", route.GetTouringVerdict())
	}
}
