package trips

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/trips/internal/gen"
)

// PhotoUploadLink is a link AI agents upload the user's photos to a trip or
// race report with. They're stored here, with the public profiles, as both
// kinds of reports use them.
type PhotoUploadLink struct {
	ID     pgtype.UUID
	UserID pgtype.UUID
	// The SHA-256 hash of the link's secret token.
	TokenSHA256 []byte
	// The report the link uploads to: exactly one is valid.
	TripReportID pgtype.UUID
	RaceReportID pgtype.UUID
	ExpireTime   time.Time
	CreateTime   time.Time
}

// CreatePhotoUploadLink stores a new upload link, and deletes the user's
// expired ones. The caller must check that the report is the user's.
func (r *Repository) CreatePhotoUploadLink(ctx context.Context, link *PhotoUploadLink) (*PhotoUploadLink, error) {
	row, err := r.q.CreatePhotoUploadLink(ctx, &gen.CreatePhotoUploadLinkParams{
		UserID:       link.UserID,
		TokenSHA256:  link.TokenSHA256,
		TripReportID: link.TripReportID,
		RaceReportID: link.RaceReportID,
		ExpireTime:   pgtype.Timestamptz{Time: link.ExpireTime, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create photo upload link: %w", err)
	}
	return photoUploadLinkFromDB(row), nil
}

// GetPhotoUploadLink gets the upload link with the hash of a token, unless
// it has expired.
func (r *Repository) GetPhotoUploadLink(ctx context.Context, tokenSHA256 []byte) (*PhotoUploadLink, error) {
	row, err := r.q.GetPhotoUploadLinkByToken(ctx, tokenSHA256)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get photo upload link: %w", err)
	}
	return photoUploadLinkFromDB(row), nil
}

func photoUploadLinkFromDB(row *gen.PhotoUploadLink) *PhotoUploadLink {
	return &PhotoUploadLink{
		ID:           row.ID,
		UserID:       row.UserID,
		TokenSHA256:  row.TokenSHA256,
		TripReportID: row.TripReportID,
		RaceReportID: row.RaceReportID,
		ExpireTime:   row.ExpireTime.Time,
		CreateTime:   row.CreateTime.Time,
	}
}
