package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities/internal/gen"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

// Photo is a photo added to an activity on Strava. Only its metadata is
// stored: the image stays on Strava's CDN.
type Photo struct {
	// Strava's ID of the photo.
	StravaID string
	Caption  string
	// The URL of the image on Strava's CDN.
	URL string
	// The size of the image in pixels, zero if unknown.
	Width, Height int32
	// Where the photo was taken, nil if unknown.
	Location *geo.Point
	// When the photo was taken, zero if unknown.
	TakenTime time.Time
	// The raw Strava response. Not set when listing photos.
	RawJSON []byte
}

func insertPhotoParams(activityID pgtype.UUID, position int, p *Photo) *gen.InsertActivityPhotoParams {
	params := &gen.InsertActivityPhotoParams{
		ActivityID: activityID,
		StravaID:   p.StravaID,
		Position:   int32(position),
		Caption:    p.Caption,
		URL:        p.URL,
		Width:      p.Width,
		Height:     p.Height,
		TakenTime:  pgtype.Timestamptz{Time: p.TakenTime, Valid: !p.TakenTime.IsZero()},
	}
	if p.Location != nil {
		params.Lat = pgtype.Float8{Float64: p.Location.Lat, Valid: true}
		params.Lng = pgtype.Float8{Float64: p.Location.Lng, Valid: true}
	}
	return params
}

// ListActivityPhotos lists the photos of one of the user's activities, in
// order.
func (r *Repository) ListActivityPhotos(ctx context.Context, userID, activityID pgtype.UUID) ([]Photo, error) {
	rows, err := r.q.ListActivityPhotos(ctx, &gen.ListActivityPhotosParams{ActivityID: activityID, UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("failed to list photos: %w", err)
	}
	photos := make([]Photo, len(rows))
	for i, row := range rows {
		photos[i] = Photo{
			StravaID: row.StravaID,
			Caption:  row.Caption,
			URL:      row.URL,
			Width:    row.Width,
			Height:   row.Height,
		}
		if row.Lat.Valid && row.Lng.Valid {
			photos[i].Location = &geo.Point{Lat: row.Lat.Float64, Lng: row.Lng.Float64}
		}
		if row.TakenTime.Valid {
			photos[i].TakenTime = row.TakenTime.Time
		}
	}
	return photos, nil
}
