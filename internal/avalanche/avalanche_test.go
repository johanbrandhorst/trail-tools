package avalanche

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/geo"
)

// newServer serves the testdata file named by the request's lat parameter,
// e.g. lat=glacier-2026-02-15, counting the requests.
func newServer(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		if r.URL.Path != "/forecasts/en/products/point" {
			http.NotFound(w, r)
			return
		}
		files := map[string]string{"1.00000": "glacier-2026-02-15", "2.00000": "glacier-early-season", "3.00000": "none"}
		name, ok := files[r.URL.Query().Get("lat")]
		if !ok {
			http.Error(w, "unknown point", http.StatusBadRequest)
			return
		}
		data, err := os.ReadFile("testdata/" + name + ".json")
		if err != nil {
			t.Errorf("ReadFile: %v", err)
		}
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return requests
	}
}

func TestForecast(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)
	c := New(srv.URL, srv.Client())

	f, err := c.Forecast(context.Background(), geo.Point{Lat: 1, Lng: -117.5})
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if f == nil {
		t.Fatal("got no forecast")
	}
	if f.Title != "Glacier" || f.Forecaster != "Parks Canada" || !strings.HasPrefix(f.URL, "https://avalanche.ca/forecasts/") {
		t.Errorf("got %q by %q at %q, want Glacier by Parks Canada on avalanche.ca", f.Title, f.Forecaster, f.URL)
	}
	if want := time.Date(2026, 2, 16, 0, 0, 0, 0, time.UTC); !f.ValidUntil.Equal(want) {
		t.Errorf("got valid until %v, want %v", f.ValidUntil, want)
	}
	if f.Confidence != "Moderate" {
		t.Errorf("got confidence %q, want Moderate", f.Confidence)
	}
	if strings.Contains(f.Highlights, "<") || !strings.Contains(f.Highlights, "open terrain.\n\nKeep the persistent") {
		t.Errorf("got highlights %q, want plain text paragraphs", f.Highlights)
	}
	// The first rating's instant is 2026-02-16T00:00Z, which is the
	// afternoon of Sunday the 15th in Vancouver.
	var dates []string
	for _, d := range f.Days {
		dates = append(dates, d.Date)
	}
	if want := []string{"2026-02-15", "2026-02-16", "2026-02-17"}; !slices.Equal(dates, want) {
		t.Errorf("got dates %v, want %v", dates, want)
	}
	day, ok := f.Day("2026-02-15")
	if !ok {
		t.Fatal("Day: got no day for 2026-02-15")
	}
	if day.Alpine.Danger != DangerConsiderable || day.Alpine.Description != "3 - Considerable" || day.Max() != DangerConsiderable {
		t.Errorf("got alpine %+v, max %v, want 3 - Considerable", day.Alpine, day.Max())
	}
	if _, ok := f.Day("2026-02-20"); ok {
		t.Error("Day: got a day for 2026-02-20, beyond the forecast")
	}
	if len(f.Problems) != 3 {
		t.Fatalf("got %d problems, want 3", len(f.Problems))
	}
	wind := f.Problems[0]
	if wind.Type != "windslab" || wind.Name != "Wind Slab" || wind.Likelihood != "Possible" || wind.MinSize != 1 || wind.MaxSize != 2 {
		t.Errorf("got problem %+v, want a possible size 1-2 wind slab", wind)
	}
	if !slices.Equal(wind.Elevations, []string{"Alpine", "Treeline"}) {
		t.Errorf("got elevations %v, want Alpine and Treeline", wind.Elevations)
	}
	if len(wind.Aspects) == 0 || !slices.IsSortedFunc(wind.Aspects, func(a, b string) int {
		return slices.Index(compass, strings.ToLower(a)) - slices.Index(compass, strings.ToLower(b))
	}) {
		t.Errorf("got aspects %v, want them in compass order", wind.Aspects)
	}
	if len(f.TravelAdvice) == 0 {
		t.Error("got no travel advice")
	}
}

func TestForecastEarlySeason(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)
	c := New(srv.URL, srv.Client())

	// Early season is unrated, but still a forecast, while the bands in
	// summer conditions are not.
	f, err := c.Forecast(context.Background(), geo.Point{Lat: 2, Lng: -117.5})
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if f == nil {
		t.Fatal("got no forecast")
	}
	day := f.Days[0]
	if day.Max() != DangerNone || day.Alpine.Description != "Early Season" {
		t.Errorf("got %+v, want an unrated early season", day)
	}
}

func TestForecastNone(t *testing.T) {
	t.Parallel()
	srv, requests := newServer(t)
	c := New(srv.URL, srv.Client())
	now := time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)
	c.nowFn = func() time.Time { return now }

	// Outside the forecast areas, there is no forecast.
	f, err := c.Forecast(context.Background(), geo.Point{Lat: 3, Lng: -115})
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if f != nil {
		t.Errorf("got %+v, want no forecast", f)
	}
	// It's cached, for nearby points too.
	if _, err := c.Forecast(context.Background(), geo.Point{Lat: 3.001, Lng: -115.001}); err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if got := requests(); got != 1 {
		t.Errorf("got %d requests, want 1", got)
	}
	// Until it expires.
	now = now.Add(cacheTTL)
	if _, err := c.Forecast(context.Background(), geo.Point{Lat: 3, Lng: -115}); err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if got := requests(); got != 2 {
		t.Errorf("got %d requests, want 2", got)
	}
}

func TestForecastError(t *testing.T) {
	t.Parallel()
	srv, _ := newServer(t)
	c := New(srv.URL, srv.Client())
	if _, err := c.Forecast(context.Background(), geo.Point{Lat: 4, Lng: -115}); err == nil {
		t.Error("Forecast: got no error for a failed request")
	}
}

func TestWarning(t *testing.T) {
	t.Parallel()
	f := &Forecast{Title: "Glacier", Forecaster: "Parks Canada", URL: "https://avalanche.ca/forecasts/x"}
	high := Rating{Danger: DangerHigh}
	tests := map[string]struct {
		day  Day
		want string
	}{
		"considerable": {
			day: Day{Date: "2026-02-15", Alpine: Rating{Danger: DangerConsiderable}},
		},
		"high in the alpine": {
			day:  Day{Date: "2026-02-15", Alpine: high, Treeline: Rating{Danger: DangerConsiderable}},
			want: "Avalanche danger is High in the alpine on Sunday, February 15 in the Glacier forecast by Parks Canada: travel in avalanche terrain is not recommended. Keep to simple terrain away from avalanche paths, or choose another day. Read the full forecast at https://avalanche.ca/forecasts/x.",
		},
		"high everywhere": {
			day:  Day{Date: "2026-02-15", Alpine: high, Treeline: high, BelowTreeline: high},
			want: "Avalanche danger is High at all elevations on Sunday, February 15 in the Glacier forecast by Parks Canada: travel in avalanche terrain is not recommended. Keep to simple terrain away from avalanche paths, or choose another day. Read the full forecast at https://avalanche.ca/forecasts/x.",
		},
		"extreme": {
			day:  Day{Date: "2026-02-15", Alpine: Rating{Danger: DangerExtreme}, Treeline: high},
			want: "Avalanche danger is Extreme in the alpine on Sunday, February 15 in the Glacier forecast by Parks Canada: avoid all avalanche terrain, and choose another day. Read the full forecast at https://avalanche.ca/forecasts/x.",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := f.Warning(tt.day); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
