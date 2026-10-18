// Package race implements the RaceService.
package race

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/races/v1/racesv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/protomask"
	"github.com/johanbrandhorst/trail-tools/internal/races"
	"github.com/johanbrandhorst/trail-tools/internal/routes"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ racesv1connect.RaceServiceHandler = (*Service)(nil)

type RaceRepository interface {
	CreateRace(ctx context.Context, race *races.Race) (*races.Race, error)
	GetRace(ctx context.Context, userID, raceID pgtype.UUID) (*races.Race, error)
	ListRaces(ctx context.Context, userID pgtype.UUID, onCalendarOnly bool) ([]*races.Race, error)
	UpdateRace(ctx context.Context, race *races.Race) (*races.Race, error)
	DeleteRace(ctx context.Context, userID, raceID pgtype.UUID) error
	CreateEvent(ctx context.Context, event *races.Event) (*races.Event, error)
	GetEvent(ctx context.Context, userID, eventID pgtype.UUID) (*races.Event, error)
	ListEvents(ctx context.Context, userID pgtype.UUID) ([]*races.Event, error)
	UpdateEvent(ctx context.Context, event *races.Event) (*races.Event, error)
	DeleteEvent(ctx context.Context, userID, eventID pgtype.UUID) error
}

type RouteRepository interface {
	GetRoute(ctx context.Context, userID, routeID pgtype.UUID) (*routes.Route, error)
}

// Service implements API handlers for the race service.
type Service struct {
	log    *slog.Logger
	report *errreport.Reporter
	races  RaceRepository
	routes RouteRepository
}

// NewService creates a new Service.
func NewService(log *slog.Logger, report *errreport.Reporter, races RaceRepository, routes RouteRepository) *Service {
	return &Service{
		log:    log,
		report: report,
		races:  races,
		routes: routes,
	}
}

func (s *Service) CreateRace(ctx context.Context, req *racesv1.CreateRaceRequest) (*racesv1.CreateRaceResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	race, err := s.raceFromProto(ctx, userID, req.GetRace())
	if err != nil {
		return nil, err
	}
	created, err := s.races.CreateRace(ctx, race)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to create race")
	}
	return racesv1.CreateRaceResponse_builder{Race: RaceToProto(created)}.Build(), nil
}

func (s *Service) GetRace(ctx context.Context, req *racesv1.GetRaceRequest) (*racesv1.GetRaceResponse, error) {
	race, err := s.getRace(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return racesv1.GetRaceResponse_builder{Race: RaceToProto(race)}.Build(), nil
}

func (s *Service) ListRaces(ctx context.Context, req *racesv1.ListRacesRequest) (*racesv1.ListRacesResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	list, err := s.races.ListRaces(ctx, userID, req.GetOnCalendarOnly())
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list races")
	}
	protoRaces := make([]*racesv1.Race, len(list))
	for i, r := range list {
		protoRaces[i] = RaceToProto(r)
	}
	return racesv1.ListRacesResponse_builder{Races: protoRaces}.Build(), nil
}

func (s *Service) UpdateRace(ctx context.Context, req *racesv1.UpdateRaceRequest) (*racesv1.UpdateRaceResponse, error) {
	existing, err := s.getRace(ctx, req.GetRace().GetId())
	if err != nil {
		return nil, err
	}
	merged := RaceToProto(existing)
	if err := protomask.Apply(merged, req.GetRace(), req.GetUpdateMask(), "id", "create_time", "update_time"); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	race, err := s.raceFromProto(ctx, existing.UserID, merged)
	if err != nil {
		return nil, err
	}
	race.ID = existing.ID
	updated, err := s.races.UpdateRace(ctx, race)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to update race")
	}
	return racesv1.UpdateRaceResponse_builder{Race: RaceToProto(updated)}.Build(), nil
}

func (s *Service) DeleteRace(ctx context.Context, req *racesv1.DeleteRaceRequest) (*racesv1.DeleteRaceResponse, error) {
	race, err := s.getRace(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.races.DeleteRace(ctx, race.UserID, race.ID); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete race")
	}
	return racesv1.DeleteRaceResponse_builder{}.Build(), nil
}

func (s *Service) getRace(ctx context.Context, id string) (*races.Race, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	raceID, err := store.StringToUUID(id)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid race ID: %v", err).WithCause(err)
	}
	race, err := s.races.GetRace(ctx, userID, raceID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get race")
	}
	return race, nil
}

