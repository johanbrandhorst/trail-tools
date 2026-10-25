package store

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func UUIDToString(in pgtype.UUID) string {
	if !in.Valid {
		return ""
	}
	s, _ := in.Value()
	return s.(string)
}

func StringToUUID(in string) (pgtype.UUID, error) {
	u := pgtype.UUID{}
	if err := u.Scan(in); err != nil {
		return pgtype.UUID{}, err
	}
	return u, nil
}

// NewUUID returns a new random UUID, for rows whose ID must be known before
// they're inserted, e.g. to reference them in the same batch.
func NewUUID() pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}
