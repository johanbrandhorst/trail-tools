package mapdata_test

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/mapdata"
)

// fakeElevation returns elevations 1000 times the latitude.
type fakeElevation struct {
	lookups int
}

func (f *fakeElevation) Points(_ context.Context, points []geo.Point) ([]float64, error) {
	f.lookups += len(points)
	elevations := make([]float64, len(points))
	for i, p := range points {
		elevations[i] = p.Lat * 1000
	}
	return elevations, nil
}

func TestFillElevations(t *testing.T) {
	t.Parallel()
	elevation := &fakeElevation{}
	client := mapdata.New(mapdata.Config{Elevation: elevation, HTTPClient: &http.Client{}})
	var points []geo.Point
	for i := range 250 {
		points = append(points, geo.Point{Lat: float64(i) * 0.001})
	}
	if err := client.FillElevations(context.Background(), points, 150); err != nil {
		t.Fatalf("FillElevations: %v", err)
	}
	if elevation.lookups > 150 {
		t.Errorf("looked up %d points, want at most 150", elevation.lookups)
	}
	for i, p := range points {
		if diff := p.Ele - p.Lat*1000; diff > 0.01 || diff < -0.01 {
			t.Fatalf("point %d: got elevation %v, want %v", i, p.Ele, p.Lat*1000)
		}
	}
}

func TestStatusErrorPlainText(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
		fmt.Fprint(w, `<?xml version="1.0"?><html><head><title>Nominatim Response</title></head>
<body><p>The data is from OpenStreetMap.</p>
<p><strong>Error</strong>: runtime error: the server is probably too busy.</p></body></html>`)
	}))
	t.Cleanup(srv.Close)
	client := mapdata.New(mapdata.Config{NominatimURL: srv.URL, HTTPClient: srv.Client()})
	_, err := client.Search(context.Background(), "Squamish", 5)
	if err == nil || strings.Contains(err.Error(), "<") || !strings.Contains(err.Error(), "Error : runtime error: the server is probably too busy.") {
		t.Errorf("unexpected error %v", err)
	}
}

func TestHourlyForecast(t *testing.T) {
	t.Parallel()
	start := time.Now().UTC().Truncate(24 * time.Hour)
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		first := start.Add(-7 * time.Hour).Unix()
		fmt.Fprintf(w, `{"utc_offset_seconds":-25200,"hourly":{"time":[%d,%d],"temperature_2m":[4.5,null],"weather_code":[61,3],"is_day":[0,1]}}`, first, first+3600)
	}))
	t.Cleanup(srv.Close)
	client := mapdata.New(mapdata.Config{ForecastURL: srv.URL, HTTPClient: srv.Client(), Elevation: &fakeElevation{}})
	weather, err := client.HourlyForecast(context.Background(), geo.Point{Lat: 49.7, Lng: -123.15, Ele: math.NaN()}, start, start.AddDate(0, 1, 0))
	if err != nil {
		t.Fatalf("HourlyForecast: %v", err)
	}
	// The dates are local dates, and the end is the end of the forecast.
	wantEnd := time.Now().UTC().AddDate(0, 0, mapdata.MaxForecastDays-1).Format(time.DateOnly)
	if query.Get("start_date") != start.Format(time.DateOnly) || query.Get("end_date") != wantEnd ||
		query.Get("timezone") != "auto" || query.Has("elevation") || query.Get("latitude") != "49.70000" {
		t.Errorf("unexpected query %v", query)
	}
	if len(weather) != 2 || weather[0].TemperatureCelsius != 4.5 || weather[0].WeatherCode != 61 || weather[0].IsDay ||
		weather[0].UTCOffset != -7*time.Hour || !weather[1].IsDay || weather[1].TemperatureCelsius != 0 ||
		!weather[1].Time.Equal(start.Add(-6*time.Hour)) {
		t.Errorf("unexpected weather %+v", weather)
	}
	// Dates past the end of the forecast aren't requested.
	query = nil
	weather, err = client.HourlyForecast(context.Background(), geo.Point{}, start.AddDate(0, 1, 0), start.AddDate(0, 2, 0))
	if err != nil || weather != nil || query != nil {
		t.Errorf("forecast past the end: %v, %v, query %v", weather, err, query)
	}
}

// hourlyResponse returns a forecast response for a time zone with hours
// starting at start, whose temperatures are their index, so tests can tell
// which hour was picked.
func hourlyResponse(timezone string, offsetSeconds int, start time.Time, hours int) string {
	var times, temperatures []string
	for i := range hours {
		times = append(times, fmt.Sprint(start.Add(time.Duration(i)*time.Hour).Unix()))
		temperatures = append(temperatures, fmt.Sprint(i))
	}
	return fmt.Sprintf(`{"timezone":%q,"utc_offset_seconds":%d,"hourly":{"time":[%s],"temperature_2m":[%s]}}`,
		timezone, offsetSeconds, strings.Join(times, ","), strings.Join(temperatures, ","))
}

