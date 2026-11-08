package plans

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/avalanche"
	"github.com/bs-tools/trailrunn.ing/internal/plans/internal/gen"
	"github.com/jackc/pgx/v5/pgtype"
)

// TouringLimits are the user's own limits for ski touring: the conditions
// they won't tour in. Forecasts for ski tours are checked against them.
type TouringLimits struct {
	// The highest avalanche danger they'll tour in, above, near and below
	// treeline. avalanche.DangerNone means no limit.
	MaxDangerAlpine, MaxDangerTreeline, MaxDangerBelowTreeline avalanche.Danger
	// The strongest gusts they'll tour in, 0 for no limit.
	MaxWindGustsKMH float64
	// The highest freezing level they'll tour under, 0 for no limit.
	MaxFreezingLevelMeters float64
	// The most snow fallen in the 72 hours before they'll tour in, 0 for
	// no limit.
	MaxSnowfallPast72HoursCM float64
	UpdateTime               time.Time
}

// The bounds of touring limits: wide enough for any sensible limit, and
// narrow enough to catch a mistaken unit, like meters per second for
// kilometers per hour.
const (
	MinTouringGustsKMH        = 10
	MaxTouringGustsKMH        = 200
	MinTouringFreezingLevel   = 100
	MaxTouringFreezingLevel   = 6000
	MinTouringSnowfallCM      = 1
	MaxTouringSnowfallCM      = 300
	maxTouringAvalancheDanger = avalanche.DangerExtreme
)

// Validate reports an error if a set limit is out of range.
func (l *TouringLimits) Validate() error {
	dangers := []struct {
		name   string
		danger avalanche.Danger
	}{
		{"max_avalanche_danger_alpine", l.MaxDangerAlpine},
		{"max_avalanche_danger_treeline", l.MaxDangerTreeline},
		{"max_avalanche_danger_below_treeline", l.MaxDangerBelowTreeline},
	}
	for _, d := range dangers {
		if d.danger < avalanche.DangerNone || d.danger > maxTouringAvalancheDanger {
			return fmt.Errorf("%s must be a danger from low to extreme, or unspecified for no limit", d.name)
		}
	}
	check := func(name string, v, lo, hi float64, unit string) error {
		if v != 0 && (v < lo || v > hi || math.IsNaN(v)) {
			return fmt.Errorf("%s must be from %g to %g %s, or unset for no limit", name, lo, hi, unit)
		}
		return nil
	}
	if err := check("max_wind_gusts_kmh", l.MaxWindGustsKMH, MinTouringGustsKMH, MaxTouringGustsKMH, "km/h"); err != nil {
		return err
	}
	if err := check("max_freezing_level_meters", l.MaxFreezingLevelMeters, MinTouringFreezingLevel, MaxTouringFreezingLevel, "m"); err != nil {
		return err
	}
	return check("max_snowfall_past_72_hours_cm", l.MaxSnowfallPast72HoursCM, MinTouringSnowfallCM, MaxTouringSnowfallCM, "cm")
}

// Empty reports whether no limit is set.
func (l *TouringLimits) Empty() bool {
	return l.MaxDangerAlpine == 0 && l.MaxDangerTreeline == 0 && l.MaxDangerBelowTreeline == 0 &&
		l.MaxWindGustsKMH == 0 && l.MaxFreezingLevelMeters == 0 && l.MaxSnowfallPast72HoursCM == 0
}

// Verdict is whether to go on a tour.
type Verdict int

const (
	// VerdictGo is within all the user's limits.
	VerdictGo Verdict = iota + 1
	// VerdictCaution is close to a limit, or a limit can't be checked, like
	// without an avalanche forecast for the day.
	VerdictCaution
	// VerdictNoGo is beyond a limit.
	VerdictNoGo
)

// Check is the result of checking one limit.
type Check struct {
	Verdict Verdict
	// Message says what was found, e.g. "Gusts up to 75 km/h, above your
	// limit of 60 km/h".
	Message string
}

// The margins below a limit within which conditions are close to it, for
// a caution: forecasts are uncertain by about this much a few days out.
const (
	cautionGustsKMH       = 10
	cautionFreezingLevelM = 200
	cautionSnowfallCM     = 5
	noAvalancheForecast   = "No avalanche forecast covers the day, so the avalanche danger can't be checked against your limits: judge it yourself"
)

// TouringConditions are the forecast conditions of a tour.
type TouringConditions struct {
	// The avalanche danger of the tour's day, nil if no forecast covers
	// it.
	Avalanche *avalanche.Day
	// The strongest gusts, the highest freezing level, and the snow that
	// fell in the 72 hours before the tour.
	MaxWindGustsKMH, MaxFreezingLevelMeters, SnowfallPast72HoursCM float64
}

