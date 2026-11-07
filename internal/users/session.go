package users

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/users/internal/gen"
)

// Session is an unexpired session.
type Session struct {
	// The signed in user.
	User User
	// When the session expires.
	Expiry time.Time
}

// GetSession gets an unexpired session, with its user, by the session's
// token.
func (r *Repository) GetSession(ctx context.Context, token string) (*Session, error) {
	hash := sha256.Sum256([]byte(token))
	// Every signed in request gets its session, so it's read with its user in
	// a single query, without a transaction, which would take two more round
	// trips.
	row, err := r.querier.GetSessionUser(ctx, hash[:])
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	user := &PasskeyUser{id: row.ID, Name: row.Name, UserHandle: row.UserHandle, AvatarKey: row.AvatarKey.String, admin: row.IsAdmin}
	return &Session{User: user, Expiry: row.Expiry.Time}, nil
}

// DeleteSession deletes a session by its token.
func (r *Repository) DeleteSession(ctx context.Context, token string) error {
	hash := sha256.Sum256([]byte(token))
	if err := r.querier.DeleteSession(ctx, hash[:]); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.ErrNotFound
		}
		return fmt.Errorf("failed to delete session: %w", err)
	}
	return nil
}

// DeleteOtherSessions signs a user out everywhere but in the session with
// the token, and returns how many sessions it ended.
func (r *Repository) DeleteOtherSessions(ctx context.Context, userID pgtype.UUID, keepToken string) (int64, error) {
	hash := sha256.Sum256([]byte(keepToken))
	n, err := r.querier.DeleteOtherSessions(ctx, &gen.DeleteOtherSessionsParams{UserID: userID, KeepTokenSHA256: hash[:]})
	if err != nil {
		return 0, fmt.Errorf("failed to delete sessions: %w", err)
	}
	return n, nil
}

// loadUser loads a user.
func (r *Repository) loadUser(ctx context.Context, querier *gen.Queries, userID pgtype.UUID) (User, error) {
	row, err := querier.GetPasskeyUser(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return &PasskeyUser{id: row.ID, Name: row.Name, UserHandle: row.UserHandle, AvatarKey: row.AvatarKey.String, admin: row.IsAdmin}, nil
}
