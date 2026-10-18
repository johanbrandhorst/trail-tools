package users

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/users/internal/gen"
)

// GetSession gets the user of an unexpired session by the session's token.
func (r *Repository) GetSession(ctx context.Context, token string) (User, error) {
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
	return &PasskeyUser{id: row.ID, Name: row.Name, UserHandle: row.UserHandle, AvatarKey: row.AvatarKey.String, admin: row.IsAdmin}, nil
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
