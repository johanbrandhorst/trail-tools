package plan

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/bs-tools/trailrunn.ing/internal/authn"
	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
	"github.com/bs-tools/trailrunn.ing/internal/plans"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Service) GetSweatProfile(ctx context.Context, _ *plansv1.GetSweatProfileRequest) (*plansv1.GetSweatProfileResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	profile, err := s.plans.GetSweatProfile(ctx, userID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get sweat profile")
	}
	return plansv1.GetSweatProfileResponse_builder{Profile: sweatProfileToProto(profile)}.Build(), nil
}

func (s *Service) SetSweatProfile(ctx context.Context, req *plansv1.SetSweatProfileRequest) (*plansv1.SetSweatProfileResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	p := req.GetProfile()
	profile := &plans.SweatProfile{SodiumMGPerLiter: p.GetSodiumMgPerLiter(), RateMLPerHour: p.GetRateMlPerHour()}
	if err := profile.Validate(); err != nil {
		return nil, invalidArgument("profile.%v", err)
	}
	set, err := s.plans.SetSweatProfile(ctx, userID, profile)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to set sweat profile")
	}
	// Runs on the Garmin calendar describe their sodium, which may change.
	s.notifyWorkoutsChanged(ctx, userID)
	return plansv1.SetSweatProfileResponse_builder{Profile: sweatProfileToProto(set)}.Build(), nil
}

// sweatProfileToProto returns a sweat profile, empty if p is nil.
func sweatProfileToProto(p *plans.SweatProfile) *plansv1.SweatProfile {
	b := plansv1.SweatProfile_builder{}
	if p == nil {
		return b.Build()
	}
	if p.SodiumMGPerLiter > 0 {
		b.SodiumMgPerLiter = &p.SodiumMGPerLiter
	}
	if p.RateMLPerHour > 0 {
		b.RateMlPerHour = &p.RateMLPerHour
	}
	b.UpdateTime = timestamppb.New(p.UpdateTime)
	return b.Build()
}
