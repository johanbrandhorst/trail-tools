package activities

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/activities/internal/gen"
	activitiesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/activities/v1"
	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/bs-tools/trailrunn.ing/internal/store"
	"github.com/bs-tools/trailrunn.ing/internal/stravaarchive"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ArchiveEntry is an activity of a Strava archive still to import.
type ArchiveEntry struct {
	StravaID int64
	Name     string
	// The path of its file in the archive, empty if it has none.
	Filename string
}

// StageStravaArchive replaces the user's activities to import from a Strava
// archive with those of the archive, but those already imported from an
// archive, and returns the activities to import, the most recent first.
func (r *Repository) StageStravaArchive(ctx context.Context, userID pgtype.UUID, activities []stravaarchive.Activity) ([]ArchiveEntry, error) {
	params := &gen.InsertStravaArchiveActivitiesParams{UserID: userID}
	for _, a := range activities {
		params.StravaIds = append(params.StravaIds, a.StravaID)
		params.Names = append(params.Names, a.Name)
		params.Descriptions = append(params.Descriptions, a.Description)
		params.SportTypes = append(params.SportTypes, a.SportType)
		params.StartTimes = append(params.StartTimes, pgtype.Timestamptz{Time: a.StartTime, Valid: true})
		params.ElapsedTimeSeconds = append(params.ElapsedTimeSeconds, a.ElapsedTimeSeconds)
		params.MovingTimeSeconds = append(params.MovingTimeSeconds, a.MovingTimeSeconds)
		params.DistanceMeters = append(params.DistanceMeters, a.DistanceMeters)
		params.ElevationGainMeters = append(params.ElevationGainMeters, a.ElevationGainMeters)
		params.AverageSpeedMps = append(params.AverageSpeedMps, a.AverageSpeedMps)
		params.MaxSpeedMps = append(params.MaxSpeedMps, a.MaxSpeedMps)
		params.AverageHeartrateBpm = append(params.AverageHeartrateBpm, a.AverageHeartrateBpm)
		params.MaxHeartrateBpm = append(params.MaxHeartrateBpm, a.MaxHeartrateBpm)
		params.AverageCadenceSpm = append(params.AverageCadenceSpm, a.AverageCadence)
		params.RelativeEfforts = append(params.RelativeEfforts, a.RelativeEffort)
		params.Filenames = append(params.Filenames, a.Filename)
	}
	var batch store.Batch
	q := gen.New(&batch)
	if err := q.DeleteStravaArchiveActivities(ctx, userID); err != nil {
		return nil, err
	}
	if err := q.InsertStravaArchiveActivities(ctx, params); err != nil {
		return nil, err
	}
	var rows []*gen.ListStravaArchiveActivitiesRow
	batch.Read(func(db store.Querier) error {
		var err error
		rows, err = gen.New(db).ListStravaArchiveActivities(ctx, userID)
		return err
	})
	if err := batch.Send(ctx, r.db); err != nil {
		return nil, fmt.Errorf("failed to stage Strava archive: %w", err)
	}
	entries := make([]ArchiveEntry, len(rows))
	for i, row := range rows {
		entries[i] = ArchiveEntry{StravaID: row.StravaID, Name: row.Name, Filename: row.Filename}
	}
	return entries, nil
}

