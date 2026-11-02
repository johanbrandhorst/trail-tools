package activities

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities/internal/gen"
	"github.com/johanbrandhorst/trail-tools/internal/blob"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// GarminConnection is a user's connection to Garmin Connect.
type GarminConnection struct {
	UserID       pgtype.UUID
	GarminUserID string
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	// The permissions the user granted, sorted.
	Permissions []string
	// When the user's workouts were last put on their Garmin calendar, zero
	// if never.
	WorkoutsSyncTime time.Time
	SyncMessage      string
	CreateTime       time.Time
}

// ErrGarminAccountInUse is returned when connecting a Garmin account that's
// connected to another user: Garmin's notifications name only the Garmin
// user, so an account can only belong to one user.
var ErrGarminAccountInUse = errors.New("the Garmin account is connected to another user")

// GarminTokens are a user's Garmin tokens.
type GarminTokens struct {
	AccessToken, RefreshToken string
	Expiry                    time.Time
}

// UpsertGarminConnection creates or replaces the user's Garmin connection.
func (r *Repository) UpsertGarminConnection(ctx context.Context, c *GarminConnection) (*GarminConnection, error) {
	row, err := r.q.UpsertGarminConnection(ctx, &gen.UpsertGarminConnectionParams{
		UserID:       c.UserID,
		GarminUserID: c.GarminUserID,
		AccessToken:  c.AccessToken,
		RefreshToken: c.RefreshToken,
		Expiry:       pgtype.Timestamptz{Time: c.Expiry, Valid: true},
		Permissions:  nonNilStrings(c.Permissions),
	})
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "garmin_connections_garmin_user_id_key" {
		return nil, ErrGarminAccountInUse
	}
	if err != nil {
		return nil, fmt.Errorf("failed to store Garmin connection: %w", err)
	}
	return garminConnectionFromDB(row), nil
}

// GetGarminConnection gets the user's Garmin connection.
func (r *Repository) GetGarminConnection(ctx context.Context, userID pgtype.UUID) (*GarminConnection, error) {
	row, err := r.q.GetGarminConnection(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get Garmin connection: %w", err)
	}
	return garminConnectionFromDB(row), nil
}

// GetGarminConnectionByGarminUser gets the connection of a Garmin user, as
// notifications identify them.
func (r *Repository) GetGarminConnectionByGarminUser(ctx context.Context, garminUserID string) (*GarminConnection, error) {
	row, err := r.q.GetGarminConnectionByGarminUser(ctx, garminUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get Garmin connection: %w", err)
	}
	return garminConnectionFromDB(row), nil
}

// ListGarminConnections lists all Garmin connections, those whose workouts
// were synced longest ago first.
func (r *Repository) ListGarminConnections(ctx context.Context) ([]*GarminConnection, error) {
	rows, err := r.q.ListGarminConnections(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list Garmin connections: %w", err)
	}
	result := make([]*GarminConnection, len(rows))
	for i, row := range rows {
		result[i] = garminConnectionFromDB(row)
	}
	return result, nil
}

// RefreshGarminTokens refreshes the user's tokens with refresh, which gets
// the stored connection and returns the new tokens, or nil if the stored
// ones are still fresh. The connection is locked meanwhile, as Garmin rotates
// refresh tokens: two refreshes at once would leave one with a used token.
func (r *Repository) RefreshGarminTokens(ctx context.Context, userID pgtype.UUID, refresh func(stored *GarminConnection) (*GarminTokens, error)) (*GarminConnection, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := r.q.WithTx(tx)
	row, err := q.GetGarminConnectionForUpdate(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get Garmin connection: %w", err)
	}
	conn := garminConnectionFromDB(row)
	tokens, err := refresh(conn)
	if err != nil {
		return nil, err
	}
	if tokens == nil {
		return conn, nil
	}
	if err := q.UpdateGarminTokens(ctx, &gen.UpdateGarminTokensParams{
		UserID:       userID,
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		Expiry:       pgtype.Timestamptz{Time: tokens.Expiry, Valid: true},
	}); err != nil {
		return nil, fmt.Errorf("failed to update Garmin tokens: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit Garmin tokens: %w", err)
	}
	conn.AccessToken, conn.RefreshToken, conn.Expiry = tokens.AccessToken, tokens.RefreshToken, tokens.Expiry
	return conn, nil
}

