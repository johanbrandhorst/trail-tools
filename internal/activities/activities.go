// Package activities stores activities synced from Strava and the Strava
// connection used to sync them.
package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities/internal/gen"
	"github.com/johanbrandhorst/trail-tools/internal/blob"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// Repository stores activities and Strava connections.
type Repository struct {
	report *errreport.Reporter
	db     *store.DB
	q      *gen.Queries
	// Stores the activities' streams and Strava's responses about them.
	blobs blob.Store
}

// NewRepository creates a new Repository on the database and blob store,
// reporting data it can't decode.
func NewRepository(report *errreport.Reporter, db *store.DB, blobs blob.Store) (*Repository, error) {
	return &Repository{
		report: report,
		db:     db,
		q:      gen.New(db),
		blobs:  blobs,
	}, nil
}

// Activity is a recorded activity.
type Activity struct {
	ID                  pgtype.UUID
	StravaID            int64
	Name                string
	Description         string
	SportType           string
	StartTime           time.Time
	StartDateLocal      time.Time
	Timezone            string
	DistanceMeters      float64
	MovingTimeSeconds   int32
	ElapsedTimeSeconds  int32
	ElevationGainMeters float64
	AverageSpeedMps     float64
	MaxSpeedMps         float64
	AverageHeartrateBpm float64
	MaxHeartrateBpm     float64
	AverageCadenceSpm   float64
	SummaryPolyline     string
	DeviceName          string
	RelativeEffort      int32
	StreamsSynced       bool
	// The number of photos on Strava.
	PhotoCount int32
}

// ActivitySummary is an activity as listed by Strava, used when syncing.
type ActivitySummary struct {
	StravaID            int64
	Name                string
	SportType           string
	StartTime           time.Time
	StartDateLocal      time.Time
	Timezone            string
	DistanceMeters      float64
	MovingTimeSeconds   int32
	ElapsedTimeSeconds  int32
	ElevationGainMeters float64
	AverageSpeedMps     float64
	MaxSpeedMps         float64
	AverageHeartrateBpm float64
	MaxHeartrateBpm     float64
	AverageCadenceSpm   float64
	SummaryPolyline     string
	RelativeEffort      int32
	PhotoCount          int32
	// The raw Strava response.
	RawJSON []byte
}

// UpsertActivity creates or updates an activity from Strava.
func (r *Repository) UpsertActivity(ctx context.Context, userID pgtype.UUID, a *ActivitySummary) (pgtype.UUID, error) {
	if err := r.putObjects(ctx, summaryObjects(userID, []*ActivitySummary{a})); err != nil {
		return pgtype.UUID{}, fmt.Errorf("failed to store activity summary: %w", err)
	}
	id, err := r.q.UpsertActivity(ctx, upsertActivityParams(userID, a))
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("failed to upsert activity: %w", err)
	}
	return id, nil
}

// StoreListedActivities creates or updates a page of the user's activities
// listed from Strava, and records how far the listing got: the activities
// that started before listBefore remain to be listed. A zero listBefore
// records that the listing finished. It takes a single round trip to the
// database.
func (r *Repository) StoreListedActivities(ctx context.Context, userID pgtype.UUID, page []*ActivitySummary, listBefore time.Time) error {
	if err := r.putObjects(ctx, summaryObjects(userID, page)); err != nil {
		return fmt.Errorf("failed to store activity summaries: %w", err)
	}
	var batch store.Batch
	q := gen.New(&batch)
	for _, a := range page {
		if _, err := q.UpsertActivity(ctx, upsertActivityParams(userID, a)); err != nil {
			return err
		}
	}
	if err := q.UpdateStravaListBefore(ctx, &gen.UpdateStravaListBeforeParams{
		UserID:     userID,
		ListBefore: pgtype.Timestamptz{Time: listBefore, Valid: !listBefore.IsZero()},
	}); err != nil {
		return err
	}
	if err := batch.Send(ctx, r.db); err != nil {
		return fmt.Errorf("failed to store listed activities: %w", err)
	}
	return nil
}

