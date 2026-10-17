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
	// When a rate limited sync resumes, or a failed one is retried. Zero
	// unless the sync is rate limited or failed; a failed sync with a zero
	// ResumeTime waits for Strava to be reconnected.
	ResumeTime time.Time
	// The number of syncs that failed in a row.
	SyncFailures int32
	// When the latest sync started, or zero if it never synced.
	SyncStartTime time.Time
	// Activities are listed newest first. While a listing is unfinished,
	// the activities that started before ListBefore remain to be listed,
	// otherwise it is zero.
	ListBefore time.Time
}

// UpsertStravaConnection creates or replaces the user's Strava connection.
// Replacing it resets its sync state, so a sync that failed, e.g. as the
// athlete revoked access, is retried.
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

// ListStravaConnections lists all users' Strava connections, those whose
// latest sync started longest ago first.
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

// StravaTokens are the OAuth tokens of a Strava connection.
type StravaTokens struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
}

// RefreshStravaTokens refreshes the tokens of the user's Strava connection
// one at a time: it locks the connection, calls refresh with it as stored,
// and stores the tokens refresh returns, if any, before unlocking it. Strava
// replaces refresh tokens, so concurrent refreshes, including those of other
// servers, must use the latest one rather than the one they read before. It
// returns the connection with its current tokens, or store.ErrNotFound if
// the user disconnected Strava. The connection's other updates wait while it
// is locked, so refresh should be quick.
func (r *Repository) RefreshStravaTokens(ctx context.Context, userID pgtype.UUID, refresh func(stored *StravaConnection) (*StravaTokens, error)) (*StravaConnection, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := r.q.WithTx(tx)
	dbConn, err := q.GetStravaConnectionForUpdate(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get Strava connection: %w", err)
	}
	conn := connectionFromDB(dbConn)
	tokens, err := refresh(conn)
	if err != nil {
		return nil, err
	}
	if tokens == nil {
		// Nothing to store. Rolling back unlocks the connection.
		return conn, nil
	}
	if err := q.UpdateStravaTokens(ctx, &gen.UpdateStravaTokensParams{
		UserID:       userID,
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		Expiry:       pgtype.Timestamptz{Time: tokens.Expiry, Valid: true},
	}); err != nil {
		return nil, fmt.Errorf("failed to update Strava tokens: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit Strava tokens: %w", err)
	}
	conn.AccessToken, conn.RefreshToken, conn.Expiry = tokens.AccessToken, tokens.RefreshToken, tokens.Expiry
	return conn, nil
}

// StartStravaSync records that a sync of the user's activities started.
func (r *Repository) StartStravaSync(ctx context.Context, userID pgtype.UUID, message string) error {
	if err := r.q.StartStravaSync(ctx, &gen.StartStravaSyncParams{UserID: userID, SyncMessage: message}); err != nil {
		return fmt.Errorf("failed to update Strava sync state: %w", err)
	}
	return nil
}

// FinishStravaSync records that the user's sync finished at lastSyncTime,
// which resets its failures.
func (r *Repository) FinishStravaSync(ctx context.Context, userID pgtype.UUID, message string, lastSyncTime time.Time) error {
	if err := r.q.FinishStravaSync(ctx, &gen.FinishStravaSyncParams{
		UserID:       userID,
		SyncMessage:  message,
		LastSyncTime: pgtype.Timestamptz{Time: lastSyncTime, Valid: true},
	}); err != nil {
		return fmt.Errorf("failed to update Strava sync state: %w", err)
	}
	return nil
}

// FailStravaSync records that the user's sync failed, after failures syncs
// in a row, and is retried at retryTime, or once Strava is reconnected if
// retryTime is zero.
func (r *Repository) FailStravaSync(ctx context.Context, userID pgtype.UUID, message string, failures int32, retryTime time.Time) error {
	if err := r.q.FailStravaSync(ctx, &gen.FailStravaSyncParams{
		UserID:       userID,
		SyncMessage:  message,
		SyncFailures: failures,
		ResumeTime:   pgtype.Timestamptz{Time: retryTime, Valid: !retryTime.IsZero()},
	}); err != nil {
		return fmt.Errorf("failed to update Strava sync state: %w", err)
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
// Strava ID, if it exists.
func (r *Repository) DeleteActivityByStravaID(ctx context.Context, userID pgtype.UUID, stravaID int64) error {
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
		SyncFailures:  c.SyncFailures,
		SyncStartTime: c.SyncStartTime.Time,
		ListBefore:    c.ListBefore.Time,
	}
}
