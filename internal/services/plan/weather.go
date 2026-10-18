package plan

import (
	"context"
	"errors"
	"math"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/mapdata"
	"github.com/johanbrandhorst/trail-tools/internal/plans"
	"github.com/johanbrandhorst/trail-tools/internal/routes"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// WeatherSource forecasts the weather.
type WeatherSource interface {
	HourlyForecast(ctx context.Context, point geo.Point, start, end time.Time) ([]mapdata.Weather, error)
}

// RouteSource gets the user's routes, for where workouts start.
type RouteSource interface {
	GetRoute(ctx context.Context, userID, routeID pgtype.UUID) (*routes.Route, error)
}

// ActivitySource finds where the user's activities start, for workouts
// without a route.
type ActivitySource interface {
	LatestStartPoint(ctx context.Context, userID pgtype.UUID) (geo.Point, bool, error)
}

const (
	// The daytime hours a workout's weather is summarized for, in local
	// time: from the start of the first to the end of the last.
	daytimeFirstHour = 6
	daytimeLastHour  = 20
	// How many days ahead workouts have a forecast.
	maxWeatherDaysAhead = mapdata.MaxForecastDays - 1
)

// weatherWindow returns the first and last date with a forecast. It starts
// a day before today in UTC, as that can still be today for the user.
func weatherWindow() (time.Time, time.Time) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	return today.AddDate(0, 0, -1), today.AddDate(0, 0, maxWeatherDaysAhead)
}

func (s *Service) ListWorkoutWeather(ctx context.Context, req *plansv1.ListWorkoutWeatherRequest) (*plansv1.ListWorkoutWeatherResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	start, err := store.ParseDate(req.GetStartDate())
	if err != nil {
		return nil, invalidArgument("invalid start_date: %v", err)
	}
	end, err := store.ParseDate(req.GetEndDate())
	if err != nil {
		return nil, invalidArgument("invalid end_date: %v", err)
	}
	if end.Before(start) {
		return nil, invalidArgument("end_date must not be before start_date")
	}
	first, last := weatherWindow()
	start, end = latest(start, first), earliest(end, last)
	if end.Before(start) {
		return plansv1.ListWorkoutWeatherResponse_builder{}.Build(), nil
	}
	workouts, err := s.plans.ListUserWorkouts(ctx, userID, start, end)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list workouts")
	}
	weather, err := s.workoutWeather(ctx, userID, workouts, false)
	if err != nil {
		return nil, err
	}
	return plansv1.ListWorkoutWeatherResponse_builder{Weather: weather}.Build(), nil
}

func (s *Service) GetWorkoutWeather(ctx context.Context, req *plansv1.GetWorkoutWeatherRequest) (*plansv1.GetWorkoutWeatherResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	workoutID, err := store.StringToUUID(req.GetWorkoutId())
	if err != nil {
		return nil, invalidArgument("invalid workout ID: %v", err)
	}
	workout, err := s.plans.GetWorkout(ctx, userID, workoutID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get workout")
	}
	if first, last := weatherWindow(); workout.Date.Before(first) || workout.Date.After(last) {
		return plansv1.GetWorkoutWeatherResponse_builder{}.Build(), nil
	}
	weather, err := s.workoutWeather(ctx, userID, []*plans.Workout{workout}, true)
	if err != nil {
		return nil, err
	}
	resp := plansv1.GetWorkoutWeatherResponse_builder{}
	if len(weather) > 0 {
		resp.Weather = weather[0]
	}
	return resp.Build(), nil
}

// indoors reports whether a workout doesn't need a forecast: rest days
// and strength training.
func indoors(w *plans.Workout) bool {
	return w.Type == plans.WorkoutTypeRest || w.Type == plans.WorkoutTypeStrength
}

// weatherLocation is where a workout's forecast is for.
type weatherLocation struct {
	point  geo.Point
	source plansv1.WeatherLocationSource
}

// workoutWeather forecasts the weather of the days of workouts, except
// those indoors or without a location. hourly includes the forecast for
// each hour of the daytime.
func (s *Service) workoutWeather(ctx context.Context, userID pgtype.UUID, workouts []*plans.Workout, hourly bool) ([]*plansv1.WorkoutWeather, error) {
	// Workouts at the same location share a forecast.
	type group struct {
		location   weatherLocation
		first      time.Time
		last       time.Time
		byLocalDay map[string][]mapdata.Weather
	}
	var groups []*group
	groupOf := make(map[*plans.Workout]*group, len(workouts))
	var fallback *weatherLocation
	for _, w := range workouts {
		if indoors(w) {
			continue
		}
		location, ok, err := s.workoutLocation(ctx, userID, w, &fallback)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var g *group
		for _, other := range groups {
			if other.location.point.Lat == location.point.Lat && other.location.point.Lng == location.point.Lng {
				g = other
				break
			}
		}
		if g == nil {
			g = &group{location: location, first: w.Date, last: w.Date}
			groups = append(groups, g)
		}
		g.first, g.last = earliest(g.first, w.Date), latest(g.last, w.Date)
		groupOf[w] = g
	}
	for _, g := range groups {
		hours, err := s.weather.HourlyForecast(ctx, g.location.point, g.first, g.last)
		if err != nil {
			s.report.Report(ctx, "Failed to get weather forecast", err)
			return nil, connect.NewError(connect.CodeUnavailable, "the weather forecast is unavailable, try again later")
		}
		g.byLocalDay = daytimeByLocalDay(hours)
	}
	var result []*plansv1.WorkoutWeather
	for _, w := range workouts {
		g := groupOf[w]
		if g == nil {
			continue
		}
		date := store.FormatDate(w.Date)
		hours := g.byLocalDay[date]
		if len(hours) == 0 {
			continue
		}
		b := plansv1.WorkoutWeather_builder{
			WorkoutId:      store.UUIDToString(w.ID),
			Date:           date,
			Location:       geov1.LatLng_builder{Lat: g.location.point.Lat, Lng: g.location.point.Lng}.Build(),
			LocationSource: g.location.source,
			Day:            summarizeDay(hours),
		}
		if hourly {
			for _, h := range hours {
				b.Hours = append(b.Hours, hourToProto(h))
			}
		}
		result = append(result, b.Build())
	}
	return result, nil
}

