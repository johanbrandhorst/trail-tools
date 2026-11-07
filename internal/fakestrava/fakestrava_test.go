package fakestrava

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/strava"
)

// newClient returns a fake Strava served by a test server and a Strava client
// for it.
func newClient(t *testing.T) (*Server, *strava.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(nil)
	t.Cleanup(srv.Close)
	fake := New(srv.URL, nil, "")
	srv.Config.Handler = fake
	client := strava.New(strava.Config{
		ClientID:     "fake",
		ClientSecret: "fake",
		RedirectURL:  "http://app.example/strava/callback",
		BaseURL:      fake.APIURL(),
		OAuthBaseURL: fake.OAuthURL(),
		HTTPClient:   fake.Client(),
	})
	return fake, client, srv
}

func TestFakeStrava(t *testing.T) {
	_, client, srv := newClient(t)
	ctx := context.Background()

	// Authorizing shows a page linking back to the app with a code.
	resp, err := srv.Client().Get(client.AuthCodeURL("the-state"))
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	link := regexp.MustCompile(`class="authorize" href="([^"]+)"`).FindSubmatch(page)
	if resp.StatusCode != http.StatusOK || link == nil {
		t.Fatalf("authorize page: status %d\n%s", resp.StatusCode, page)
	}
	back, err := url.Parse(strings.ReplaceAll(string(link[1]), "&amp;", "&"))
	if err != nil || back.Host != "app.example" || back.Query().Get("state") != "the-state" ||
		!strings.Contains(back.Query().Get("scope"), "activity:read") {
		t.Fatalf("authorize link %q", link[1])
	}
	token, athlete, err := client.Exchange(ctx, back.Query().Get("code"))
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if athlete.ID != athleteID || athlete.FirstName != "Demo" || token.Expiry.Before(time.Now()) {
		t.Errorf("got athlete %+v and token %+v", athlete, token)
	}
	refreshed, err := client.Refresh(ctx, token.RefreshToken)
	if err != nil || refreshed.AccessToken == token.AccessToken {
		t.Errorf("Refresh = %v, %v", refreshed, err)
	}
	// Tokens keep working when the server restarts.
	restarted := New(srv.URL, nil, "")
	restartedClient := strava.New(strava.Config{BaseURL: restarted.APIURL(), HTTPClient: restarted.Client()})
	if _, err := restartedClient.ListActivities(ctx, token.AccessToken, time.Time{}, 1); err != nil {
		t.Errorf("ListActivities after a restart: %v", err)
	}
	if _, err := client.ListActivities(ctx, "not-a-token", time.Time{}, 10); !errors.Is(err, strava.ErrUnauthorized) {
		t.Errorf("ListActivities without a token = %v, want ErrUnauthorized", err)
	}

	// Activities are listed newest first, a page at a time.
	var all []*strava.Activity
	var before time.Time
	for {
		page, err := client.ListActivities(ctx, token.AccessToken, before, 50)
		if err != nil {
			t.Fatalf("ListActivities: %v", err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		before = page[len(page)-1].StartDate
	}
	if len(all) < 50 {
		t.Fatalf("got %d activities, want months of them", len(all))
	}
	sports := map[string]int{}
	for i, a := range all {
		sports[a.SportType]++
		if i > 0 && !a.StartDate.Before(all[i-1].StartDate) {
			t.Errorf("activity %d isn't older than the one before", i)
		}
		if a.StartDate.After(time.Now()) || a.TimezoneName() != "America/Vancouver" || a.Distance <= 0 || a.MovingTime <= 0 {
			t.Errorf("unexpected activity %+v", a)
		}
	}
	for _, sport := range []string{"Run", "TrailRun", "Hike", "Ride", "MountainBikeRide", "BackcountrySki"} {
		if sports[sport] == 0 {
			t.Errorf("no %s activities in %v", sport, sports)
		}
	}

	// Now and then, a trail run is a race.
	racing := false
	for _, a := range all {
		if a.SportType == "TrailRun" && bytes.Contains(a.Raw, []byte(`"workout_type":1`)) {
			racing = true
		}
	}
	if !racing {
		t.Error("no races")
	}

	var long, manual *strava.Activity
	for _, a := range all {
		if a.SportType == "TrailRun" && a.TotalPhotoCount >= 3 && long == nil {
			long = a
		}
		if a.Map.SummaryPolyline == "" && manual == nil {
			manual = a
		}
	}
	if long == nil || manual == nil {
		t.Fatalf("no long run with photos (%v) or manual activity (%v)", long, manual)
	}
	if long.TotalElevationGain < 500 || long.ElapsedTime < long.MovingTime || long.AverageHeartrate < 100 {
		t.Errorf("unexpected long run %+v", long)
	}
	detail, err := client.GetActivity(ctx, token.AccessToken, long.ID)
	if err != nil || detail.DeviceName == "" || detail.Name != long.Name {
		t.Errorf("GetActivity = %+v, %v", detail, err)
	}
	streams, err := client.GetStreams(ctx, token.AccessToken, long.ID)
	if err != nil {
		t.Fatalf("GetStreams: %v", err)
	}
	n := len(streams.Time)
	if n < 100 || len(streams.LatLng) != n || len(streams.Altitude) != n || len(streams.Heartrate) != n || len(streams.Distance) != n {
		t.Fatalf("got streams of %d points with inconsistent lengths", n)
	}
	for i := 1; i < n; i++ {
		if streams.Time[i] < streams.Time[i-1] || streams.Distance[i] < streams.Distance[i-1] {
			t.Fatalf("streams go back in time or distance at %d", i)
		}
	}
	if _, err := client.GetStreams(ctx, token.AccessToken, manual.ID); !errors.Is(err, strava.ErrNotFound) {
		t.Errorf("GetStreams of a manual activity = %v, want ErrNotFound", err)
	}

	// Photos have images, mostly with a location.
	photos, err := client.GetActivityPhotos(ctx, token.AccessToken, long.ID)
	if err != nil || len(photos) != int(long.TotalPhotoCount) {
		t.Fatalf("GetActivityPhotos = %d photos, %v, want %d", len(photos), err, long.TotalPhotoCount)
	}
	p := photos[0]
	if p.CreatedAt.Before(long.StartDate) || p.Width != photoWidth || p.UniqueID == "" {
		t.Errorf("unexpected photo %+v", p)
	}
	resp, err = srv.Client().Get(p.URL)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil || cfg.Width != photoWidth || cfg.Height != photoHeight {
		t.Errorf("photo image: %v, %+v", err, cfg)
	}
}

func TestPages(t *testing.T) {
	fake, client, srv := newClient(t)
	ctx := context.Background()
	token, _, err := client.Exchange(ctx, "fake-code")
	if err != nil {
		t.Fatal(err)
	}
	activities, err := client.ListActivities(ctx, token.AccessToken, time.Time{}, 200)
	if err != nil {
		t.Fatal(err)
	}
	var long *strava.Activity
	for _, a := range activities {
		if a.TotalPhotoCount > 0 {
			long = a
			break
		}
	}
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := srv.Client().Get(fake.WebURL() + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	status, page := get(fmt.Sprintf("/activities/%d", long.ID))
	if status != http.StatusOK || !strings.Contains(page, template.HTMLEscapeString(long.Name)) ||
		!strings.Contains(page, "<path d=\"M") || !strings.Contains(page, fmt.Sprintf("/photos/%d/1.jpg", long.ID)) {
		t.Errorf("activity page: status %d\n%s", status, page)
	}
	status, page = get(fmt.Sprintf("/athletes/%d", athleteID))
	if status != http.StatusOK || !strings.Contains(page, "Demo Runner") || strings.Count(page, "/activities/") != len(activities) {
		t.Errorf("athlete page: status %d, %d activities linked, want %d", status, strings.Count(page, "/activities/"), len(activities))
	}
	for _, path := range []string{"/activities/1", "/activities/nope", "/athletes/1"} {
		if status, _ := get(path); status != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, status)
		}
	}
}

func TestGenerateIsStable(t *testing.T) {
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	ctx := context.Background()
	first, second := generate(ctx, now, nil), generate(ctx, now.Add(-time.Hour), nil)
	if len(first) == 0 {
		t.Fatal("no activities")
	}
	// An hour earlier, at most the evening's activity is missing.
	byID := map[int64]*activity{}
	for _, a := range second {
		byID[a.id] = a
	}
	for _, a := range first {
		if b := byID[a.id]; b != nil && (b.distance != a.distance || b.name != a.name || !b.start.Equal(a.start)) {
			t.Errorf("activity %d differs between generations", a.id)
		}
	}
	if len(first)-len(second) > 1 {
		t.Errorf("got %d and %d activities, want the same but for today's", len(first), len(second))
	}
}
