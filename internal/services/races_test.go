package services_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"connectrpc.com/connect/v2"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestRacesAndRacePlans(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// A 22km course climbing 200m.
	course, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
		Name:      "Course",
		Waypoints: routesv1.Waypoints_builder{Waypoints: latLngs(0, 0, 0.1, 0, 0.2, 0)}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
		Race: racesv1.Race_builder{
			Name:           "Mountain Ultra",
			Date:           "2027-06-12",
			StartTime:      "5:00",
			DistanceMeters: 22239,
			CutoffSeconds:  6 * 3600,
			Priority:       racesv1.RacePriority_RACE_PRIORITY_A,
			CourseRouteId:  course.GetRoute().GetId(),
		}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRace: %v", err)
	}
	raceID := created.GetRace().GetId()
	if created.GetRace().GetStartTime() != "05:00" {
		t.Errorf("start time should be normalised, got %q", created.GetRace().GetStartTime())
	}

	// Validation errors.
	_, err = e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
		Race: racesv1.Race_builder{Name: "Bad", Date: "12/06/2027"}.Build(),
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	// Races are private.
	_, err = e.otherRaces.GetRace(ctx, racesv1.GetRaceRequest_builder{Id: raceID}.Build())
	wantCode(t, err, connect.CodeNotFound)

	// Update only the notes.
	updated, err := e.races.UpdateRace(ctx, racesv1.UpdateRaceRequest_builder{
		Race:       racesv1.Race_builder{Id: raceID, Notes: "Bring poles", Name: "ignored"}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRace: %v", err)
	}
	if updated.GetRace().GetNotes() != "Bring poles" || updated.GetRace().GetName() != "Mountain Ultra" {
		t.Errorf("unexpected race %v", updated.GetRace())
	}

	// Races start off the calendar, and only races on it can be planned.
	if created.GetRace().GetOnCalendar() {
		t.Error("a new race is on the calendar")
	}
	_, err = e.racePlans.CreateRacePlan(ctx, racesv1.CreateRacePlanRequest_builder{RaceId: raceID}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)
	onCalendar, err := e.races.ListRaces(ctx, racesv1.ListRacesRequest_builder{OnCalendarOnly: true}.Build())
	if err != nil || len(onCalendar.GetRaces()) != 0 {
		t.Fatalf("got %d races on the calendar (%v), want none", len(onCalendar.GetRaces()), err)
	}
	if _, err := e.races.UpdateRace(ctx, racesv1.UpdateRaceRequest_builder{
		Race:       racesv1.Race_builder{Id: raceID, OnCalendar: true}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"on_calendar"}},
	}.Build()); err != nil {
		t.Fatalf("UpdateRace: %v", err)
	}
	if onCalendar, err = e.races.ListRaces(ctx, racesv1.ListRacesRequest_builder{OnCalendarOnly: true}.Build()); err != nil || len(onCalendar.GetRaces()) != 1 {
		t.Fatalf("got %d races on the calendar (%v), want 1", len(onCalendar.GetRaces()), err)
	}

	// A new plan is seeded with the finish and core contingencies, and most
	// rules fail.
	newPlan, err := e.racePlans.CreateRacePlan(ctx, racesv1.CreateRacePlanRequest_builder{RaceId: raceID}.Build())
	if err != nil {
		t.Fatalf("CreateRacePlan: %v", err)
	}
	planID := newPlan.GetPlan().GetId()
	if cps := newPlan.GetPlan().GetCheckpoints(); len(cps) != 1 || cps[0].GetName() != "Finish" || cps[0].GetCutoffSeconds() != 6*3600 {
		t.Errorf("unexpected seeded checkpoints %v", cps)
	}
	if n := len(newPlan.GetPlan().GetContingencies()); n != 7 {
		t.Errorf("got %d seeded contingencies, want 7", n)
	}
	if a := newPlan.GetAnalysis(); a.GetRulesPassed() >= a.GetRulesTotal() || a.GetRulesTotal() < 10 {
		t.Errorf("unexpected rule counts %d/%d", a.GetRulesPassed(), a.GetRulesTotal())
	}

	// Fill in checkpoints and nutrition.
	plan := racesv1.RacePlan_builder{
		Id:    planID,
		Goals: racesv1.RaceGoals_builder{AFinishSeconds: 3 * 3600, BFinishSeconds: 3*3600 + 1800, CFinishSeconds: 4 * 3600}.Build(),
		Checkpoints: []*racesv1.Checkpoint{
			racesv1.Checkpoint_builder{
				Name:                  "CP1",
				DistanceMeters:        11119,
				CutoffSeconds:         5400,
				PlannedSegmentSeconds: 5000,
				PlannedStopSeconds:    300,
				CrewAccess:            true,
				Intake:                []*racesv1.Intake{racesv1.Intake_builder{Product: "Gel", Quantity: 3}.Build()},
			}.Build(),
			racesv1.Checkpoint_builder{
				Name:                  "Finish",
				DistanceMeters:        22239,
				CutoffSeconds:         6 * 3600,
				PlannedSegmentSeconds: 5400,
				Intake:                []*racesv1.Intake{racesv1.Intake_builder{Product: "Mystery bar", Quantity: 1}.Build()},
			}.Build(),
		},
		Nutrition: racesv1.NutritionStrategy_builder{
			CarbsGramsPerHour: 80,
			FluidMlPerHour:    500,
			SodiumMgPerHour:   500,
			Products: []*racesv1.NutritionProduct{
				racesv1.NutritionProduct_builder{Name: "Gel", CarbsGrams: 25, SodiumMg: 100}.Build(),
			},
		}.Build(),
	}.Build()
	updatedPlan, err := e.racePlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{
		Plan:       plan,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"goals", "checkpoints", "nutrition"}},
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRacePlan: %v", err)
	}
	analysis := updatedPlan.GetAnalysis()
	cp1 := analysis.GetCheckpoints()[0]
	if cp1.GetArrivalClockTime() != "06:23" || cp1.GetCutoffBufferSeconds() != 400 || cp1.GetCarbsGrams() != 75 {
		t.Errorf("unexpected CP1 analysis %v", cp1)
	}
	// The segment elevation comes from the course: 100m per segment.
	if cp1.GetSegmentElevationGainMeters() < 95 || cp1.GetSegmentElevationGainMeters() > 105 {
		t.Errorf("got CP1 gain %v from the course, want ~100", cp1.GetSegmentElevationGainMeters())
	}
	if analysis.GetPlannedFinishSeconds() != 10700 {
		t.Errorf("got finish %d, want 10700", analysis.GetPlannedFinishSeconds())
	}
	rules := map[string]*racesv1.RuleResult{}
	for _, r := range analysis.GetRules() {
		rules[r.GetId()] = r
	}
	for id, want := range map[string]racesv1.RuleStatus{
		"goals":           racesv1.RuleStatus_RULE_STATUS_PASS,
		"checkpoints":     racesv1.RuleStatus_RULE_STATUS_PASS,
		"segment-times":   racesv1.RuleStatus_RULE_STATUS_PASS,
		"cutoff-buffers":  racesv1.RuleStatus_RULE_STATUS_WARN,
		"known-products":  racesv1.RuleStatus_RULE_STATUS_FAIL,
		"segment-fueling": racesv1.RuleStatus_RULE_STATUS_FAIL,
		"night-gear":      racesv1.RuleStatus_RULE_STATUS_FAIL,
		"crew":            racesv1.RuleStatus_RULE_STATUS_PASS,
		"contingencies":   racesv1.RuleStatus_RULE_STATUS_FAIL,
	} {
		if got := rules[id].GetStatus(); got != want {
			t.Errorf("rule %s: got %v (%s), want %v", id, got, rules[id].GetMessage(), want)
		}
	}
	if !strings.Contains(rules["known-products"].GetMessage(), "Mystery bar") {
		t.Errorf("unexpected known-products message %q", rules["known-products"].GetMessage())
	}
	// Other sections were left alone.
	if n := len(updatedPlan.GetPlan().GetContingencies()); n != 7 {
		t.Errorf("contingencies were changed, got %d", n)
	}

	_, err = e.racePlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{
		Plan:       racesv1.RacePlan_builder{Id: planID}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"share_url"}},
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	// Share the plan, view it anonymously, then stop sharing.
	shared, err := e.racePlans.ShareRacePlan(ctx, racesv1.ShareRacePlanRequest_builder{Id: planID}.Build())
	if err != nil {
		t.Fatalf("ShareRacePlan: %v", err)
	}
	shareURL := shared.GetShareUrl()
	token, ok := strings.CutPrefix(shareURL, "https://trail.example/share/race-plans#")
	if !ok || token == "" {
		t.Fatalf("unexpected share URL %q", shareURL)
	}
	again, err := e.racePlans.ShareRacePlan(ctx, racesv1.ShareRacePlanRequest_builder{Id: planID}.Build())
	if err != nil || again.GetShareUrl() != shareURL {
		t.Errorf("sharing again should return the same URL: %v %v", again, err)
	}
	view, err := e.anonPlans.GetSharedRacePlan(ctx, racesv1.GetSharedRacePlanRequest_builder{Token: token}.Build())
	if err != nil {
		t.Fatalf("GetSharedRacePlan: %v", err)
	}
	if view.GetRace().GetName() != "Mountain Ultra" || len(view.GetCourse()) == 0 || view.GetRace().GetCourseRouteId() != "" {
		t.Errorf("unexpected shared plan %v", view.GetRace())
	}
	if _, err := e.racePlans.UnshareRacePlan(ctx, racesv1.UnshareRacePlanRequest_builder{Id: planID}.Build()); err != nil {
		t.Fatalf("UnshareRacePlan: %v", err)
	}
	_, err = e.anonPlans.GetSharedRacePlan(ctx, racesv1.GetSharedRacePlanRequest_builder{Token: token}.Build())
	wantCode(t, err, connect.CodeNotFound)

	// Other users can't see the plan, and anonymous users can't use the
	// authenticated API.
	_, err = e.anonPlans.GetRacePlan(ctx, racesv1.GetRacePlanRequest_builder{Id: planID}.Build())
	wantCode(t, err, connect.CodeUnauthenticated)

	list, err := e.racePlans.ListRacePlans(ctx, racesv1.ListRacePlansRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("ListRacePlans: %v", err)
	}
	if len(list.GetPlans()) != 1 || list.GetPlans()[0].GetShared() {
		t.Errorf("unexpected plans %v", list.GetPlans())
	}

	// Deleting the race deletes its plans.
	if _, err := e.races.DeleteRace(ctx, racesv1.DeleteRaceRequest_builder{Id: raceID}.Build()); err != nil {
		t.Fatalf("DeleteRace: %v", err)
	}
	_, err = e.racePlans.GetRacePlan(ctx, racesv1.GetRacePlanRequest_builder{Id: planID}.Build())
	wantCode(t, err, connect.CodeNotFound)
}

