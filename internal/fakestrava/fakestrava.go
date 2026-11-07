// Package fakestrava is a stand-in for Strava, for running trailrunn.ing
// locally without a Strava API application. It has one athlete, who lives in
// Squamish, BC, with about four months of generated runs, trail runs and
// races, hikes, ski tours, and road and mountain bike rides up to today:
// tracks with elevation, heart rate and cadence, and photos on the longer
// outings.
//
// It serves the parts of Strava's OAuth flow and API that trailrunn.ing uses.
// Authorizing always succeeds, after a click on a page that says it's fake.
package fakestrava

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

const (
	// Path is where the server is mounted.
	Path = "/fake-strava/"
	// athleteID is the Strava ID of the athlete.
	athleteID = 31415926
	// tokenLifetime is how long access tokens are valid, as on Strava.
	tokenLifetime = 6 * time.Hour
	// accessPrefix starts access tokens, followed by their expiry.
	accessPrefix = "fake-access-"
)

// Server is a fake Strava. It is an http.Handler serving under Path.
type Server struct {
	// The URL the server is reachable at by browsers, including Path.
	baseURL string
	nowFn   func() time.Time
	mux     *http.ServeMux

	// Routes tracks along mapped paths, nil to make them up.
	router Router
	// Where routed tracks are cached.
	cacheDir string

	mu sync.Mutex
	// The latest generation of activities.
	gen *generation
}

// generation is a generation of the activities, for a day and the regions
// of the router.
type generation struct {
	key  string
	done chan struct{}
	// The activities, newest first, once done.
	activities []*activity
	byID       map[int64]*activity
}

// DefaultCacheDir is where routed tracks are cached by default.
var DefaultCacheDir = filepath.Join(os.TempDir(), "trail-tools-fake-strava")

// New returns a fake Strava reachable by browsers at the base URL of the
// application, e.g. http://localhost:8080. With a router, the tracks of
// activities starting in its regions follow mapped paths; they're cached in
// cacheDir, as routing them takes a while. Without one, or elsewhere, tracks
// are made up.
func New(appURL string, router Router, cacheDir string) *Server {
	s := &Server{
		baseURL:  strings.TrimSuffix(appURL, "/") + strings.TrimSuffix(Path, "/"),
		nowFn:    time.Now,
		mux:      http.NewServeMux(),
		router:   router,
		cacheDir: cacheDir,
	}
	p := strings.TrimSuffix(Path, "/")
	s.mux.HandleFunc("GET "+p+"/oauth/authorize", s.authorize)
	s.mux.HandleFunc("POST "+p+"/oauth/token", s.token)
	s.mux.HandleFunc("GET "+p+"/api/v3/athlete/activities", s.authenticated(s.listActivities))
	s.mux.HandleFunc("GET "+p+"/api/v3/activities/{id}", s.authenticated(s.getActivity))
	s.mux.HandleFunc("GET "+p+"/api/v3/activities/{id}/streams", s.authenticated(s.getStreams))
	s.mux.HandleFunc("GET "+p+"/api/v3/activities/{id}/photos", s.authenticated(s.getPhotos))
	s.mux.HandleFunc("GET "+p+"/photos/{activity}/{photo}", s.photoImage)
	s.mux.HandleFunc("GET "+p+"/activities/{id}", s.activityPageHandler)
	s.mux.HandleFunc("GET "+p+"/athletes/{id}", s.athletePageHandler)
	return s
}

// APIURL is the base URL of the API, for the Strava client.
func (s *Server) APIURL() string { return s.baseURL + "/api/v3" }

// OAuthURL is the base URL of the OAuth endpoints, for the Strava client.
func (s *Server) OAuthURL() string { return s.baseURL + "/oauth" }

// Client returns an HTTP client that calls the server directly, without
// going through the network.
func (s *Server) Client() *http.Client {
	return &http.Client{Transport: handlerTransport{s}}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// handlerTransport serves requests with a handler.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, r)
	return rec.Result(), nil
}

