package races

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/races/internal/gen"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/trips"
)

// RaceReportPhoto is a photo of a race report. Photos are located like the
// photos of trip reports.
type RaceReportPhoto struct {
	ID       pgtype.UUID
	ReportID pgtype.UUID
	// The order of the photo among the report's.
	Position int32
	// The images are stored under <ObjectKey>/<size>.jpg.
	ObjectKey string
	Width     int32
	Height    int32
	Caption   string
	// Nil if the photo has no location.
	Location *trips.Location
	// Zero if unknown.
	TakenTime  time.Time
	CreateTime time.Time
}

// RaceReportPhotoOrder reports whether photo a goes before photo b.
type RaceReportPhotoOrder func(a, b *RaceReportPhoto) bool

// AddRaceReportPhoto adds a photo to one of the user's race reports: before
// the first photo it goes before by the order, or else last.
func (r *Repository) AddRaceReportPhoto(ctx context.Context, userID pgtype.UUID, p *RaceReportPhoto, before RaceReportPhotoOrder) (*RaceReportPhoto, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := r.q.WithTx(tx)
	// Lock the report, so that the positions of its photos don't change
	// meanwhile.
	if _, err := q.LockRaceReport(ctx, &gen.LockRaceReportParams{ID: p.ReportID, UserID: userID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get race report: %w", err)
	}
	rows, err := q.ListRaceReportPhotos(ctx, p.ReportID)
	if err != nil {
		return nil, fmt.Errorf("failed to list race report photos: %w", err)
	}
	var position pgtype.Int4
	for _, row := range rows {
		if existing := raceReportPhotoFromDB(row); before(p, existing) {
			position = pgtype.Int4{Int32: existing.Position, Valid: true}
			break
		}
	}
	if position.Valid {
		if err := q.ShiftRaceReportPhotos(ctx, &gen.ShiftRaceReportPhotosParams{ReportID: p.ReportID, Position: position.Int32}); err != nil {
			return nil, fmt.Errorf("failed to make room for the photo: %w", err)
		}
	}
	params := &gen.AddRaceReportPhotoParams{
		ReportID:  p.ReportID,
		Position:  position,
		ObjectKey: p.ObjectKey,
		Width:     p.Width,
		Height:    p.Height,
		Caption:   p.Caption,
	}
	if !p.TakenTime.IsZero() {
		params.TakenTime = pgtype.Timestamptz{Time: p.TakenTime, Valid: true}
	}
	params.Lat, params.Lng, params.LocationSource, params.TrackDistanceMeters = photoLocationToPG(p.Location)
	row, err := q.AddRaceReportPhoto(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to add race report photo: %w", err)
	}
	if err := q.TouchRaceReport(ctx, p.ReportID); err != nil {
		return nil, fmt.Errorf("failed to update race report: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return raceReportPhotoFromDB(row), nil
}

// ListRaceReportPhotos lists the photos of a race report in order. The
// caller must check that the report can be accessed.
func (r *Repository) ListRaceReportPhotos(ctx context.Context, reportID pgtype.UUID) ([]*RaceReportPhoto, error) {
	rows, err := r.q.ListRaceReportPhotos(ctx, reportID)
	if err != nil {
		return nil, fmt.Errorf("failed to list race report photos: %w", err)
	}
	photos := make([]*RaceReportPhoto, len(rows))
	for i, row := range rows {
		photos[i] = raceReportPhotoFromDB(row)
	}
	return photos, nil
}

// GetRaceReportPhoto gets a photo of one of the user's race reports.
func (r *Repository) GetRaceReportPhoto(ctx context.Context, userID, photoID pgtype.UUID) (*RaceReportPhoto, error) {
	row, err := r.q.GetRaceReportPhoto(ctx, &gen.GetRaceReportPhotoParams{ID: photoID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get race report photo: %w", err)
	}
	return raceReportPhotoFromDB(row), nil
}

// RaceReportPhotoAccess is what's needed to serve a photo: where it's
// stored, and who can see it.
type RaceReportPhotoAccess struct {
	ObjectKey  string
	CreateTime time.Time
	// The author of the report.
	UserID pgtype.UUID
	// Whether the report is published.
	Published bool
}

// GetRaceReportPhotoAccess gets what's needed to serve any race report
// photo.
func (r *Repository) GetRaceReportPhotoAccess(ctx context.Context, photoID pgtype.UUID) (*RaceReportPhotoAccess, error) {
	row, err := r.q.GetRaceReportPhotoAccess(ctx, photoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get race report photo: %w", err)
	}
	return &RaceReportPhotoAccess{
		ObjectKey:  row.ObjectKey,
		CreateTime: row.CreateTime.Time,
		UserID:     row.UserID,
		Published:  row.Published,
	}, nil
}

// UpdateRaceReportPhotoCaption sets the caption of a photo of one of the
// user's race reports.
func (r *Repository) UpdateRaceReportPhotoCaption(ctx context.Context, userID, photoID pgtype.UUID, caption string) (*RaceReportPhoto, error) {
	row, err := r.q.UpdateRaceReportPhotoCaption(ctx, &gen.UpdateRaceReportPhotoCaptionParams{ID: photoID, UserID: userID, Caption: caption})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to update race report photo: %w", err)
	}
	return raceReportPhotoFromDB(row), nil
}

// UpdateRaceReportPhotoLocation sets or, with a nil location, clears the
// location of a photo of one of the user's race reports.
func (r *Repository) UpdateRaceReportPhotoLocation(ctx context.Context, userID, photoID pgtype.UUID, loc *trips.Location) (*RaceReportPhoto, error) {
	params := &gen.UpdateRaceReportPhotoLocationParams{ID: photoID, UserID: userID}
	params.Lat, params.Lng, params.LocationSource, params.TrackDistanceMeters = photoLocationToPG(loc)
	row, err := r.q.UpdateRaceReportPhotoLocation(ctx, params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to update race report photo: %w", err)
	}
	return raceReportPhotoFromDB(row), nil
}

// SetRaceReportPhotoLocations sets the locations of photos of a race
// report, in one round trip, e.g. after its track changed. The caller checks
// the report is the user's.
func (r *Repository) SetRaceReportPhotoLocations(ctx context.Context, reportID pgtype.UUID, photos []*RaceReportPhoto) error {
	var batch store.Batch
	q := gen.New(&batch)
	for _, p := range photos {
		params := &gen.MoveRaceReportPhotoParams{ID: p.ID, ReportID: reportID}
		params.Lat, params.Lng, params.LocationSource, params.TrackDistanceMeters = photoLocationToPG(p.Location)
		if err := q.MoveRaceReportPhoto(ctx, params); err != nil {
			return err
		}
	}
	if err := batch.Send(ctx, r.db); err != nil {
		return fmt.Errorf("failed to set race report photo locations: %w", err)
	}
	return nil
}

// DeleteRaceReportPhoto deletes a photo of one of the user's race reports.
// It returns the photo's object key, whose images should be deleted.
func (r *Repository) DeleteRaceReportPhoto(ctx context.Context, userID, photoID pgtype.UUID) (string, error) {
	key, err := r.q.DeleteRaceReportPhoto(ctx, &gen.DeleteRaceReportPhotoParams{ID: photoID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", store.ErrNotFound
		}
		return "", fmt.Errorf("failed to delete race report photo: %w", err)
	}
	return key, nil
}

func raceReportPhotoFromDB(row *gen.RaceReportPhoto) *RaceReportPhoto {
	p := &RaceReportPhoto{
		ID:         row.ID,
		ReportID:   row.ReportID,
		Position:   row.Position,
		ObjectKey:  row.ObjectKey,
		Width:      row.Width,
		Height:     row.Height,
		Caption:    row.Caption,
		CreateTime: row.CreateTime.Time,
	}
	if row.TakenTime.Valid {
		p.TakenTime = row.TakenTime.Time
	}
	if row.Lat.Valid && row.Lng.Valid && row.LocationSource.Valid {
		p.Location = &trips.Location{
			Lat:                    row.Lat.Float64,
			Lng:                    row.Lng.Float64,
			Source:                 trips.LocationSource(row.LocationSource.PhotoLocationSource),
			TrackDistanceMeters:    row.TrackDistanceMeters.Float64,
			HasTrackDistanceMeters: row.TrackDistanceMeters.Valid,
		}
	}
	return p
}

func photoLocationToPG(loc *trips.Location) (lat, lng pgtype.Float8, source gen.NullPhotoLocationSource, trackDistance pgtype.Float8) {
	if loc == nil {
		return lat, lng, source, trackDistance
	}
	lat = pgtype.Float8{Float64: loc.Lat, Valid: true}
	lng = pgtype.Float8{Float64: loc.Lng, Valid: true}
	source = gen.NullPhotoLocationSource{PhotoLocationSource: gen.PhotoLocationSource(loc.Source), Valid: true}
	if loc.HasTrackDistanceMeters {
		trackDistance = pgtype.Float8{Float64: loc.TrackDistanceMeters, Valid: true}
	}
	return lat, lng, source, trackDistance
}
