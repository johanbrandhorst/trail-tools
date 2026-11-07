package route

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routes"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// unsafeFilenameChars matches characters that are replaced in download file
// names.
var unsafeFilenameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// The pace FIT courses are planned at, as on the forecast page: a flat speed
// and a climbing rate.
const (
	CourseSpeedKMH           = 8
	CourseClimbMetersPerHour = 600
)

// DownloadHandler serves routes as GPX files at /routes/{id}.gpx, and as FIT
// courses for Garmin and other devices at /routes/{id}.fit. It must be
// mounted at /routes/ behind the authn middleware. Other paths are passed to
// next, so the frontend can serve its own /routes pages.
func (s *Service) DownloadHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/routes/")
		id, isGPX := strings.CutSuffix(name, ".gpx")
		if !isGPX {
			var isFIT bool
			if id, isFIT = strings.CutSuffix(name, ".fit"); !isFIT {
				next.ServeHTTP(w, r)
				return
			}
		}
		if strings.Contains(id, "/") || r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		userID, ok := authn.GetUserID(r.Context())
		if !ok {
			http.Error(w, "sign in to download routes", http.StatusUnauthorized)
			return
		}
		routeID, err := store.StringToUUID(id)
		if err != nil {
			http.Error(w, "invalid route ID", http.StatusBadRequest)
			return
		}
		route, err := s.routes.GetRoute(r.Context(), userID, routeID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			s.report.Report(r.Context(), "Failed to get route", err)
			http.Error(w, "failed to get route", http.StatusInternalServerError)
			return
		}
		WriteRouteFile(w, r, s.report, route, !isGPX)
	})
}

// WriteRouteFile writes a route as a GPX file download, or a FIT course if
// asFIT is set, named after the route.
func WriteRouteFile(w http.ResponseWriter, r *http.Request, report *errreport.Reporter, route *routes.Route, asFIT bool) {
	points, _ := route.Path()
	filename := DownloadFilename(route.Name, "route")
	if !asFIT {
		w.Header().Set("Content-Type", "application/gpx+xml")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.gpx"`, filename))
		if err := geo.WriteGPX(w, route.Name, points); err != nil {
			report.Report(r.Context(), "Failed to write GPX", err)
		}
		return
	}
	if len(points) < 2 {
		http.Error(w, "the route has no path", http.StatusConflict)
		return
	}
	elapsed := geo.ElapsedSeconds(points, CourseSpeedKMH, CourseClimbMetersPerHour)
	course := &geo.FITCourse{
		Name:           route.Name,
		Points:         points,
		ElapsedSeconds: elapsed,
		CoursePoints:   highPoint(points, elapsed),
		Start:          time.Now(),
	}
	WriteFIT(w, r, report, filename, course)
}

// highPoint returns a course point at the highest point of a path with the
// elapsed seconds at each point, unless that's its start or finish.
func highPoint(points []geo.Point, elapsed []float64) []geo.CoursePoint {
	dists := geo.CumulativeDistances(points)
	high := 0
	for i, p := range points {
		if p.Ele > points[high].Ele {
			high = i
		}
	}
	if high == 0 || high == len(points)-1 {
		return nil
	}
	return []geo.CoursePoint{{Name: "High point", DistanceMeters: dists[high], ElapsedSeconds: elapsed[high], Type: geo.CoursePointSummit}}
}

// DownloadFilename returns a name for a downloaded file, without its
// extension, made from a title, or fallback if it has no safe characters.
func DownloadFilename(title, fallback string) string {
	filename := strings.Trim(unsafeFilenameChars.ReplaceAllString(title, "-"), "-")
	if filename == "" {
		return fallback
	}
	return filename
}

// WriteFIT writes a course as a FIT file download named filename.fit.
func WriteFIT(w http.ResponseWriter, r *http.Request, report *errreport.Reporter, filename string, course *geo.FITCourse) {
	w.Header().Set("Content-Type", "application/vnd.ant.fit")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.fit"`, filename))
	if err := geo.WriteFITCourse(w, course); err != nil {
		report.Report(r.Context(), "Failed to write FIT course", err)
	}
}