var authorizePage = template.Must(template.New("authorize").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Fake Strava</title>
<style>
body { font-family: system-ui, sans-serif; background: #f7f7fa; color: #242428; display: flex; justify-content: center; padding: 48px 16px; }
main { background: white; border-radius: 12px; box-shadow: 0 1px 3px rgba(0,0,0,.15); max-width: 420px; padding: 32px; }
h1 { color: #fc5200; font-size: 22px; margin: 0 0 8px; }
p { line-height: 1.5; }
.actions { display: flex; gap: 12px; margin-top: 24px; }
a { border-radius: 6px; padding: 10px 16px; text-decoration: none; font-weight: 600; }
.authorize { background: #fc5200; color: white; }
.cancel { color: #6d6d78; }
</style>
</head>
<body>
<main>
<h1>Fake Strava</h1>
<p>This isn't Strava. It's a stand-in for running trailrunn.ing locally, with a made-up athlete and about four months of generated activities around Squamish.</p>
<p>trailrunn.ing asks to read your activities.</p>
<div class="actions">
<a class="authorize" href="{{.Authorize}}">Authorize</a>
<a class="cancel" href="{{.Cancel}}">Cancel</a>
</div>
</main>
</body>
</html>
`))

// authorize shows a page to authorize the application, which redirects back
// to it with a code, or with an error when cancelled.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.Scheme == "" {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	back := func(params url.Values) string {
		u := *redirect
		values := u.Query()
		values.Set("state", q.Get("state"))
		for k, v := range params {
			values[k] = v
		}
		u.RawQuery = values.Encode()
		return u.String()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = authorizePage.Execute(w, map[string]string{
		"Authorize": back(url.Values{"code": {"fake-" + rand.Text()}, "scope": {"read,activity:read_all"}}),
		"Cancel":    back(url.Values{"error": {"access_denied"}}),
	})
}

// token exchanges authorization codes and refresh tokens for access tokens.
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		if !strings.HasPrefix(r.Form.Get("code"), "fake-") {
			writeJSON(w, http.StatusBadRequest, map[string]any{"message": "Bad Request", "errors": []any{map[string]string{"field": "code", "code": "invalid"}}})
			return
		}
	case "refresh_token":
		if !strings.HasPrefix(r.Form.Get("refresh_token"), "fake-refresh-") {
			writeJSON(w, http.StatusBadRequest, map[string]any{"message": "Bad Request", "errors": []any{map[string]string{"field": "refresh_token", "code": "invalid"}}})
			return
		}
	default:
		http.Error(w, "unsupported grant_type", http.StatusBadRequest)
		return
	}
	// Tokens hold their expiry, so they keep working when the server
	// restarts, as Strava's do.
	expiry := s.nowFn().Add(tokenLifetime)
	access := fmt.Sprintf("%s%d-%s", accessPrefix, expiry.Unix(), rand.Text())
	resp := map[string]any{
		"token_type":    "Bearer",
		"access_token":  access,
		"refresh_token": "fake-refresh-" + rand.Text(),
		"expires_at":    expiry.Unix(),
		"expires_in":    int(tokenLifetime.Seconds()),
	}
	if r.Form.Get("grant_type") == "authorization_code" {
		resp["athlete"] = map[string]any{"id": athleteID, "firstname": "Demo", "lastname": "Runner"}
	}
	writeJSON(w, http.StatusOK, resp)
}

// authenticated checks the access token of API requests.
func (s *Server) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		rest, ok := strings.CutPrefix(token, accessPrefix)
		expiry, _, _ := strings.Cut(rest, "-")
		unix, err := strconv.ParseInt(expiry, 10, 64)
		if !ok || err != nil || s.nowFn().After(time.Unix(unix, 0)) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "Authorization Error"})
			return
		}
		// Plenty of the rate limit left.
		w.Header().Set("X-RateLimit-Limit", "600,30000")
		w.Header().Set("X-RateLimit-Usage", "1,1")
		next(w, r)
	}
}

// Prepare generates the activities, so that the first requests don't wait
// for their tracks to be routed.
func (s *Server) Prepare(ctx context.Context) error {
	_, _, err := s.load(ctx)
	return err
}

// load returns the activities, generated for today and the regions the
// router covers. A new generation runs in the background until it's done,
// even if ctx is cancelled, and requests wait for it.
func (s *Server) load(ctx context.Context) ([]*activity, map[int64]*activity, error) {
	now := s.nowFn()
	var regions []geo.Bounds
	if s.router != nil {
		var err error
		if regions, err = s.router.Regions(ctx); err != nil {
			return nil, nil, fmt.Errorf("failed to get the routing regions: %w", err)
		}
	}
	key := fmt.Sprint(now.In(timezone).Format(time.DateOnly), regions)
	s.mu.Lock()
	g := s.gen
	if g == nil || g.key != key {
		g = &generation{key: key, done: make(chan struct{})}
		s.gen = g
		t := &tracer{router: s.router, regions: regions, cacheDir: s.cacheDir}
		go func() {
			defer close(g.done)
			g.activities = generate(context.WithoutCancel(ctx), now, t)
			g.byID = make(map[int64]*activity, len(g.activities))
			for _, a := range g.activities {
				g.byID[a.id] = a
			}
		}()
	}
	s.mu.Unlock()
	select {
	case <-g.done:
		return g.activities, g.byID, nil
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
}

// loadOrFail returns the activities, or writes an error.
func (s *Server) loadOrFail(w http.ResponseWriter, r *http.Request) ([]*activity, map[int64]*activity, bool) {
	activities, byID, err := s.load(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"message": err.Error()})
		return nil, nil, false
	}
	return activities, byID, true
}

// activity returns the activity of a request, or writes a not found error.
func (s *Server) activity(w http.ResponseWriter, r *http.Request) *activity {
	_, byID, ok := s.loadOrFail(w, r)
	if !ok {
		return nil
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if a := byID[id]; err == nil && a != nil {
		return a
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"message": "Record Not Found"})
	return nil
}

// listActivities lists the activities before a time, newest first.
func (s *Server) listActivities(w http.ResponseWriter, r *http.Request) {
	activities, _, ok := s.loadOrFail(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	before := s.nowFn().Add(time.Hour)
	if v, err := strconv.ParseInt(q.Get("before"), 10, 64); err == nil {
		before = time.Unix(v, 0)
	}
	perPage := 30
	if v, err := strconv.Atoi(q.Get("per_page")); err == nil && v > 0 {
		perPage = min(v, 200)
	}
	page := 1
	if v, err := strconv.Atoi(q.Get("page")); err == nil && v > 0 {
		page = v
	}
	var matching []map[string]any
	for _, a := range activities {
		if a.start.Before(before) {
			matching = append(matching, s.summary(a))
		}
	}
	from := min((page-1)*perPage, len(matching))
	to := min(from+perPage, len(matching))
	writeJSON(w, http.StatusOK, append([]map[string]any{}, matching[from:to]...))
}

func (s *Server) getActivity(w http.ResponseWriter, r *http.Request) {
	a := s.activity(w, r)
	if a == nil {
		return
	}
	detail := s.summary(a)
	detail["description"] = a.description
	detail["resource_state"] = 3
	if !a.manual {
		detail["device_name"] = "Garmin Forerunner 965"
	}
	writeJSON(w, http.StatusOK, detail)
}

// summary returns an activity as Strava lists it.
func (s *Server) summary(a *activity) map[string]any {
	local := a.start.In(timezone)
	_, offset := local.Zone()
	hasHR := a.streams != nil
	// Strava marks races with a workout type: 1 for runs, 11 for rides.
	workoutType := 0
	if a.race {
		workoutType = 1
	}
	return map[string]any{
		"resource_state":       2,
		"id":                   a.id,
		"athlete":              map[string]any{"id": athleteID, "resource_state": 1},
		"name":                 a.name,
		"type":                 a.sportType,
		"sport_type":           a.sportType,
		"start_date":           a.start.UTC().Format(time.RFC3339),
		"start_date_local":     local.Format("2006-01-02T15:04:05") + "Z",
		"timezone":             fmt.Sprintf("(GMT%+03d:%02d) %s", offset/3600, abs(offset%3600/60), timezone),
		"workout_type":         workoutType,
		"utc_offset":           offset,
		"distance":             a.distance,
		"moving_time":          a.movingTime,
		"elapsed_time":         a.elapsedTime,
		"total_elevation_gain": a.gain,
		"average_speed":        a.avgSpeed,
		"max_speed":            a.maxSpeed,
		"has_heartrate":        hasHR,
		"average_heartrate":    a.avgHR,
		"max_heartrate":        a.maxHR,
		"average_cadence":      a.avgCadence,
		"suffer_score":         a.sufferScore,
		"total_photo_count":    len(a.photos),
		"manual":               a.manual,
		"map": map[string]any{
			"id":               fmt.Sprintf("a%d", a.id),
			"summary_polyline": a.polyline,
			"resource_state":   2,
		},
	}
}

// getStreams returns the streams of an activity, keyed by type.
func (s *Server) getStreams(w http.ResponseWriter, r *http.Request) {
	a := s.activity(w, r)
	if a == nil {
		return
	}
	if a.streams == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Record Not Found"})
		return
	}
	st := a.streams
	stream := func(data any, n int) map[string]any {
		return map[string]any{"data": data, "series_type": "distance", "original_size": n, "resolution": "high"}
	}
	n := len(st.time)
	writeJSON(w, http.StatusOK, map[string]any{
		"time":            stream(st.time, n),
		"distance":        stream(st.distance, n),
		"latlng":          stream(st.latlng, n),
		"altitude":        stream(st.altitude, n),
		"heartrate":       stream(st.hr, n),
		"cadence":         stream(st.cadence, n),
		"velocity_smooth": stream(st.velocity, n),
		"grade_smooth":    stream(st.grade, n),
	})
}

// getPhotos lists the photos of an activity.
func (s *Server) getPhotos(w http.ResponseWriter, r *http.Request) {
	a := s.activity(w, r)
	if a == nil {
		return
	}
	photos := []map[string]any{}
	for i, p := range a.photos {
		photo := map[string]any{
			"unique_id":  p.id,
			"caption":    p.caption,
			"source":     1,
			"created_at": p.taken.UTC().Format(time.RFC3339),
			"urls":       map[string]string{"2048": fmt.Sprintf("%s/photos/%d/%d.jpg", s.baseURL, a.id, i+1)},
			"sizes":      map[string][]int{"2048": {photoWidth, photoHeight}},
		}
		if p.location != nil {
			photo["location"] = []float64{p.location.Lat, p.location.Lng}
		}
		photos = append(photos, photo)
	}
	writeJSON(w, http.StatusOK, photos)
}

// photoImage serves the image of a photo.
func (s *Server) photoImage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("activity"), 10, 64)
	number, err2 := strconv.Atoi(strings.TrimSuffix(r.PathValue("photo"), ".jpg"))
	if err != nil || err2 != nil {
		http.NotFound(w, r)
		return
	}
	_, byID, ok := s.loadOrFail(w, r)
	if !ok {
		return
	}
	a := byID[id]
	if a == nil || number < 1 || number > len(a.photos) {
		http.NotFound(w, r)
		return
	}
	data, err := landscape(a.photos[number-1].seed)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func abs(n int) int {
	return max(n, -n)
}
