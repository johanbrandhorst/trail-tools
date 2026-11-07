package services_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestRacePlanCourse(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	course, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Name:      "Course",
		Waypoints: routesv1.Waypoints_builder{Waypoints: latLngs(0, 0, 0.1, 0, 0.2, 0)}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	race, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
		Race: racesv1.Race_builder{Name: "Mountain Ultra", Date: "2027-06-12", CourseRouteId: course.GetRoute().GetId(), OnCalendar: true}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRace: %v", err)
	}
	plan, err := e.racePlans.CreateRacePlan(ctx, racesv1.CreateRacePlanRequest_builder{RaceId: race.GetRace().GetId()}.Build())
	if err != nil {
		t.Fatalf("CreateRacePlan: %v", err)
	}
	planID := plan.GetPlan().GetId()
	_, err = e.racePlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{
		Plan: racesv1.RacePlan_builder{
			Id: planID,
			Checkpoints: []*racesv1.Checkpoint{
				racesv1.Checkpoint_builder{Name: "Halfway hut", DistanceMeters: 11119, Aid: "water, gels", PlannedSegmentSeconds: 5000, PlannedStopSeconds: 300}.Build(),
				racesv1.Checkpoint_builder{Name: "Finish", DistanceMeters: 22239, PlannedSegmentSeconds: 5400}.Build(),
			},
		}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"checkpoints"}},
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRacePlan: %v", err)
	}

	// The course is a FIT course with the checkpoints as course points.
	resp, fit := e.get("/race-plans/"+planID+".fit", e.token)
	if resp.StatusCode != http.StatusOK || len(fit) < 100 || string(fit[8:12]) != ".FIT" ||
		resp.Header.Get("Content-Disposition") != `attachment; filename="Mountain-Ultra.fit"` {
		t.Fatalf("unexpected FIT response %d %v", resp.StatusCode, resp.Header)
	}
	for _, name := range []string{"Mountain Ultra", "Halfway hut", "Finish"} {
		if !bytes.Contains(fit, []byte(name)) {
			t.Errorf("FIT course doesn't contain %q", name)
		}
	}

	for name, tc := range map[string]struct {
		path, token string
		want        int
	}{
		"another user": {"/race-plans/" + planID + ".fit", e.otherToken, http.StatusNotFound},
		"signed out":   {"/race-plans/" + planID + ".fit", "", http.StatusUnauthorized},
		"invalid ID":   {"/race-plans/nope.fit", e.token, http.StatusBadRequest},
		"other path":   {"/race-plans/" + planID, e.token, http.StatusNotFound},
	} {
		if resp, _ := e.get(tc.path, tc.token); resp.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d", name, resp.StatusCode, tc.want)
		}
	}

	// A race without a course has no FIT course.
	noCourse, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
		Race: racesv1.Race_builder{Name: "Road race", Date: "2027-07-01", OnCalendar: true}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.racePlans.CreateRacePlan(ctx, racesv1.CreateRacePlanRequest_builder{RaceId: noCourse.GetRace().GetId()}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if resp, _ := e.get("/race-plans/"+other.GetPlan().GetId()+".fit", e.token); resp.StatusCode != http.StatusConflict {
		t.Errorf("race without a course: status %d, want 409", resp.StatusCode)
	}
}
