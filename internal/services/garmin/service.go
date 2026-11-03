package garmin

import (
	"context"
	"errors"
	"slices"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/garmin"
	garminv1 "github.com/johanbrandhorst/trail-tools/internal/gen/garmin/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/garmin/v1/garminv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ garminv1connect.GarminServiceHandler = (*Service)(nil)

// StatusRepository reads the status of users' Garmin connections.
type StatusRepository interface {
	GetGarminConnection(ctx context.Context, userID pgtype.UUID) (*activities.GarminConnection, error)
	CountGarminStatus(ctx context.Context, userID pgtype.UUID) (*activities.GarminCounts, error)
}

// syncTimeout bounds a sync the user asked for, which puts at most two
// weeks of workouts on the calendar and downloads a few files.
const syncTimeout = 45 * time.Second

// Service implements the GarminService. Without Garmin API credentials,
// the syncer is nil, and it only reports that Garmin isn't configured.
type Service struct {
	report *errreport.Reporter
	repo   StatusRepository
	syncer *Syncer
}

// NewService creates a Service. syncer is nil if Garmin isn't configured.
func NewService(report *errreport.Reporter, repo StatusRepository, syncer *Syncer) *Service {
	return &Service{report: report, repo: repo, syncer: syncer}
}

func (s *Service) GetGarminConnection(ctx context.Context, req *garminv1.GetGarminConnectionRequest) (*garminv1.GetGarminConnectionResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	conn, err := s.connection(ctx, userID)
	if err != nil {
		return nil, err
	}
	return garminv1.GetGarminConnectionResponse_builder{Connection: conn}.Build(), nil
}

func (s *Service) SyncGarmin(ctx context.Context, req *garminv1.SyncGarminRequest) (*garminv1.SyncGarminResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if s.syncer == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the server is not configured with Garmin API credentials")
	}
	syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	if err := s.syncer.SyncUser(syncCtx, userID); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeFailedPrecondition, "no Garmin account is connected; connect it in the browser at /garmin/connect")
		case errors.Is(err, garmin.ErrUnauthorized):
			return nil, connect.NewError(connect.CodeFailedPrecondition, "Garmin rejected the connection; reconnect Garmin in the browser at /garmin/connect")
		case errors.Is(err, garmin.ErrRateLimited):
			return nil, connect.NewError(connect.CodeResourceExhausted, "Garmin's rate limit was reached; the sync is retried within the hour")
		}
		// The sync records what failed in the connection's message, which
		// the response returns, so a partial failure isn't an error.
		s.report.Report(ctx, "Failed to sync Garmin, retrying within the hour", err)
	}
	conn, err := s.connection(ctx, userID)
	if err != nil {
		return nil, err
	}
	return garminv1.SyncGarminResponse_builder{Connection: conn}.Build(), nil
}

func (s *Service) DisconnectGarmin(ctx context.Context, req *garminv1.DisconnectGarminRequest) (*garminv1.DisconnectGarminResponse, error) {
	// Connections are credentials, so only the web app's session may remove
	// them.
	user, err := authn.GetSessionUser(ctx)
	if err != nil {
		return nil, err
	}
	if s.syncer == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the server is not configured with Garmin API credentials")
	}
	if err := s.syncer.Disconnect(ctx, user.ID()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, "no Garmin account is connected")
		}
		s.report.Report(ctx, "Failed to disconnect Garmin", err)
		return nil, connect.NewError(connect.CodeInternal, "failed to disconnect Garmin; try again").WithCause(err)
	}
	return &garminv1.DisconnectGarminResponse{}, nil
}

// connection returns the status of the user's connection.
func (s *Service) connection(ctx context.Context, userID pgtype.UUID) (*garminv1.GarminConnection, error) {
	if s.syncer == nil {
		return garminv1.GarminConnection_builder{}.Build(), nil
	}
	conn, err := s.repo.GetGarminConnection(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return garminv1.GarminConnection_builder{Configured: true}.Build(), nil
	}
	if err != nil {
		s.report.Report(ctx, "Failed to get Garmin connection", err)
		return nil, connect.NewError(connect.CodeInternal, "failed to get the Garmin connection").WithCause(err)
	}
	counts, err := s.repo.CountGarminStatus(ctx, userID)
	if err != nil {
		s.report.Report(ctx, "Failed to count Garmin files and workouts", err)
		return nil, connect.NewError(connect.CodeInternal, "failed to get the Garmin connection").WithCause(err)
	}
	b := garminv1.GarminConnection_builder{
		Connected:         true,
		Configured:        true,
		ActivitiesAllowed: slices.Contains(conn.Permissions, garmin.PermissionActivityExport),
		WorkoutsAllowed:   slices.Contains(conn.Permissions, garmin.PermissionWorkoutImport),
		SyncMessage:       conn.SyncMessage,
		CoursesAllowed:    slices.Contains(conn.Permissions, garmin.PermissionCourseImport),
		WorkoutCount:      int32(counts.Workouts),
		PendingFileCount:  int32(counts.PendingFiles),
		CourseCount:       int32(counts.Courses),
	}
	if !conn.WorkoutsSyncTime.IsZero() {
		b.WorkoutsSyncTime = timestamppb.New(conn.WorkoutsSyncTime)
	}
	return b.Build(), nil
}

