package users

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/users/internal/gen"
)

// PrivacyZone is a circle around a place, like the user's home, within which
// their tracks and photos are hidden on public pages.
type PrivacyZone struct {
	ID pgtype.UUID
	// A name for the user, e.g. "Home".
	Name         string
	Center       geo.Point
	RadiusMeters int32
	// Seed is the user's secret that public pages derive the circle they
	// hide from, so it isn't centered on the zone; see track.HiddenZones.
	// Set by ListPrivacyZones, and ignored by SetPrivacyZones.
	Seed [16]byte
}

// ListPrivacyZones lists the user's privacy zones, the oldest first.
func (r *Repository) ListPrivacyZones(ctx context.Context, userID pgtype.UUID) ([]PrivacyZone, error) {
	rows, err := r.querier.ListPrivacyZones(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list privacy zones: %w", err)
	}
	zones := make([]PrivacyZone, len(rows))
	for i, row := range rows {
		zones[i] = PrivacyZone{
			ID:           row.ID,
			Name:         row.Name,
			Center:       geo.Point{Lat: row.Lat, Lng: row.Lng},
			RadiusMeters: row.RadiusMeters,
			Seed:         row.PrivacySeed.Bytes,
		}
	}
	return zones, nil
}

// SetPrivacyZones replaces the user's privacy zones, in one round trip.
func (r *Repository) SetPrivacyZones(ctx context.Context, userID pgtype.UUID, zones []PrivacyZone) error {
	params := &gen.InsertPrivacyZonesParams{UserID: userID}
	for _, z := range zones {
		params.Names = append(params.Names, z.Name)
		params.Lats = append(params.Lats, z.Center.Lat)
		params.Lngs = append(params.Lngs, z.Center.Lng)
		params.RadiiMeters = append(params.RadiiMeters, z.RadiusMeters)
	}
	var batch store.Batch
	q := gen.New(&batch)
	if err := q.DeletePrivacyZones(ctx, userID); err != nil {
		return err
	}
	if len(zones) > 0 {
		if err := q.InsertPrivacyZones(ctx, params); err != nil {
			return err
		}
	}
	if err := batch.Send(ctx, r.db); err != nil {
		return fmt.Errorf("failed to set privacy zones: %w", err)
	}
	return nil
}