// UpdateGarminPermissions records the permissions the user granted.
func (r *Repository) UpdateGarminPermissions(ctx context.Context, userID pgtype.UUID, permissions []string) error {
	if err := r.q.UpdateGarminPermissions(ctx, &gen.UpdateGarminPermissionsParams{UserID: userID, Permissions: nonNilStrings(permissions)}); err != nil {
		return fmt.Errorf("failed to update Garmin permissions: %w", err)
	}
	return nil
}

// FinishGarminWorkoutsSync records that the user's workouts were synced to
// Garmin, with what happened.
func (r *Repository) FinishGarminWorkoutsSync(ctx context.Context, userID pgtype.UUID, at time.Time, message string) error {
	if err := r.q.FinishGarminWorkoutsSync(ctx, &gen.FinishGarminWorkoutsSyncParams{
		UserID:           userID,
		WorkoutsSyncTime: pgtype.Timestamptz{Time: at, Valid: true},
		SyncMessage:      message,
	}); err != nil {
		return fmt.Errorf("failed to record Garmin sync: %w", err)
	}
	return nil
}

// SetGarminSyncMessage records what the last sync did or why it failed.
func (r *Repository) SetGarminSyncMessage(ctx context.Context, userID pgtype.UUID, message string) error {
	if err := r.q.SetGarminSyncMessage(ctx, &gen.SetGarminSyncMessageParams{UserID: userID, SyncMessage: message}); err != nil {
		return fmt.Errorf("failed to record Garmin sync: %w", err)
	}
	return nil
}

