package plan

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/bs-tools/trailrunn.ing/internal/authn"
	"github.com/bs-tools/trailrunn.ing/internal/avalanche"
	geov1 "github.com/bs-tools/trailrunn.ing/internal/gen/geo/v1"
	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
	"github.com/bs-tools/trailrunn.ing/internal/plans"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Service) GetTouringLimits(ctx context.Context, _ *plansv1.GetTouringLimitsRequest) (*plansv1.GetTouringLimitsResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	limits, err := s.plans.GetTouringLimits(ctx, userID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get touring limits")
	}
	return plansv1.GetTouringLimitsResponse_builder{Limits: touringLimitsToProto(limits)}.Build(), nil
}

func (s *Service) SetTouringLimits(ctx context.Context, req *plansv1.SetTouringLimitsRequest) (*plansv1.SetTouringLimitsResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	l := req.GetLimits()
	limits := &plans.TouringLimits{
		MaxDangerAlpine:          avalanche.Danger(l.GetMaxAvalancheDangerAlpine()),
		MaxDangerTreeline:        avalanche.Danger(l.GetMaxAvalancheDangerTreeline()),
		MaxDangerBelowTreeline:   avalanche.Danger(l.GetMaxAvalancheDangerBelowTreeline()),
		MaxWindGustsKMH:          l.GetMaxWindGustsKmh(),
		MaxFreezingLevelMeters:   l.GetMaxFreezingLevelMeters(),
		MaxSnowfallPast72HoursCM: l.GetMaxSnowfallPast_72HoursCm(),
	}
	// An optional limit that is set must be in range: 0 would read as no
	// limit.
	for name, set := range map[string]bool{
		"max_wind_gusts_kmh":            l.HasMaxWindGustsKmh() && limits.MaxWindGustsKMH == 0,
		"max_freezing_level_meters":     l.HasMaxFreezingLevelMeters() && limits.MaxFreezingLevelMeters == 0,
		"max_snowfall_past_72_hours_cm": l.HasMaxSnowfallPast_72HoursCm() && limits.MaxSnowfallPast72HoursCM == 0,
	} {
		if set {
			return nil, invalidArgument("limits.%s must be positive, or unset for no limit", name)
		}
	}
	if err := limits.Validate(); err != nil {
		return nil, invalidArgument("limits.%v", err)
	}
	set, err := s.plans.SetTouringLimits(ctx, userID, limits)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to set touring limits")
	}
	return plansv1.SetTouringLimitsResponse_builder{Limits: touringLimitsToProto(set)}.Build(), nil
}

// touringLimitsToProto returns touring limits, empty if l is nil.
func touringLimitsToProto(l *plans.TouringLimits) *plansv1.TouringLimits {
	b := plansv1.TouringLimits_builder{}
	if l == nil {
		return b.Build()
	}
	b.MaxAvalancheDangerAlpine = geov1.AvalancheDanger(l.MaxDangerAlpine)
	b.MaxAvalancheDangerTreeline = geov1.AvalancheDanger(l.MaxDangerTreeline)
	b.MaxAvalancheDangerBelowTreeline = geov1.AvalancheDanger(l.MaxDangerBelowTreeline)
	if l.MaxWindGustsKMH > 0 {
		b.MaxWindGustsKmh = &l.MaxWindGustsKMH
	}
	if l.MaxFreezingLevelMeters > 0 {
		b.MaxFreezingLevelMeters = &l.MaxFreezingLevelMeters
	}
	if l.MaxSnowfallPast72HoursCM > 0 {
		b.MaxSnowfallPast_72HoursCm = &l.MaxSnowfallPast72HoursCM
	}
	b.UpdateTime = timestamppb.New(l.UpdateTime)
	return b.Build()
}