// GetStravaArchiveActivity returns an activity of a Strava archive still to
// import, or store.ErrNotFound.
func (r *Repository) GetStravaArchiveActivity(ctx context.Context, userID pgtype.UUID, stravaID int64) (*stravaarchive.Activity, error) {
	row, err := r.q.GetStravaArchiveActivity(ctx, &gen.GetStravaArchiveActivityParams{UserID: userID, StravaID: stravaID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get Strava archive activity: %w", err)
	}
	return &stravaarchive.Activity{
		StravaID:            row.StravaID,
		Name:                row.Name,
		Description:         row.Description,
		SportType:           row.SportType,
		StartTime:           row.StartTime.Time,
		ElapsedTimeSeconds:  row.ElapsedTimeSeconds,
		MovingTimeSeconds:   row.MovingTimeSeconds,
		DistanceMeters:      row.DistanceMeters,
		ElevationGainMeters: row.ElevationGainMeters,
		AverageSpeedMps:     row.AverageSpeedMps,
		MaxSpeedMps:         row.MaxSpeedMps,
		AverageHeartrateBpm: row.AverageHeartrateBpm,
		MaxHeartrateBpm:     row.MaxHeartrateBpm,
		AverageCadence:      row.AverageCadenceSpm,
		RelativeEffort:      row.RelativeEffort,
		Filename:            row.Filename,
	}, nil
}

// ImportedArchiveActivity is an activity of a Strava archive ready to store.
type ImportedArchiveActivity struct {
	*stravaarchive.Activity
	// The sport type and local date, used for a new activity: one synced
	// from Strava already keeps its own.
	SportType      string
	StartDateLocal time.Time
	// In steps per minute.
	AverageCadenceSpm float64
	// The samples from the activity's file, nil without one.
	Streams *activitiesv1.ActivityStreams
	Bounds  *geo.Bounds
}

// ImportArchiveActivity stores an activity of the user's Strava archive,
// marked as imported from it, so it stays when Strava is disconnected. An
// activity synced from Strava already gets the archive's data and samples,
// and one from Garmin Connect that started within two minutes of it is the
// same one: it keeps Garmin's stats and samples. The activity is no longer
// to import. It returns the activity's ID.
func (r *Repository) ImportArchiveActivity(ctx context.Context, userID pgtype.UUID, a *ImportedArchiveActivity) (pgtype.UUID, error) {
	track := ""
	if a.Streams != nil {
		track = trackFromStreams(a.Streams)
	}
	var row *gen.UpsertArchiveActivityRow
	var batch store.Batch
	q := gen.New(&batch)
	if err := q.AttachStravaToGarminActivity(ctx, &gen.AttachStravaToGarminActivityParams{
		StravaID:  a.StravaID,
		UserID:    userID,
		StartTime: pgtype.Timestamptz{Time: a.StartTime, Valid: true},
	}); err != nil {
		return pgtype.UUID{}, err
	}
	batch.Read(func(db store.Querier) error {
		var err error
		row, err = gen.New(db).UpsertArchiveActivity(ctx, &gen.UpsertArchiveActivityParams{
			UserID:              userID,
			StravaID:            pgtype.Int8{Int64: a.StravaID, Valid: true},
			Name:                a.Name,
			Description:         a.Description,
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
			SummaryPolyline:     track,
			RelativeEffort:      a.RelativeEffort,
		})
		return err
	})
	if err := queueLinkWorkouts(ctx, q, userID); err != nil {
		return pgtype.UUID{}, err
	}
	if err := batch.Send(ctx, r.db); err != nil {
		return pgtype.UUID{}, fmt.Errorf("failed to store archive activity: %w", err)
	}
	// Garmin's samples stay; the archive's replace Strava's otherwise, so
	// none of the activity's data comes from Strava's API.
	streams := a.Streams
	if row.HasGarminSamples {
		streams = nil
	}
	if streams != nil {
		encoded, err := encodeStreams(streams)
		if err != nil {
			return pgtype.UUID{}, err
		}
		if err := r.putObjects(ctx, []object{streamsObject(userID, row.ID, encoded)}); err != nil {
			return pgtype.UUID{}, fmt.Errorf("failed to store archive activity samples: %w", err)
		}
	}
	batch = store.Batch{}
	q = gen.New(&batch)
	if streams != nil {
		// The activity needs no samples from Strava's API.
		params := &gen.MarkActivityStreamsSyncedParams{ID: row.ID}
		if a.Bounds != nil {
			params.MinLat, params.MinLng = pgtype.Float8{Float64: a.Bounds.MinLat, Valid: true}, pgtype.Float8{Float64: a.Bounds.MinLng, Valid: true}
			params.MaxLat, params.MaxLng = pgtype.Float8{Float64: a.Bounds.MaxLat, Valid: true}, pgtype.Float8{Float64: a.Bounds.MaxLng, Valid: true}
		}
		if err := q.MarkActivityStreamsSynced(ctx, params); err != nil {
			return pgtype.UUID{}, err
		}
		if err := q.UpsertActivityStreams(ctx, &gen.UpsertActivityStreamsParams{ActivityID: row.ID, TrackPolyline: track}); err != nil {
			return pgtype.UUID{}, err
		}
		if err := q.SetStravaSamples(ctx, row.ID); err != nil {
			return pgtype.UUID{}, err
		}
	}
	if err := q.DeleteStravaArchiveActivity(ctx, &gen.DeleteStravaArchiveActivityParams{UserID: userID, StravaID: a.StravaID}); err != nil {
		return pgtype.UUID{}, err
	}
	if err := batch.Send(ctx, r.db); err != nil {
		return pgtype.UUID{}, fmt.Errorf("failed to store archive activity: %w", err)
	}
	return row.ID, nil
}

// DeleteStaleStravaArchives deletes the activities to import of archives
// whose import started before a time, as the user left it unfinished.
func (r *Repository) DeleteStaleStravaArchives(ctx context.Context, before time.Time) error {
	if err := r.q.DeleteStaleStravaArchiveActivities(ctx, pgtype.Timestamptz{Time: before, Valid: true}); err != nil {
		return fmt.Errorf("failed to delete stale Strava archive imports: %w", err)
	}
	return nil
}
