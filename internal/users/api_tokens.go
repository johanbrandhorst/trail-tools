package users

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/users/internal/gen"
)

// apiTokenPrefix identifies API token secrets, so they can be recognised if
// leaked.
const apiTokenPrefix = "tt_"

// APIToken is a token used to authenticate API clients, such as MCP clients.
type APIToken struct {
	*gen.APIToken
}

// CreateAPIToken creates a new API token for the user. It returns the token
// and its secret. Only a hash of the secret is stored.
func (r *Repository) CreateAPIToken(ctx context.Context, userID pgtype.UUID, name string) (*APIToken, string, error) {
	secret := apiTokenPrefix + rand.Text()
	hash := sha256.Sum256([]byte(secret))
	token, err := r.querier.CreateAPIToken(ctx, &gen.CreateAPITokenParams{
		UserID:       userID,
		Name:         name,
		SecretSHA256: hash[:],
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to create API token: %w", err)
	}
	return &APIToken{APIToken: token}, secret, nil
}

// ListAPITokens lists the user's API tokens, newest first.
func (r *Repository) ListAPITokens(ctx context.Context, userID pgtype.UUID) ([]*APIToken, error) {
	tokens, err := r.querier.ListAPITokens(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list API tokens: %w", err)
	}
	result := make([]*APIToken, len(tokens))
	for i, token := range tokens {
		result[i] = &APIToken{APIToken: token}
	}
	return result, nil
}

// DeleteAPIToken deletes one of the user's API tokens.
func (r *Repository) DeleteAPIToken(ctx context.Context, userID pgtype.UUID, tokenID pgtype.UUID) error {
	n, err := r.querier.DeleteAPIToken(ctx, &gen.DeleteAPITokenParams{
		ID:     tokenID,
		UserID: userID,
	})
	if err != nil {
		return fmt.Errorf("failed to delete API token: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// GetAPITokenUser returns the user owning the API token with the provided
// secret, and records that the token was used. It returns store.ErrNotFound if
// the secret does not match a token.
func (r *Repository) GetAPITokenUser(ctx context.Context, secret string) (User, error) {
	if !strings.HasPrefix(secret, apiTokenPrefix) {
		return nil, store.ErrNotFound
	}
	hash := sha256.Sum256([]byte(secret))
	userID, err := r.querier.UseAPIToken(ctx, hash[:])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get API token: %w", err)
	}
	return r.loadUser(ctx, r.querier, userID)
}
