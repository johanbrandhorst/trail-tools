package garmin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/garmin"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/plans"
	"github.com/johanbrandhorst/trail-tools/internal/routes"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// RouteRepository reads the routes of workouts.
type RouteRepository interface {
	GetRoute(ctx context.Context, userID, routeID pgtype.UUID) (*routes.Route, error)
}

// syncCourses puts the routes of the user's runs and races of the coming
// days on their Garmin account as courses, which Garmin syncs to the watch
// to navigate, replacing those whose route changed and removing those no
// longer coming up. Routes that didn't change aren't sent again. It returns
// what it did.
func (s *Syncer) syncCourses(ctx context.Context, conn *activities.GarminConnection, token string, workouts []*plans.Workout) (string, error) {
	// The routes coming up, in date order.
	var upcoming []pgtype.UUID
	seen := map[pgtype.UUID]bool{}
	for _, w := range workouts {
		if !w.RouteID.Valid || w.CompletedActivityID.Valid || seen[w.RouteID] {
			continue
		}
		seen[w.RouteID] = true
		upcoming = append(upcoming, w.RouteID)
	}
	onGarmin, err := s.plans.ListGarminCourses(ctx, conn.UserID)
	if err != nil {
		return "", err
	}
	byRoute := map[pgtype.UUID]*plans.GarminCourse{}
	for _, c := range onGarmin {
		byRoute[c.RouteID] = c
	}
	added, removed := 0, 0
	var skipped []string
	for _, routeID := range upcoming {
		route, err := s.routes.GetRoute(ctx, conn.UserID, routeID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		course, ok := courseFromRoute(route)
		if !ok {
			skipped = append(skipped, route.Name)
			continue
		}
		hash := courseHash(course)
		existing := byRoute[routeID]
		if existing != nil && existing.ContentHash == hash {
			continue
		}
		if existing != nil {
			if err := s.removeCourse(ctx, conn.UserID, token, existing); err != nil {
				return "", err
			}
			removed++
		}
		id, err := s.client.CreateCourse(ctx, token, course)
		if err != nil {
			return "", err
		}
		if err := s.plans.AddGarminCourse(ctx, conn.UserID, &plans.GarminCourse{RouteID: routeID, GarminCourseID: id, ContentHash: hash}); err != nil {
			return "", err
		}
		added++
	}
	// Courses of routes no longer coming up, or deleted, are removed, so
	// the watch only holds the courses about to be run.
	for routeID, c := range byRoute {
		if !seen[routeID] {
			if err := s.removeCourse(ctx, conn.UserID, token, c); err != nil {
				return "", err
			}
			removed++
		}
	}
	var message string
	if added > 0 || removed > 0 {
		message = fmt.Sprintf("Put %d courses on Garmin and removed %d.", added, removed)
	}
	if len(skipped) > 0 {
		message = strings.TrimSpace(message + fmt.Sprintf(" Routes too long for a Garmin course: %s.", strings.Join(skipped, ", ")))
	}
	return message, nil
}

func (s *Syncer) removeCourse(ctx context.Context, userID pgtype.UUID, token string, c *plans.GarminCourse) error {
	if err := s.client.DeleteCourse(ctx, token, c.GarminCourseID); err != nil {
		return err
	}
	return s.plans.RemoveGarminCourse(ctx, userID, c.ID)
}

// removeAllCourses removes the courses put on the user's Garmin account,
// when they disconnect.
func (s *Syncer) removeAllCourses(ctx context.Context, userID pgtype.UUID, token string) error {
	courses, err := s.plans.ListGarminCourses(ctx, userID)
	if err != nil {
		return err
	}
	for _, c := range courses {
		if err := s.client.DeleteCourse(ctx, token, c.GarminCourseID); err != nil {
			return err
		}
	}
	return nil
}

// courseFromRoute returns a route as a Garmin course, with points at most
// garmin.MaxCoursePointSpacingMeters apart, and whether it fits in one.
func courseFromRoute(r *routes.Route) (*garmin.Course, bool) {
	path, _ := r.Path()
	if len(path) < 2 {
		return nil, false
	}
	var points []geo.Point
	for i := 1; i < len(path); i++ {
		segment := geo.Densify(path[i-1], path[i], garmin.MaxCoursePointSpacingMeters)
		if i > 1 {
			segment = segment[1:]
		}
		points = append(points, segment...)
		if len(points) > garmin.MaxCoursePoints {
			return nil, false
		}
	}
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "Route"
	}
	course := &garmin.Course{
		CourseName:          truncate(name, 100),
		Description:         truncate(r.Description, 1024),
		DistanceMeters:      math.Round(r.Stats.DistanceMeters*10) / 10,
		ElevationGainMeters: math.Round(r.Stats.ElevationGainMeters*10) / 10,
		ElevationLossMeters: math.Round(r.Stats.ElevationLossMeters*10) / 10,
		ActivityType:        courseActivityType(r),
		CoordinateSystem:    "WGS84",
		GeoPoints:           make([]garmin.CoursePoint, len(points)),
	}
	for i, p := range points {
		point := garmin.CoursePoint{Latitude: round(p.Lat, 6), Longitude: round(p.Lng, 6)}
		if !math.IsNaN(p.Ele) {
			ele := round(p.Ele, 1)
			point.ElevationMeters = &ele
		}
		course.GeoPoints[i] = point
	}
	return course, true
}

// courseActivityType returns the activity type of a route's course:
// running for routes mostly paved, trail running otherwise.
func courseActivityType(r *routes.Route) string {
	var road, total float64
	for terrain, meters := range r.Stats.TerrainMeters {
		total += meters
		if terrain == geo.TerrainPaved {
			road += meters
		}
	}
	if total > 0 && road > total/2 {
		return garmin.CourseRunning
	}
	return garmin.CourseTrailRunning
}

// courseHash returns a hash of a course as sent, to send it again only when
// it changes.
func courseHash(c *garmin.Course) string {
	data, _ := json.Marshal(c)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func round(v float64, decimals int) float64 {
	scale := math.Pow(10, float64(decimals))
	return math.Round(v*scale) / scale
}
