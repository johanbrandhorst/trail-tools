package user

import (
	"context"
	"math"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	usersv1 "github.com/johanbrandhorst/trail-tools/internal/gen/users/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/users"
)

const (
	// maxPrivacyZones is the most privacy zones a user can have.
	maxPrivacyZones = 20
	// maxZoneNameLength is the most characters of a zone's name.
	maxZoneNameLength = 100
	// minZoneRadiusMeters and maxZoneRadiusMeters bound a zone's radius.
	minZoneRadiusMeters, maxZoneRadiusMeters = 100, 5000
)

func (s *Service) ListPrivacyZones(ctx context.Context, _ *usersv1.ListPrivacyZonesRequest) (*usersv1.ListPrivacyZonesResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	zones, err := s.zones.ListPrivacyZones(ctx, userID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list privacy zones")
	}
	return usersv1.ListPrivacyZonesResponse_builder{Zones: zonesToProto(zones)}.Build(), nil
}

func (s *Service) SetPrivacyZones(ctx context.Context, req *usersv1.SetPrivacyZonesRequest) (*usersv1.SetPrivacyZonesResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if len(req.GetZones()) > maxPrivacyZones {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "at most %d privacy zones can be set", maxPrivacyZones)
	}
	zones := make([]users.PrivacyZone, len(req.GetZones()))
	for i, z := range req.GetZones() {
		name := strings.TrimSpace(z.GetName())
		lat, lng, radius := z.GetLat(), z.GetLng(), z.GetRadiusMeters()
		switch {
		case utf8.RuneCountInString(name) > maxZoneNameLength:
			return nil, connect.Errorf(connect.CodeInvalidArgument, "zone %d: the name must be at most %d characters", i+1, maxZoneNameLength)
		case math.IsNaN(lat) || math.IsNaN(lng) || lat < -90 || lat > 90 || lng < -180 || lng > 180:
			return nil, connect.Errorf(connect.CodeInvalidArgument, "zone %d: lat must be from -90 to 90 and lng from -180 to 180", i+1)
		case radius < minZoneRadiusMeters || radius > maxZoneRadiusMeters:
			return nil, connect.Errorf(connect.CodeInvalidArgument, "zone %d: radius_meters must be from %d to %d", i+1, minZoneRadiusMeters, maxZoneRadiusMeters)
		}
		zones[i] = users.PrivacyZone{Name: name, Center: geo.Point{Lat: lat, Lng: lng}, RadiusMeters: radius}
	}
	if err := s.zones.SetPrivacyZones(ctx, userID, zones); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to set privacy zones")
	}
	// The zones get new IDs.
	saved, err := s.zones.ListPrivacyZones(ctx, userID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list privacy zones")
	}
	return usersv1.SetPrivacyZonesResponse_builder{Zones: zonesToProto(saved)}.Build(), nil
}

func zonesToProto(zones []users.PrivacyZone) []*usersv1.PrivacyZone {
	out := make([]*usersv1.PrivacyZone, len(zones))
	for i, z := range zones {
		out[i] = usersv1.PrivacyZone_builder{
			Id:           store.UUIDToString(z.ID),
			Name:         z.Name,
			Lat:          z.Center.Lat,
			Lng:          z.Center.Lng,
			RadiusMeters: z.RadiusMeters,
		}.Build()
	}
	return out
}
