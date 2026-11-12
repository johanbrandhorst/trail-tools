package activities

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/activities/internal/gen"
	"github.com/bs-tools/trailrunn.ing/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
	// Whether the user agreed to keep their activities from Strava if
	// Strava is disconnected. Without it, they're deleted then.
	KeepActivities bool
}

// UpsertStravaConnection creates or replaces the user's Strava connection.
// Replacing it resets its sync state, so a sync that failed, e.g. as the
// athlete revoked access, is retried.
//
// Connecting again stops deleting the user's activities from an earlier
// connection, if they're still being deleted: the user granted access to
// Strava's data again, and the sync fetches them again anyway.
func (r *Repository) UpsertStravaConnection(ctx context.Context, conn *StravaConnection) (*StravaConnection, error) {
	var dbConn *gen.StravaConnection
	var batch store.Batch
	batch.Read(func(db store.Querier) error {
		var err error
		dbConn, err = gen.New(db).UpsertStravaConnection(ctx, &gen.UpsertStravaConnectionParams{
			UserID:       conn.UserID,
			AthleteID:    conn.AthleteID,
			AthleteName:  conn.AthleteName,
			AccessToken:  conn.AccessToken,
			RefreshToken: conn.RefreshToken,
			Expiry:       pgtype.Timestamptz{Time: conn.Expiry, Valid: true},
		})
		return err
	})
	if err := gen.New(&batch).CancelStravaActivityPurge(ctx, conn.UserID); err != nil {
		return nil, err
	}
	if err := batch.Send(ctx, r.db); err != nil {
		return nil, fmt.Errorf("failed to upsert Strava connection: %w", err)
	}
	return connectionFromDB(dbConn), nil
}

