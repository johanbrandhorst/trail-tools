package plan

import (
	"context"
	"fmt"

	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
	"github.com/bs-tools/trailrunn.ing/internal/geo"
)

// ForestSource tells how much of a route, or of the trails around a place,
// is in forest, from the imported map data.
type ForestSource interface {
	ForestDistances(ctx context.Context, paths [][]geo.Point) ([]float64, error)
	ForestShare(ctx context.Context, center geo.Point, radius float64) (float64, error)
}

// SetForests sets where forests are looked up, for warnings about high
// winds in forest. Without it, there are none.
func (s *Service) SetForests(forests ForestSource) {
	s.forests = forests
}

const (
	// highWindGustsKmh is the strength of gusts from which running in forest
	// is dangerous: gale force, 8 on the Beaufort scale, when twigs and
	// branches break off trees, and the strength forestry services warn
	// people out of the woods at. Weakened trees can fall too.
	highWindGustsKmh = 60
	// minForestMeters is how much of a route has to be in forest for a
	// warning: a few minutes of running under trees, rather than passing a
	// wood, or a stray match of the route to the map.
	minForestMeters = 300
	// forestAreaRadiusMeters is the radius of the area around a workout's
	// location whose trails are taken to be where it goes, when it has no
	// route.
	forestAreaRadiusMeters = 1000
	// minForestShare is the share of the trails around a workout's location
	// that have to be in forest for a warning, when it has no route.
	minForestShare = 0.5
)

// windy is a workout whose forecast has high winds, to check for forest.
type windy struct {
	// The index of the workout's forecast.
	index    int
	location weatherLocation
	// Whether the gusts are forecast during the workout, rather than during
	// the daytime of a workout without a start time.
	during bool
	gusts  float64
	// The route, nil for workouts without one, or whose route has no path.
	path []geo.Point
}

// windWarnings returns the warnings of the workouts with high winds that
// are in forest, by the index of their forecast. Looking up forests is best
// effort: if it fails, the forecasts go without warnings.
func (s *Service) windWarnings(ctx context.Context, workouts []windy) map[int]*plansv1.WeatherWarning {
	if s.forests == nil || len(workouts) == 0 {
		return nil
	}
	warnings := make(map[int]*plansv1.WeatherWarning)
	var paths [][]geo.Point
	var routed []windy
	// Workouts at the same location share their surroundings.
	shares := make(map[geo.Point]float64)
	for _, w := range workouts {
		if len(w.path) > 0 {
			paths = append(paths, w.path)
			routed = append(routed, w)
			continue
		}
		point := geo.Point{Lat: w.location.point.Lat, Lng: w.location.point.Lng}
		share, ok := shares[point]
		if !ok {
			var err error
			share, err = s.forests.ForestShare(ctx, point, forestAreaRadiusMeters)
			if err != nil {
				s.report.Report(ctx, "Failed to look up forests, leaving out wind warnings", err)
				return nil
			}
			shares[point] = share
		}
		if share >= minForestShare {
			warnings[w.index] = windWarning(w, 0)
		}
	}
	if len(paths) > 0 {
		distances, err := s.forests.ForestDistances(ctx, paths)
		if err != nil {
			s.report.Report(ctx, "Failed to look up forests, leaving out wind warnings", err)
			return nil
		}
		for i, w := range routed {
			if distances[i] >= minForestMeters {
				warnings[w.index] = windWarning(w, distances[i])
			}
		}
	}
	return warnings
}

// windWarning returns the warning of a workout with high winds in forest,
// with forestMeters of its route in forest, or 0 if it has no route.
func windWarning(w windy, forestMeters float64) *plansv1.WeatherWarning {
	when := "during the workout"
	if !w.during {
		when = "during the day"
	}
	where := fmt.Sprintf("%.1f km of its route is in forest", forestMeters/1000)
	if forestMeters == 0 {
		switch w.location.source {
		case plansv1.WeatherLocationSource_WEATHER_LOCATION_SOURCE_LATEST_ACTIVITY:
			where = "the trails around where your latest activity started are mostly in forest"
		default:
			where = "the trails around its location are mostly in forest"
		}
	}
	return plansv1.WeatherWarning_builder{
		Kind: plansv1.WeatherWarningKind_WEATHER_WARNING_KIND_WIND_IN_FOREST,
		Message: fmt.Sprintf("Gusts up to %.0f km/h are forecast %s, and %s, where wind brings down branches and trees. "+
			"Choose a route in the open, or move the workout to a calmer day.", w.gusts, when, where),
		MaxWindGustsKmh:      w.gusts,
		ForestDistanceMeters: forestMeters,
	}.Build()
}
