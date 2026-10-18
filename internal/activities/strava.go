package activities

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities/internal/gen"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// SyncState is the state of a Strava sync.
type SyncState string

// Sync states.
const (
	SyncStateIdle        SyncState = "idle"
	SyncStateRunning     SyncState = "running"
	SyncStateRateLimited SyncState = "rate_limited"
	SyncStateFailed      SyncState = "failed"
)

// StravaConnection is a user's connection to Strava.
type StravaConnection struct {
	UserID       pgtype.UUID
	AthleteID    int64
	AthleteName  string
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	SyncState    SyncState
	SyncMessage  string
	// When the sync state last changed.
	SyncStateTime time.Time
	// Zero if the user has never synced.
	LastSyncTime time.Time
	// Zero unless the sync is rate limited.
	ResumeTime time.Time
	// Activities are listed newest first. While a listing is unfinished,
	// the activities that started before ListBefore remain to be listed,
	// otherwise it is zero.
	ListBefore time.Time
}

// UpsertStravaConnection creates or replaces the user's Strava connection.
func (r *Repository) UpsertStravaConnection(ctx context.Context, conn *StravaConnection) (*StravaConnection, error) {
	dbConn, err := r.q.UpsertStravaConnection(ctx, &gen.UpsertStravaConnectionParams{
		UserID:       conn.UserID,
		AthleteID:    conn.AthleteID,
		AthleteName:  conn.AthleteName,
		AccessToken:  conn.AccessToken,
		RefreshToken: conn.RefreshToken,
		Expiry:       pgtype.Timestamptz{Time: conn.Expiry, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upsert Strava connection: %w", err)
	}
	return connectionFromDB(dbConn), nil
}

// GetStravaConnection gets the user's Strava connection. It returns
// store.ErrNotFound if the user has not connected Strava.
func (r *Repository) GetStravaConnection(ctx context.Context, userID pgtype.UUID) (*StravaConnection, error) {
	dbConn, err := r.q.GetStravaConnection(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get Strava connection: %w", err)
	}
	return connectionFromDB(dbConn), nil
}

// ListStravaConnections lists all users' Strava connections.
func (r *Repository) ListStravaConnections(ctx context.Context) ([]*StravaConnection, error) {
	dbConns, err := r.q.ListStravaConnections(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list Strava connections: %w", err)
	}
	conns := make([]*StravaConnection, len(dbConns))
	for i, dbConn := range dbConns {
		conns[i] = connectionFromDB(dbConn)
	}
	return conns, nil
}

// ListStravaConnectionsByAthlete lists the connections to a Strava athlete.
func (r *Repository) ListStravaConnectionsByAthlete(ctx context.Context, athleteID int64) ([]*StravaConnection, error) {
	dbConns, err := r.q.ListStravaConnectionsByAthlete(ctx, athleteID)
	if err != nil {
		return nil, fmt.Errorf("failed to list Strava connections: %w", err)
	}
	conns := make([]*StravaConnection, len(dbConns))
	for i, dbConn := range dbConns {
		conns[i] = connectionFromDB(dbConn)
	}
	return conns, nil
}

// UpdateStravaTokens stores refreshed Strava tokens.
func (r *Repository) UpdateStravaTokens(ctx context.Context, userID pgtype.UUID, accessToken, refreshToken string, expiry time.Time) error {
	if err := r.q.UpdateStravaTokens(ctx, &gen.UpdateStravaTokensParams{
		UserID:       userID,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Expiry:       pgtype.Timestamptz{Time: expiry, Valid: true},
	}); err != nil {
		return fmt.Errorf("failed to update Strava tokens: %w", err)
	}
	return nil
}

// UpdateStravaSyncState records the state of the user's sync. lastSyncTime
// and resumeTime may be zero; a zero lastSyncTime leaves it unchanged.
func (r *Repository) UpdateStravaSyncState(ctx context.Context, userID pgtype.UUID, state SyncState, message string, resumeTime, lastSyncTime time.Time) error {
	if err := r.q.UpdateStravaSyncState(ctx, &gen.UpdateStravaSyncStateParams{
		UserID:       userID,
		SyncState:    gen.StravaSyncState(state),
		SyncMessage:  message,
		ResumeTime:   pgtype.Timestamptz{Time: resumeTime, Valid: !resumeTime.IsZero()},
		LastSyncTime: pgtype.Timestamptz{Time: lastSyncTime, Valid: !lastSyncTime.IsZero()},
	}); err != nil {
		return fmt.Errorf("failed to update Strava sync state: %w", err)
	}
	return nil
}

// DeleteStravaConnection deletes the user's Strava connection.
func (r *Repository) DeleteStravaConnection(ctx context.Context, userID pgtype.UUID) error {
	n, err := r.q.DeleteStravaConnection(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to delete Strava connection: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// DeleteActivityByStravaID deletes one of the user's activities by its
// Strava ID, with its streams and Strava's responses about it. The objects
// are deleted first, so that if that fails the activity is still there to
// delete them again.
func (r *Repository) DeleteActivityByStravaID(ctx context.Context, userID pgtype.UUID, stravaID int64) error {
	keys := make([]string, 0, len(responseNames)+1)
	for _, name := range responseNames {
		keys = append(keys, responseKey(userID, stravaID, name))
	}
	id, err := r.q.GetActivityIDByStravaID(ctx, &gen.GetActivityIDByStravaIDParams{UserID: userID, StravaID: stravaID})
	switch {
	case err == nil:
		keys = append(keys, streamsKey(userID, id))
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("failed to get activity: %w", err)
	}
	if err := r.deleteObjects(ctx, keys); err != nil {
		return fmt.Errorf("failed to delete activity data: %w", err)
	}
	if err := r.q.DeleteActivityByStravaID(ctx, &gen.DeleteActivityByStravaIDParams{UserID: userID, StravaID: stravaID}); err != nil {
		return fmt.Errorf("failed to delete activity: %w", err)
	}
	return nil
}

func connectionFromDB(c *gen.StravaConnection) *StravaConnection {
	return &StravaConnection{
		UserID:        c.UserID,
		AthleteID:     c.AthleteID,
		AthleteName:   c.AthleteName,
		AccessToken:   c.AccessToken,
		RefreshToken:  c.RefreshToken,
		Expiry:        c.Expiry.Time,
		SyncState:     SyncState(c.SyncState),
		SyncMessage:   c.SyncMessage,
		SyncStateTime: c.SyncStateTime.Time,
		LastSyncTime:  c.LastSyncTime.Time,
		ResumeTime:    c.ResumeTime.Time,
		ListBefore:    c.ListBefore.Time,
	}
}