func TestRacePlanSections(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	race, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
		Race: racesv1.Race_builder{Name: "Race", Date: "2027-06-12", DistanceMeters: 30000, OnCalendar: true}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.racePlans.CreateRacePlan(ctx, racesv1.CreateRacePlanRequest_builder{RaceId: race.GetRace().GetId()}.Build())
	if err != nil {
		t.Fatal(err)
	}
	planID := created.GetPlan().GetId()

	// Every section with every field set, and lists out of any natural order.
	sections := racesv1.RacePlan_builder{
		Goals: racesv1.RaceGoals_builder{AFinishSeconds: 10000, BFinishSeconds: 11000, CFinishSeconds: 12000, Notes: "Enjoy it"}.Build(),
		Checkpoints: []*racesv1.Checkpoint{
			racesv1.Checkpoint_builder{
				Name:                  "CP1",
				DistanceMeters:        10000,
				ElevationGainMeters:   500,
				ElevationLossMeters:   200,
				CutoffSeconds:         7200,
				CrewAccess:            true,
				DropBag:               true,
				Aid:                   "Water, gels",
				PlannedSegmentSeconds: 3600,
				PlannedStopSeconds:    120,
				SegmentStrategy:       "Hike the climb",
				Intake: []*racesv1.Intake{
					racesv1.Intake_builder{Product: "Gel", Quantity: 2}.Build(),
					racesv1.Intake_builder{Product: "Drink", Quantity: 0.5}.Build(),
				},
				Tasks:            "Refill flasks",
				CrewInstructions: "Have a fresh vest ready",
				DropBagContents:  "Socks",
			}.Build(),
			racesv1.Checkpoint_builder{Name: "CP2", DistanceMeters: 20000, PlannedSegmentSeconds: 3600}.Build(),
			racesv1.Checkpoint_builder{
				Name:                  "Finish",
				DistanceMeters:        30000,
				PlannedSegmentSeconds: 3600,
				Intake:                []*racesv1.Intake{racesv1.Intake_builder{Product: "Gel", Quantity: 3}.Build()},
			}.Build(),
		},
		Pacing: racesv1.PacingStrategy_builder{Strategy: "Even effort", HikeGradePercent: 12.5, HeartRateCapBpm: 150, Notes: "RPE 6"}.Build(),
		Nutrition: racesv1.NutritionStrategy_builder{
			CarbsGramsPerHour: 90,
			FluidMlPerHour:    600,
			SodiumMgPerHour:   700,
			Products: []*racesv1.NutritionProduct{
				racesv1.NutritionProduct_builder{Name: "Gel", CarbsGrams: 25, SodiumMg: 50, CaffeineMg: 75}.Build(),
				racesv1.NutritionProduct_builder{Name: "Drink", CarbsGrams: 40, SodiumMg: 500, FluidMl: 500}.Build(),
			},
			Notes: "Caffeine after dark",
		}.Build(),
		Gear: []*racesv1.GearItem{
			racesv1.GearItem_builder{Name: "Vest", Location: "worn", Packed: true}.Build(),
			racesv1.GearItem_builder{Name: "Jacket", Mandatory: true, Location: "vest", Notes: "Taped seams"}.Build(),
			racesv1.GearItem_builder{Name: "Headlamp", Mandatory: true, Location: "drop bag CP1"}.Build(),
		},
		Crew: racesv1.CrewPlan_builder{HasCrew: true, Members: "Sam", Notes: "Drive to CP1"}.Build(),
		Contingencies: []*racesv1.Contingency{
			racesv1.Contingency_builder{Scenario: racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_WEATHER, Trigger: "Storm", Response: "Wait"}.Build(),
			racesv1.Contingency_builder{Scenario: racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_GI_DISTRESS, Trigger: "Nausea", Response: "Slow down"}.Build(),
			racesv1.Contingency_builder{Scenario: racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_OTHER}.Build(),
		},
		RaceMorning: []*racesv1.TimelineItem{
			racesv1.TimelineItem_builder{Time: "04:00", Description: "Wake up"}.Build(),
			racesv1.TimelineItem_builder{Time: "04:15", Description: "Breakfast"}.Build(),
		},
		Mental: racesv1.MentalStrategy_builder{Mantras: []string{"Relentless forward progress", "This too shall pass"}, Notes: "One checkpoint at a time"}.Build(),
		Notes:  "Have fun",
	}.Build()
	request := proto.CloneOf(sections)
	request.SetId(planID)
	if _, err := e.racePlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{Plan: request}.Build()); err != nil {
		t.Fatalf("UpdateRacePlan: %v", err)
	}
	got, err := e.racePlans.GetRacePlan(ctx, racesv1.GetRacePlanRequest_builder{Id: planID}.Build())
	if err != nil {
		t.Fatal(err)
	}
	stored := proto.CloneOf(got.GetPlan())
	for _, field := range []string{"id", "race_id", "share_url", "create_time", "update_time", "race_day"} {
		stored.ProtoReflect().Clear(stored.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(field)))
	}
	// The server gives each checkpoint an ID.
	for _, cp := range stored.GetCheckpoints() {
		if cp.GetId() == "" {
			t.Errorf("checkpoint %q has no ID", cp.GetName())
		}
		cp.SetId("")
	}
	if !proto.Equal(stored, sections) {
		t.Errorf("sections didn't round trip:\ngot  %v\nwant %v", stored, sections)
	}

	// Shrinking the checkpoints removes the intakes of the removed
	// checkpoints, and updating them replaces the intakes of the others.
	_, err = e.racePlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{
		Plan: racesv1.RacePlan_builder{
			Id: planID,
			Checkpoints: []*racesv1.Checkpoint{
				racesv1.Checkpoint_builder{
					Name:           "Finish",
					DistanceMeters: 30000,
					Intake:         []*racesv1.Intake{racesv1.Intake_builder{Product: "Bar", Quantity: 1}.Build()},
				}.Build(),
			},
		}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"checkpoints"}},
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	var checkpoints, intakes int
	if err := e.db.QueryRow(ctx, `select (select count(*) from race_plan_checkpoints), (select count(*) from race_plan_intakes)`).Scan(&checkpoints, &intakes); err != nil {
		t.Fatal(err)
	}
	if checkpoints != 1 || intakes != 1 {
		t.Errorf("got %d checkpoints and %d intakes, want 1 and 1", checkpoints, intakes)
	}
	got, err = e.racePlans.GetRacePlan(ctx, racesv1.GetRacePlanRequest_builder{Id: planID}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if cps := got.GetPlan().GetCheckpoints(); len(cps) != 1 || cps[0].GetName() != "Finish" || len(cps[0].GetIntake()) != 1 || cps[0].GetIntake()[0].GetProduct() != "Bar" {
		t.Errorf("unexpected checkpoints %v", cps)
	}
	// Other sections were left alone.
	if !proto.Equal(got.GetPlan().GetNutrition(), sections.GetNutrition()) || len(got.GetPlan().GetGear()) != 3 {
		t.Errorf("other sections changed: %v", got.GetPlan())
	}

	// Contingencies need a scenario.
	_, err = e.racePlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{
		Plan: racesv1.RacePlan_builder{
			Id:            planID,
			Contingencies: []*racesv1.Contingency{racesv1.Contingency_builder{Trigger: "Lost"}.Build()},
		}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"contingencies"}},
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	// Clearing the sections leaves an empty plan.
	if _, err := e.racePlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{
		Plan: racesv1.RacePlan_builder{Id: planID}.Build(),
	}.Build()); err != nil {
		t.Fatal(err)
	}
	got, err = e.racePlans.GetRacePlan(ctx, racesv1.GetRacePlanRequest_builder{Id: planID}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if p := got.GetPlan(); len(p.GetCheckpoints()) != 0 || len(p.GetNutrition().GetProducts()) != 0 || len(p.GetGear()) != 0 ||
		len(p.GetContingencies()) != 0 || len(p.GetMental().GetMantras()) != 0 || p.GetGoals().GetAFinishSeconds() != 0 || p.GetNotes() != "" {
		t.Errorf("plan wasn't cleared: %v", p)
	}

	// Other users can't update the plan.
	_, err = e.otherPlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{
		Plan:       racesv1.RacePlan_builder{Id: planID, Notes: "Mine now"}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
	}.Build())
	wantCode(t, err, connect.CodeNotFound)
}

