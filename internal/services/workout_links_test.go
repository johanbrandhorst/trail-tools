package services_test

import (
	"context"
	"testing"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/activities"
	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
	"github.com/bs-tools/trailrunn.ing/internal/store"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestLinkWorkouts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	plan, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{Name: "Build", StartDate: "2027-03-01", EndDate: "2027-03-31"}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	base := plansv1.RunWorkout_builder{Intensity: plansv1.RunIntensity_RUN_INTENSITY_BASE}.Build()
	hard := plansv1.RunWorkout_builder{Intensity: plansv1.RunIntensity_RUN_INTENSITY_HARD}.Build()
	// Listed out of order, so pairing goes by start time.
	created, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
		PlanId: plan.GetPlan().GetId(),
		Workouts: []*plansv1.Workout{
			plansv1.Workout_builder{Date: "2027-03-02", Title: "Evening tempo", StartTime: "18:30", Run: hard}.Build(),
			plansv1.Workout_builder{Date: "2027-03-02", Title: "Morning shakeout", StartTime: "07:00", Run: base}.Build(),
			plansv1.Workout_builder{Date: "2027-03-03", Title: "Ride", CrossTraining: plansv1.CrossTrainingWorkout_builder{}.Build()}.Build(),
			plansv1.Workout_builder{Date: "2027-03-04", Title: "Easy run", Run: base}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	evening, morning, ride, easy := created.GetWorkouts()[0], created.GetWorkouts()[1], created.GetWorkouts()[2], created.GetWorkouts()[3]

	nextID := int64(5000)
	// Sync an activity, starting at start in UTC on the local day of
	// start minus eight hours, in Vancouver.
	sync := func(sport string, start time.Time) string {
		t.Helper()
		nextID++
		id, _, err := e.activities.UpsertActivity(ctx, e.userID, &activities.ActivitySummary{
			StravaID:       nextID,
			Name:           sport,
			SportType:      sport,
			StartTime:      start,
			StartDateLocal: start.Add(-8 * time.Hour).Truncate(24 * time.Hour),
			Timezone:       "America/Vancouver",
			RawJSON:        []byte("{}"),
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

	// The day's runs complete its run workouts in order, the evening run
	// starting on the next day in UTC, and the walk none.
	sync("Walk", time.Date(2027, 3, 2, 20, 0, 0, 0, time.UTC))
	morningRun := sync("TrailRun", time.Date(2027, 3, 2, 15, 0, 0, 0, time.UTC))
	eveningRun := sync("Run", time.Date(2027, 3, 3, 2, 30, 0, 0, time.UTC))
	if got := completedBy(morning); got != morningRun {
		t.Errorf("the morning workout is completed by %q, want the morning run %s", got, morningRun)
	}
	if got := completedBy(evening); got != eveningRun {
		t.Errorf("the evening workout is completed by %q, want the evening run %s", got, eveningRun)
	}

	// A ride completes the cross-training of its day, and a walk nothing.
	sync("Walk", time.Date(2027, 3, 3, 16, 0, 0, 0, time.UTC))
	if got := completedBy(ride); got != "" {
		t.Errorf("a walk completed the cross-training: %q", got)
	}
	rideID := sync("Ride", time.Date(2027, 3, 3, 17, 0, 0, 0, time.UTC))
	if got := completedBy(ride); got != rideID {
		t.Errorf("the cross-training is completed by %q, want the ride %s", got, rideID)
	}

	// Removing a wrong link keeps it removed as more activities sync.
	easyRun := sync("Run", time.Date(2027, 3, 4, 16, 0, 0, 0, time.UTC))
	if got := completedBy(easy); got != easyRun {
		t.Fatalf("the easy run is completed by %q, want %s", got, easyRun)
	}
	if _, err := e.plans.UpdateWorkout(ctx, plansv1.UpdateWorkoutRequest_builder{
		Workout:    plansv1.Workout_builder{Id: easy.GetId()}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"completed_activity_id"}},
	}.Build()); err != nil {
		t.Fatalf("UpdateWorkout: %v", err)
	}
	sync("Run", time.Date(2027, 3, 4, 20, 0, 0, 0, time.UTC))
	if got := completedBy(easy); got != "" {
		t.Errorf("a workout whose link was removed is linked again to %q", got)
	}
	// Setting it by hand still works.
	if _, err := e.plans.UpdateWorkout(ctx, plansv1.UpdateWorkoutRequest_builder{
		Workout:    plansv1.Workout_builder{Id: easy.GetId(), CompletedActivityId: easyRun}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"completed_activity_id"}},
	}.Build()); err != nil {
		t.Fatalf("UpdateWorkout: %v", err)
	}
	if got := completedBy(easy); got != easyRun {
		t.Errorf("the easy run is completed by %q after setting it, want %s", got, easyRun)
	}
}
