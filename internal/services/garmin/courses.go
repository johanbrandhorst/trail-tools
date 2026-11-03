package garmin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

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
// longer coming up. The courses the user sent themselves are kept, and
// updated when their route changes. Routes that didn't change aren't sent
// again. It returns what it did.
func (s *Syncer) syncCourses(ctx context.Context, conn *activities.GarminConnection, token string, workouts []*plans.Workout) (string, error) {
	onGarmin, err := s.plans.ListGarminCourses(ctx, conn.UserID)
	if err != nil {
		return "", err
	}
	byRoute := map[pgtype.UUID]*plans.GarminCourse{}
	for _, c := range onGarmin {
		byRoute[c.RouteID] = c
	}
	// The routes coming up, in date order, then those the user sent.
	upcoming := upcomingRoutes(workouts)
	wanted := slices.Clone(upcoming)
	seen := map[pgtype.UUID]bool{}
	for _, id := range upcoming {
		seen[id] = true
	}
	for _, c := range onGarmin {
		if c.Sent && c.RouteID.Valid && !seen[c.RouteID] {
			seen[c.RouteID] = true
			wanted = append(wanted, c.RouteID)
		}
	}
	added, removed := 0, 0
	var skipped []string
	for _, routeID := range wanted {
		route, err := s.routes.GetRoute(ctx, conn.UserID, routeID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		existing := byRoute[routeID]
		changed, err := s.putCourse(ctx, conn.UserID, token, route, existing, existing != nil && existing.Sent)
		if errors.Is(err, errCourseTooLong) {
			skipped = append(skipped, route.Name)
			continue
		}
		if err != nil {
			return "", err
		}
		if changed {
			added++
			if existing != nil {
				removed++
			}
		}
	}
	// Courses of routes no longer coming up or sent, or deleted, are
	// removed, so the watch only holds the courses about to be run.
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

// upcomingRoutes returns the routes of workouts not yet completed, in date
// order, each once.
func upcomingRoutes(workouts []*plans.Workout) []pgtype.UUID {
	var routes []pgtype.UUID
	seen := map[pgtype.UUID]bool{}
	for _, w := range workouts {
		if !w.RouteID.Valid || w.CompletedActivityID.Valid || seen[w.RouteID] {
			continue
		}
		seen[w.RouteID] = true
		routes = append(routes, w.RouteID)
	}
	return routes
}

// errCourseTooLong is returned for routes with more points than a Garmin
// course takes.
var errCourseTooLong = errors.New("the route is too long for a Garmin course")

// putCourse puts a route's course on the user's Garmin account, replacing
// existing, the route's course already there, if any, unless it's the
// same. It returns whether it sent the course.
func (s *Syncer) putCourse(ctx context.Context, userID pgtype.UUID, token string, route *routes.Route, existing *plans.GarminCourse, sent bool) (bool, error) {
	course, ok := courseFromRoute(route)
	if !ok {
		return false, errCourseTooLong
	}
	hash := courseHash(course)
	if existing != nil && existing.ContentHash == hash {
		if existing.Sent != sent {
			return false, s.plans.SetGarminCourseSent(ctx, userID, route.ID, sent)
		}
		return false, nil
	}
	if existing != nil {
		if err := s.removeCourse(ctx, userID, token, existing); err != nil {
			return false, err
		}
	}
	id, err := s.client.CreateCourse(ctx, token, course)
	if err != nil {
		return false, err
	}
	if err := s.plans.AddGarminCourse(ctx, userID, &plans.GarminCourse{RouteID: route.ID, GarminCourseID: id, ContentHash: hash, Sent: sent}); err != nil {
		return false, err
	}
	return true, nil
}

// ErrNotConnected is returned for users without a Garmin connection.
var ErrNotConnected = errors.New("no Garmin account is connected")

// ErrCoursesNotAllowed is returned when the user doesn't let the app put
// courses on their Garmin account.
var ErrCoursesNotAllowed = errors.New("the Garmin account doesn't allow putting courses on it")

// RouteStatus is whether a route is on the user's Garmin account.
type RouteStatus struct {
	// Whether the route's course is on Garmin.
	OnGarmin bool
	// Whether the user sent it themselves, so it stays until they remove
	// it.
	Sent bool
}

// courseConnection returns the user's connection, if it may put courses on
// their account.
func (s *Syncer) courseConnection(ctx context.Context, userID pgtype.UUID) (*activities.GarminConnection, error) {
	conn, err := s.activities.GetGarminConnection(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotConnected
	}
	if err != nil {
		return nil, err
	}
	if !slices.Contains(conn.Permissions, garmin.PermissionCourseImport) {
		return nil, ErrCoursesNotAllowed
	}
	return conn, nil
}

// SendRoute puts a route on the user's Garmin account as a course, which
// stays there until they remove it with RemoveRoute.
func (s *Syncer) SendRoute(ctx context.Context, userID, routeID pgtype.UUID) error {
	conn, err := s.courseConnection(ctx, userID)
	if err != nil {
		return err
	}
	unlock := s.lockCalendar(userID)
	defer unlock()
	route, err := s.routes.GetRoute(ctx, userID, routeID)
	if err != nil {
		return err
	}
	existing, err := s.course(ctx, userID, routeID)
	if err != nil {
		return err
	}
	token, err := s.token(ctx, conn)
	if err != nil {
		return err
	}
	_, err = s.putCourse(ctx, userID, token, route, existing, true)
	return err
}

// RemoveRoute removes a route the user sent from their Garmin account. A
// route an upcoming workout runs stays until the workout is past.
func (s *Syncer) RemoveRoute(ctx context.Context, userID, routeID pgtype.UUID) error {
	conn, err := s.courseConnection(ctx, userID)
	if err != nil {
		return err
	}
	unlock := s.lockCalendar(userID)
	defer unlock()
	existing, err := s.course(ctx, userID, routeID)
	if err != nil || existing == nil {
		return err
	}
	today := s.nowFn().UTC().Truncate(24 * time.Hour)
	workouts, err := s.plans.ListUserWorkouts(ctx, userID, today, today.AddDate(0, 0, calendarDays))
	if err != nil {
		return err
	}
	if slices.Contains(upcomingRoutes(workouts), routeID) {
		return s.plans.SetGarminCourseSent(ctx, userID, routeID, false)
	}
	token, err := s.token(ctx, conn)
	if err != nil {
		return err
	}
	return s.removeCourse(ctx, userID, token, existing)
}

// RouteStatus returns whether a route is on the user's Garmin account.
func (s *Syncer) RouteStatus(ctx context.Context, userID, routeID pgtype.UUID) (RouteStatus, error) {
	c, err := s.course(ctx, userID, routeID)
	if err != nil || c == nil {
		return RouteStatus{}, err
	}
	return RouteStatus{OnGarmin: true, Sent: c.Sent}, nil
}

// course returns the course of a route on the user's Garmin account, or nil
// if there's none.
func (s *Syncer) course(ctx context.Context, userID, routeID pgtype.UUID) (*plans.GarminCourse, error) {
	courses, err := s.plans.ListGarminCourses(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, c := range courses {
		if c.RouteID == routeID {
			return c, nil
		}
	}
	return nil, nil
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