// Evaluate checks the conditions against the limits that are set, and
// returns the overall verdict, the worst of the checks, and the checks.
func (l *TouringLimits) Evaluate(c TouringConditions) (Verdict, []Check) {
	var checks []Check
	bands := []struct {
		name  string
		limit avalanche.Danger
		got   func(avalanche.Day) avalanche.Rating
	}{
		{"in the alpine", l.MaxDangerAlpine, func(d avalanche.Day) avalanche.Rating { return d.Alpine }},
		{"at treeline", l.MaxDangerTreeline, func(d avalanche.Day) avalanche.Rating { return d.Treeline }},
		{"below treeline", l.MaxDangerBelowTreeline, func(d avalanche.Day) avalanche.Rating { return d.BelowTreeline }},
	}
	hasDangerLimit := l.MaxDangerAlpine != 0 || l.MaxDangerTreeline != 0 || l.MaxDangerBelowTreeline != 0
	if hasDangerLimit && c.Avalanche == nil {
		checks = append(checks, Check{VerdictCaution, noAvalancheForecast})
	}
	for _, band := range bands {
		if band.limit == 0 || c.Avalanche == nil {
			continue
		}
		rating := band.got(*c.Avalanche)
		switch {
		case rating.Danger == avalanche.DangerNone:
			checks = append(checks, Check{VerdictCaution, fmt.Sprintf("Avalanche danger %s isn't rated (%s), so it can't be checked against your limit of %s: judge it yourself",
				band.name, rating.Description, band.limit)})
		case rating.Danger > band.limit:
			checks = append(checks, Check{VerdictNoGo, fmt.Sprintf("Avalanche danger %s is %s, above your limit of %s", band.name, rating.Danger, band.limit)})
		default:
			checks = append(checks, Check{VerdictGo, fmt.Sprintf("Avalanche danger %s is %s, within your limit of %s", band.name, rating.Danger, band.limit)})
		}
	}
	checks = appendMaxCheck(checks, "Gusts up to %.0f km/h", c.MaxWindGustsKMH, l.MaxWindGustsKMH, cautionGustsKMH, "%.0f km/h")
	checks = appendMaxCheck(checks, "The freezing level up to %.0f m", c.MaxFreezingLevelMeters, l.MaxFreezingLevelMeters, cautionFreezingLevelM, "%.0f m")
	checks = appendMaxCheck(checks, "%.0f cm of snow in the past 3 days", c.SnowfallPast72HoursCM, l.MaxSnowfallPast72HoursCM, cautionSnowfallCM, "%.0f cm")
	verdict := VerdictGo
	for _, check := range checks {
		verdict = max(verdict, check.Verdict)
	}
	return verdict, checks
}

// appendMaxCheck checks a value against a maximum, if one is set: above it
// is a no-go, and within margin below it a caution.
func appendMaxCheck(checks []Check, what string, got, limit, margin float64, unit string) []Check {
	if limit == 0 {
		return checks
	}
	found := fmt.Sprintf(what, got)
	limitText := fmt.Sprintf(unit, limit)
	switch {
	case got > limit:
		return append(checks, Check{VerdictNoGo, fmt.Sprintf("%s, above your limit of %s", found, limitText)})
	case got > limit-margin:
		return append(checks, Check{VerdictCaution, fmt.Sprintf("%s, close to your limit of %s", found, limitText)})
	}
	return append(checks, Check{VerdictGo, fmt.Sprintf("%s, within your limit of %s", found, limitText)})
}

// GetTouringLimits gets the user's touring limits, nil if they haven't set
// any.
func (r *Repository) GetTouringLimits(ctx context.Context, userID pgtype.UUID) (*TouringLimits, error) {
	rows, err := r.q.GetTouringLimits(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get touring limits: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return touringLimitsFromDB(rows[0]), nil
}

// SetTouringLimits replaces the user's touring limits.
func (r *Repository) SetTouringLimits(ctx context.Context, userID pgtype.UUID, l *TouringLimits) (*TouringLimits, error) {
	danger := func(d avalanche.Danger) pgtype.Int2 { return pgtype.Int2{Int16: int16(d), Valid: d > 0} }
	value := func(v float64) pgtype.Float8 { return pgtype.Float8{Float64: v, Valid: v > 0} }
	row, err := r.q.SetTouringLimits(ctx, &gen.SetTouringLimitsParams{
		UserID:                          userID,
		MaxAvalancheDangerAlpine:        danger(l.MaxDangerAlpine),
		MaxAvalancheDangerTreeline:      danger(l.MaxDangerTreeline),
		MaxAvalancheDangerBelowTreeline: danger(l.MaxDangerBelowTreeline),
		MaxWindGustsKmh:                 value(l.MaxWindGustsKMH),
		MaxFreezingLevelMeters:          value(l.MaxFreezingLevelMeters),
		MaxSnowfallPast72HoursCm:        value(l.MaxSnowfallPast72HoursCM),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to set touring limits: %w", err)
	}
	return touringLimitsFromDB(row), nil
}

func touringLimitsFromDB(row *gen.TouringLimit) *TouringLimits {
	return &TouringLimits{
		MaxDangerAlpine:          avalanche.Danger(row.MaxAvalancheDangerAlpine.Int16),
		MaxDangerTreeline:        avalanche.Danger(row.MaxAvalancheDangerTreeline.Int16),
		MaxDangerBelowTreeline:   avalanche.Danger(row.MaxAvalancheDangerBelowTreeline.Int16),
		MaxWindGustsKMH:          row.MaxWindGustsKmh.Float64,
		MaxFreezingLevelMeters:   row.MaxFreezingLevelMeters.Float64,
		MaxSnowfallPast72HoursCM: row.MaxSnowfallPast72HoursCm.Float64,
		UpdateTime:               row.UpdateTime.Time,
	}
}
