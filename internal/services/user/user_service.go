package user

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/blob"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/gen/users/v1/usersv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/users"
)

var _ usersv1connect.UserServiceHandler = (*Service)(nil)

type APITokenRepository interface {
	CreateAPIToken(ctx context.Context, userID pgtype.UUID, name string) (*users.APIToken, string, error)
	ListAPITokens(ctx context.Context, userID pgtype.UUID) ([]*users.APIToken, error)
	DeleteAPIToken(ctx context.Context, userID pgtype.UUID, tokenID pgtype.UUID) error
}

type AvatarRepository interface {
	SetAvatarKey(ctx context.Context, userID pgtype.UUID, key string) (string, error)
}

// Service implements API handlers for the user service.
// PrivacyZoneRepository stores privacy zones.
type PrivacyZoneRepository interface {
	ListPrivacyZones(ctx context.Context, userID pgtype.UUID) ([]users.PrivacyZone, error)
	SetPrivacyZones(ctx context.Context, userID pgtype.UUID, zones []users.PrivacyZone) error
}

type Service struct {
	log     *slog.Logger
	report  *errreport.Reporter
	tokens  APITokenRepository
	avatars AvatarRepository
	zones   PrivacyZoneRepository
	blobs   blob.Store
}

// NewService creates a new Service from the provided logger, repositories
// and the blob store avatar images are stored in.
func NewService(log *slog.Logger, report *errreport.Reporter, tokens APITokenRepository, avatars AvatarRepository, zones PrivacyZoneRepository, blobs blob.Store) *Service {
	return &Service{
		log:     log,
		report:  report,
		tokens:  tokens,
		avatars: avatars,
		zones:   zones,
		blobs:   blobs,
	}
}
