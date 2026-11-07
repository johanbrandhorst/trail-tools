package services_test

import (
	"context"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestRaceEvents(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	_, err := e.races.CreateRaceEvent(ctx, racesv1.CreateRaceEventRequest_builder{
		Event: racesv1.RaceEvent_builder{Name: " "}.Build(),
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	created, err := e.races.CreateRaceEvent(ctx, racesv1.CreateRaceEventRequest_builder{
		Event: racesv1.RaceEvent_builder{Name: "Squamish 50/50", Location: "Squamish, BC"}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRaceEvent: %v", err)
	}
	eventID := created.GetEvent().GetId()
	if created.GetEvent().GetStartDate() != "" || len(created.GetEvent().GetRaceIds()) != 0 {
		t.Errorf("a new event has races: %v", created.GetEvent())
	}

	// Events are private, and races can only join the user's own events.
	_, err = e.otherRaces.GetRaceEvent(ctx, racesv1.GetRaceEventRequest_builder{Id: eventID}.Build())
	wantCode(t, err, connect.CodeNotFound)
	_, err = e.otherRaces.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
		Race: racesv1.Race_builder{Name: "Squamish 50K", Date: "2027-08-15", EventId: eventID}.Build(),
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	// The 50K on Sunday is created first, then the 50 mile on Saturday.
	var raceIDs []string
	for _, race := range []struct{ name, date string }{
		{"Squamish 50K", "2027-08-15"},
		{"Squamish 50 Mile", "2027-08-14"},
	} {
		resp, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
			Race: racesv1.Race_builder{Name: race.name, Date: race.date, EventId: eventID}.Build(),
		}.Build())
		if err != nil {
			t.Fatalf("CreateRace: %v", err)
		}
		if resp.GetRace().GetEventId() != eventID {
			t.Errorf("race event is %q, want %q", resp.GetRace().GetEventId(), eventID)
		}
		raceIDs = append(raceIDs, resp.GetRace().GetId())
	}
	got, err := e.races.GetRaceEvent(ctx, racesv1.GetRaceEventRequest_builder{Id: eventID}.Build())
	if err != nil {
		t.Fatalf("GetRaceEvent: %v", err)
	}
	if want := []string{raceIDs[1], raceIDs[0]}; !slices.Equal(got.GetEvent().GetRaceIds(), want) {
		t.Errorf("race IDs are %v, want %v in date order", got.GetEvent().GetRaceIds(), want)
	}
	if got.GetEvent().GetStartDate() != "2027-08-14" || got.GetEvent().GetEndDate() != "2027-08-15" {
		t.Errorf("event runs %s to %s", got.GetEvent().GetStartDate(), got.GetEvent().GetEndDate())
	}

	// Events are listed by their first race, events without races last.
	if _, err := e.races.CreateRaceEvent(ctx, racesv1.CreateRaceEventRequest_builder{
		Event: racesv1.RaceEvent_builder{Name: "A festival"}.Build(),
	}.Build()); err != nil {
		t.Fatalf("CreateRaceEvent: %v", err)
	}
	earlier, err := e.races.CreateRaceEvent(ctx, racesv1.CreateRaceEventRequest_builder{
		Event: racesv1.RaceEvent_builder{Name: "Zegama weekend"}.Build(),
	}.Build())
	if err != nil {
		t.Fatalf("CreateRaceEvent: %v", err)
	}
	if _, err := e.races.CreateRace(ctx, racesv1.CreateRaceRequest_builder{
		Race: racesv1.Race_builder{Name: "Zegama", Date: "2027-05-23", EventId: earlier.GetEvent().GetId()}.Build(),
	}.Build()); err != nil {
		t.Fatalf("CreateRace: %v", err)
	}
	list, err := e.races.ListRaceEvents(ctx, racesv1.ListRaceEventsRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("ListRaceEvents: %v", err)
	}
	var names []string
	for _, event := range list.GetEvents() {
		names = append(names, event.GetName())
	}
	if want := []string{"Zegama weekend", "Squamish 50/50", "A festival"}; !slices.Equal(names, want) {
		t.Errorf("events are %v, want %v", names, want)
	}

	updated, err := e.races.UpdateRaceEvent(ctx, racesv1.UpdateRaceEventRequest_builder{
		Event:      racesv1.RaceEvent_builder{Id: eventID, WebsiteUrl: "https://squamish50.com", Name: "ignored"}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"website_url"}},
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRaceEvent: %v", err)
	}
	if updated.GetEvent().GetName() != "Squamish 50/50" || updated.GetEvent().GetWebsiteUrl() != "https://squamish50.com" {
		t.Errorf("unexpected event %v", updated.GetEvent())
	}
	if len(updated.GetEvent().GetRaceIds()) != 2 {
		t.Errorf("updated event has %d races, want 2", len(updated.GetEvent().GetRaceIds()))
	}

	// A race can leave its event.
	left, err := e.races.UpdateRace(ctx, racesv1.UpdateRaceRequest_builder{
		Race:       racesv1.Race_builder{Id: raceIDs[0]}.Build(),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"event_id"}},
	}.Build())
	if err != nil {
		t.Fatalf("UpdateRace: %v", err)
	}
	if left.GetRace().GetEventId() != "" {
		t.Errorf("race is still in event %q", left.GetRace().GetEventId())
	}

	// Deleting an event keeps its races.
	if _, err := e.races.DeleteRaceEvent(ctx, racesv1.DeleteRaceEventRequest_builder{Id: eventID}.Build()); err != nil {
		t.Fatalf("DeleteRaceEvent: %v", err)
	}
	_, err = e.races.GetRaceEvent(ctx, racesv1.GetRaceEventRequest_builder{Id: eventID}.Build())
	wantCode(t, err, connect.CodeNotFound)
	race, err := e.races.GetRace(ctx, racesv1.GetRaceRequest_builder{Id: raceIDs[1]}.Build())
	if err != nil {
		t.Fatalf("GetRace: %v", err)
	}
	if race.GetRace().GetEventId() != "" {
		t.Errorf("race is in deleted event %q", race.GetRace().GetEventId())
	}
}
