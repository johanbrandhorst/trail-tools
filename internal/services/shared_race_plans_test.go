package services_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// sharePlan creates a race on the calendar with a plan of two checkpoints,
// shares the plan and returns the plan's ID and share token.
func (e *env) sharePlan(race *racesv1.Race, plan *racesv1.RacePlan) (planID, token string) {
	e.t.Helper()
	ctx := context.Background()
	created, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{Race: race}.Build())
	if err != nil {
		e.t.Fatalf("CreateRace: %v", err)
	}
	p, err := e.racePlans.CreateRacePlan(ctx, racesv1.CreateRacePlanRequest_builder{RaceId: created.GetRace().GetId()}.Build())
	if err != nil {
		e.t.Fatalf("CreateRacePlan: %v", err)
	}
	planID = p.GetPlan().GetId()
	plan.SetId(planID)
	plan.SetCheckpoints([]*racesv1.Checkpoint{
		racesv1.Checkpoint_builder{Name: "Half", DistanceMeters: 5000, PlannedSegmentSeconds: 1800, CrewAccess: true}.Build(),
		racesv1.Checkpoint_builder{Name: "Finish", DistanceMeters: 10_000, PlannedSegmentSeconds: 1800}.Build(),
	})
	if _, err := e.racePlans.UpdateRacePlan(ctx, racesv1.UpdateRacePlanRequest_builder{
		Plan:       plan,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"checkpoints", "nutrition", "crew", "notes"}},
	}.Build()); err != nil {
		e.t.Fatalf("UpdateRacePlan: %v", err)
	}
	shared, err := e.racePlans.ShareRacePlan(ctx, racesv1.ShareRacePlanRequest_builder{Id: planID}.Build())
	if err != nil {
		e.t.Fatalf("ShareRacePlan: %v", err)
	}
	token, ok := strings.CutPrefix(shared.GetShareUrl(), "https://trail.example/share/race-plans#")
	if !ok {
		e.t.Fatalf("unexpected share URL %q", shared.GetShareUrl())
	}
	return planID, token
}