// workoutLocation returns where a workout's forecast is for: the start of
// its route, or else where the user's most recent activity started, which
// is looked up once and kept in fallback. It returns false if there is
// neither.
func (s *Service) workoutLocation(ctx context.Context, userID pgtype.UUID, w *plans.Workout, fallback **weatherLocation) (weatherLocation, bool, error) {
	if w.RouteID.Valid {
		route, err := s.routes.GetRoute(ctx, userID, w.RouteID)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return weatherLocation{}, false, s.toConnectError(ctx, err, "failed to get route")
		default:
			if path, _ := route.Path(); len(path) > 0 {
				return weatherLocation{point: path[0], source: plansv1.WeatherLocationSource_WEATHER_LOCATION_SOURCE_ROUTE_START}, true, nil
			}
		}
	}
	if *fallback == nil {
		point, ok, err := s.activities.LatestStartPoint(ctx, userID)
		if err != nil {
			return weatherLocation{}, false, s.toConnectError(ctx, err, "failed to get latest activity")
		}
		location := weatherLocation{point: point}
		if ok {
			location.source = plansv1.WeatherLocationSource_WEATHER_LOCATION_SOURCE_LATEST_ACTIVITY
		}
		*fallback = &location
	}
	if (*fallback).source == plansv1.WeatherLocationSource_WEATHER_LOCATION_SOURCE_UNSPECIFIED {
		return weatherLocation{}, false, nil
	}
	return **fallback, true, nil
}

// daytimeByLocalDay groups the daytime hours of a forecast by their local
// date, as YYYY-MM-DD. Each hour is placed with its own UTC offset rather
// than one for the whole forecast: a forecast spans 16 days, and after a
// daylight saving time transition within it a single offset would shift
// every later day by an hour, dropping an hour at one end of the daytime and
// adding one from the night at the other.
func daytimeByLocalDay(hours []mapdata.Weather) map[string][]mapdata.Weather {
	byLocalDay := make(map[string][]mapdata.Weather)
	for _, h := range hours {
		// Shifting the UTC time by the offset gives a time whose UTC fields
		// are the local ones, without needing the time zone.
		local := h.Time.UTC().Add(h.UTCOffset)
		if local.Hour() < daytimeFirstHour || local.Hour() > daytimeLastHour {
			continue
		}
		day := local.Format(time.DateOnly)
		byLocalDay[day] = append(byLocalDay[day], h)
	}
	return byLocalDay
}

// summarizeDay summarizes the weather of the hours of a day.
func summarizeDay(hours []mapdata.Weather) *plansv1.DayWeather {
	b := plansv1.DayWeather_builder{
		MinTemperatureCelsius: math.Inf(1),
		MaxTemperatureCelsius: math.Inf(-1),
	}
	for _, h := range hours {
		b.MinTemperatureCelsius = min(b.MinTemperatureCelsius, h.TemperatureCelsius)
		b.MaxTemperatureCelsius = max(b.MaxTemperatureCelsius, h.TemperatureCelsius)
		b.MaxPrecipitationProbability = max(b.MaxPrecipitationProbability, int32(h.PrecipitationProbability))
		b.PrecipitationMm += h.PrecipitationMM
		b.SnowfallCm += h.SnowfallCM
		b.MaxWindSpeedKmh = max(b.MaxWindSpeedKmh, h.WindSpeedKMH)
		b.MaxWindGustsKmh = max(b.MaxWindGustsKmh, h.WindGustsKMH)
		// Higher WMO codes are more severe weather.
		b.WeatherCode = max(b.WeatherCode, int32(h.WeatherCode))
	}
	b.Description = mapdata.WeatherDescription(int(b.WeatherCode))
	return b.Build()
}

func hourToProto(h mapdata.Weather) *plansv1.HourWeather {
	return plansv1.HourWeather_builder{
		Time:                       timestamppb.New(h.Time),
		TemperatureCelsius:         h.TemperatureCelsius,
		ApparentTemperatureCelsius: h.ApparentTemperature,
		PrecipitationProbability:   int32(h.PrecipitationProbability),
		PrecipitationMm:            h.PrecipitationMM,
		WeatherCode:                int32(h.WeatherCode),
		WindSpeedKmh:               h.WindSpeedKMH,
		WindGustsKmh:               h.WindGustsKMH,
		WindDirectionDegrees:       int32(h.WindDirectionDegrees),
		IsDay:                      h.IsDay,
	}.Build()
}

func earliest(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