// SetStravaKeepActivities records whether the user agrees to keep their
// activities from Strava if Strava is disconnected. It returns
// store.ErrNotFound if Strava isn't connected.
func (r *Repository) SetStravaKeepActivities(ctx context.Context, userID pgtype.UUID, keep bool) error {
	n, err := r.q.SetStravaKeepActivities(ctx, &gen.SetStravaKeepActivitiesParams{UserID: userID, KeepActivities: keep})
	if err != nil {
		return fmt.Errorf("failed to set whether to keep Strava activities: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// DisconnectStrava deletes the user's Strava connection and, unless the
// user agreed to keep their activities from Strava, starts deleting them,
// see PurgeStravaActivities. It returns store.ErrNotFound if Strava isn't
// connected.
func (r *Repository) DisconnectStrava(ctx context.Context, userID pgtype.UUID, keep bool) error {
	var batch store.Batch
	q := gen.New(&batch)
	var deleted int64
	batch.Read(func(db store.Querier) error {
		var err error
		deleted, err = gen.New(db).DeleteStravaConnection(ctx, userID)
		return err
	})
	if !keep {
		if err := q.AddStravaActivityPurge(ctx, userID); err != nil {
			return err
		}
	}
	// The connection and the purge are one transaction, so a connection
	// that's gone always leaves its purge behind.
	if err := batch.Send(ctx, r.db); err != nil {
		return fmt.Errorf("failed to disconnect Strava: %w", err)
	}
	if deleted == 0 {
		// Nothing was connected, so nothing is to be deleted either.
		if !keep {
			if err := r.q.CancelStravaActivityPurge(ctx, userID); err != nil {
				return fmt.Errorf("failed to disconnect Strava: %w", err)
			}
		}
		return store.ErrNotFound
	}
	return nil
}

// ListStravaActivityPurges returns the users whose activities from Strava
// are being deleted, those who disconnected first first.
func (r *Repository) ListStravaActivityPurges(ctx context.Context) ([]pgtype.UUID, error) {
	rows, err := r.q.ListStravaActivityPurges(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list Strava activity purges: %w", err)
	}
	users := make([]pgtype.UUID, len(rows))
	for i, row := range rows {
		users[i] = row.UserID
	}
	return users, nil
}

// PurgingStravaActivities returns whether the user's activities from Strava
// are being deleted.
func (r *Repository) PurgingStravaActivities(ctx context.Context, userID pgtype.UUID) (bool, error) {
	purging, err := r.q.HasStravaActivityPurge(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("failed to check for a Strava activity purge: %w", err)
	}
	return purging, nil
}

// PurgeStravaActivities deletes a batch of up to limit of the data the
// user's activities got from Strava's API, after they disconnected Strava
// without agreeing to keep it, and returns whether all of it is deleted.
// Activities only on Strava are deleted, with their samples. Activities on
// Garmin Connect too are kept with Garmin's data, losing their Strava ID and
// Strava's samples. Activities the user imported from their own Strava
// archive are kept, as their data came from the user. Strava's responses
// and photos are deleted for all of them. Objects are deleted before the
// rows, so that if deleting them fails, the next batch tries again.
func (r *Repository) PurgeStravaActivities(ctx context.Context, userID pgtype.UUID, limit int32) (done bool, err error) {
	purges, err := r.q.GetStravaActivityPurge(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("failed to get Strava activity purge: %w", err)
	}
	if len(purges) == 0 {
		return true, nil
	}
	after := purges[0].AfterActivityID
	rows, err := r.q.ListStravaActivitiesToPurge(ctx, &gen.ListStravaActivitiesToPurgeParams{UserID: userID, AfterID: after, RowLimit: limit})
	if err != nil {
		return false, fmt.Errorf("failed to list Strava activities to delete: %w", err)
	}
	var keys []string
	var deleted, detached, archived []pgtype.UUID
	for _, row := range rows {
		for _, name := range responseNames {
			keys = append(keys, responseKey(userID, row.StravaID.Int64, name))
		}
		switch {
		case row.FromStravaArchive:
			archived = append(archived, row.ID)
		case row.OnGarmin:
			detached = append(detached, row.ID)
			if !row.GarminSamples {
				keys = append(keys, streamsKey(userID, row.ID))
			}
		default:
			deleted = append(deleted, row.ID)
			keys = append(keys, streamsKey(userID, row.ID))
		}
	}
	if err := r.deleteObjects(ctx, keys); err != nil {
		return false, fmt.Errorf("failed to delete Strava activity data: %w", err)
	}
	var batch store.Batch
	q := gen.New(&batch)
	if err := q.DeleteStravaActivityPhotos(ctx, &gen.DeleteStravaActivityPhotosParams{UserID: userID, Ids: append(detached, archived...)}); err != nil {
		return false, err
	}
	if err := q.DetachStravaActivities(ctx, &gen.DetachStravaActivitiesParams{UserID: userID, Ids: detached}); err != nil {
		return false, err
	}
	if err := q.DeleteStravaActivitiesByID(ctx, &gen.DeleteStravaActivitiesByIDParams{UserID: userID, Ids: deleted}); err != nil {
		return false, err
	}
	done = len(rows) < int(limit)
	if done {
		if err := q.CancelStravaActivityPurge(ctx, userID); err != nil {
			return false, err
		}
	} else if err := q.AdvanceStravaActivityPurge(ctx, &gen.AdvanceStravaActivityPurgeParams{UserID: userID, AfterActivityID: rows[len(rows)-1].ID}); err != nil {
		return false, err
	}
	if err := batch.Send(ctx, r.db); err != nil {
		return false, fmt.Errorf("failed to delete Strava activities: %w", err)
	}
	return done, nil
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
// Strava ID, with its streams and Strava's responses about it. An activity
// that's on Garmin Connect too is kept, without its Strava ID, and with its
// samples if they're Garmin's. The objects are deleted first, so that if
// that fails the activity is still there to delete them again.
func (r *Repository) DeleteActivityByStravaID(ctx context.Context, userID pgtype.UUID, stravaID int64) error {
	keys := make([]string, 0, len(responseNames)+1)
	for _, name := range responseNames {
		keys = append(keys, responseKey(userID, stravaID, name))
	}
	row, err := r.q.GetActivityIDByStravaID(ctx, &gen.GetActivityIDByStravaIDParams{UserID: userID, StravaID: stravaID})
	switch {
	case err == nil:
		// Garmin's samples stay with the activity.
		if row.GarminActivityID.Valid && row.GarminSamples {
			break
		}
		keys = append(keys, streamsKey(userID, row.ID))
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("failed to get activity: %w", err)
	}
	if err := r.deleteObjects(ctx, keys); err != nil {
		return fmt.Errorf("failed to delete activity data: %w", err)
	}
	if _, err := r.q.DeleteActivityByStravaID(ctx, &gen.DeleteActivityByStravaIDParams{UserID: userID, StravaID: stravaID}); err != nil {
		return fmt.Errorf("failed to delete activity: %w", err)
	}
	return nil
}

func connectionFromDB(c *gen.StravaConnection) *StravaConnection {
	return &StravaConnection{
		UserID:         c.UserID,
		AthleteID:      c.AthleteID,
		AthleteName:    c.AthleteName,
		AccessToken:    c.AccessToken,
		RefreshToken:   c.RefreshToken,
		Expiry:         c.Expiry.Time,
		SyncState:      SyncState(c.SyncState),
		SyncMessage:    c.SyncMessage,
		SyncStateTime:  c.SyncStateTime.Time,
		LastSyncTime:   c.LastSyncTime.Time,
		ResumeTime:     c.ResumeTime.Time,
		SyncFailures:   c.SyncFailures,
		SyncStartTime:  c.SyncStartTime.Time,
		ListBefore:     c.ListBefore.Time,
		KeepActivities: c.KeepActivities,
	}
}
