// Package trips stores trip reports, their photos and the public profiles
// they are published under.
package trips

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/trips/internal/gen"
)

var (
	// ErrHandleTaken is returned when another user has the handle.
	ErrHandleTaken = errors.New("the handle is taken")
	// ErrSlugTaken is returned when another of the user's reports has the
	// slug.
	ErrSlugTaken = errors.New("the slug is taken")
	// ErrNotReportPhoto is returned when choosing a cover photo that isn't
	// one of the report's photos.
	ErrNotReportPhoto = errors.New("the photo isn't one of the report's photos")
	// ErrPhotosMismatch is returned when setting the sections of a report
	// with photos that aren't the report's, or with a photo twice.
	ErrPhotosMismatch = errors.New("the photos don't match the report's photos")
	// ErrSectionsMismatch is returned when setting the sections of a report
	// with a section of another report, or with a section twice.
	ErrSectionsMismatch = errors.New("the sections don't match the report's sections")
	// ErrPhotoImported is returned when adding a photo from Strava that the
	// report already has.
	ErrPhotoImported = errors.New("the photo was already imported")
)

// Repository stores trip reports, photos and public profiles.
type Repository struct {
	log *slog.Logger
	db  *store.DB
	q   *gen.Queries
}

// NewRepository creates a new Repository from the provided logger and database.
func NewRepository(log *slog.Logger, db *store.DB) (*Repository, error) {
	return &Repository{
		log: log,
		db:  db,
		q:   gen.New(db),
	}, nil
}

// Profile is a user's public profile.
type Profile struct {
	UserID      pgtype.UUID
	Handle      string
	DisplayName string
}

// Report is a trip report.
type Report struct {
	ID         pgtype.UUID
	UserID     pgtype.UUID
	ActivityID pgtype.UUID
	Title      string
	Slug       string
	// The local date of the trip.
	Date      time.Time
	Timezone  string
	SportType string
	// The start time of the activity, zero if unknown.
	StartTime           time.Time
	DistanceMeters      float64
	MovingTimeSeconds   int32
	ElapsedTimeSeconds  int32
	ElevationGainMeters float64
	// A protobuf encoded activities.v1.ActivityStreams message. Nil if the
	// report has no track, or it wasn't loaded.
	Track    []byte
	HasTrack bool
	// The photo shown on top of the report, if one was chosen. Use
	// CoverPhoto for the photo to show.
	CoverPhotoID pgtype.UUID
	// The tags, the names of trips.v1.TripTag without the prefix in
	// lowercase, e.g. "beginner_friendly".
	Tags []string
	// Whether the report is published, and when it was first published.
	Published   bool
	PublishTime time.Time
	CreateTime  time.Time
	UpdateTime  time.Time
}

// LocationSource is where the location of a photo comes from.
type LocationSource string

const (
	LocationGPS       LocationSource = "gps"
	LocationTrackTime LocationSource = "track_time"
	LocationManual    LocationSource = "manual"
)

// Location is where a photo was taken.
type Location struct {
	Lat, Lng float64
	Source   LocationSource
	// The distance along the report's track of the point nearest to the
	// location, if the report has a track.
	TrackDistanceMeters    float64
	HasTrackDistanceMeters bool
}

// Photo is a photo of a trip report.
type Photo struct {
	ID       pgtype.UUID
	ReportID pgtype.UUID
	// The section of the story the photo is in.
	SectionID pgtype.UUID
	// The order of the photo within its section.
	Position int32
	// The images are stored under <ObjectKey>/<size>.jpg.
	ObjectKey string
	Width     int32
	Height    int32
	Caption   string
	// Nil if the photo has no location.
	Location *Location
	// Zero if unknown.
	TakenTime time.Time
	// Strava's ID of the photo, if it was imported from Strava.
	StravaPhotoID string
	CreateTime    time.Time
}