func TestSharedRacePlanRace(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// A race in an event, with the runner's own notes, and a plan with what
	// the crew need.
	event, err := e.races.CreateRaceEvent(ctx, racesv1.CreateRaceEventRequest_builder{
		Event: racesv1.RaceEvent_builder{Name: "Ridge Weekend"}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRaceEvent: %v", err)
	}
	_, token := e.sharePlan(racesv1.Race_builder{
		Name:                "Ridge 10K",
		Date:                "2027-06-12",
		StartTime:           "08:00",
		Location:            "Ridge Village",
		WebsiteUrl:          "https://ridge.example/10k",
		DistanceMeters:      10_000,
		ElevationGainMeters: 400,
		CutoffSeconds:       3 * 3600,
		Priority:            racesv1.RacePriority_RACE_PRIORITY_B,
		Notes:               "Only a tune-up; don't tell the coach",
		OnCalendar:          true,
		EventId:             event.GetEvent().GetId(),
	}.Build(), racesv1.RacePlan_builder{
		Nutrition: racesv1.NutritionStrategy_builder{CarbsGramsPerHour: 80, Notes: "Gel every 30 minutes"}.Build(),
		Crew:      racesv1.CrewPlan_builder{HasCrew: true, Members: "Sam", Notes: "Meet me at Half with a fresh bottle"}.Build(),
		Notes:     "Text Sam at the start",
	}.Build())

	// The crew see the race facts and the whole plan, nutrition and crew
	// notes included...
	view, err := e.anonPlans.GetSharedRacePlan(ctx, racesv1.GetSharedRacePlanRequest_builder{Token: token}.Build())
	if err != nil {
		t.Fatalf("GetSharedRacePlan: %v", err)
	}
	race := view.GetRace()
	if race.GetName() != "Ridge 10K" || race.GetDate() != "2027-06-12" || race.GetStartTime() != "08:00" ||
		race.GetLocation() != "Ridge Village" || race.GetWebsiteUrl() != "https://ridge.example/10k" ||
		race.GetDistanceMeters() != 10_000 || race.GetElevationGainMeters() != 400 || race.GetCutoffSeconds() != 3*3600 ||
		race.GetPriority() != racesv1.RacePriority_RACE_PRIORITY_B {
		t.Errorf("shared race is missing what crew need: %v", race)
	}
	plan := view.GetPlan()
	if plan.GetNutrition().GetNotes() != "Gel every 30 minutes" || plan.GetCrew().GetNotes() != "Meet me at Half with a fresh bottle" ||
		plan.GetNotes() != "Text Sam at the start" || len(plan.GetCheckpoints()) != 2 {
		t.Errorf("shared plan is missing what crew need: %v", plan)
	}
	// ...but not the runner's own race notes, or IDs and calendar state
	// that only mean something to the runner.
	if race.GetNotes() != "" || race.GetId() != "" || race.GetEventId() != "" || race.GetCourseRouteId() != "" ||
		race.GetOnCalendar() || race.HasCreateTime() || race.HasUpdateTime() {
		t.Errorf("shared race exposes the owner's fields: %v", race)
	}
}

func TestRaceWebsiteURLs(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// Only http and https links are allowed, as the website is a link on
	// shared pages.
	for _, url := range []string{
		"javascript:alert(document.cookie)",
		"JavaScript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"ftp://ridge.example/",
		"ridge.example",
		"https://",
		"https://ridge.example/" + strings.Repeat("a", 2048),
	} {
		_, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
			Race: racesv1.Race_builder{Name: "Race", Date: "2027-06-12", WebsiteUrl: url}.Build(),
		}.Build())
		wantCode(t, err, connect.CodeInvalidArgument)
		_, err = e.races.CreateRaceEvent(ctx, racesv1.CreateRaceEventRequest_builder{
			Event: racesv1.RaceEvent_builder{Name: "Event", WebsiteUrl: url}.Build(),
		}.Build())
		wantCode(t, err, connect.CodeInvalidArgument)
	}

	// Real links are kept, trimmed.
	for _, url := range []string{"https://ridge.example/10k?year=2027", "http://ridge.example"} {
		created, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
			Race: racesv1.Race_builder{Name: "Race", Date: "2027-06-12", WebsiteUrl: " " + url + " "}.Build(),
		}.Build())
		if err != nil {
			t.Fatalf("CreateRace with %q: %v", url, err)
		}
		if got := created.GetRace().GetWebsiteUrl(); got != url {
			t.Errorf("got website %q, want %q", got, url)
		}
		event, err := e.races.CreateRaceEvent(ctx, racesv1.CreateRaceEventRequest_builder{
			Event: racesv1.RaceEvent_builder{Name: "Event", WebsiteUrl: url}.Build(),
		}.Build())
		if err != nil {
			t.Fatalf("CreateRaceEvent with %q: %v", url, err)
		}
		if got := event.GetEvent().GetWebsiteUrl(); got != url {
			t.Errorf("got event website %q, want %q", got, url)
		}
	}

	// Updating a website is validated too.
	created, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
		Race: racesv1.Race_builder{Name: "Race", Date: "2027-06-12"}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRace: %v", err)
	}
	_, err = e.races.UpdateRace(ctx, racesv1.UpdateRaceRequest_builder{
		Race:       racesv1.Race_builder{Id: created.GetRace().GetId(), WebsiteUrl: "javascript:alert(1)"}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"website_url"}},
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestRaceDayInputLimits(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	_, token := e.sharePlan(
		racesv1.Race_builder{Name: "Ridge 10K", Date: "2027-06-12", DistanceMeters: 10_000, OnCalendar: true}.Build(),
		racesv1.RacePlan_builder{}.Build(),
	)
	updateRaceDay := func(raceDay *racesv1.RaceDay, paths ...string) error {
		_, err := e.anonPlans.UpdateRaceDay(ctx, racesv1.UpdateRaceDayRequest_builder{
			ShareToken: new(token),
			RaceDay:    raceDay,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
		}.Build())
		return err
	}

	// Crew with the share link can't store arbitrarily long LiveTrack links
	// or tokens.
	long := "https://livetrack.garmin.com/session/aaaaaaaa-bbbb/token/" + strings.Repeat("A", 600)
	err := updateRaceDay(racesv1.RaceDay_builder{LivetrackUrl: long}.Build(), "livetrack_url")
	wantCode(t, err, connect.CodeInvalidArgument)
	longToken := "https://livetrack.garmin.com/session/aaaaaaaa-bbbb/token/" + strings.Repeat("A", 200)
	err = updateRaceDay(racesv1.RaceDay_builder{LivetrackUrl: longToken}.Build(), "livetrack_url")
	wantCode(t, err, connect.CodeInvalidArgument)

	// Times out of range, or not valid at all, are invalid arguments rather
	// than internal errors.
	bad := map[string]*timestamppb.Timestamp{
		"year 10000":    {Seconds: 253402300800},
		"beyond the db": {Seconds: 10_000_000_000_000},
		"far past":      {Seconds: -62135596801},
		"year 1900":     timestamppb.New(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)),
		"invalid nanos": {Seconds: 1_800_000_000, Nanos: -1},
	}
	for name, ts := range bad {
		err := updateRaceDay(racesv1.RaceDay_builder{StartTime: ts}.Build(), "start_time")
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("start_time %s: got %v, want InvalidArgument", name, err)
		}
		err = updateRaceDay(racesv1.RaceDay_builder{Splits: []*racesv1.CheckpointSplit{
			racesv1.CheckpointSplit_builder{CheckpointIndex: 0, ArrivalTime: ts}.Build(),
		}}.Build(), "splits")
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("splits arrival_time %s: got %v, want InvalidArgument", name, err)
		}
		_, err = e.anonPlans.SetCheckpointSplit(ctx, racesv1.SetCheckpointSplitRequest_builder{
			ShareToken: new(token),
			Split:      racesv1.CheckpointSplit_builder{CheckpointIndex: 0, DepartureTime: ts}.Build(),
		}.Build())
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("SetCheckpointSplit departure_time %s: got %v, want InvalidArgument", name, err)
		}
	}

	// Real times are still recorded.
	start := time.Date(2027, 6, 12, 8, 0, 0, 0, time.UTC)
	if err := updateRaceDay(racesv1.RaceDay_builder{StartTime: timestamppb.New(start)}.Build(), "start_time"); err != nil {
		t.Fatalf("UpdateRaceDay: %v", err)
	}
	split, err := e.anonPlans.SetCheckpointSplit(ctx, racesv1.SetCheckpointSplitRequest_builder{
		ShareToken: new(token),
		Split:      racesv1.CheckpointSplit_builder{CheckpointIndex: 0, ArrivalTime: timestamppb.New(start.Add(30 * time.Minute))}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("SetCheckpointSplit: %v", err)
	}
	if got := split.GetStatus().GetStartTime().AsTime(); !got.Equal(start) {
		t.Errorf("got start time %v, want %v", got, start)
	}
}
