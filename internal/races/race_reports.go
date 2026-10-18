package races

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/races/internal/gen"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// ErrSlugTaken is returned when another of the user's race reports has the
// slug.
var ErrSlugTaken = errors.New("the slug is taken")

// RaceReport is the story of a race, with its result.
type RaceReport struct {
	ID     pgtype.UUID
	UserID pgtype.UUID
	// Invalid if the race or activity was deleted, or there was none.
	RaceID     pgtype.UUID
	ActivityID pgtype.UUID
	Title      string
	Slug       string
	// Copied from the race, so the report stands on its own.
	RaceName            string
	RaceDate            time.Time
	DistanceMeters      float64
	ElevationGainMeters float64
	// The finish time, 0 if unknown or the runner did not finish, and the
	// placement as free text.
	FinishSeconds int32
	Placement     string
	// Whether the runner did not finish (DNF), and if so where and when they
	// stopped.
	DidNotFinish bool
	Stop         RaceStop
	// The story, in sections.
	Sections RaceReportSections
	// A copy of the activity's track, an encoded activities.v1.ActivityStreams
	// message, nil if there is none. Segments are distances along it.
	Track []byte
	// Whether the report is published, and when it was first published.
	Published   bool
	PublishTime time.Time
	CreateTime  time.Time
	UpdateTime  time.Time
}

// RaceReportSegment is a stretch of a race report's track the runner
// comments on.
type RaceReportSegment struct {
	Title string
	// Plain text, with paragraphs separated by blank lines.
	Body string
	// Where the stretch starts and ends along the report's track.
	StartDistanceMeters, EndDistanceMeters float64
}

// RaceStop is where and when a runner who did not finish stopped. Zero
// values are unknown.
type RaceStop struct {
	// The distance along the course from the start.
	DistanceMeters float64
	// The name of the checkpoint or place.
	Checkpoint string
	// The elapsed time.
	Seconds int32
}

// RaceReportSections are the sections of a race report's story, as plain
// text with paragraphs separated by blank lines.
type RaceReportSections struct {
	Summary     string
	Preparation string
	RaceDay     string
	Nutrition   string
	WentWell    string
	WentBadly   string
	Lessons     string
}

// CreateRaceReport stores a new race report. It returns ErrSlugTaken if
// another of the user's race reports has the slug.
func (r *Repository) CreateRaceReport(ctx context.Context, report *RaceReport) (*RaceReport, error) {
	row, err := r.q.CreateRaceReport(ctx, &gen.CreateRaceReportParams{
		UserID:              report.UserID,
		RaceID:              report.RaceID,
		ActivityID:          report.ActivityID,
		Title:               report.Title,
		Slug:                report.Slug,
		RaceName:            report.RaceName,
		RaceDate:            store.DateToPG(report.RaceDate),
		DistanceMeters:      report.DistanceMeters,
		ElevationGainMeters: report.ElevationGainMeters,
		FinishSeconds:       report.FinishSeconds,
		Track:               report.Track,
	})
	if err != nil {
		if isUniqueViolation(err, "race_reports_user_id_slug_key") {
			return nil, ErrSlugTaken
		}
		return nil, fmt.Errorf("failed to create race report: %w", err)
	}
	return raceReportFromDB(row), nil
}

// GetRaceReport gets one of the user's race reports.
func (r *Repository) GetRaceReport(ctx context.Context, userID, reportID pgtype.UUID) (*RaceReport, error) {
	row, err := r.q.GetRaceReport(ctx, &gen.GetRaceReportParams{ID: reportID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get race report: %w", err)
	}
	return raceReportFromDB(row), nil
}

// GetRaceReportBySlug gets one of the user's race reports by its slug.
func (r *Repository) GetRaceReportBySlug(ctx context.Context, userID pgtype.UUID, slug string) (*RaceReport, error) {
	row, err := r.q.GetRaceReportBySlug(ctx, &gen.GetRaceReportBySlugParams{UserID: userID, Slug: slug})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get race report: %w", err)
	}
	return raceReportFromDB(row), nil
}

// RaceReportFilter limits the race reports listed.
type RaceReportFilter struct {
	PublishedOnly bool
	// Only the reports on this race, if valid.
	RaceID pgtype.UUID
}