func TestMCP(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// Unauthenticated requests are rejected.
	resp, err := e.srv.Client().Post(e.srv.URL+"/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("got status %d, want 401 with a challenge", resp.StatusCode)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   e.srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: &bearerTransport{token: e.token}},
	}, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"CreateRace", "ListRaces", "PreviewRoute", "GetRouteForecast"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing tool %s in %v", want, names)
		}
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "CreateRace",
		Arguments: map[string]any{
			"race": map[string]any{"name": "Agent race", "date": "2027-01-01", "distanceMeters": 50000},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("CreateRace failed: %v", res.Content[0].(*mcp.TextContent).Text)
	}
	// The race was created for the token's user.
	races, err := e.races.ListRaces(ctx, racesv1.ListRacesRequest_builder{}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if len(races.GetRaces()) != 1 || races.GetRaces()[0].GetName() != "Agent race" {
		t.Errorf("unexpected races %v", races.GetRaces())
	}
	var out struct {
		Race struct {
			ID string `json:"id"`
		} `json:"race"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil || out.Race.ID == "" {
		t.Errorf("unexpected tool output %v: %v", res.Content, err)
	}

	// Errors are returned to the agent so it can correct itself.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "CreateRace",
		Arguments: map[string]any{"race": map[string]any{"name": "No date"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "invalid_argument") {
		t.Errorf("expected an invalid argument tool error, got %v", res.Content)
	}
}

type bearerTransport struct {
	token string
}

func (b *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(req)
}
