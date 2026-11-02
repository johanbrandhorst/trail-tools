// Package routes stores running routes.
package routes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routes/internal/gen"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// Profile controls how legs between waypoints are computed.
type Profile string

// Routing profiles.
const (
	ProfileTrail    Profile = "trail"
	ProfileShortest Profile = "shortest"
	ProfileStraight Profile = "straight"
	// ProfileHeatmap prefers paths in the user's personal heatmap.
	ProfileHeatmap Profile = "heatmap"
)

// Source describes how a route was created.
type Source string

// Route sources.
const (
	SourceWaypoints Source = "waypoints"
	SourceGPX       Source = "gpx"
	SourceActivity  Source = "activity"
)

// LatLng is a coordinate.
type LatLng struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Leg is the path between two consecutive waypoints.
type Leg struct {
	From LatLng `json:"from"`
	To   LatLng `json:"to"`
	// The profile the leg was routed with. Empty for imported legs.
	Profile Profile `json:"profile,omitempty"`
	// The path as [lat, lng, elevation] triples.
	Points [][3]float64 `json:"points"`
	// The terrain of each segment of the path, if known. Terrains[i] is the
	// terrain between Points[i] and Points[i+1].
	Terrains []geo.Terrain `json:"terrains,omitempty"`
	// How far the waypoints are from where routing joined the map, in
	// meters: the leg goes straight between them, off any mapped path.
	// Zero for legs that weren't routed, and those routed before it was
	// recorded.
	FromOffMapMeters float64 `json:"from_off_map_meters,omitempty"`
	ToOffMapMeters   float64 `json:"to_off_map_meters,omitempty"`
}

// GeoPoints returns the leg's path as geo points.
func (l *Leg) GeoPoints() []geo.Point {
	points := make([]geo.Point, len(l.Points))
	for i, p := range l.Points {
		points[i] = geo.Point{Lat: p[0], Lng: p[1], Ele: p[2]}
	}
	return points
}

// Reversed returns the leg in the opposite direction.
func (l Leg) Reversed() Leg {
	n := len(l.Points)
	reversed := Leg{
		From: l.To, To: l.From, Profile: l.Profile, Points: make([][3]float64, n),
		FromOffMapMeters: l.ToOffMapMeters, ToOffMapMeters: l.FromOffMapMeters,
	}
	for i, p := range l.Points {
		reversed.Points[n-1-i] = p
	}
	if l.Terrains != nil {
		// Terrains[i] is the terrain after Points[i], and the last entry is
		// unused.
		reversed.Terrains = make([]geo.Terrain, len(l.Terrains))
		for i := 0; i+1 < len(l.Terrains); i++ {
			reversed.Terrains[len(l.Terrains)-2-i] = l.Terrains[i]
		}
		reversed.Terrains[len(l.Terrains)-1] = geo.TerrainUnknown
	}
	return reversed
}

// NewLeg creates a leg from a path. Unknown elevations are stored as 0.
func NewLeg(from, to LatLng, profile Profile, points []geo.Point, terrains []geo.Terrain) Leg {
	leg := Leg{From: from, To: to, Profile: profile, Points: make([][3]float64, len(points))}
	for i, p := range points {
		ele := p.Ele
		if math.IsNaN(ele) {
			ele = 0
		}
		leg.Points[i] = [3]float64{p.Lat, p.Lng, ele}
	}
	for _, t := range terrains {
		if t != geo.TerrainUnknown {
			leg.Terrains = terrains
			break
		}
	}
	return leg
}

// Route is a running route.
type Route struct {
	ID          pgtype.UUID
	UserID      pgtype.UUID
	Name        string
	Description string
	// The default profile. Each leg stores the profile it was routed with.
	Profile   Profile
	Source    Source
	Waypoints []LatLng
	// The legs between waypoints. Not set when listing routes.
	Legs       []Leg
	Stats      geo.TrackStats
	CreateTime time.Time
	UpdateTime time.Time
}

// Path returns the full path of the route and the terrain of each segment.
func (r *Route) Path() ([]geo.Point, []geo.Terrain) {
	var points []geo.Point
	var terrains []geo.Terrain
	for _, leg := range r.Legs {
		legPoints := leg.GeoPoints()
		legTerrains := leg.Terrains
		if legTerrains == nil {
			legTerrains = make([]geo.Terrain, len(legPoints))
		}
		if len(points) > 0 && len(legPoints) > 0 {
			// Consecutive legs share their joining point. The segment after
			// the joining point is the first segment of this leg.
			terrains[len(terrains)-1] = legTerrains[0]
			legPoints = legPoints[1:]
			legTerrains = legTerrains[1:]
		}
		points = append(points, legPoints...)
		terrains = append(terrains, legTerrains...)
	}
	return points, terrains
}

// Repository stores routes.
type Repository struct {
	log *slog.Logger
	q   *gen.Queries
}

// NewRepository creates a new Repository from the provided logger and database.
func NewRepository(log *slog.Logger, db *store.DB) (*Repository, error) {
	return &Repository{
		log: log,
		q:   gen.New(db),
	}, nil
}

