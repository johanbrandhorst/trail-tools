package activities

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities/internal/gen"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/proto"
)

const (
	// moveStreamsBatchSize is how many activities' streams are moved at
	// once. The streams of a long activity are a few megabytes, so a batch
	// stays well within the server's memory.
	moveStreamsBatchSize = 10
	// moveResponsesBatchSize is how many activities' responses are moved at
	// once. They're tens of kilobytes at most.
	moveResponsesBatchSize = 100
)

// MoveToBlobs moves the streams and Strava's responses that were stored in
// the database before they were stored in the blob store, a batch at a time,
// until none is left or ctx is done. It computes the tracks missing from the
// moved streams on the way. It returns whether all of it has moved.
//
// Objects that exist are left as they are, as a sync stored them since, so
// they're newer than the data in the database.
func (r *Repository) MoveToBlobs(ctx context.Context) (bool, error) {
	for _, move := range []func(context.Context) (bool, error){r.moveStreams, r.moveResponses, r.movePhotoResponses} {
		for {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			done, err := move(ctx)
			if err != nil {
				return false, err
			}
			if done {
				break
			}
		}
	}
	return true, nil
}

// moveStreams moves a batch of streams, and returns whether none were left.
func (r *Repository) moveStreams(ctx context.Context) (bool, error) {
	rows, err := r.q.ListUnmovedStreams(ctx, moveStreamsBatchSize)
	if err != nil {
		return false, fmt.Errorf("failed to list streams to move: %w", err)
	}
	objects := make([]object, 0, len(rows))
	tracks := make([]string, len(rows))
	for i, row := range rows {
		streams := &activitiesv1.ActivityStreams{}
		if err := proto.Unmarshal(row.Streams, streams); err != nil {
			// They're cleared regardless, or they would be moved again and
			// again. The activity's summary polyline is used instead.
			r.report.Report(ctx, "Dropping undecodable streams", err, slog.String("activity_id", store.UUIDToString(row.ActivityID)))
			continue
		}
		encoded, err := encodeStreams(streams)
		if err != nil {
			r.report.Report(ctx, "Dropping unencodable streams", err, slog.String("activity_id", store.UUIDToString(row.ActivityID)))
			continue
		}
		objects = append(objects, streamsObject(row.UserID, row.ActivityID, encoded))
		if !row.TrackPolyline.Valid {
			tracks[i] = trackFromStreams(streams)
		}
	}
	if err := r.putObjects(ctx, objects, true); err != nil {
		return false, fmt.Errorf("failed to move streams: %w", err)
	}
	var batch store.Batch
	q := gen.New(&batch)
	for i, row := range rows {
		if err := q.ClearMovedStreams(ctx, &gen.ClearMovedStreamsParams{ActivityID: row.ActivityID, TrackPolyline: tracks[i]}); err != nil {
			return false, err
		}
	}
	if err := batch.Send(ctx, r.db); err != nil {
		return false, fmt.Errorf("failed to clear moved streams: %w", err)
	}
	return len(rows) < moveStreamsBatchSize, nil
}

// moveResponses moves a batch of Strava's responses for activities'
// summaries and details, and returns whether none were left.
func (r *Repository) moveResponses(ctx context.Context) (bool, error) {
	rows, err := r.q.ListUnmovedResponses(ctx, moveResponsesBatchSize)
	if err != nil {
		return false, fmt.Errorf("failed to list responses to move: %w", err)
	}
	objects := make([]object, 0, 2*len(rows))
	ids := make([]pgtype.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
		if row.SummaryJSON != nil {
			objects = append(objects, responseObject(row.UserID, row.StravaID, summaryResponse, row.SummaryJSON))
		}
		if row.DetailJSON != nil {
			objects = append(objects, responseObject(row.UserID, row.StravaID, detailResponse, row.DetailJSON))
		}
	}
	if err := r.putObjects(ctx, objects, true); err != nil {
		return false, fmt.Errorf("failed to move responses: %w", err)
	}
	if err := r.q.ClearMovedResponses(ctx, ids); err != nil {
		return false, fmt.Errorf("failed to clear moved responses: %w", err)
	}
	return len(rows) < moveResponsesBatchSize, nil
}

// movePhotoResponses moves a batch of Strava's responses for activities'
// photos, and returns whether none were left.
func (r *Repository) movePhotoResponses(ctx context.Context) (bool, error) {
	rows, err := r.q.ListUnmovedPhotoResponses(ctx, moveResponsesBatchSize)
	if err != nil {
		return false, fmt.Errorf("failed to list photo responses to move: %w", err)
	}
	objects := make([]object, len(rows))
	ids := make([]pgtype.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ActivityID
		objects[i] = responseObject(row.UserID, row.StravaID, photosResponse, row.Responses)
	}
	if err := r.putObjects(ctx, objects, true); err != nil {
		return false, fmt.Errorf("failed to move photo responses: %w", err)
	}
	if err := r.q.ClearMovedPhotoResponses(ctx, ids); err != nil {
		return false, fmt.Errorf("failed to clear moved photo responses: %w", err)
	}
	return len(rows) < moveResponsesBatchSize, nil
}
