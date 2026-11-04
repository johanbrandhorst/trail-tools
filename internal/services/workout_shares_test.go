package services_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
	"github.com/johanbrandhorst/trail-tools/internal/services/plan"
)

func TestSharedWorkout(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	plans, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{Name: "Build", StartDate: "2027-03-01", EndDate: "2027-03-31"}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	route, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Name:      "Hill loop",
		Waypoints: routesv1.Waypoints_builder{Waypoints: latLngs(0, 0, 0.01, 0, 0.02, 0)}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	exercises, err := e.exercises.CreateExercises(ctx, plansv1.CreateExercisesRequest_builder{
		Exercises: []*plansv1.Exercise{plansv1.Exercise_builder{
			Name: "Split squat", Instructions: []string{"Lower slowly."},
			Animations: []*plansv1.ExerciseAnimation{splitSquatAnimation()},
		}.Build()},
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	run := baseRun()
	run.SetRouteId(route.GetRoute().GetId())
	created, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
		PlanId: plans.GetPlan().GetId(),
		Workouts: []*plansv1.Workout{
			plansv1.Workout_builder{Date: "2027-03-04", Title: "Thursday hills", StartTime: "18:00", Run: run}.Build(),
			plansv1.Workout_builder{Date: "2027-03-05", Title: "Legs", Strength: plansv1.StrengthWorkout_builder{
				Exercises: []*plansv1.PlannedExercise{plansv1.PlannedExercise_builder{
					ExerciseId: exercises.GetExercises()[0].GetId(), Sets: 3, Reps: 8,
				}.Build()},
			}.Build()}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	hills, legs := created.GetWorkouts()[0], created.GetWorkouts()[1]
	if hills.GetShareUrl() != "" {
		t.Errorf("a new workout is shared at %q", hills.GetShareUrl())
	}

	share := func(id string) string {
		t.Helper()
		resp, err := e.shares.ShareWorkout(ctx, plansv1.ShareWorkoutRequest_builder{Id: id}.Build())
		if err != nil {
			t.Fatalf("ShareWorkout: %v", err)
		}
		token, ok := strings.CutPrefix(resp.GetShareUrl(), "https://trail.example/share/workouts#")
		if !ok || token == "" {
			t.Fatalf("unexpected share URL %q", resp.GetShareUrl())
		}
		return token
	}
	token := share(hills.GetId())
	// Sharing again returns the same link, which the workout shows.
	if again := share(hills.GetId()); again != token {
		t.Errorf("sharing again gave token %q, want %q", again, token)
	}
	got, err := e.plans.GetWorkout(ctx, plansv1.GetWorkoutRequest_builder{Id: hills.GetId()}.Build())
	if err != nil || !strings.HasSuffix(got.GetWorkout().GetShareUrl(), "#"+token) {
		t.Errorf("got workout %v, %v, want its share link", got, err)
	}
	// Another user can't share the workout.
	_, err = e.otherShares.ShareWorkout(ctx, plansv1.ShareWorkoutRequest_builder{Id: hills.GetId()}.Build())
	wantCode(t, err, connect.CodeNotFound)

	// Anyone with the link sees the workout and its route, without its plan.
	shared, err := e.anonShares.GetSharedWorkout(ctx, plansv1.GetSharedWorkoutRequest_builder{Token: token}.Build())
	if err != nil {
		t.Fatalf("GetSharedWorkout: %v", err)
	}
	if w := shared.GetWorkout(); w.GetTitle() != "Thursday hills" || w.GetStartTime() != "18:00" || w.GetPlanId() != "" {
		t.Errorf("unexpected shared workout %v", w)
	}
	if r := shared.GetRoute(); r.GetName() != "Hill loop" || len(r.GetPoints()) < 2 || r.GetStats().GetDistanceMeters() == 0 {
		t.Errorf("unexpected shared route %v", r)
	}

	// The route downloads with the token, and nothing without it.
	download := func(file, token string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, e.srv.URL+plan.ShareDownloadPath+file, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set(plan.ShareTokenHeader, token)
		}
		resp, err := e.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if code, body := download("route.gpx", token); code != http.StatusOK || !strings.Contains(body, "<gpx") {
		t.Errorf("route.gpx: got %d %.100q", code, body)
	}
	if code, body := download("route.fit", token); code != http.StatusOK || !strings.Contains(body, ".FIT") {
		t.Errorf("route.fit: got %d, %d bytes", code, len(body))
	}
	if code, _ := download("route.gpx", ""); code != http.StatusNotFound {
		t.Errorf("route.gpx without a token: got %d, want 404", code)
	}
	if code, _ := download("workout.fit", token); code != http.StatusConflict {
		t.Errorf("workout.fit of a run: got %d, want 409", code)
	}

	// A shared strength workout has its exercises, with their animations,
	// and downloads for a watch.
	legsToken := share(legs.GetId())
	sharedLegs, err := e.anonShares.GetSharedWorkout(ctx, plansv1.GetSharedWorkoutRequest_builder{Token: legsToken}.Build())
	if err != nil {
		t.Fatalf("GetSharedWorkout: %v", err)
	}
	if ex := sharedLegs.GetExercises(); len(ex) != 1 || ex[0].GetName() != "Split squat" || len(ex[0].GetAnimations()) != 1 {
		t.Errorf("unexpected shared exercises %v", ex)
	}
	if code, body := download("workout.fit", legsToken); code != http.StatusOK || !strings.Contains(body, ".FIT") {
		t.Errorf("workout.fit: got %d, %d bytes", code, len(body))
	}

	// Unsharing stops the link working.
	if _, err := e.shares.UnshareWorkout(ctx, plansv1.UnshareWorkoutRequest_builder{Id: hills.GetId()}.Build()); err != nil {
		t.Fatalf("UnshareWorkout: %v", err)
	}
	_, err = e.anonShares.GetSharedWorkout(ctx, plansv1.GetSharedWorkoutRequest_builder{Token: token}.Build())
	wantCode(t, err, connect.CodeNotFound)
	if code, _ := download("route.gpx", token); code != http.StatusNotFound {
		t.Errorf("route.gpx after unsharing: got %d, want 404", code)
	}
	if newToken := share(hills.GetId()); newToken == token {
		t.Error("sharing again after unsharing reused the old link")
	}
}