// DeleteGarminConnection deletes the user's Garmin connection. Their
// activities stay.
func (r *Repository) DeleteGarminConnection(ctx context.Context, userID pgtype.UUID) error {
	n, err := r.q.DeleteGarminConnection(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to delete Garmin connection: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func garminConnectionFromDB(c *gen.GarminConnection) *GarminConnection {
	return &GarminConnection{
		UserID:           c.UserID,
		GarminUserID:     c.GarminUserID,
		AccessToken:      c.AccessToken,
		RefreshToken:     c.RefreshToken,
		Expiry:           c.Expiry.Time,
		Permissions:      c.Permissions,
		WorkoutsSyncTime: c.WorkoutsSyncTime.Time,
		SyncMessage:      c.SyncMessage,
		CreateTime:       c.CreateTime.Time,
	}
}

// GarminActivity is an activity's summary from Garmin.
type GarminActivity struct {
	GarminActivityID    int64
	Name                string
	SportType           string
	StartTime           time.Time
	StartDateLocal      time.Time
	DistanceMeters      float64
	MovingTimeSeconds   int32
	ElapsedTimeSeconds  int32
	ElevationGainMeters float64
	AverageSpeedMps     float64
	MaxSpeedMps         float64
	AverageHeartrateBpm float64
	MaxHeartrateBpm     float64
	AverageCadenceSpm   float64
	DeviceName          string
	// The raw summary, kept in the blob store.
	RawJSON []byte
}

// StoredGarminActivity is an activity from Garmin as stored.
type StoredGarminActivity struct {
	ID pgtype.UUID
	// Whether the activity is on Strava too.
	OnStrava bool
	// Where its samples come from, and whether it has them.
	DataSource DataSource
	HasSamples bool
	SportType  string
}

// garminResponseKey returns the key of Garmin's data about an activity,
// keyed by Garmin's ID so it's stored before the activity.
func garminResponseKey(userID pgtype.UUID, garminID int64, name string) string {
	return "users/" + store.UUIDToString(userID) + "/garmin/activities/" + strconv.FormatInt(garminID, 10) + "/" + name
}

// UpsertGarminActivity creates or updates an activity from Garmin's summary.
// An activity from Strava that's the same one, which Strava says it got from
// Garmin or started within two minutes of it, gets Garmin's ID instead, so
// there's one activity. Strava's name stays, and the stats are the source's
// whose samples the activity has.
func (r *Repository) UpsertGarminActivity(ctx context.Context, userID pgtype.UUID, a *GarminActivity) (*StoredGarminActivity, error) {
	if len(a.RawJSON) > 0 {
		gz, err := gzipped(a.RawJSON)
		if err != nil {
			return nil, err
		}
		if err := r.blobs.Put(ctx, garminResponseKey(userID, a.GarminActivityID, "summary.json.gz"), gz, "application/gzip"); err != nil {
			return nil, fmt.Errorf("failed to store Garmin summary: %w", err)
		}
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := r.q.WithTx(tx)
	type row struct {
		ID            pgtype.UUID
		StravaID      pgtype.Int8
		DataSource    string
		StreamsSynced bool
		GarminSamples bool
		SportType     string
	}
	var stored row
	existing, err := q.GetGarminActivity(ctx, &gen.GetGarminActivityParams{UserID: userID, GarminActivityID: pgtype.Int8{Int64: a.GarminActivityID, Valid: true}})
	switch {
	case err == nil:
		stored = row(*existing)
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("failed to get activity: %w", err)
	default:
		attached, err := q.AttachGarminToStravaActivity(ctx, &gen.AttachGarminToStravaActivityParams{
			GarminActivityID: a.GarminActivityID,
			UserID:           userID,
			StartTime:        pgtype.Timestamptz{Time: a.StartTime, Valid: true},
		})
		switch {
		case err == nil:
			stored = row(*attached)
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("failed to match activity: %w", err)
		default:
			inserted, err := q.InsertGarminActivity(ctx, &gen.InsertGarminActivityParams{
				UserID:              userID,
				GarminActivityID:    pgtype.Int8{Int64: a.GarminActivityID, Valid: true},
				Name:                a.Name,
				SportType:           a.SportType,
				StartTime:           pgtype.Timestamptz{Time: a.StartTime, Valid: true},
				StartDateLocal:      store.DateToPG(a.StartDateLocal),
				DistanceMeters:      a.DistanceMeters,
				MovingTimeSeconds:   a.MovingTimeSeconds,
				ElapsedTimeSeconds:  a.ElapsedTimeSeconds,
				ElevationGainMeters: a.ElevationGainMeters,
				AverageSpeedMps:     a.AverageSpeedMps,
				MaxSpeedMps:         a.MaxSpeedMps,
				AverageHeartrateBpm: a.AverageHeartrateBpm,
				MaxHeartrateBpm:     a.MaxHeartrateBpm,
				AverageCadenceSpm:   a.AverageCadenceSpm,
				DeviceName:          a.DeviceName,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create activity: %w", err)
			}
			stored = row(*inserted)
		}
	}
	if err := q.UpdateGarminActivity(ctx, &gen.UpdateGarminActivityParams{
		ID:                  stored.ID,
		Name:                a.Name,
		SportType:           a.SportType,
		DistanceMeters:      a.DistanceMeters,
		MovingTimeSeconds:   a.MovingTimeSeconds,
		ElapsedTimeSeconds:  a.ElapsedTimeSeconds,
		ElevationGainMeters: a.ElevationGainMeters,
		AverageSpeedMps:     a.AverageSpeedMps,
		MaxSpeedMps:         a.MaxSpeedMps,
		AverageHeartrateBpm: a.AverageHeartrateBpm,
		MaxHeartrateBpm:     a.MaxHeartrateBpm,
		AverageCadenceSpm:   a.AverageCadenceSpm,
		DeviceName:          a.DeviceName,
	}); err != nil {
		return nil, fmt.Errorf("failed to update activity: %w", err)
	}
	if err := queueLinkStrengthWorkouts(ctx, q, userID); err != nil {
		return nil, fmt.Errorf("failed to link strength workouts: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit activity: %w", err)
	}
	return &StoredGarminActivity{
		ID:         stored.ID,
		OnStrava:   stored.StravaID.Valid,
		DataSource: DataSource(stored.DataSource),
		HasSamples: stored.GarminSamples || stored.DataSource == string(DataSourceStrava) && stored.StreamsSynced,
		SportType:  stored.SportType,
	}, nil
}

// StoreGarminActivityFile stores an activity's FIT file from Garmin, and its
// samples from the file, which become the activity's own. A nil streams
// stores only the file, for an activity whose samples are Strava's.
func (r *Repository) StoreGarminActivityFile(ctx context.Context, userID, activityID pgtype.UUID, garminID int64, file []byte, streams *activitiesv1.ActivityStreams, bounds *geo.Bounds) error {
	gz, err := gzipped(file)
	if err != nil {
		return err
	}
	objects := []object{{key: garminResponseKey(userID, garminID, "activity.fit.gz"), data: gz, contentType: "application/gzip"}}
	var track string
	if streams != nil {
		encoded, err := encodeStreams(streams)
		if err != nil {
			return err
		}
		objects = append(objects, streamsObject(userID, activityID, encoded))
		track = trackFromStreams(streams)
	}
	if err := r.putObjects(ctx, objects); err != nil {
		return fmt.Errorf("failed to store Garmin file: %w", err)
	}
	if streams == nil {
		return nil
	}
	params := &gen.StoreGarminSamplesParams{ID: activityID, SummaryPolyline: track}
	if bounds != nil {
		params.MinLat, params.MinLng = pgtype.Float8{Float64: bounds.MinLat, Valid: true}, pgtype.Float8{Float64: bounds.MinLng, Valid: true}
		params.MaxLat, params.MaxLng = pgtype.Float8{Float64: bounds.MaxLat, Valid: true}, pgtype.Float8{Float64: bounds.MaxLng, Valid: true}
	}
	var batch store.Batch
	q := gen.New(&batch)
	if err := q.StoreGarminSamples(ctx, params); err != nil {
		return err
	}
	if err := q.UpsertActivityStreams(ctx, &gen.UpsertActivityStreamsParams{ActivityID: activityID, TrackPolyline: track}); err != nil {
		return err
	}
	if err := batch.Send(ctx, r.db); err != nil {
		return fmt.Errorf("failed to store Garmin samples: %w", err)
	}
	return nil
}

// GetGarminActivityFile gets the FIT file of one of the user's activities
// from Garmin, gzipped. It returns store.ErrNotFound if there's none.
func (r *Repository) GetGarminActivityFile(ctx context.Context, userID pgtype.UUID, garminID int64) ([]byte, error) {
	gz, err := r.blobs.Get(ctx, garminResponseKey(userID, garminID, "activity.fit.gz"))
	if errors.Is(err, blob.ErrNotFound) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get Garmin file: %w", err)
	}
	return gunzipped(gz)
}

// DeleteGarminActivity deletes an activity deleted on Garmin, with Garmin's
// data about it. An activity that's on Strava too is kept, without its
// Garmin ID; its samples stay with it, as Strava would give it the same.
func (r *Repository) DeleteGarminActivity(ctx context.Context, userID pgtype.UUID, garminID int64) error {
	keys := []string{
		garminResponseKey(userID, garminID, "summary.json.gz"),
		garminResponseKey(userID, garminID, "activity.fit.gz"),
	}
	row, err := r.q.DeleteGarminActivity(ctx, &gen.DeleteGarminActivityParams{UserID: userID, GarminActivityID: garminID})
	if err != nil {
		return fmt.Errorf("failed to delete activity: %w", err)
	}
	if row.Deleted {
		keys = append(keys, streamsKey(userID, row.ID))
	}
	if err := r.deleteObjects(ctx, keys); err != nil {
		return fmt.Errorf("failed to delete activity data: %w", err)
	}
	return nil
}

// GarminActivityFile is an activity file Garmin offered, to download.
type GarminActivityFile struct {
	UserID           pgtype.UUID
	GarminActivityID int64
	CallbackURL      string
	ExpireTime       time.Time
}

// MaxGarminFileAttempts is how many times downloading a file may fail
// before it's given up.
const MaxGarminFileAttempts = 3

// AddGarminActivityFile records a file Garmin offered, replacing an earlier
// offer of the activity's file.
func (r *Repository) AddGarminActivityFile(ctx context.Context, f *GarminActivityFile) error {
	if err := r.q.UpsertGarminActivityFile(ctx, &gen.UpsertGarminActivityFileParams{
		UserID:           f.UserID,
		GarminActivityID: f.GarminActivityID,
		CallbackURL:      f.CallbackURL,
		ExpireTime:       pgtype.Timestamptz{Time: f.ExpireTime, Valid: true},
	}); err != nil {
		return fmt.Errorf("failed to record Garmin file: %w", err)
	}
	return nil
}

// ListGarminActivityFiles lists the files to download, of a user, or of all
// users if userID is invalid, oldest first.
func (r *Repository) ListGarminActivityFiles(ctx context.Context, userID pgtype.UUID, limit int32) ([]*GarminActivityFile, error) {
	rows, err := r.q.ListGarminActivityFiles(ctx, &gen.ListGarminActivityFilesParams{UserID: userID, MaxAttempts: MaxGarminFileAttempts, RowLimit: limit})
	if err != nil {
		return nil, fmt.Errorf("failed to list Garmin files: %w", err)
	}
	result := make([]*GarminActivityFile, len(rows))
	for i, row := range rows {
		result[i] = &GarminActivityFile{UserID: row.UserID, GarminActivityID: row.GarminActivityID, CallbackURL: row.CallbackURL, ExpireTime: row.ExpireTime.Time}
	}
	return result, nil
}

// CountGarminStatus counts the user's activity files waiting to download,
// and the workouts on their Garmin calendar.
func (r *Repository) CountGarminStatus(ctx context.Context, userID pgtype.UUID) (pendingFiles, workouts int64, err error) {
	row, err := r.q.CountGarminStatus(ctx, &gen.CountGarminStatusParams{UserID: userID, MaxAttempts: MaxGarminFileAttempts})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to count Garmin files and workouts: %w", err)
	}
	return row.PendingFiles, row.Workouts, nil
}

// FinishGarminActivityFile removes a file that was downloaded, or isn't
// needed.
func (r *Repository) FinishGarminActivityFile(ctx context.Context, userID pgtype.UUID, garminID int64) error {
	if err := r.q.DeleteGarminActivityFile(ctx, &gen.DeleteGarminActivityFileParams{UserID: userID, GarminActivityID: garminID}); err != nil {
		return fmt.Errorf("failed to remove Garmin file: %w", err)
	}
	return nil
}

// RecordGarminActivityFileFailure records that downloading a file failed.
func (r *Repository) RecordGarminActivityFileFailure(ctx context.Context, userID pgtype.UUID, garminID int64) error {
	if err := r.q.RecordGarminActivityFileFailure(ctx, &gen.RecordGarminActivityFileFailureParams{UserID: userID, GarminActivityID: garminID}); err != nil {
		return fmt.Errorf("failed to record Garmin file failure: %w", err)
	}
	return nil
}

// DeleteExpiredGarminActivityFiles removes files whose links expired or
// that failed too often.
func (r *Repository) DeleteExpiredGarminActivityFiles(ctx context.Context) error {
	if err := r.q.DeleteExpiredGarminActivityFiles(ctx, MaxGarminFileAttempts); err != nil {
		return fmt.Errorf("failed to remove expired Garmin files: %w", err)
	}
	return nil
}

func gzipped(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, fmt.Errorf("failed to compress: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("failed to compress: %w", err)
	}
	return buf.Bytes(), nil
}

// maxGunzippedSize bounds decompressed files, as fit.MaxFileSize does.
const maxGunzippedSize = 32 << 20

func gunzipped(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decompress: %w", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(r, maxGunzippedSize)); err != nil {
		return nil, fmt.Errorf("failed to decompress: %w", err)
	}
	return buf.Bytes(), nil
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// GetGarminActivity gets one of the user's activities by its Garmin ID.
func (r *Repository) GetGarminActivity(ctx context.Context, userID pgtype.UUID, garminID int64) (*StoredGarminActivity, error) {
	row, err := r.q.GetGarminActivity(ctx, &gen.GetGarminActivityParams{UserID: userID, GarminActivityID: pgtype.Int8{Int64: garminID, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get activity: %w", err)
	}
	return &StoredGarminActivity{
		ID:         row.ID,
		OnStrava:   row.StravaID.Valid,
		DataSource: DataSource(row.DataSource),
		HasSamples: row.GarminSamples || row.DataSource == string(DataSourceStrava) && row.StreamsSynced,
		SportType:  row.SportType,
	}, nil
}