// ListRaceReports lists the user's race reports, most recent race first.
func (r *Repository) ListRaceReports(ctx context.Context, userID pgtype.UUID, filter RaceReportFilter) ([]*RaceReport, error) {
	rows, err := r.q.ListRaceReports(ctx, &gen.ListRaceReportsParams{
		UserID:        userID,
		PublishedOnly: filter.PublishedOnly,
		RaceID:        filter.RaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list race reports: %w", err)
	}
	reports := make([]*RaceReport, len(rows))
	for i, row := range rows {
		reports[i] = raceReportFromDB(row)
	}
	return reports, nil
}

// RaceReportUpdate is a change to a race report. Nil fields are unchanged.
type RaceReportUpdate struct {
	ID, UserID  pgtype.UUID
	Title, Slug *string
	// SetActivity sets the activity to ActivityID, which may be invalid to
	// unset it, and the track to Track, its track.
	SetActivity   bool
	ActivityID    pgtype.UUID
	Track         []byte
	FinishSeconds *int32
	Placement     *string
	Summary       *string
	Preparation   *string
	RaceDay       *string
	Nutrition     *string
	WentWell      *string
	WentBadly     *string
	Lessons       *string
	DidNotFinish  *bool
	// The fields of the stop to change.
	StopDistanceMeters *float64
	StopCheckpoint     *string
	StopSeconds        *int32
}

// UpdateRaceReport updates one of the user's race reports. It returns
// ErrSlugTaken if another of the user's race reports has the slug.
func (r *Repository) UpdateRaceReport(ctx context.Context, u *RaceReportUpdate) (*RaceReport, error) {
	text := func(s *string) pgtype.Text {
		if s == nil {
			return pgtype.Text{}
		}
		return pgtype.Text{String: *s, Valid: true}
	}
	params := &gen.UpdateRaceReportParams{
		ID:          u.ID,
		UserID:      u.UserID,
		Title:       text(u.Title),
		Slug:        text(u.Slug),
		SetActivity: u.SetActivity,
		ActivityID:  u.ActivityID,
		Track:       u.Track,
		Placement:   text(u.Placement),
		Summary:     text(u.Summary),
		Preparation: text(u.Preparation),
		RaceDay:     text(u.RaceDay),
		Nutrition:   text(u.Nutrition),
		WentWell:    text(u.WentWell),
		WentBadly:   text(u.WentBadly),
		Lessons:     text(u.Lessons),
	}
	if u.FinishSeconds != nil {
		params.FinishSeconds = pgtype.Int4{Int32: *u.FinishSeconds, Valid: true}
	}
	if u.DidNotFinish != nil {
		params.DidNotFinish = pgtype.Bool{Bool: *u.DidNotFinish, Valid: true}
	}
	if u.StopDistanceMeters != nil {
		params.StopDistanceMeters = pgtype.Float8{Float64: *u.StopDistanceMeters, Valid: true}
	}
	params.StopCheckpoint = text(u.StopCheckpoint)
	if u.StopSeconds != nil {
		params.StopSeconds = pgtype.Int4{Int32: *u.StopSeconds, Valid: true}
	}
	row, err := r.q.UpdateRaceReport(ctx, params)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil, store.ErrNotFound
		case isUniqueViolation(err, "race_reports_user_id_slug_key"):
			return nil, ErrSlugTaken
		}
		return nil, fmt.Errorf("failed to update race report: %w", err)
	}
	return raceReportFromDB(row), nil
}