// Section is a section of a report's story, like a chapter of a blog post.
type Section struct {
	// Invalid for a new section.
	ID       pgtype.UUID
	ReportID pgtype.UUID
	// Empty for a section without a heading, e.g. an introduction.
	Title string
	// Plain text, with paragraphs separated by blank lines.
	Body string
	// The stretch of the track the section is about, if HasDistances.
	StartDistanceMeters, EndDistanceMeters float64
	HasDistances                           bool
	// The IDs of the section's photos in order, when setting sections.
	PhotoIDs []pgtype.UUID
}

// Contains returns whether the section is about the stretch of the track at
// the distance.
func (s *Section) Contains(distanceMeters float64) bool {
	return s.HasDistances && distanceMeters >= s.StartDistanceMeters && distanceMeters <= s.EndDistanceMeters
}

// PhotoOrder reports whether photo a goes before photo b in a section.
type PhotoOrder func(a, b *Photo) bool

// SectionFor returns the section a photo taken at the location belongs in:
// the first section about the stretch of the track where it was taken, or
// else the last section. It returns nil if there are no sections.
func SectionFor(sections []*Section, loc *Location) *Section {
	if len(sections) == 0 {
		return nil
	}
	if loc != nil && loc.HasTrackDistanceMeters {
		for _, s := range sections {
			if s.Contains(loc.TrackDistanceMeters) {
				return s
			}
		}
	}
	return sections[len(sections)-1]
}

// GetProfile gets the user's public profile.
func (r *Repository) GetProfile(ctx context.Context, userID pgtype.UUID) (*Profile, error) {
	row, err := r.q.GetProfile(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get profile: %w", err)
	}
	return &Profile{UserID: row.UserID, Handle: row.Handle, DisplayName: row.DisplayName}, nil
}

// GetProfileByHandle gets a public profile by its handle.
func (r *Repository) GetProfileByHandle(ctx context.Context, handle string) (*Profile, error) {
	row, err := r.q.GetProfileByHandle(ctx, handle)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get profile: %w", err)
	}
	return &Profile{UserID: row.UserID, Handle: row.Handle, DisplayName: row.DisplayName}, nil
}

// UpsertProfile creates or updates the user's public profile. It returns
// ErrHandleTaken if another user has the handle.
func (r *Repository) UpsertProfile(ctx context.Context, p *Profile) (*Profile, error) {
	row, err := r.q.UpsertProfile(ctx, &gen.UpsertProfileParams{
		UserID:      p.UserID,
		Handle:      p.Handle,
		DisplayName: p.DisplayName,
	})
	if err != nil {
		if isUniqueViolation(err, "public_profiles_handle_key") {
			return nil, ErrHandleTaken
		}
		return nil, fmt.Errorf("failed to update profile: %w", err)
	}
	return &Profile{UserID: row.UserID, Handle: row.Handle, DisplayName: row.DisplayName}, nil
}

// GetUserName returns the name the user signed up with.
func (r *Repository) GetUserName(ctx context.Context, userID pgtype.UUID) (string, error) {
	name, err := r.q.GetUserName(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", store.ErrNotFound
		}
		return "", fmt.Errorf("failed to get user name: %w", err)
	}
	return name, nil
}

