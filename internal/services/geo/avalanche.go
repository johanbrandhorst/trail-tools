package geo

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/bs-tools/trailrunn.ing/internal/authn"
	geov1 "github.com/bs-tools/trailrunn.ing/internal/gen/geo/v1"
	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/bs-tools/trailrunn.ing/internal/services/route"
)

// SetAvalanches sets where avalanche forecasts come from. Without it, there
// are none.
func (s *Service) SetAvalanches(avalanches route.Avalanches) {
	s.avalanches = avalanches
}

func (s *Service) GetAvalancheForecast(ctx context.Context, req *geov1.GetAvalancheForecastRequest) (*geov1.GetAvalancheForecastResponse, error) {
	if _, ok := authn.GetUserID(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if !req.HasLocation() {
		return nil, connect.NewError(connect.CodeInvalidArgument, "location is required")
	}
	lat, lng := req.GetLocation().GetLat(), req.GetLocation().GetLng()
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "location must have a latitude between -90 and 90 and a longitude between -180 and 180")
	}
	if s.avalanches == nil {
		return geov1.GetAvalancheForecastResponse_builder{}.Build(), nil
	}
	f, err := s.avalanches.Forecast(ctx, geo.Point{Lat: lat, Lng: lng})
	if err != nil {
		s.report.Report(ctx, "Failed to get an avalanche forecast", err)
		return nil, connect.NewError(connect.CodeUnavailable,
			"the avalanche forecast is unavailable, try again later or read it on avalanche.ca").WithCause(err)
	}
	return geov1.GetAvalancheForecastResponse_builder{Forecast: route.AvalancheForecastToProto(f)}.Build(), nil
}