func (s *Service) raceFromProto(ctx context.Context, userID pgtype.UUID, r *racesv1.Race) (*races.Race, error) {
	invalid := func(format string, args ...any) error {
		return connect.Errorf(connect.CodeInvalidArgument, format, args...)
	}
	name := strings.TrimSpace(r.GetName())
	if name == "" {
		return nil, invalid("name is required")
	}
	date, err := store.ParseDate(r.GetDate())
	if err != nil {
		return nil, invalid("date: %v", err)
	}
	startTime := strings.TrimSpace(r.GetStartTime())
	if startTime != "" {
		t, err := time.Parse("15:04", startTime)
		if err != nil {
			return nil, invalid("start_time must be HH:MM")
		}
		startTime = t.Format("15:04")
	}
	if r.GetDistanceMeters() < 0 || r.GetElevationGainMeters() < 0 || r.GetCutoffSeconds() < 0 {
		return nil, invalid("distance, elevation gain and cutoff must not be negative")
	}
	website, err := parseWebsiteURL(r.GetWebsiteUrl())
	if err != nil {
		return nil, invalid("%v", err)
	}
	race := &races.Race{
		UserID:              userID,
		Name:                name,
		Date:                date,
		StartTime:           startTime,
		Location:            r.GetLocation(),
		WebsiteURL:          website,
		DistanceMeters:      r.GetDistanceMeters(),
		ElevationGainMeters: r.GetElevationGainMeters(),
		CutoffSeconds:       r.GetCutoffSeconds(),
		Priority:            priorityFromProto(r.GetPriority()),
		Notes:               r.GetNotes(),
		OnCalendar:          r.GetOnCalendar(),
	}
	if r.GetCourseRouteId() != "" {
		if race.CourseRouteID, err = store.StringToUUID(r.GetCourseRouteId()); err != nil {
			return nil, invalid("invalid course_route_id: %v", err)
		}
		if _, err := s.routes.GetRoute(ctx, userID, race.CourseRouteID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, invalid("route %s does not exist", r.GetCourseRouteId())
			}
			return nil, s.toConnectError(ctx, err, "failed to get route")
		}
	}
	if r.GetEventId() != "" {
		if race.EventID, err = store.StringToUUID(r.GetEventId()); err != nil {
			return nil, invalid("invalid event_id: %v", err)
		}
		if _, err := s.races.GetEvent(ctx, userID, race.EventID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, invalid("race event %s does not exist", r.GetEventId())
			}
			return nil, s.toConnectError(ctx, err, "failed to get race event")
		}
	}
	return race, nil
}

func priorityFromProto(p racesv1.RacePriority) races.Priority {
	switch p {
	case racesv1.RacePriority_RACE_PRIORITY_A:
		return races.PriorityA
	case racesv1.RacePriority_RACE_PRIORITY_B:
		return races.PriorityB
	case racesv1.RacePriority_RACE_PRIORITY_C:
		return races.PriorityC
	default:
		return races.PriorityUnset
	}
}

func priorityToProto(p races.Priority) racesv1.RacePriority {
	switch p {
	case races.PriorityA:
		return racesv1.RacePriority_RACE_PRIORITY_A
	case races.PriorityB:
		return racesv1.RacePriority_RACE_PRIORITY_B
	case races.PriorityC:
		return racesv1.RacePriority_RACE_PRIORITY_C
	default:
		return racesv1.RacePriority_RACE_PRIORITY_UNSPECIFIED
	}
}

// RaceToProto converts a race to its API representation.
func RaceToProto(r *races.Race) *racesv1.Race {
	return racesv1.Race_builder{
		Id:                  store.UUIDToString(r.ID),
		Name:                r.Name,
		Date:                store.FormatDate(r.Date),
		StartTime:           r.StartTime,
		Location:            r.Location,
		WebsiteUrl:          websiteURL(r.WebsiteURL),
		DistanceMeters:      r.DistanceMeters,
		ElevationGainMeters: r.ElevationGainMeters,
		CutoffSeconds:       r.CutoffSeconds,
		Priority:            priorityToProto(r.Priority),
		CourseRouteId:       store.UUIDToString(r.CourseRouteID),
		Notes:               r.Notes,
		OnCalendar:          r.OnCalendar,
		EventId:             store.UUIDToString(r.EventID),
		CreateTime:          timestamppb.New(r.CreateTime),
		UpdateTime:          timestamppb.New(r.UpdateTime),
	}.Build()
}

func (s *Service) toConnectError(ctx context.Context, err error, msg string) error {
	if errors.Is(err, store.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, "race not found")
	}
	s.report.Report(ctx, msg, err)
	return connect.NewError(connect.CodeInternal, msg).WithCause(err)
}
