package users

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/users/internal/gen"
)

// SetAvatarKey sets the key of the user's avatar image, or removes the
// avatar if key is empty. It returns the key of the replaced avatar, if any,
// so its image can be deleted.
func (r *Repository) SetAvatarKey(ctx context.Context, userID pgtype.UUID, key string) (string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(context.Background())
	q := r.querier.WithTx(tx)
	old, err := q.GetAvatarKeyForUpdate(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", store.ErrNotFound
		}
		return "", fmt.Errorf("failed to get avatar: %w", err)
	}
	if err := q.SetAvatarKey(ctx, &gen.SetAvatarKeyParams{ID: userID, AvatarKey: pgtype.Text{String: key, Valid: key != ""}}); err != nil {
		return "", fmt.Errorf("failed to set avatar: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("failed to commit transaction: %w", err)
	}
	return old.String, nil
}
