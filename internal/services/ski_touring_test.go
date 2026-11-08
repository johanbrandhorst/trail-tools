package services_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/bs-tools/trailrunn.ing/internal/activities"
	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
	tripsv1 "github.com/bs-tools/trailrunn.ing/internal/gen/trips/v1"
	"github.com/bs-tools/trailrunn.ing/internal/store"
)

func TestSkiTouringWorkouts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	plan, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{Name: "Winter", StartDate: "2027-01-01", EndDate: "2027-01-31"}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	skiTour := plansv1.CrossTrainingWorkout_builder{
		Sport: plansv1.CrossTrainingSport_CROSS_TRAINING_SPORT_SKI_TOURING, TargetElevationGainMeters: 1200,
	}.Build()
	other := plansv1.CrossTrainingWorkout_builder{}.Build()
	created, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
		PlanId: plan.GetPlan().GetId(),
		Workouts: []*plansv1.Workout{
			// A ride and a ski tour on the same day, the ride first.
			plansv1.Workout_builder{Date: "2027-01-02", Title: "Ride", StartTime: "07:00", CrossTraining: other}.Build(),
			plansv1.Workout_builder{Date: "2027-01-02", Title: "Ski tour", StartTime: "09:00", CrossTraining: skiTour}.Build(),
			// A ski tour planned as other cross-training.
			plansv1.Workout_builder{Date: "2027-01-03", Title: "Tour", CrossTraining: other}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	ride, tour, untyped := created.GetWorkouts()[0], created.GetWorkouts()[1], created.GetWorkouts()[2]
	if got := tour.GetCrossTraining().GetSport(); got != plansv1.CrossTrainingSport_CROSS_TRAINING_SPORT_SKI_TOURING {
		t.Errorf("got sport %v, want ski touring", got)
	}
	if got := ride.GetCrossTraining().GetSport(); got != plansv1.CrossTrainingSport_CROSS_TRAINING_SPORT_UNSPECIFIED {
		t.Errorf("got sport %v for the ride, want unspecified", got)
	}

	nextID := int64(7000)
	sync := func(sport string, start time.Time) string {
		t.Helper()
		nextID++
		id, _, err := e.activities.UpsertActivity(ctx, e.userID, &activities.ActivitySummary{
			StravaID: nextID, Name: sport, SportType: sport, StartTime: start,
			StartDateLocal: start.Truncate(24 * time.Hour), Timezone: "UTC", RawJSON: []byte("{}"),
		})
		if err != nil {
			t.Fatal(err)
		}
		return store.UUIDToString(id)
	}
	completedBy := func(w *plansv1.Workout) string {
		t.Helper()
		got, err := e.plans.GetWorkout(ctx, plansv1.GetWorkoutRequest_builder{Id: w.GetId()}.Build())
		if err != nil {
			t.Fatal(err)
		}
		return got.GetWorkout().GetCompletedActivityId()
	}

	// The ski touring activity completes the ski tour, though it started
	// first, and the ride the ride.
	skiing := sync("BackcountrySki", time.Date(2027, 1, 2, 6, 0, 0, 0, time.UTC))
	riding := sync("Ride", time.Date(2027, 1, 2, 15, 0, 0, 0, time.UTC))
	if got := completedBy(tour); got != skiing {
		t.Errorf("the ski tour is completed by %q, want the ski touring activity %s", got, skiing)
	}
	if got := completedBy(ride); got != riding {
		t.Errorf("the ride is completed by %q, want the ride %s", got, riding)
	}
	// Without a ski tour planned, a ski touring activity completes other
	// cross-training.
	untypedTour := sync("BackcountrySki", time.Date(2027, 1, 3, 9, 0, 0, 0, time.UTC))
	if got := completedBy(untyped); got != untypedTour {
		t.Errorf("the untyped tour is completed by %q, want %s", got, untypedTour)
	}

	// Unknown sports are rejected.
	_, err = e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
		PlanId: plan.GetPlan().GetId(),
		Workouts: []*plansv1.Workout{plansv1.Workout_builder{
			Date: "2027-01-04", CrossTraining: plansv1.CrossTrainingWorkout_builder{Sport: 99}.Build(),
		}.Build()},
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestTripReportSport(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	created, err := e.trips.CreateTripReport(ctx, tripsv1.CreateTripReportRequest_builder{Title: "Rogers Pass"}.Build())
	if err != nil {
		t.Fatalf("CreateTripReport: %v", err)
	}
	id := created.GetReport().GetId()
	if got := created.GetReport().GetStats().GetSportType(); got != "" {
		t.Errorf("a report without an activity has sport %q, want none", got)
	}
	updated, err := e.trips.UpdateTripReport(ctx, tripsv1.UpdateTripReportRequest_builder{Id: id, SportType: new("BackcountrySki")}.Build())
	if err != nil {
		t.Fatalf("UpdateTripReport: %v", err)
	}
	if got := updated.GetReport().GetStats().GetSportType(); got != "BackcountrySki" {
		t.Errorf("got sport %q, want BackcountrySki", got)
	}
	// Other updates keep it.
	updated, err = e.trips.UpdateTripReport(ctx, tripsv1.UpdateTripReportRequest_builder{Id: id, Title: new("Rogers Pass, Asulkan")}.Build())
	if err != nil || updated.GetReport().GetStats().GetSportType() != "BackcountrySki" {
		t.Errorf("after a title update: %v, %v", updated.GetReport().GetStats(), err)
	}
	for _, bad := range []string{"", "backcountry ski", "Ski<script>"} {
		_, err := e.trips.UpdateTripReport(ctx, tripsv1.UpdateTripReportRequest_builder{Id: id, SportType: new(bad)}.Build())
		wantCode(t, err, connect.CodeInvalidArgument)
	}
	// Other users can't change it.
	_, err = e.otherTrips.UpdateTripReport(ctx, tripsv1.UpdateTripReportRequest_builder{Id: id, SportType: new("Run")}.Build())
	wantCode(t, err, connect.CodeNotFound)
}
