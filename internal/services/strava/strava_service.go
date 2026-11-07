// Package strava implements the StravaService, the Strava OAuth flow and
// the background sync of activities.
package strava

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	stravav1 "github.com/johanbrandhorst/trail-tools/internal/gen/strava/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/strava/v1/stravav1connect"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/strava"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ stravav1connect.StravaServiceHandler = (*Service)(nil)

type ConnectionRepository interface {
	GetStravaConnection(ctx context.Context, userID pgtype.UUID) (*activities.StravaConnection, error)
	UpsertStravaConnection(ctx context.Context, conn *activities.StravaConnection) (*activities.StravaConnection, error)
	DeleteStravaConnection(ctx context.Context, userID pgtype.UUID) error
	CountActivities(ctx context.Context, userID pgtype.UUID, window time.Duration) (total, pendingStreams int64, err error)
}

// Service implements API handlers for the Strava service. If the server is
// not configured with Strava credentials, the syncer and OAuth client are nil.
type Service struct {
	log    *slog.Logger
	report *errreport.Reporter
	repo   ConnectionRepository
	syncer *Syncer
	oauth  *OAuth
	// The URL of Strava's website.
	webURL string
}

// NewService creates a new Service. syncer and oauth may be nil if Strava is
// not configured. webURL is the URL of Strava's website, usually strava.WebURL, for links
// to athletes.
func NewService(log *slog.Logger, report *errreport.Reporter, repo ConnectionRepository, syncer *Syncer, oauth *OAuth, webURL string) *Service {
	return &Service{
		log:    log,
		report: report,
		repo:   repo,
		syncer: syncer,
		oauth:  oauth,
		webURL: webURL,
	}
}

func (s *Service) GetStravaConnection(ctx context.Context, req *stravav1.GetStravaConnectionRequest) (*stravav1.GetStravaConnectionResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	conn, err := s.connection(ctx, userID)
	if err != nil {
		return nil, err
	}
	return stravav1.GetStravaConnectionResponse_builder{Connection: conn}.Build(), nil
}

func (s *Service) SyncStrava(ctx context.Context, req *stravav1.SyncStravaRequest) (*stravav1.SyncStravaResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if s.syncer == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the server is not configured with Strava API credentials")
	}
	if _, err := s.repo.GetStravaConnection(ctx, userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, "no Strava account is connected; connect one in the browser at /strava/connect")
		}
		return nil, s.internalError(ctx, "failed to get Strava connection", err)
	}
	if err := s.syncer.Request(ctx, userID, req.GetFull()); err != nil {
		return nil, s.internalError(ctx, "failed to start the sync", err)
	}
	conn, err := s.connection(ctx, userID)
	if err != nil {
		return nil, err
	}
	return stravav1.SyncStravaResponse_builder{Connection: conn}.Build(), nil
}

// activitySyncBudget is how long SyncActivity syncs an activity for, before
// leaving it to the backlog, so the page asking for it isn't kept waiting.
const activitySyncBudget = 20 * time.Second

func (s *Service) SyncActivity(ctx context.Context, req *stravav1.SyncActivityRequest) (*stravav1.SyncActivityResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	activityID, err := store.StringToUUID(req.GetActivityId())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid activity ID: %v", err).WithCause(err)
	}
	if s.syncer == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the server is not configured with Strava API credentials")
	}
	if _, err := s.repo.GetStravaConnection(ctx, userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, "no Strava account is connected")
		}
		return nil, s.internalError(ctx, "failed to get Strava connection", err)
	}
	syncCtx, cancel := context.WithTimeout(ctx, activitySyncBudget)
	defer cancel()
	synced, resumeTime, err := s.syncer.SyncPending(syncCtx, userID, activityID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, connect.NewError(connect.CodeNotFound, "activity not found")
	case errors.Is(err, strava.ErrUnauthorized):
		return nil, connect.NewError(connect.CodeFailedPrecondition, "Strava access was revoked or expired; reconnect Strava")
	case err != nil:
		return nil, s.internalError(ctx, "failed to sync the activity", err)
	}
	resp := stravav1.SyncActivityResponse_builder{Synced: synced}
	if !resumeTime.IsZero() {
		resp.ResumeTime = timestamppb.New(resumeTime)
	}
	return resp.Build(), nil
}

func (s *Service) DisconnectStrava(ctx context.Context, req *stravav1.DisconnectStravaRequest) (*stravav1.DisconnectStravaResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if err := s.repo.DeleteStravaConnection(ctx, userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, "no Strava account is connected")
		}
		return nil, s.internalError(ctx, "failed to disconnect Strava", err)
	}
	return stravav1.DisconnectStravaResponse_builder{}.Build(), nil
}

func (s *Service) connection(ctx context.Context, userID pgtype.UUID) (*stravav1.StravaConnection, error) {
	b := stravav1.StravaConnection_builder{
		Configured: s.oauth != nil,
		SyncState:  stravav1.SyncState_SYNC_STATE_IDLE,
	}
	conn, err := s.repo.GetStravaConnection(ctx, userID)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return nil, s.internalError(ctx, "failed to get Strava connection", err)
	default:
		b.Connected = true
		b.AthleteName = conn.AthleteName
		b.AthleteId = fmt.Sprint(conn.AthleteID)
		b.AthleteUrl = fmt.Sprintf("%s/athletes/%d", s.webURL, conn.AthleteID)
		b.SyncState = syncStateToProto(conn.SyncState)
		b.SyncMessage = conn.SyncMessage
		if !conn.LastSyncTime.IsZero() {
			b.LastSyncTime = timestamppb.New(conn.LastSyncTime)
		}
		if !conn.ResumeTime.IsZero() && conn.SyncState == activities.SyncStateRateLimited {
			b.ResumeTime = timestamppb.New(conn.ResumeTime)
		}
		// A sync recorded as running may have been interrupted, e.g. by a
		// restart or the end of its time budget, and not resumed yet.
		if conn.SyncState == activities.SyncStateRunning && (s.syncer == nil || !s.syncer.Active(conn)) {
			b.SyncState = stravav1.SyncState_SYNC_STATE_IDLE
			b.SyncMessage = "The last sync was interrupted and continues with the next scheduled sync"
			if s.syncer != nil && s.syncer.Background() {
				b.SyncMessage = "The last sync was interrupted and resumes shortly"
			}
		}
	}
	total, pending, err := s.repo.CountActivities(ctx, userID, eagerSyncAge)
	if err != nil {
		return nil, s.internalError(ctx, "failed to count activities", err)
	}
	b.ActivityCount = int32(total)
	b.PendingStreamCount = int32(pending)
	return b.Build(), nil
}

func syncStateToProto(state activities.SyncState) stravav1.SyncState {
	switch state {
	case activities.SyncStateRunning:
		return stravav1.SyncState_SYNC_STATE_RUNNING
	case activities.SyncStateRateLimited:
		return stravav1.SyncState_SYNC_STATE_RATE_LIMITED
	case activities.SyncStateFailed:
		return stravav1.SyncState_SYNC_STATE_FAILED
	default:
		return stravav1.SyncState_SYNC_STATE_IDLE
	}
}

func (s *Service) internalError(ctx context.Context, msg string, err error) error {
	s.report.Report(ctx, msg, err)
	return connect.NewError(connect.CodeInternal, msg).WithCause(err)
}

// timeNow is overridden in tests.
var timeNow = time.Now
