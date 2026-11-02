package garmin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

// PermissionCourseImport lets the app put courses on the user's Garmin
// Connect account, which syncs them to the watch.
const PermissionCourseImport = "COURSE_IMPORT"

// Activity types of courses.
const (
	CourseRunning      = "RUNNING"
	CourseTrailRunning = "TRAIL_RUNNING"
	CourseHiking       = "HIKING"
)

// Limits of courses, from the Courses API's acceptance criteria.
const (
	// MaxCoursePointSpacingMeters is the furthest apart consecutive points
	// of a course may be, which also matches Garmin Connect's own course
	// points and elevation.
	MaxCoursePointSpacingMeters = 100
	// MaxCoursePoints is about the most points a course can have.
	MaxCoursePoints = 10_000
)

// Course is a course of the Courses API: a path the watch navigates.
type Course struct {
	// Required.
	CourseName  string `json:"courseName"`
	Description string `json:"description,omitempty"`
	// Required, in meters.
	DistanceMeters float64 `json:"distance"`
	// Required, in meters.
	ElevationGainMeters float64 `json:"elevationGain"`
	ElevationLossMeters float64 `json:"elevationLoss"`
	// Required, at most MaxCoursePointSpacingMeters apart.
	GeoPoints []CoursePoint `json:"geoPoints"`
	// CourseRunning, CourseTrailRunning or CourseHiking.
	ActivityType string `json:"activityType"`
	// "WGS84", as routes are.
	CoordinateSystem string `json:"coordinateSystem"`
}

// CoursePoint is a point of a course's path.
type CoursePoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	// In meters, nil to let Garmin Connect fill it in.
	ElevationMeters *float64 `json:"elevation,omitempty"`
	// Set for points of interest on the course, like aid stations.
	Information *CoursePointInfo `json:"information,omitempty"`
}

// CoursePointInfo describes a point of interest on a course.
type CoursePointInfo struct {
	Name string `json:"name"`
	// Like "AID_STATION", "WATER", "SUMMIT" or "GENERIC".
	CoursePointType string `json:"coursePointType"`
}

// CreateCourse adds a course to the user's Garmin Connect account, which
// syncs it to their watch, and returns its ID.
func (c *Client) CreateCourse(ctx context.Context, accessToken string, course *Course) (int64, error) {
	var out struct {
		CourseID ID `json:"courseId"`
	}
	if err := c.do(ctx, accessToken, http.MethodPost, c.apiURL+"/training-api/courses/v1/course", course, &out); err != nil {
		return 0, fmt.Errorf("failed to create course: %w", err)
	}
	if out.CourseID == 0 {
		return 0, errors.New("garmin returned no course ID")
	}
	return int64(out.CourseID), nil
}

// DeleteCourse deletes a course. A course that no longer exists isn't an
// error.
func (c *Client) DeleteCourse(ctx context.Context, accessToken string, id int64) error {
	err := c.do(ctx, accessToken, http.MethodDelete, c.apiURL+"/training-api/courses/v1/course/"+strconv.FormatInt(id, 10), nil, nil)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("failed to delete course: %w", err)
	}
	return nil
}
