package lactate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/lactate/internal/gen"
	"github.com/bs-tools/trailrunn.ing/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Share is a link that lets someone record a test for the user, like a
// friend holding the lactate meter, without signing in. The first browser
// that opens it records with it.
type Share struct {
	ID     pgtype.UUID
	UserID pgtype.UUID
	// The SHA-256 hash of the secret of the browser that opened the link,
	// nil until one does.
	RecorderSHA256 []byte
	// The recorded test, invalid until the first reading is saved.
	TestID pgtype.UUID
	// The workout the test is for, invalid if none. The recorded test is
	// linked to it.
	WorkoutID  pgtype.UUID
	ExpireTime time.Time
	CreateTime time.Time
}

// Recording is a share opened by a browser, with the name of the user the
// test is recorded for.
type Recording struct {
	Share
	OwnerName string
}

// CreateShare stores a new share with the SHA-256 hash of its link's token,
// for a test run in a workout if workoutID is valid.
func (r *Repository) CreateShare(ctx context.Context, userID pgtype.UUID, tokenSHA256 []byte, expireTime time.Time, workoutID pgtype.UUID) (*Share, error) {
	if err := r.checkWorkout(ctx, userID, workoutID); err != nil {
		return nil, err
	}
	s, err := r.q.CreateLactateTestShare(ctx, &gen.CreateLactateTestShareParams{
		UserID:      userID,
		TokenSHA256: tokenSHA256,
		ExpireTime:  pgtype.Timestamptz{Time: expireTime, Valid: true},
		WorkoutID:   workoutID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create lactate test share: %w", err)
	}
	return shareFromDB(s), nil
}

// ListShares lists the user's shares that haven't expired, the latest first.
func (r *Repository) ListShares(ctx context.Context, userID pgtype.UUID) ([]*Share, error) {
	rows, err := r.q.ListLactateTestShares(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list lactate test shares: %w", err)
	}
	shares := make([]*Share, len(rows))
	for i, row := range rows {
		shares[i] = shareFromDB(row)
	}
	return shares, nil
}

// DeleteShare deletes one of the user's shares. A test it recorded is kept.
func (r *Repository) DeleteShare(ctx context.Context, userID, shareID pgtype.UUID) error {
	n, err := r.q.DeleteLactateTestShare(ctx, &gen.DeleteLactateTestShareParams{ID: shareID, UserID: userID})
	if err != nil {
		return fmt.Errorf("failed to delete lactate test share: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// GetShareByToken gets the share whose link's token has the SHA-256 hash,
// unless it expired.
func (r *Repository) GetShareByToken(ctx context.Context, tokenSHA256 []byte) (*Share, error) {
	s, err := r.q.GetLactateTestShareByToken(ctx, tokenSHA256)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get lactate test share: %w", err)
	}
	return shareFromDB(s), nil
}

// OpenShare records the browser that opened a share, by the SHA-256 hash of
// its secret. It returns false if another browser opened it first, or it
// expired.
func (r *Repository) OpenShare(ctx context.Context, shareID pgtype.UUID, recorderSHA256 []byte) (bool, error) {
	n, err := r.q.OpenLactateTestShare(ctx, &gen.OpenLactateTestShareParams{ID: shareID, RecorderSHA256: recorderSHA256})
	if err != nil {
		return false, fmt.Errorf("failed to open lactate test share: %w", err)
	}
	return n == 1, nil
}

// GetRecording gets the share a browser opened, by the SHA-256 hash of its
// secret, unless it expired.
func (r *Repository) GetRecording(ctx context.Context, recorderSHA256 []byte) (*Recording, error) {
	row, err := r.q.GetLactateTestRecording(ctx, recorderSHA256)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get lactate test recording: %w", err)
	}
	return &Recording{
		Share: *shareFromDB(&gen.LactateTestShare{
			ID:             row.ID,
			UserID:         row.UserID,
			TokenSHA256:    row.TokenSHA256,
			RecorderSHA256: row.RecorderSHA256,
			TestID:         row.TestID,
			WorkoutID:      row.WorkoutID,
			ExpireTime:     row.ExpireTime,
			CreateTime:     row.CreateTime,
		}),
		OwnerName: row.OwnerName,
	}, nil
}

// SetShareTest records the test a share recorded.
func (r *Repository) SetShareTest(ctx context.Context, shareID, testID pgtype.UUID) error {
	if err := r.q.SetLactateTestShareTest(ctx, &gen.SetLactateTestShareTestParams{ID: shareID, TestID: testID}); err != nil {
		return fmt.Errorf("failed to set the recorded lactate test: %w", err)
	}
	return nil
}

func shareFromDB(s *gen.LactateTestShare) *Share {
	return &Share{
		ID:             s.ID,
		UserID:         s.UserID,
		RecorderSHA256: s.RecorderSHA256,
		TestID:         s.TestID,
		WorkoutID:      s.WorkoutID,
		ExpireTime:     s.ExpireTime.Time,
		CreateTime:     s.CreateTime.Time,
	}
}
