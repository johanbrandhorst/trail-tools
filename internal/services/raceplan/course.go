package raceplan

import (
	"cmp"
	"net/http"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/races"
	"github.com/johanbrandhorst/trail-tools/internal/services/route"
)

// CourseHandler serves the course of a race plan as a FIT course for Garmin
// and other devices at /race-plans/{id}.fit: the race's course route with
// its checkpoints as course points, timed by the plan. It must be mounted at
// /race-plans/ behind the authn middleware. Other paths are passed to next,
// so the frontend can serve its own /race-plans pages.
func (s *Service) CourseHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/race-plans/"), ".fit")
		if !ok || strings.Contains(id, "/") || r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		plan, race, err := s.getPlan(r.Context(), id)
		if err != nil {
			http.Error(w, connect.CodeOf(err).String(), httpStatus(connect.CodeOf(err)))
			return
		}
		points, err := s.course(r.Context(), race)
		if err != nil {
			http.Error(w, "failed to get the course", http.StatusInternalServerError)
			return
		}
		if len(points) < 2 {
			http.Error(w, "the race has no course route", http.StatusConflict)
			return
		}
		checkpoints := slices.Clone(plan.Checkpoints)
		slices.SortStableFunc(checkpoints, func(a, b *races.Checkpoint) int {
			return cmp.Compare(a.DistanceMeters, b.DistanceMeters)
		})
		elapsed, arrivals := plannedSeconds(points, checkpoints)
		course := &geo.FITCourse{
			Name:           race.Name,
			Points:         points,
			ElapsedSeconds: elapsed,
			CoursePoints:   checkpointCoursePoints(checkpoints, arrivals),
			Start:          time.Now(),
		}
		route.WriteFIT(w, r, s.report, route.DownloadFilename(race.Name, "race"), course)
	})
}

// httpStatus returns the HTTP status of a Connect error code.
func httpStatus(code connect.Code) int {
	switch code {
	case connect.CodeInvalidArgument:
		return http.StatusBadRequest
	case connect.CodeUnauthenticated:
		return http.StatusUnauthorized
	case connect.CodeNotFound:
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// checkpointCoursePoints returns the checkpoints as course points reached at
// the arrival times: as food where there's aid, so devices show what to
// expect.
func checkpointCoursePoints(checkpoints []*races.Checkpoint, arrivals []float64) []geo.CoursePoint {
	points := make([]geo.CoursePoint, len(checkpoints))
	for i, cp := range checkpoints {
		points[i] = geo.CoursePoint{Name: cp.Name, DistanceMeters: cp.DistanceMeters, ElapsedSeconds: arrivals[i], Type: geo.CoursePointGeneric}
		if strings.TrimSpace(cp.Aid) != "" {
			points[i].Type = geo.CoursePointFood
		}
	}
	return points
}

// plannedSeconds returns the planned elapsed seconds at each point of the
// course and the arrival at each checkpoint, from checkpoints sorted by
// distance. Each segment between checkpoints takes its planned time, spread
// along it like the pace model so climbs take longer, and the planned stops
// are added at the checkpoints. Segments without a planned time, and the
// course after the last checkpoint, follow the pace model.
func plannedSeconds(points []geo.Point, checkpoints []*races.Checkpoint) (seconds, arrivals []float64) {
	dists := geo.CumulativeDistances(points)
	model := geo.ElapsedSeconds(points, route.CourseSpeedKMH, route.CourseClimbMetersPerHour)
	// modelAt returns the model's elapsed seconds at a distance.
	modelAt := func(d float64) float64 {
		i, _ := slices.BinarySearch(dists, d)
		if i == 0 {
			return model[0]
		}
		if i >= len(dists) {
			return model[len(model)-1]
		}
		f := (d - dists[i-1]) / (dists[i] - dists[i-1])
		return model[i-1] + (model[i]-model[i-1])*f
	}
	// The segment the walk is in: the model's time and the planned time at
	// its start, and the checkpoint at its end.
	segmentModel, departure := 0.0, 0.0
	next := 0
	timeAt := func(d float64) float64 {
		elapsed := modelAt(d) - segmentModel
		if next < len(checkpoints) && checkpoints[next].PlannedSegmentSeconds > 0 {
			if span := modelAt(checkpoints[next].DistanceMeters) - segmentModel; span > 0 {
				elapsed *= float64(checkpoints[next].PlannedSegmentSeconds) / span
			}
		}
		return departure + elapsed
	}
	// passCheckpoint moves the walk past the next checkpoint.
	passCheckpoint := func() {
		cp := checkpoints[next]
		arrival := timeAt(cp.DistanceMeters)
		arrivals = append(arrivals, arrival)
		departure = arrival + float64(cp.PlannedStopSeconds)
		segmentModel = modelAt(cp.DistanceMeters)
		next++
	}
	seconds = make([]float64, len(points))
	for i, d := range dists {
		// Points within a meter of a checkpoint arrive at it.
		for next < len(checkpoints) && checkpoints[next].DistanceMeters < d-1 {
			passCheckpoint()
		}
		seconds[i] = timeAt(d)
	}
	// Checkpoints at or past the finish.
	for next < len(checkpoints) {
		passCheckpoint()
	}
	return seconds, arrivals
}
