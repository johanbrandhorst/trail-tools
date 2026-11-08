package route

import (
	"context"
	"log/slog"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/activities"
	"github.com/bs-tools/trailrunn.ing/internal/errreport"
	activitiesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/activities/v1"
	"github.com/bs-tools/trailrunn.ing/internal/gen/routes/v1/routesv1connect"
	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/bs-tools/trailrunn.ing/internal/routes"
	"github.com/jackc/pgx/v5/pgtype"
)

var _ routesv1connect.RouteServiceHandler = (*Service)(nil)

type RouteRepository interface {
	CreateRoute(ctx context.Context, route *routes.Route) (*routes.Route, error)
	GetRoute(ctx context.Context, userID, routeID pgtype.UUID) (*routes.Route, error)
	ListRoutes(ctx context.Context, userID pgtype.UUID) ([]*routes.Route, error)
	UpdateRoute(ctx context.Context, route *routes.Route) (*routes.Route, error)
	DeleteRoute(ctx context.Context, userID, routeID pgtype.UUID) error
}

type ActivityRepository interface {
	GetActivity(ctx context.Context, userID, activityID pgtype.UUID) (*activities.Activity, error)
	GetActivityStreams(ctx context.Context, userID, activityID pgtype.UUID) (*activitiesv1.ActivityStreams, error)
	ListTracksInBounds(ctx context.Context, userID pgtype.UUID, bounds geo.Bounds, startDate, endDate time.Time, precision float64) (tracks []activities.Track, truncated bool, err error)
}

// Service implements API handlers for the route service.
type Service struct {
	log        *slog.Logger
	report     *errreport.Reporter
	routes     RouteRepository
	activities ActivityRepository
	router     Router
	mapData    MapData
	legs       *legCache
	// Gets avalanche forecasts, nil if there are none.
	avalanches Avalanches
	// Gets the user's touring limits, nil if there are none.
	touringLimits TouringLimitsSource
}

// NewService creates a new Service.
func NewService(log *slog.Logger, report *errreport.Reporter, routes RouteRepository, activities ActivityRepository, router Router, mapData MapData) *Service {
	return &Service{
		log:        log,
		report:     report,
		routes:     routes,
		activities: activities,
		router:     router,
		mapData:    mapData,
		legs:       newLegCache(),
	}
}