// summaryObjects returns the objects of Strava's responses for summaries.
func summaryObjects(userID pgtype.UUID, summaries []*ActivitySummary) []object {
	objects := make([]object, 0, len(summaries))
	for _, a := range summaries {
		if len(a.RawJSON) > 0 {
			objects = append(objects, responseObject(userID, a.StravaID, summaryResponse, a.RawJSON))
		}
	}
	return objects
}

func upsertActivityParams(userID pgtype.UUID, a *ActivitySummary) *gen.UpsertActivityParams {
	params := &gen.UpsertActivityParams{
		UserID:              userID,
		StravaID:            a.StravaID,
		Name:                a.Name,
		SportType:           a.SportType,
		StartTime:           pgtype.Timestamptz{Time: a.StartTime, Valid: true},
		StartDateLocal:      store.DateToPG(a.StartDateLocal),
		Timezone:            a.Timezone,
		DistanceMeters:      a.DistanceMeters,
		MovingTimeSeconds:   a.MovingTimeSeconds,
		ElapsedTimeSeconds:  a.ElapsedTimeSeconds,
		ElevationGainMeters: a.ElevationGainMeters,
		AverageSpeedMps:     a.AverageSpeedMps,
		MaxSpeedMps:         a.MaxSpeedMps,
		AverageHeartrateBpm: a.AverageHeartrateBpm,
		MaxHeartrateBpm:     a.MaxHeartrateBpm,
		AverageCadenceSpm:   a.AverageCadenceSpm,
		SummaryPolyline:     a.SummaryPolyline,
		RelativeEffort:      a.RelativeEffort,
		PhotoCount:          a.PhotoCount,
	}
	if a.SummaryPolyline != "" {
		if points, err := geo.DecodePolyline(a.SummaryPolyline); err == nil {
			if b, ok := geo.BoundsOf(points); ok {
				params.MinLat, params.MinLng = pgtype.Float8{Float64: b.MinLat, Valid: true}, pgtype.Float8{Float64: b.MinLng, Valid: true}
				params.MaxLat, params.MaxLng = pgtype.Float8{Float64: b.MaxLat, Valid: true}, pgtype.Float8{Float64: b.MaxLng, Valid: true}
			}
		}
	}
	return params
}

// LatestActivityStartTime returns the start time of the user's most recent
// activity, or the zero time if there are none.
func (r *Repository) LatestActivityStartTime(ctx context.Context, userID pgtype.UUID) (time.Time, error) {
	t, err := r.q.GetLatestActivityStartTime(ctx, userID)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get latest activity: %w", err)
	}
	return t.Time, nil
}

// LatestStartPoint returns where the user's most recent activity with a
// track started. It returns false if there is none.
func (r *Repository) LatestStartPoint(ctx context.Context, userID pgtype.UUID) (geo.Point, bool, error) {
	polyline, err := r.q.GetLatestActivityPolyline(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return geo.Point{}, false, nil
		}
		return geo.Point{}, false, fmt.Errorf("failed to get latest activity: %w", err)
	}
	points, err := geo.DecodePolyline(polyline)
	if err != nil || len(points) == 0 {
		return geo.Point{}, false, nil
	}
	return points[0], true, nil
}

// PendingStreams is an activity whose streams have not been synced.
type PendingStreams struct {
	ID       pgtype.UUID
	StravaID int64
}

// MaxActivitySyncFailures is how many times syncing an activity's details
// may fail before the backlog skips it, so an activity that can't be synced
// doesn't fail every sync. It is synced again once it changes on Strava or
// a user asks for it with RequestActivitySync.
const MaxActivitySyncFailures = 3

