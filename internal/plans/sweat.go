package plans

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/plans/internal/gen"
	"github.com/bs-tools/trailrunn.ing/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
)

// SweatProfile is how the user sweats, for their sodium intake on runs.
type SweatProfile struct {
	// How much sodium a liter of their sweat has, from a sweat test, 0 if
	// unknown.
	SodiumMGPerLiter float64
	// How fast they sweat running easy in mild weather, 0 if they haven't
	// measured it: then DefaultSweatRateMLPerHour is assumed.
	RateMLPerHour float64
	UpdateTime    time.Time
}

// DefaultSweatRateMLPerHour is the sweat rate assumed for running easy at
// mild temperatures if the user hasn't measured theirs: runners typically
// lose 0.5 to 1.2 liters an hour.
const DefaultSweatRateMLPerHour = 800

// Sweat rates rise by sweatHeatPerDegree for each degree Celsius it feels
// above sweatMildCelsius, up to maxSweatHeatIncrease, and fall by
// sweatColdPerDegree for each degree below it, down to minSweatColdFactor.
// Hard running sweats hardRunSweatFactor as much as easy running, as more
// heat is made.
const (
	sweatMildCelsius     = 15
	sweatHeatPerDegree   = 0.05
	maxSweatHeatIncrease = 1
	sweatColdPerDegree   = 0.02
	minSweatColdFactor   = 0.6
	hardRunSweatFactor   = 1.2
)

// The bounds of a sweat profile: well outside what's measured, to catch
// mistaken units like mmol/L for mg/L.
const (
	MinSweatSodiumMGPerLiter = 100
	MaxSweatSodiumMGPerLiter = 4000
	MinSweatRateMLPerHour    = 200
	MaxSweatRateMLPerHour    = 3000
)

// Validate reports an error if a set value is out of range.
func (p *SweatProfile) Validate() error {
	if v := p.SodiumMGPerLiter; v != 0 && (v < MinSweatSodiumMGPerLiter || v > MaxSweatSodiumMGPerLiter || math.IsNaN(v)) {
		return fmt.Errorf("sodium_mg_per_liter must be from %d to %d, or unset if unknown; to convert mmol/L, multiply by 23", MinSweatSodiumMGPerLiter, MaxSweatSodiumMGPerLiter)
	}
	if v := p.RateMLPerHour; v != 0 && (v < MinSweatRateMLPerHour || v > MaxSweatRateMLPerHour || math.IsNaN(v)) {
		return fmt.Errorf("rate_ml_per_hour must be from %d to %d, or unset if unmeasured", MinSweatRateMLPerHour, MaxSweatRateMLPerHour)
	}
	return nil
}

// SweatRate returns the milliliters an hour the user is expected to sweat on
// a run, hard or easy, at an apparent temperature in degrees Celsius, or
// mild temperatures if it's nil.
func (p *SweatProfile) SweatRate(apparentCelsius *float64, hard bool) float64 {
	rate := p.RateMLPerHour
	if rate == 0 {
		rate = DefaultSweatRateMLPerHour
	}
	if apparentCelsius != nil {
		if t := *apparentCelsius; t > sweatMildCelsius {
			rate *= 1 + math.Min(maxSweatHeatIncrease, (t-sweatMildCelsius)*sweatHeatPerDegree)
		} else {
			rate *= math.Max(minSweatColdFactor, 1-(sweatMildCelsius-t)*sweatColdPerDegree)
		}
	}
	if hard {
		rate *= hardRunSweatFactor
	}
	return rate
}

// GetSweatProfile gets the user's sweat profile, nil if they haven't set
// one.
func (r *Repository) GetSweatProfile(ctx context.Context, userID pgtype.UUID) (*SweatProfile, error) {
	rows, err := r.q.GetSweatProfile(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sweat profile: %w", err)
	}
	return sweatProfileFromDB(rows), nil
}

// SetSweatProfile replaces the user's sweat profile.
func (r *Repository) SetSweatProfile(ctx context.Context, userID pgtype.UUID, p *SweatProfile) (*SweatProfile, error) {
	row, err := r.q.SetSweatProfile(ctx, &gen.SetSweatProfileParams{
		UserID:           userID,
		SodiumMgPerLiter: pgtype.Float8{Float64: p.SodiumMGPerLiter, Valid: p.SodiumMGPerLiter > 0},
		RateMlPerHour:    pgtype.Float8{Float64: p.RateMLPerHour, Valid: p.RateMLPerHour > 0},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to set sweat profile: %w", err)
	}
	return sweatProfileFromDB([]*gen.SweatProfile{row}), nil
}

// queueSweatProfile queues reading the user's sweat profile into profile,
// with the reads of their workouts.
func queueSweatProfile(ctx context.Context, b *store.Batch, userID pgtype.UUID, profile **SweatProfile) {
	b.Read(func(db store.Querier) error {
		rows, err := gen.New(db).GetSweatProfile(ctx, userID)
		*profile = sweatProfileFromDB(rows)
		return err
	})
}

func sweatProfileFromDB(rows []*gen.SweatProfile) *SweatProfile {
	if len(rows) == 0 {
		return nil
	}
	row := rows[0]
	return &SweatProfile{
		SodiumMGPerLiter: row.SodiumMgPerLiter.Float64,
		RateMLPerHour:    row.RateMlPerHour.Float64,
		UpdateTime:       row.UpdateTime.Time,
	}
}
