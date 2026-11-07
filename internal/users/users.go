package users

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/users/internal/gen"
)

type User interface {
	ID() pgtype.UUID
	// IsAdmin returns whether the user is an administrator, who can invite
	// new users.
	IsAdmin() bool
}

// Repository allows storing and querying of users and related data.
type Repository struct {
	log     *slog.Logger
	querier *gen.Queries
	db      *store.DB
}

// NewRepository creates a new Repository from the provided logger and database.
func NewRepository(log *slog.Logger, db *store.DB) (*Repository, error) {
	return &Repository{
		log:     log,
		querier: gen.New(db),
		db:      db,
	}, nil
}