// ListActivitiesWithoutStreams lists activities whose streams have not been
// synced yet: those requested with RequestActivitySync first, then those
// that failed to sync less often, then the most recent first, back to those
// that started window before the latest one. Activities that failed
// MaxActivitySyncFailures times are left out.
func (r *Repository) ListActivitiesWithoutStreams(ctx context.Context, userID pgtype.UUID, window time.Duration, limit int32) ([]PendingStreams, error) {
	rows, err := r.q.ListActivitiesWithoutStreams(ctx, &gen.ListActivitiesWithoutStreamsParams{
		UserID:       userID,
		MaxFailures:  MaxActivitySyncFailures,
		RecentWindow: intervalOf(window),
		RowLimit:     limit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list activities without streams: %w", err)
	}
	pending := make([]PendingStreams, len(rows))
	for i, row := range rows {
		pending[i] = PendingStreams{ID: row.ID, StravaID: row.StravaID}
	}
	return pending, nil
}

// RequestActivitySync marks one of the user's activities as requested, so
// it's synced before the rest of the activities waiting for streams, even if
// it failed to sync before. The mark is cleared once its streams are synced.
func (r *Repository) RequestActivitySync(ctx context.Context, userID, activityID pgtype.UUID) error {
	if err := r.q.RequestActivitySync(ctx, &gen.RequestActivitySyncParams{ID: activityID, UserID: userID}); err != nil {
		return fmt.Errorf("failed to request an activity sync: %w", err)
	}
	return nil
}

// RecordActivitySyncFailure records that syncing the details of one of the
// user's activities failed, so the backlog syncs it after the others, and
// skips it after MaxActivitySyncFailures failures.
func (r *Repository) RecordActivitySyncFailure(ctx context.Context, userID, activityID pgtype.UUID) error {
	if err := r.q.RecordActivitySyncFailure(ctx, &gen.RecordActivitySyncFailureParams{ID: activityID, UserID: userID}); err != nil {
		return fmt.Errorf("failed to record an activity sync failure: %w", err)
	}
	return nil
}

// ActivityCounts counts a user's activities.
type ActivityCounts struct {
	Total int64
	// The activities waiting for streams that the backlog syncs.
	PendingStreams int64
	// The activities waiting for streams that the backlog skips, as they
	// failed to sync MaxActivitySyncFailures times.
	FailedStreams int64
}

// CountActivities returns the number of activities and the number waiting
// for streams, as listed by ListActivitiesWithoutStreams with window.
func (r *Repository) CountActivities(ctx context.Context, userID pgtype.UUID, window time.Duration) (total, pendingStreams int64, err error) {
	counts, err := r.CountActivitiesByState(ctx, userID, window)
	return counts.Total, counts.PendingStreams, err
}

// CountActivitiesByState counts the user's activities by their sync state,
// with the pending ones as listed by ListActivitiesWithoutStreams with
// window.
func (r *Repository) CountActivitiesByState(ctx context.Context, userID pgtype.UUID, window time.Duration) (ActivityCounts, error) {
	row, err := r.q.CountActivities(ctx, &gen.CountActivitiesParams{
		UserID:       userID,
		MaxFailures:  MaxActivitySyncFailures,
		RecentWindow: intervalOf(window),
	})
	if err != nil {
		return ActivityCounts{}, fmt.Errorf("failed to count activities: %w", err)
	}
	return ActivityCounts{Total: row.Total, PendingStreams: row.PendingStreams, FailedStreams: row.FailedStreams}, nil
}

// intervalOf converts a duration to an interval.
func intervalOf(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}

// Detail is what Strava's detailed activity adds to an activity's summary.
type Detail struct {
	Description string
	DeviceName  string
	// The raw Strava response.
	RawJSON []byte
	// The activity's photos, in order.
	Photos []Photo
}

// StoreActivityDetail stores the detail of one of the user's activities,
// replacing its photos, unless detail is nil, and its streams with its
// track, unless streams is nil, as when it has none. It marks the
// activity's streams as synced. stravaID is Strava's ID of the activity.
//
// The streams and Strava's responses are stored in the blob store first,
// then the rest in a single round trip to the database, all or none of it.
// If storing it fails, the activity isn't marked synced, and objects stored
// meanwhile are replaced when it's synced again.
func (r *Repository) StoreActivityDetail(ctx context.Context, userID, activityID pgtype.UUID, stravaID int64, detail *Detail, streams *activitiesv1.ActivityStreams, bounds *geo.Bounds) error {
	var objects []object
	var track string
	if streams != nil {
		encoded, err := encodeStreams(streams)
		if err != nil {
			return err
		}
		objects = append(objects, streamsObject(userID, activityID, encoded))
		track = trackFromStreams(streams)
	}
	photosRemoved := false
	if detail != nil {
		if len(detail.RawJSON) > 0 {
			objects = append(objects, responseObject(userID, stravaID, detailResponse, detail.RawJSON))
		}
		responses := make([]json.RawMessage, 0, len(detail.Photos))
		for _, p := range detail.Photos {
			if len(p.RawJSON) > 0 {
				responses = append(responses, p.RawJSON)
			}
		}
		if len(responses) > 0 {
			data, err := json.Marshal(responses)
			if err != nil {
				return fmt.Errorf("failed to encode photo responses: %w", err)
			}
			objects = append(objects, responseObject(userID, stravaID, photosResponse, data))
		} else {
			photosRemoved = true
		}
	}
	if err := r.putObjects(ctx, objects); err != nil {
		return fmt.Errorf("failed to store activity detail: %w", err)
	}
	if photosRemoved {
		// The photos were removed on Strava since the last sync.
		if err := r.blobs.Delete(ctx, responseKey(userID, stravaID, photosResponse)); err != nil {
			return fmt.Errorf("failed to delete photo responses: %w", err)
		}
	}

	var batch store.Batch
	q := gen.New(&batch)
	if detail != nil {
		if err := q.SetActivityDetail(ctx, &gen.SetActivityDetailParams{
			ID:          activityID,
			Description: detail.Description,
			DeviceName:  detail.DeviceName,
		}); err != nil {
			return err
		}
		if err := q.DeleteActivityPhotos(ctx, activityID); err != nil {
			return err
		}
		for i, p := range detail.Photos {
			if err := q.InsertActivityPhoto(ctx, insertPhotoParams(activityID, i, &p)); err != nil {
				return err
			}
		}
	}
	params := &gen.MarkActivityStreamsSyncedParams{ID: activityID}
	if bounds != nil {
		params.MinLat, params.MinLng = pgtype.Float8{Float64: bounds.MinLat, Valid: true}, pgtype.Float8{Float64: bounds.MinLng, Valid: true}
		params.MaxLat, params.MaxLng = pgtype.Float8{Float64: bounds.MaxLat, Valid: true}, pgtype.Float8{Float64: bounds.MaxLng, Valid: true}
	}
	if err := q.MarkActivityStreamsSynced(ctx, params); err != nil {
		return err
	}
	if streams != nil {
		if err := q.UpsertActivityStreams(ctx, &gen.UpsertActivityStreamsParams{ActivityID: activityID, TrackPolyline: track}); err != nil {
			return err
		}
	}
	if err := batch.Send(ctx, r.db); err != nil {
		return fmt.Errorf("failed to store activity detail: %w", err)
	}
	return nil
}

// ListActivitiesFilter filters listed activities.
type ListActivitiesFilter struct {
	// Inclusive local date range. Zero values are unbounded.
	StartDate, EndDate time.Time
	// Sport types to include. Empty includes all.
	SportTypes []string
	Sort       Sort
	Limit      int32
	Offset     int32
}

// Sort is the order to list activities in.
type Sort string

// Activity orders. The zero value lists the most recent first, the others
// the biggest first.
const (
	SortRecent        Sort = ""
	SortDistance      Sort = "distance"
	SortElevationGain Sort = "elevation_gain"
	SortMovingTime    Sort = "moving_time"
)

// ListActivities lists the user's activities in the filter's order, most
// recent first by default.
func (r *Repository) ListActivities(ctx context.Context, userID pgtype.UUID, filter ListActivitiesFilter) ([]*Activity, error) {
	sportTypes := filter.SportTypes
	if sportTypes == nil {
		sportTypes = []string{}
	}
	rows, err := r.q.ListActivities(ctx, &gen.ListActivitiesParams{
		UserID:     userID,
		Limit:      filter.Limit,
		Offset:     filter.Offset,
		StartDate:  store.DateToPG(filter.StartDate),
		EndDate:    store.DateToPG(filter.EndDate),
		SportTypes: sportTypes,
		Sort:       string(filter.Sort),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list activities: %w", err)
	}
	result := make([]*Activity, len(rows))
	for i, row := range rows {
		result[i] = (*activityRow)(row).toActivity()
	}
	return result, nil
}

// GetActivity gets one of the user's activities.
func (r *Repository) GetActivity(ctx context.Context, userID, activityID pgtype.UUID) (*Activity, error) {
	row, err := r.q.GetActivity(ctx, &gen.GetActivityParams{ID: activityID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get activity: %w", err)
	}
	return (*activityRow)(row).toActivity(), nil
}

// GetActivityStreams gets the streams of one of the user's activities. It
// returns store.ErrNotFound if the activity has no streams.
func (r *Repository) GetActivityStreams(ctx context.Context, userID, activityID pgtype.UUID) (*activitiesv1.ActivityStreams, error) {
	// The key has the user's ID, so other users' streams aren't found.
	data, err := r.blobs.Get(ctx, streamsKey(userID, activityID))
	switch {
	case errors.Is(err, blob.ErrNotFound):
		return nil, store.ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("failed to get activity streams: %w", err)
	}
	streams, err := decodeStreams(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode activity streams: %w", err)
	}
	return streams, nil
}

// ActivityTotals are the totals of a single activity, used for summaries.
type ActivityTotals struct {
	StartDateLocal      time.Time
	DistanceMeters      float64
	MovingTimeSeconds   int32
	ElevationGainMeters float64
}

// ListActivityTotals lists the totals of each activity in an inclusive date
// range, in date order.
func (r *Repository) ListActivityTotals(ctx context.Context, userID pgtype.UUID, startDate, endDate time.Time, sportTypes []string) ([]ActivityTotals, error) {
	if sportTypes == nil {
		sportTypes = []string{}
	}
	rows, err := r.q.ListActivityTotals(ctx, &gen.ListActivityTotalsParams{
		UserID:     userID,
		StartDate:  store.DateToPG(startDate),
		EndDate:    store.DateToPG(endDate),
		SportTypes: sportTypes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list activity totals: %w", err)
	}
	totals := make([]ActivityTotals, len(rows))
	for i, row := range rows {
		totals[i] = ActivityTotals{
			StartDateLocal:      store.DateFromPG(row.StartDateLocal),
			DistanceMeters:      row.DistanceMeters,
			MovingTimeSeconds:   row.MovingTimeSeconds,
			ElevationGainMeters: row.ElevationGainMeters,
		}
	}
	return totals, nil
}

// activityRow has the same fields as the rows returned by the activity
// queries, so they can be converted to it.
type activityRow struct {
	ID                  pgtype.UUID
	StravaID            int64
	Name                string
	Description         string
	SportType           string
	StartTime           pgtype.Timestamptz
	StartDateLocal      pgtype.Date
	Timezone            string
	DistanceMeters      float64
	MovingTimeSeconds   int32
	ElapsedTimeSeconds  int32
	ElevationGainMeters float64
	AverageSpeedMps     float64
	MaxSpeedMps         float64
	AverageHeartrateBpm float64
	MaxHeartrateBpm     float64
	AverageCadenceSpm   float64
	SummaryPolyline     string
	DeviceName          string
	RelativeEffort      int32
	StreamsSynced       bool
	PhotoCount          int32
}

func (row *activityRow) toActivity() *Activity {
	return &Activity{
		ID:                  row.ID,
		StravaID:            row.StravaID,
		Name:                row.Name,
		Description:         row.Description,
		SportType:           row.SportType,
		StartTime:           row.StartTime.Time,
		StartDateLocal:      store.DateFromPG(row.StartDateLocal),
		Timezone:            row.Timezone,
		DistanceMeters:      row.DistanceMeters,
		MovingTimeSeconds:   row.MovingTimeSeconds,
		ElapsedTimeSeconds:  row.ElapsedTimeSeconds,
		ElevationGainMeters: row.ElevationGainMeters,
		AverageSpeedMps:     row.AverageSpeedMps,
		MaxSpeedMps:         row.MaxSpeedMps,
		AverageHeartrateBpm: row.AverageHeartrateBpm,
		MaxHeartrateBpm:     row.MaxHeartrateBpm,
		AverageCadenceSpm:   row.AverageCadenceSpm,
		SummaryPolyline:     row.SummaryPolyline,
		DeviceName:          row.DeviceName,
		RelativeEffort:      row.RelativeEffort,
		StreamsSynced:       row.StreamsSynced,
		PhotoCount:          row.PhotoCount,
	}
}