// SetRaceReportPublished publishes or unpublishes one of the user's race
// reports, keeping the time it was first published.
func (r *Repository) SetRaceReportPublished(ctx context.Context, userID, reportID pgtype.UUID, published bool) (*RaceReport, error) {
	row, err := r.q.SetRaceReportPublished(ctx, &gen.SetRaceReportPublishedParams{
		ID:        reportID,
		UserID:    userID,
		Published: published,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to publish race report: %w", err)
	}
	return raceReportFromDB(row), nil
}

// SetRaceReportTrack stores the track of one of the user's race reports
// made before reports had one. It leaves a report with a track as it is.
func (r *Repository) SetRaceReportTrack(ctx context.Context, userID, reportID pgtype.UUID, track []byte) error {
	if err := r.q.SetRaceReportTrack(ctx, &gen.SetRaceReportTrackParams{ID: reportID, UserID: userID, Track: track}); err != nil {
		return fmt.Errorf("failed to set race report track: %w", err)
	}
	return nil
}

// ListRaceReportSegments lists the segments of a race report, in order.
// The caller checks the report is the user's.
func (r *Repository) ListRaceReportSegments(ctx context.Context, reportID pgtype.UUID) ([]RaceReportSegment, error) {
	rows, err := r.q.ListRaceReportSegments(ctx, reportID)
	if err != nil {
		return nil, fmt.Errorf("failed to list race report segments: %w", err)
	}
	segments := make([]RaceReportSegment, len(rows))
	for i, row := range rows {
		segments[i] = RaceReportSegment{
			Title:               row.Title,
			Body:                row.Body,
			StartDistanceMeters: row.StartDistanceMeters,
			EndDistanceMeters:   row.EndDistanceMeters,
		}
	}
	return segments, nil
}

// SetRaceReportSegments replaces the segments of a race report, in one
// round trip. The caller checks the report is the user's.
func (r *Repository) SetRaceReportSegments(ctx context.Context, reportID pgtype.UUID, segments []RaceReportSegment) error {
	params := &gen.InsertRaceReportSegmentsParams{ReportID: reportID}
	for i, s := range segments {
		params.Positions = append(params.Positions, int32(i))
		params.Titles = append(params.Titles, s.Title)
		params.Bodies = append(params.Bodies, s.Body)
		params.StartDistancesMeters = append(params.StartDistancesMeters, s.StartDistanceMeters)
		params.EndDistancesMeters = append(params.EndDistancesMeters, s.EndDistanceMeters)
	}
	var batch store.Batch
	q := gen.New(&batch)
	if err := q.DeleteRaceReportSegments(ctx, reportID); err != nil {
		return err
	}
	if len(segments) > 0 {
		if err := q.InsertRaceReportSegments(ctx, params); err != nil {
			return err
		}
	}
	if err := q.TouchRaceReport(ctx, reportID); err != nil {
		return err
	}
	if err := batch.Send(ctx, r.db); err != nil {
		return fmt.Errorf("failed to set race report segments: %w", err)
	}
	return nil
}

// DeleteRaceReport deletes one of the user's race reports and its photos.
// It returns the photos' object keys, whose images should be deleted.
func (r *Repository) DeleteRaceReport(ctx context.Context, userID, reportID pgtype.UUID) ([]string, error) {
	rows, err := r.q.DeleteRaceReport(ctx, &gen.DeleteRaceReportParams{ID: reportID, UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("failed to delete race report: %w", err)
	}
	// A deleted report has a row even without photos.
	if len(rows) == 0 {
		return nil, store.ErrNotFound
	}
	var keys []string
	for _, key := range rows {
		if key.Valid {
			keys = append(keys, key.String)
		}
	}
	return keys, nil
}

func raceReportFromDB(row *gen.RaceReport) *RaceReport {
	return &RaceReport{
		ID:                  row.ID,
		UserID:              row.UserID,
		RaceID:              row.RaceID,
		ActivityID:          row.ActivityID,
		Title:               row.Title,
		Slug:                row.Slug,
		RaceName:            row.RaceName,
		RaceDate:            store.DateFromPG(row.RaceDate),
		DistanceMeters:      row.DistanceMeters,
		ElevationGainMeters: row.ElevationGainMeters,
		FinishSeconds:       row.FinishSeconds,
		Placement:           row.Placement,
		DidNotFinish:        row.DidNotFinish,
		Stop: RaceStop{
			DistanceMeters: row.StopDistanceMeters,
			Checkpoint:     row.StopCheckpoint,
			Seconds:        row.StopSeconds,
		},
		Sections: RaceReportSections{
			Summary:     row.Summary,
			Preparation: row.Preparation,
			RaceDay:     row.RaceDay,
			Nutrition:   row.Nutrition,
			WentWell:    row.WentWell,
			WentBadly:   row.WentBadly,
			Lessons:     row.Lessons,
		},
		Track:       row.Track,
		Published:   row.Published,
		PublishTime: row.PublishTime.Time,
		CreateTime:  row.CreateTime.Time,
		UpdateTime:  row.UpdateTime.Time,
	}
}

// isUniqueViolation returns whether err is a violation of the unique
// constraint.
func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