func TestForecastFractionalOffset(t *testing.T) {
	t.Parallel()
	// Open-Meteo's hours are local hours, so in Nepal, at +5:45, they start
	// at a quarter past the UTC hour: local midnight is 18:15 UTC.
	start := time.Date(2026, 10, 17, 18, 15, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, hourlyResponse("Asia/Kathmandu", 20700, start, 48))
	}))
	t.Cleanup(srv.Close)
	client := mapdata.New(mapdata.Config{ForecastURL: srv.URL, HTTPClient: srv.Client(), Elevation: &fakeElevation{}})
	tests := map[string]struct {
		time time.Time
		// wantHour is the index of the hour containing time, or -1 if the
		// forecast doesn't cover it.
		wantHour int
	}{
		"start of an hour":               {time: start.Add(3 * time.Hour), wantHour: 3},
		"on the UTC hour":                {time: time.Date(2026, 10, 18, 2, 0, 0, 0, time.UTC), wantHour: 7},
		"last second of an hour":         {time: start.Add(5*time.Hour - time.Second), wantHour: 4},
		"within the last hour":           {time: start.Add(47*time.Hour + 30*time.Minute), wantHour: 47},
		"before the forecast":            {time: start.Add(-time.Minute), wantHour: -1},
		"after the end of the last hour": {time: start.Add(48 * time.Hour), wantHour: -1},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			point := geo.Point{Lat: 27.99, Lng: 86.93, Ele: math.NaN()}
			weather, err := client.Forecast(context.Background(), []mapdata.ForecastQuery{{Point: point, Time: tt.time}})
			if tt.wantHour < 0 {
				if err == nil {
					t.Errorf("got weather %+v, want an error", weather)
				}
				return
			}
			if err != nil {
				t.Fatalf("Forecast: %v", err)
			}
			wantTime := start.Add(time.Duration(tt.wantHour) * time.Hour)
			if len(weather) != 1 || weather[0].TemperatureCelsius != float64(tt.wantHour) || !weather[0].Time.Equal(wantTime) {
				t.Fatalf("got weather %+v, want hour %d starting at %v", weather, tt.wantHour, wantTime)
			}
			if want := 5*time.Hour + 45*time.Minute; weather[0].UTCOffset != want {
				t.Errorf("got UTC offset %v, want %v", weather[0].UTCOffset, want)
			}
		})
	}
}

func TestHourlyForecastDaylightSavingTime(t *testing.T) {
	t.Parallel()
	// Central European Summer Time, at +2, ends at 01:00 UTC on October 25,
	// 2026, after which it's +1. Open-Meteo gives the offset at the start of
	// the forecast for all of it.
	start := time.Date(2026, 10, 23, 22, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC)
	tests := map[string]struct {
		timezone string
		// wantOffsets are the offsets of the hours before and from end.
		wantOffsets [2]time.Duration
	}{
		"time zone":                 {timezone: "Europe/Berlin", wantOffsets: [2]time.Duration{2 * time.Hour, time.Hour}},
		"unknown time zone":         {timezone: "Europe/Nowhere", wantOffsets: [2]time.Duration{2 * time.Hour, 2 * time.Hour}},
		"time zone not in response": {wantOffsets: [2]time.Duration{2 * time.Hour, 2 * time.Hour}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, hourlyResponse(tt.timezone, 7200, start, 72))
			}))
			t.Cleanup(srv.Close)
			client := mapdata.New(mapdata.Config{ForecastURL: srv.URL, HTTPClient: srv.Client(), Elevation: &fakeElevation{}})
			from := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
			weather, err := client.HourlyForecast(context.Background(), geo.Point{Lat: 52.52, Lng: 13.4, Ele: math.NaN()}, from, from.AddDate(0, 0, 2))
			if err != nil {
				t.Fatalf("HourlyForecast: %v", err)
			}
			if len(weather) != 72 {
				t.Fatalf("got %d hours, want 72", len(weather))
			}
			for _, w := range weather {
				want := tt.wantOffsets[0]
				if !w.Time.Before(end) {
					want = tt.wantOffsets[1]
				}
				if w.UTCOffset != want {
					t.Errorf("hour at %v: got UTC offset %v, want %v", w.Time, w.UTCOffset, want)
				}
			}
		})
	}
}