// CreateReport creates a trip report, with a first section with the text.
// It returns ErrSlugTaken if another of the user's reports has the slug.
func (r *Repository) CreateReport(ctx context.Context, report *Report, body string) (*Report, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := r.q.WithTx(tx)
	row, err := q.CreateReport(ctx, &gen.CreateReportParams{
		UserID:              report.UserID,
		ActivityID:          report.ActivityID,
		Title:               report.Title,
		Slug:                report.Slug,
		Date:                store.DateToPG(report.Date),
		Timezone:            report.Timezone,
		SportType:           report.SportType,
		StartTime:           timestampToPG(report.StartTime),
		DistanceMeters:      report.DistanceMeters,
		MovingTimeSeconds:   report.MovingTimeSeconds,
		ElapsedTimeSeconds:  report.ElapsedTimeSeconds,
		ElevationGainMeters: report.ElevationGainMeters,
		Track:               report.Track,
	})
	if err != nil {
		if isUniqueViolation(err, "trip_reports_user_id_slug_key") {
			return nil, ErrSlugTaken
		}
		return nil, fmt.Errorf("failed to create trip report: %w", err)
	}
	if _, err := q.CreateSection(ctx, &gen.CreateSectionParams{ReportID: row.ID, Body: body}); err != nil {
		return nil, fmt.Errorf("failed to create section: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return reportFromDB(row), nil
}

// GetReport gets one of the user's reports, with its track.
func (r *Repository) GetReport(ctx context.Context, userID, reportID pgtype.UUID) (*Report, error) {
	row, err := r.q.GetReport(ctx, &gen.GetReportParams{ID: reportID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get trip report: %w", err)
	}
	return reportFromDB(row), nil
}

// GetReportBySlug gets one of the user's reports by its slug, with its
// track.
func (r *Repository) GetReportBySlug(ctx context.Context, userID pgtype.UUID, slug string) (*Report, error) {
	row, err := r.q.GetReportBySlug(ctx, &gen.GetReportBySlugParams{UserID: userID, Slug: slug})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get trip report: %w", err)
	}
	return reportFromDB(row), nil
}

// ListReportsFilter filters listed reports.
type ListReportsFilter struct {
	// Only list reports made from this activity, if valid.
	ActivityID pgtype.UUID
	// Only list published reports.
	PublishedOnly bool
}

// ListReports lists the user's reports without their tracks, most recent
// trip first.
func (r *Repository) ListReports(ctx context.Context, userID pgtype.UUID, filter ListReportsFilter) ([]*Report, error) {
	rows, err := r.q.ListReports(ctx, &gen.ListReportsParams{
		UserID:        userID,
		ActivityID:    filter.ActivityID,
		PublishedOnly: filter.PublishedOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list trip reports: %w", err)
	}
	reports := make([]*Report, len(rows))
	for i, row := range rows {
		reports[i] = &Report{
			ID:                  row.ID,
			UserID:              row.UserID,
			ActivityID:          row.ActivityID,
			Title:               row.Title,
			Slug:                row.Slug,
			Date:                store.DateFromPG(row.Date),
			Timezone:            row.Timezone,
			SportType:           row.SportType,
			StartTime:           timestampFromPG(row.StartTime),
			DistanceMeters:      row.DistanceMeters,
			MovingTimeSeconds:   row.MovingTimeSeconds,
			ElapsedTimeSeconds:  row.ElapsedTimeSeconds,
			ElevationGainMeters: row.ElevationGainMeters,
			HasTrack:            row.HasTrack,
			CoverPhotoID:        row.CoverPhotoID,
			Tags:                row.Tags,
			Published:           row.Published,
			PublishTime:         timestampFromPG(row.PublishTime),
			CreateTime:          row.CreateTime.Time,
			UpdateTime:          row.UpdateTime.Time,
		}
	}
	return reports, nil
}

// ReportUpdate is an update of a report. Nil fields are unchanged.
type ReportUpdate struct {
	ID, UserID  pgtype.UUID
	Title, Slug *string
	Date        *time.Time
	// The photo shown on top of the report, or an invalid UUID to use the
	// first photo.
	CoverPhotoID *pgtype.UUID
	// The tags. Nil to leave them unchanged.
	Tags []string
}

// UpdateReport updates one of the user's reports. It returns ErrSlugTaken if
// another of the user's reports has the slug.
func (r *Repository) UpdateReport(ctx context.Context, u *ReportUpdate) (*Report, error) {
	params := &gen.UpdateReportParams{ID: u.ID, UserID: u.UserID}
	if u.Title != nil {
		params.Title = pgtype.Text{String: *u.Title, Valid: true}
	}
	if u.Slug != nil {
		params.Slug = pgtype.Text{String: *u.Slug, Valid: true}
	}
	if u.Date != nil {
		params.Date = store.DateToPG(*u.Date)
	}
	if u.CoverPhotoID != nil {
		params.SetCoverPhoto = true
		params.CoverPhotoID = *u.CoverPhotoID
	}
	if u.Tags != nil {
		params.Tags = u.Tags
	}
	row, err := r.q.UpdateReport(ctx, params)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil, store.ErrNotFound
		case isForeignKeyViolation(err, "trip_reports_cover_photo_fkey"):
			return nil, ErrNotReportPhoto
		case isUniqueViolation(err, "trip_reports_user_id_slug_key"):
			return nil, ErrSlugTaken
		}
		return nil, fmt.Errorf("failed to update trip report: %w", err)
	}
	return reportFromDB(row), nil
}

// SetReportPublished publishes or unpublishes one of the user's reports. The
// publish time is set when it is first published.
func (r *Repository) SetReportPublished(ctx context.Context, userID, reportID pgtype.UUID, published bool) (*Report, error) {
	row, err := r.q.SetReportPublished(ctx, &gen.SetReportPublishedParams{
		ID:        reportID,
		UserID:    userID,
		Published: published,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to publish trip report: %w", err)
	}
	return reportFromDB(row), nil
}

// DeleteReport deletes one of the user's reports and its photos. It returns
// the object keys of the deleted photos, whose images should be deleted.
func (r *Repository) DeleteReport(ctx context.Context, userID, reportID pgtype.UUID) ([]string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := r.q.WithTx(tx)
	exists, err := q.ReportExists(ctx, &gen.ReportExistsParams{ID: reportID, UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("failed to get trip report: %w", err)
	}
	if !exists {
		return nil, store.ErrNotFound
	}
	keys, err := q.ListPhotoObjectKeys(ctx, reportID)
	if err != nil {
		return nil, fmt.Errorf("failed to list photos: %w", err)
	}
	if _, err := q.DeleteReport(ctx, &gen.DeleteReportParams{ID: reportID, UserID: userID}); err != nil {
		return nil, fmt.Errorf("failed to delete trip report: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return keys, nil
}

// AddPhoto adds a photo to one of the user's reports, in the section about
// where it was taken, or else the last section: before the first photo it
// goes before by the order, or else last.
func (r *Repository) AddPhoto(ctx context.Context, userID pgtype.UUID, p *Photo, before PhotoOrder) (*Photo, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := r.q.WithTx(tx)
	// Lock the report, so its sections don't change meanwhile.
	if _, err := q.LockReport(ctx, &gen.LockReportParams{ID: p.ReportID, UserID: userID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get trip report: %w", err)
	}
	rows, err := q.ListSections(ctx, []pgtype.UUID{p.ReportID})
	if err != nil {
		return nil, fmt.Errorf("failed to list sections: %w", err)
	}
	sections := make([]*Section, len(rows))
	for i, row := range rows {
		sections[i] = sectionFromDB(row)
	}
	section := SectionFor(sections, p.Location)
	if section == nil {
		return nil, errors.New("the report has no sections")
	}
	// Insert the photo before the first photo of the section taken after it,
	// or else last.
	var position pgtype.Int4
	photoRows, err := q.ListPhotos(ctx, []pgtype.UUID{p.ReportID})
	if err != nil {
		return nil, fmt.Errorf("failed to list photos: %w", err)
	}
	for _, row := range photoRows {
		if existing := photoFromDB(row); existing.SectionID == section.ID && before(p, existing) {
			position = pgtype.Int4{Int32: existing.Position, Valid: true}
			break
		}
	}
	if position.Valid {
		if err := q.ShiftPhotos(ctx, &gen.ShiftPhotosParams{SectionID: section.ID, Position: position.Int32}); err != nil {
			return nil, fmt.Errorf("failed to make room for the photo: %w", err)
		}
	}
	params := &gen.AddPhotoParams{
		Position:      position,
		ReportID:      p.ReportID,
		SectionID:     section.ID,
		ObjectKey:     p.ObjectKey,
		Width:         p.Width,
		Height:        p.Height,
		Caption:       p.Caption,
		TakenTime:     timestampToPG(p.TakenTime),
		StravaPhotoID: pgtype.Text{String: p.StravaPhotoID, Valid: p.StravaPhotoID != ""},
	}
	params.Lat, params.Lng, params.LocationSource, params.TrackDistanceMeters = locationToPG(p.Location)
	row, err := q.AddPhoto(ctx, params)
	if err != nil {
		if isUniqueViolation(err, "trip_photos_report_id_strava_photo_id_key") {
			return nil, ErrPhotoImported
		}
		return nil, fmt.Errorf("failed to add photo: %w", err)
	}
	if err := q.TouchReport(ctx, p.ReportID); err != nil {
		return nil, fmt.Errorf("failed to update trip report: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return photoFromDB(row), nil
}

// ListPhotos lists the photos of reports in order. The caller must check
// that the reports can be accessed.
func (r *Repository) ListPhotos(ctx context.Context, reportIDs ...pgtype.UUID) (map[pgtype.UUID][]*Photo, error) {
	rows, err := r.q.ListPhotos(ctx, reportIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to list photos: %w", err)
	}
	photos := map[pgtype.UUID][]*Photo{}
	for _, row := range rows {
		photos[row.ReportID] = append(photos[row.ReportID], photoFromDB(row))
	}
	return photos, nil
}

// GetPhoto gets a photo of one of the user's reports.
func (r *Repository) GetPhoto(ctx context.Context, userID, photoID pgtype.UUID) (*Photo, error) {
	row, err := r.q.GetPhoto(ctx, &gen.GetPhotoParams{ID: photoID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get photo: %w", err)
	}
	return photoFromDB(row), nil
}

// PhotoAccess is what's needed to serve a photo: where it's stored, and who
// can see it.
type PhotoAccess struct {
	ObjectKey  string
	CreateTime time.Time
	// The author of the report.
	UserID pgtype.UUID
	// Whether the report is published.
	Published bool
}

// GetPhotoAccess gets what's needed to serve any photo.
func (r *Repository) GetPhotoAccess(ctx context.Context, photoID pgtype.UUID) (*PhotoAccess, error) {
	row, err := r.q.GetPhotoAccess(ctx, photoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get photo: %w", err)
	}
	return &PhotoAccess{
		ObjectKey:  row.ObjectKey,
		CreateTime: row.CreateTime.Time,
		UserID:     row.UserID,
		Published:  row.Published,
	}, nil
}

// UpdatePhotoCaption sets the caption of a photo of one of the user's
// reports.
func (r *Repository) UpdatePhotoCaption(ctx context.Context, userID, photoID pgtype.UUID, caption string) (*Photo, error) {
	row, err := r.q.UpdatePhotoCaption(ctx, &gen.UpdatePhotoCaptionParams{ID: photoID, UserID: userID, Caption: caption})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to update photo: %w", err)
	}
	return photoFromDB(row), nil
}

// UpdatePhotoLocation sets or, with a nil location, clears the location of
// a photo of one of the user's reports.
func (r *Repository) UpdatePhotoLocation(ctx context.Context, userID, photoID pgtype.UUID, loc *Location) (*Photo, error) {
	params := &gen.UpdatePhotoLocationParams{ID: photoID, UserID: userID}
	params.Lat, params.Lng, params.LocationSource, params.TrackDistanceMeters = locationToPG(loc)
	row, err := r.q.UpdatePhotoLocation(ctx, params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to update photo: %w", err)
	}
	return photoFromDB(row), nil
}

// SetSections replaces the sections of one of the user's reports, in order.
// Sections with an ID are updated, sections without one are created, and
// the report's other sections are deleted. Each section's photos move to it
// in the order given; photos that aren't listed stay in their section, or
// move to the first section if theirs is deleted. It returns
// ErrSectionsMismatch or ErrPhotosMismatch for sections or photos that
// aren't the report's or are listed twice.
func (r *Repository) SetSections(ctx context.Context, userID, reportID pgtype.UUID, sections []*Section) ([]*Section, error) {
	if len(sections) == 0 {
		return nil, errors.New("a report needs a section")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := r.q.WithTx(tx)
	if _, err := q.LockReport(ctx, &gen.LockReportParams{ID: reportID, UserID: userID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get trip report: %w", err)
	}
	current, err := q.ListSections(ctx, []pgtype.UUID{reportID})
	if err != nil {
		return nil, fmt.Errorf("failed to list sections: %w", err)
	}
	unused := map[pgtype.UUID]bool{}
	for _, row := range current {
		unused[row.ID] = true
	}
	var kept, photoIDs, photoSections []pgtype.UUID
	listed := map[pgtype.UUID]bool{}
	for i, sec := range sections {
		start := pgtype.Float8{Float64: sec.StartDistanceMeters, Valid: sec.HasDistances}
		end := pgtype.Float8{Float64: sec.EndDistanceMeters, Valid: sec.HasDistances}
		id := sec.ID
		if id.Valid {
			if !unused[id] {
				return nil, ErrSectionsMismatch
			}
			delete(unused, id)
			if _, err := q.UpdateSection(ctx, &gen.UpdateSectionParams{
				ID:                  id,
				ReportID:            reportID,
				Position:            int32(i),
				Title:               sec.Title,
				Body:                sec.Body,
				StartDistanceMeters: start,
				EndDistanceMeters:   end,
			}); err != nil {
				return nil, fmt.Errorf("failed to update section: %w", err)
			}
		} else {
			row, err := q.CreateSection(ctx, &gen.CreateSectionParams{
				ReportID:            reportID,
				Position:            int32(i),
				Title:               sec.Title,
				Body:                sec.Body,
				StartDistanceMeters: start,
				EndDistanceMeters:   end,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create section: %w", err)
			}
			id = row.ID
		}
		kept = append(kept, id)
		for _, photoID := range sec.PhotoIDs {
			if listed[photoID] {
				return nil, ErrPhotosMismatch
			}
			listed[photoID] = true
			photoIDs = append(photoIDs, photoID)
			photoSections = append(photoSections, id)
		}
	}
	if len(photoIDs) > 0 {
		n, err := q.SetPhotoSections(ctx, &gen.SetPhotoSectionsParams{
			ReportID:   reportID,
			PhotoIds:   photoIDs,
			SectionIds: photoSections,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to move photos: %w", err)
		}
		if n != int64(len(photoIDs)) {
			return nil, ErrPhotosMismatch
		}
	}
	if len(unused) > 0 {
		if err := q.MovePhotosOutOfSections(ctx, &gen.MovePhotosOutOfSectionsParams{
			SectionID: kept[0],
			ReportID:  reportID,
			KeptIds:   kept,
		}); err != nil {
			return nil, fmt.Errorf("failed to move photos: %w", err)
		}
		if err := q.DeleteSectionsExcept(ctx, &gen.DeleteSectionsExceptParams{ReportID: reportID, KeptIds: kept}); err != nil {
			return nil, fmt.Errorf("failed to delete sections: %w", err)
		}
	}
	if err := q.TouchReport(ctx, reportID); err != nil {
		return nil, fmt.Errorf("failed to update trip report: %w", err)
	}
	rows, err := q.ListSections(ctx, []pgtype.UUID{reportID})
	if err != nil {
		return nil, fmt.Errorf("failed to list sections: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	result := make([]*Section, len(rows))
	for i, row := range rows {
		result[i] = sectionFromDB(row)
	}
	return result, nil
}

// ListSections lists the sections of reports in order. The caller must
// check that the reports can be accessed.
func (r *Repository) ListSections(ctx context.Context, reportIDs ...pgtype.UUID) (map[pgtype.UUID][]*Section, error) {
	rows, err := r.q.ListSections(ctx, reportIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to list sections: %w", err)
	}
	sections := map[pgtype.UUID][]*Section{}
	for _, row := range rows {
		sections[row.ReportID] = append(sections[row.ReportID], sectionFromDB(row))
	}
	return sections, nil
}

// ListExcerpts returns the text of the first section with text of each of
// the reports, if they have one. The caller must check that the reports can
// be accessed.
func (r *Repository) ListExcerpts(ctx context.Context, reportIDs ...pgtype.UUID) (map[pgtype.UUID]string, error) {
	rows, err := r.q.ListExcerpts(ctx, reportIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to list excerpts: %w", err)
	}
	excerpts := make(map[pgtype.UUID]string, len(rows))
	for _, row := range rows {
		excerpts[row.ReportID] = row.Body
	}
	return excerpts, nil
}

// DeletePhoto deletes a photo of one of the user's reports. It returns the
// photo's object key, whose images should be deleted.
func (r *Repository) DeletePhoto(ctx context.Context, userID, photoID pgtype.UUID) (string, error) {
	key, err := r.q.DeletePhoto(ctx, &gen.DeletePhotoParams{ID: photoID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", store.ErrNotFound
		}
		return "", fmt.Errorf("failed to delete photo: %w", err)
	}
	return key, nil
}

func reportFromDB(row *gen.TripReport) *Report {
	return &Report{
		ID:                  row.ID,
		UserID:              row.UserID,
		ActivityID:          row.ActivityID,
		Title:               row.Title,
		Slug:                row.Slug,
		Date:                store.DateFromPG(row.Date),
		Timezone:            row.Timezone,
		SportType:           row.SportType,
		StartTime:           timestampFromPG(row.StartTime),
		DistanceMeters:      row.DistanceMeters,
		MovingTimeSeconds:   row.MovingTimeSeconds,
		ElapsedTimeSeconds:  row.ElapsedTimeSeconds,
		ElevationGainMeters: row.ElevationGainMeters,
		Track:               row.Track,
		HasTrack:            row.Track != nil,
		CoverPhotoID:        row.CoverPhotoID,
		Tags:                row.Tags,
		Published:           row.Published,
		PublishTime:         timestampFromPG(row.PublishTime),
		CreateTime:          row.CreateTime.Time,
		UpdateTime:          row.UpdateTime.Time,
	}
}

func sectionFromDB(row *gen.TripReportSection) *Section {
	return &Section{
		ID:                  row.ID,
		ReportID:            row.ReportID,
		Title:               row.Title,
		Body:                row.Body,
		StartDistanceMeters: row.StartDistanceMeters.Float64,
		EndDistanceMeters:   row.EndDistanceMeters.Float64,
		HasDistances:        row.StartDistanceMeters.Valid && row.EndDistanceMeters.Valid,
	}
}

func photoFromDB(row *gen.TripPhoto) *Photo {
	p := &Photo{
		ID:            row.ID,
		ReportID:      row.ReportID,
		SectionID:     row.SectionID,
		Position:      row.Position,
		ObjectKey:     row.ObjectKey,
		Width:         row.Width,
		Height:        row.Height,
		Caption:       row.Caption,
		TakenTime:     timestampFromPG(row.TakenTime),
		StravaPhotoID: row.StravaPhotoID.String,
		CreateTime:    row.CreateTime.Time,
	}
	if row.Lat.Valid && row.Lng.Valid && row.LocationSource.Valid {
		p.Location = &Location{
			Lat:                    row.Lat.Float64,
			Lng:                    row.Lng.Float64,
			Source:                 LocationSource(row.LocationSource.PhotoLocationSource),
			TrackDistanceMeters:    row.TrackDistanceMeters.Float64,
			HasTrackDistanceMeters: row.TrackDistanceMeters.Valid,
		}
	}
	return p
}

func locationToPG(loc *Location) (lat, lng pgtype.Float8, source gen.NullPhotoLocationSource, trackDistance pgtype.Float8) {
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

func timestampToPG(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func timestampFromPG(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

// isUniqueViolation returns whether err is a violation of the unique
// constraint.
func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

func isForeignKeyViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == constraint
}

// CoverPhoto returns the photo shown on top of a report: the chosen one, or
// the first photo. photos are the report's photos in order. It returns nil
// for a report without photos.
func CoverPhoto(r *Report, photos []*Photo) *Photo {
	for _, p := range photos {
		if p.ID == r.CoverPhotoID {
			return p
		}
	}
	if len(photos) == 0 {
		return nil
	}
	return photos[0]
}
