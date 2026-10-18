package race

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/protomask"
	"github.com/johanbrandhorst/trail-tools/internal/races"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Service) CreateRaceEvent(ctx context.Context, req *racesv1.CreateRaceEventRequest) (*racesv1.CreateRaceEventResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	event, err := eventFromProto(userID, req.GetEvent())
	if err != nil {
		return nil, err
	}
	created, err := s.races.CreateEvent(ctx, event)
	if err != nil {
		return nil, s.toEventConnectError(ctx, err, "failed to create race event")
	}
	return racesv1.CreateRaceEventResponse_builder{Event: eventToProto(created, nil)}.Build(), nil
}

func (s *Service) GetRaceEvent(ctx context.Context, req *racesv1.GetRaceEventRequest) (*racesv1.GetRaceEventResponse, error) {
	event, err := s.getEvent(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	eventRaces, err := s.eventRaces(ctx, event.UserID)
	if err != nil {
		return nil, err
	}
	return racesv1.GetRaceEventResponse_builder{
		Event: eventToProto(event, eventRaces[event.ID]),
	}.Build(), nil
}

func (s *Service) ListRaceEvents(ctx context.Context, _ *racesv1.ListRaceEventsRequest) (*racesv1.ListRaceEventsResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	events, err := s.races.ListEvents(ctx, userID)
	if err != nil {
		return nil, s.toEventConnectError(ctx, err, "failed to list race events")
	}
	eventRaces, err := s.eventRaces(ctx, userID)
	if err != nil {
		return nil, err
	}
	protoEvents := make([]*racesv1.RaceEvent, len(events))
	for i, event := range events {
		protoEvents[i] = eventToProto(event, eventRaces[event.ID])
	}
	// In the order of their first race, events without races last. The
	// events are listed by name, so the sort keeps ties by name.
	slices.SortStableFunc(protoEvents, func(a, b *racesv1.RaceEvent) int {
		if (a.GetStartDate() == "") != (b.GetStartDate() == "") {
			if a.GetStartDate() == "" {
				return 1
			}
			return -1
		}
		return cmp.Compare(a.GetStartDate(), b.GetStartDate())
	})
	return racesv1.ListRaceEventsResponse_builder{Events: protoEvents}.Build(), nil
}

func (s *Service) UpdateRaceEvent(ctx context.Context, req *racesv1.UpdateRaceEventRequest) (*racesv1.UpdateRaceEventResponse, error) {
	existing, err := s.getEvent(ctx, req.GetEvent().GetId())
	if err != nil {
		return nil, err
	}
	merged := eventToProto(existing, nil)
	if err := protomask.Apply(merged, req.GetEvent(), req.GetUpdateMask(), "id", "race_ids", "start_date", "end_date", "create_time", "update_time"); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	event, err := eventFromProto(existing.UserID, merged)
	if err != nil {
		return nil, err
	}
	event.ID = existing.ID
	updated, err := s.races.UpdateEvent(ctx, event)
	if err != nil {
		return nil, s.toEventConnectError(ctx, err, "failed to update race event")
	}
	eventRaces, err := s.eventRaces(ctx, updated.UserID)
	if err != nil {
		return nil, err
	}
	return racesv1.UpdateRaceEventResponse_builder{
		Event: eventToProto(updated, eventRaces[updated.ID]),
	}.Build(), nil
}

func (s *Service) DeleteRaceEvent(ctx context.Context, req *racesv1.DeleteRaceEventRequest) (*racesv1.DeleteRaceEventResponse, error) {
	event, err := s.getEvent(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.races.DeleteEvent(ctx, event.UserID, event.ID); err != nil {
		return nil, s.toEventConnectError(ctx, err, "failed to delete race event")
	}
	return racesv1.DeleteRaceEventResponse_builder{}.Build(), nil
}

func (s *Service) getEvent(ctx context.Context, id string) (*races.Event, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	eventID, err := store.StringToUUID(id)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid race event ID: %v", err).WithCause(err)
	}
	event, err := s.races.GetEvent(ctx, userID, eventID)
	if err != nil {
		return nil, s.toEventConnectError(ctx, err, "failed to get race event")
	}
	return event, nil
}

// eventRaces returns the user's races by the event they're part of, in date
// order.
func (s *Service) eventRaces(ctx context.Context, userID pgtype.UUID) (map[pgtype.UUID][]*races.Race, error) {
	list, err := s.races.ListRaces(ctx, userID, false)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list races")
	}
	byEvent := map[pgtype.UUID][]*races.Race{}
	for _, race := range list {
		if race.EventID.Valid {
			byEvent[race.EventID] = append(byEvent[race.EventID], race)
		}
	}
	return byEvent, nil
}

func eventFromProto(userID pgtype.UUID, e *racesv1.RaceEvent) (*races.Event, error) {
	name := strings.TrimSpace(e.GetName())
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "name is required")
	}
	website, err := parseWebsiteURL(e.GetWebsiteUrl())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error())
	}
	return &races.Event{
		UserID:     userID,
		Name:       name,
		Location:   strings.TrimSpace(e.GetLocation()),
		WebsiteURL: website,
		Notes:      e.GetNotes(),
	}, nil
}

// eventToProto converts an event and its races, in date order, to its API
// representation.
func eventToProto(e *races.Event, eventRaces []*races.Race) *racesv1.RaceEvent {
	raceIDs := make([]string, len(eventRaces))
	for i, race := range eventRaces {
		raceIDs[i] = store.UUIDToString(race.ID)
	}
	var startDate, endDate string
	if len(eventRaces) > 0 {
		startDate = store.FormatDate(eventRaces[0].Date)
		endDate = store.FormatDate(eventRaces[len(eventRaces)-1].Date)
	}
	return racesv1.RaceEvent_builder{
		Id:         store.UUIDToString(e.ID),
		Name:       e.Name,
		Location:   e.Location,
		WebsiteUrl: websiteURL(e.WebsiteURL),
		Notes:      e.Notes,
		RaceIds:    raceIDs,
		StartDate:  startDate,
		EndDate:    endDate,
		CreateTime: timestamppb.New(e.CreateTime),
		UpdateTime: timestamppb.New(e.UpdateTime),
	}.Build()
}

func (s *Service) toEventConnectError(ctx context.Context, err error, msg string) error {
	if errors.Is(err, store.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, "race event not found")
	}
	return s.toConnectError(ctx, err, msg)
}