// CreateRoute stores a new route.
func (r *Repository) CreateRoute(ctx context.Context, route *Route) (*Route, error) {
	waypoints, legs, stats, err := marshalRoute(route)
	if err != nil {
		return nil, err
	}
	dbRoute, err := r.q.CreateRoute(ctx, &gen.CreateRouteParams{
		UserID:      route.UserID,
		Name:        route.Name,
		Description: route.Description,
		Profile:     gen.RoutingProfile(route.Profile),
		Source:      gen.RouteSource(route.Source),
		Waypoints:   waypoints,
		Legs:        legs,
		Stats:       stats,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create route: %w", err)
	}
	return routeFromDB(dbRoute.ID, dbRoute.UserID, dbRoute.Name, dbRoute.Description, dbRoute.Profile,
		dbRoute.Source, dbRoute.Waypoints, dbRoute.Legs, dbRoute.Stats, dbRoute.CreateTime, dbRoute.UpdateTime)
}

// GetRoute gets one of the user's routes, including its legs.
func (r *Repository) GetRoute(ctx context.Context, userID, routeID pgtype.UUID) (*Route, error) {
	dbRoute, err := r.q.GetRoute(ctx, &gen.GetRouteParams{ID: routeID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get route: %w", err)
	}
	return routeFromDB(dbRoute.ID, dbRoute.UserID, dbRoute.Name, dbRoute.Description, dbRoute.Profile,
		dbRoute.Source, dbRoute.Waypoints, dbRoute.Legs, dbRoute.Stats, dbRoute.CreateTime, dbRoute.UpdateTime)
}

// ListRoutes lists the user's routes without their legs, most recently
// updated first.
func (r *Repository) ListRoutes(ctx context.Context, userID pgtype.UUID) ([]*Route, error) {
	rows, err := r.q.ListRoutes(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list routes: %w", err)
	}
	routes := make([]*Route, 0, len(rows))
	for _, row := range rows {
		route, err := routeFromDB(row.ID, row.UserID, row.Name, row.Description, row.Profile,
			row.Source, row.Waypoints, nil, row.Stats, row.CreateTime, row.UpdateTime)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, nil
}

// UpdateRoute replaces a route's details and geometry.
func (r *Repository) UpdateRoute(ctx context.Context, route *Route) (*Route, error) {
	waypoints, legs, stats, err := marshalRoute(route)
	if err != nil {
		return nil, err
	}
	dbRoute, err := r.q.UpdateRoute(ctx, &gen.UpdateRouteParams{
		ID:          route.ID,
		UserID:      route.UserID,
		Name:        route.Name,
		Description: route.Description,
		Profile:     gen.RoutingProfile(route.Profile),
		Waypoints:   waypoints,
		Legs:        legs,
		Stats:       stats,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to update route: %w", err)
	}
	return routeFromDB(dbRoute.ID, dbRoute.UserID, dbRoute.Name, dbRoute.Description, dbRoute.Profile,
		dbRoute.Source, dbRoute.Waypoints, dbRoute.Legs, dbRoute.Stats, dbRoute.CreateTime, dbRoute.UpdateTime)
}

// DeleteRoute deletes one of the user's routes.
func (r *Repository) DeleteRoute(ctx context.Context, userID, routeID pgtype.UUID) error {
	n, err := r.q.DeleteRoute(ctx, &gen.DeleteRouteParams{ID: routeID, UserID: userID})
	if err != nil {
		return fmt.Errorf("failed to delete route: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func marshalRoute(route *Route) (waypoints, legs, stats []byte, err error) {
	waypoints, err = json.Marshal(route.Waypoints)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal waypoints: %w", err)
	}
	legs, err = json.Marshal(route.Legs)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal legs: %w", err)
	}
	stats, err = json.Marshal(route.Stats)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal stats: %w", err)
	}
	return waypoints, legs, stats, nil
}

func routeFromDB(
	id, userID pgtype.UUID,
	name, description string,
	profile gen.RoutingProfile,
	source gen.RouteSource,
	waypoints, legs, stats []byte,
	createTime, updateTime pgtype.Timestamptz,
) (*Route, error) {
	route := &Route{
		ID:          id,
		UserID:      userID,
		Name:        name,
		Description: description,
		Profile:     Profile(profile),
		Source:      Source(source),
		CreateTime:  createTime.Time,
		UpdateTime:  updateTime.Time,
	}
	if err := json.Unmarshal(waypoints, &route.Waypoints); err != nil {
		return nil, fmt.Errorf("failed to unmarshal waypoints: %w", err)
	}
	if legs != nil {
		if err := json.Unmarshal(legs, &route.Legs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal legs: %w", err)
		}
	}
	if err := json.Unmarshal(stats, &route.Stats); err != nil {
		return nil, fmt.Errorf("failed to unmarshal stats: %w", err)
	}
	if route.Stats.RepeatedMeters == nil && legs != nil {
		// Stored before the repeated distance was computed.
		points, _ := route.Path()
		repeated := geo.RepeatedDistance(points)
		route.Stats.RepeatedMeters = &repeated
	}
	return route, nil
}