func (s *Service) SendRouteToGarmin(ctx context.Context, req *garminv1.SendRouteToGarminRequest) (*garminv1.SendRouteToGarminResponse, error) {
	status, err := s.routeAction(ctx, req.GetRouteId(), s.syncer.SendRoute)
	if err != nil {
		return nil, err
	}
	return garminv1.SendRouteToGarminResponse_builder{Status: status}.Build(), nil
}

func (s *Service) RemoveRouteFromGarmin(ctx context.Context, req *garminv1.RemoveRouteFromGarminRequest) (*garminv1.RemoveRouteFromGarminResponse, error) {
	status, err := s.routeAction(ctx, req.GetRouteId(), s.syncer.RemoveRoute)
	if err != nil {
		return nil, err
	}
	return garminv1.RemoveRouteFromGarminResponse_builder{Status: status}.Build(), nil
}

func (s *Service) GetRouteGarminStatus(ctx context.Context, req *garminv1.GetRouteGarminStatusRequest) (*garminv1.GetRouteGarminStatusResponse, error) {
	status, err := s.routeAction(ctx, req.GetRouteId(), nil)
	if err != nil {
		return nil, err
	}
	return garminv1.GetRouteGarminStatusResponse_builder{Status: status}.Build(), nil
}

// routeAction does something to a route's course on Garmin, if action is
// set, and returns the route's status after.
func (s *Service) routeAction(ctx context.Context, id string, action func(ctx context.Context, userID, routeID pgtype.UUID) error) (*garminv1.RouteGarminStatus, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	routeID, err := store.StringToUUID(id)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid route ID: %v", err)
	}
	if s.syncer == nil {
		if action == nil {
			return garminv1.RouteGarminStatus_builder{}.Build(), nil
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the server is not configured with Garmin API credentials")
	}
	if action != nil {
		if err := action(ctx, userID, routeID); err != nil {
			switch {
			case errors.Is(err, ErrNotConnected):
				return nil, connect.NewError(connect.CodeFailedPrecondition, "no Garmin account is connected; connect it in the browser at /garmin/connect")
			case errors.Is(err, ErrCoursesNotAllowed):
				return nil, connect.NewError(connect.CodeFailedPrecondition, "Garmin doesn't allow putting courses on the account; allow it in Garmin Connect, or reconnect Garmin")
			case errors.Is(err, errCourseTooLong):
				return nil, connect.NewError(connect.CodeInvalidArgument, "the route has too many points for a Garmin course; split it into shorter routes")
			case errors.Is(err, store.ErrNotFound):
				return nil, connect.NewError(connect.CodeNotFound, "route not found")
			case errors.Is(err, garmin.ErrUnauthorized):
				return nil, connect.NewError(connect.CodeFailedPrecondition, "Garmin rejected the connection; reconnect Garmin in the browser at /garmin/connect")
			case errors.Is(err, garmin.ErrRateLimited):
				return nil, connect.NewError(connect.CodeResourceExhausted, "Garmin's rate limit was reached; try again later")
			}
			s.report.Report(ctx, "Failed to change a route on Garmin", err)
			return nil, connect.NewError(connect.CodeInternal, "failed to change the route on Garmin; try again").WithCause(err)
		}
	}
	status, err := s.syncer.RouteStatus(ctx, userID, routeID)
	if err != nil {
		s.report.Report(ctx, "Failed to get a route's Garmin status", err)
		return nil, connect.NewError(connect.CodeInternal, "failed to get the route's Garmin status").WithCause(err)
	}
	return garminv1.RouteGarminStatus_builder{OnGarmin: status.OnGarmin, Sent: status.Sent}.Build(), nil
}
