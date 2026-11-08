package route

import (
	"context"

	"github.com/bs-tools/trailrunn.ing/internal/avalanche"
	geov1 "github.com/bs-tools/trailrunn.ing/internal/gen/geo/v1"
	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/bs-tools/trailrunn.ing/internal/plans"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Avalanches gets avalanche forecasts.
type Avalanches interface {
	// Forecast returns the forecast for a point, nil if there is none.
	Forecast(ctx context.Context, point geo.Point) (*avalanche.Forecast, error)
}

// SetAvalanches sets where avalanche forecasts come from, for route
// forecasts. Without it, they have none.
func (s *Service) SetAvalanches(avalanches Avalanches) {
	s.avalanches = avalanches
}

// TouringLimitsSource gets the user's touring limits, for verdicts on ski
// tours.
type TouringLimitsSource interface {
	// GetTouringLimits returns the user's limits, nil if they have none.
	GetTouringLimits(ctx context.Context, userID pgtype.UUID) (*plans.TouringLimits, error)
}

// SetTouringLimitsSource sets where the user's touring limits come from,
// for verdicts on ski tours. Without it, there are none.
func (s *Service) SetTouringLimitsSource(limits TouringLimitsSource) {
	s.touringLimits = limits
}

// AvalancheForecastToProto converts an avalanche forecast, nil to nil.
func AvalancheForecastToProto(f *avalanche.Forecast) *geov1.AvalancheForecast {
	if f == nil {
		return nil
	}
	b := geov1.AvalancheForecast_builder{
		Title:        f.Title,
		Forecaster:   f.Forecaster,
		Url:          f.URL,
		Highlights:   f.Highlights,
		Confidence:   f.Confidence,
		TravelAdvice: f.TravelAdvice,
	}
	if !f.IssueTime.IsZero() {
		b.IssueTime = timestamppb.New(f.IssueTime)
	}
	if !f.ValidUntil.IsZero() {
		b.ValidUntilTime = timestamppb.New(f.ValidUntil)
	}
	for _, d := range f.Days {
		b.Days = append(b.Days, geov1.AvalancheDangerDay_builder{
			Date:          d.Date,
			Alpine:        dangerRatingToProto(d.Alpine),
			Treeline:      dangerRatingToProto(d.Treeline),
			BelowTreeline: dangerRatingToProto(d.BelowTreeline),
		}.Build())
	}
	for _, p := range f.Problems {
		b.Problems = append(b.Problems, geov1.AvalancheProblem_builder{
			Type:       p.Type,
			Name:       p.Name,
			Elevations: p.Elevations,
			Aspects:    p.Aspects,
			Likelihood: p.Likelihood,
			MinSize:    p.MinSize,
			MaxSize:    p.MaxSize,
			Comment:    p.Comment,
		}.Build())
	}
	return b.Build()
}

// AvalancheDangerToProto converts a danger rating. The enum's numbers are
// the levels of the danger scale, like avalanche.Danger's.
func AvalancheDangerToProto(d avalanche.Danger) geov1.AvalancheDanger {
	return geov1.AvalancheDanger(d)
}

func dangerRatingToProto(r avalanche.Rating) *geov1.AvalancheDangerRating {
	return geov1.AvalancheDangerRating_builder{
		Danger:      AvalancheDangerToProto(r.Danger),
		Description: r.Description,
	}.Build()
}

// HighestPoint returns the highest point of a path, which must not be
// empty: the avalanche forecast of a route is for its highest point, which
// is in the most avalanche terrain, and so in the area that matters most.
func HighestPoint(path []geo.Point) geo.Point {
	high := path[0]
	for _, p := range path[1:] {
		if p.Ele > high.Ele {
			high = p
		}
	}
	return high
}

// TouringVerdictToProto converts the verdict of checking a tour against the
// user's touring limits.
func TouringVerdictToProto(verdict plans.Verdict, checks []plans.Check) *geov1.TouringVerdict {
	b := geov1.TouringVerdict_builder{Level: touringVerdictLevelToProto(verdict)}
	for _, c := range checks {
		b.Checks = append(b.Checks, geov1.TouringCheck_builder{Level: touringVerdictLevelToProto(c.Verdict), Message: c.Message}.Build())
	}
	return b.Build()
}

func touringVerdictLevelToProto(v plans.Verdict) geov1.TouringVerdictLevel {
	switch v {
	case plans.VerdictGo:
		return geov1.TouringVerdictLevel_TOURING_VERDICT_LEVEL_GO
	case plans.VerdictCaution:
		return geov1.TouringVerdictLevel_TOURING_VERDICT_LEVEL_CAUTION
	case plans.VerdictNoGo:
		return geov1.TouringVerdictLevel_TOURING_VERDICT_LEVEL_NO_GO
	}
	return geov1.TouringVerdictLevel_TOURING_VERDICT_LEVEL_UNSPECIFIED
}
