package strava

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRateLimit(t *testing.T) {
	t.Parallel()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-ReadRateLimit-Limit", "100,1000")
		w.Header().Set("X-ReadRateLimit-Usage", fmt.Sprintf("%d,%d", 97+calls, 500+calls))
		fmt.Fprint(w, `[{"id": 1, "name": "Morning Run", "sport_type": "TrailRun", "start_date": "2026-09-07T07:00:00Z",
			"start_date_local": "2026-09-07T08:00:00Z", "timezone": "(GMT+01:00) Europe/London", "average_cadence": 85}]`)
	}))
	t.Cleanup(srv.Close)
	client := New(Config{BaseURL: srv.URL, HTTPClient: srv.Client()})
	now := time.Date(2026, 9, 7, 10, 3, 0, 0, time.UTC)
	client.nowFn = func() time.Time { return now }
	ctx := context.Background()

	activities, err := client.ListActivities(ctx, "token", time.Time{}, 10)
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	a := activities[0]
	if a.TimezoneName() != "Europe/London" || !a.LocalDate().Equal(time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)) || !a.IsFootSport() || len(a.Raw) == 0 {
		t.Errorf("unexpected activity %+v", a)
	}
	// 98 of 100 requests used: the next request would be too close to the
	// limit, so it is rejected until the next 15 minute window.
	_, err = client.ListActivities(ctx, "token", time.Time{}, 10)
	var rateErr *RateLimitError
	if !errors.As(err, &rateErr) || !rateErr.ResetAt.Equal(time.Date(2026, 9, 7, 10, 15, 0, 0, time.UTC)) {
		t.Fatalf("got %v, want rate limit until 10:15", err)
	}
	if calls != 1 {
		t.Errorf("rate limited request should not reach Strava, got %d calls", calls)
	}
	now = now.Add(15 * time.Minute)
	if _, err := client.ListActivities(ctx, "token", time.Time{}, 10); err != nil {
		t.Fatalf("request after reset: %v", err)
	}
	if _, err := New(Config{BaseURL: srv.URL, HTTPClient: srv.Client()}).ListActivities(ctx, "bad", time.Time{}, 10); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("got %v, want ErrUnauthorized", err)
	}
}

func TestRateLimitPriority(t *testing.T) {
	t.Parallel()
	var calls int
	var usage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-ReadRateLimit-Limit", "100,1000")
		w.Header().Set("X-ReadRateLimit-Usage", usage)
		fmt.Fprint(w, `[]`)
	}))
	t.Cleanup(srv.Close)
	now := time.Date(2026, 9, 7, 10, 3, 0, 0, time.UTC)
	newClient := func(reserve float64) *Client {
		client := New(Config{BaseURL: srv.URL, HTTPClient: srv.Client(), BackgroundReserve: reserve})
		client.nowFn = func() time.Time { return now }
		return client
	}
	background := context.Background()
	interactive := WithPriority(background, PriorityInteractive)
	list := func(client *Client, ctx context.Context) error {
		_, err := client.ListActivities(ctx, "token", time.Time{}, 10)
		return err
	}
	wantLimited := func(err error, reset time.Time) {
		t.Helper()
		var rateErr *RateLimitError
		if !errors.As(err, &rateErr) || !rateErr.ResetAt.Equal(reset) {
			t.Errorf("got %v, want rate limit until %s", err, reset)
		}
	}
	quarterHour, midnight := time.Date(2026, 9, 7, 10, 15, 0, 0, time.UTC), time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)

	// 88 of 100 requests in the window used: background requests leave the
	// remaining 10%, less the margin, to interactive requests.
	client := newClient(0)
	usage = "88,500"
	if err := list(client, background); err != nil {
		t.Fatal(err)
	}
	wantLimited(list(client, background), quarterHour)
	if calls != 1 {
		t.Errorf("rate limited request should not reach Strava, got %d calls", calls)
	}
	usage = "98,501"
	if err := list(client, interactive); err != nil {
		t.Fatalf("interactive request within the reserve: %v", err)
	}
	// Interactive requests stop at the margin, which blocks all requests.
	wantLimited(list(client, interactive), quarterHour)
	wantLimited(list(client, background), quarterHour)
	if calls != 2 {
		t.Errorf("got %d calls, want 2", calls)
	}

	// The daily reserve applies the same way.
	now = now.Add(15 * time.Minute)
	usage = "10,898"
	if err := list(client, interactive); err != nil {
		t.Fatal(err)
	}
	wantLimited(list(client, background), midnight)
	if err := list(client, interactive); err != nil {
		t.Fatalf("interactive request within the daily reserve: %v", err)
	}

	// The reserve can be disabled.
	client = newClient(-1)
	usage = "88,500"
	if err := list(client, background); err != nil {
		t.Fatal(err)
	}
	if err := list(client, background); err != nil {
		t.Errorf("without a reserve, background requests continue to the margin: %v", err)
	}
}

func TestTooManyRequests(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "200,2000")
		w.Header().Set("X-RateLimit-Usage", "10,2000")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	client := New(Config{BaseURL: srv.URL, HTTPClient: srv.Client()})
	now := time.Date(2026, 9, 7, 10, 3, 0, 0, time.UTC)
	client.nowFn = func() time.Time { return now }
	_, err := client.GetStreams(context.Background(), "token", 1)
	var rateErr *RateLimitError
	// The daily limit is used up, so wait until midnight UTC.
	if !errors.As(err, &rateErr) || !rateErr.ResetAt.Equal(time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("got %v, want rate limit until midnight", err)
	}
}

func TestGetStreams(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/activities/2/streams" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"latlng": {"data": [[1.5, 2.5], [1.6, 2.6]]}, "altitude": {"data": [10, 11]}, "time": {"data": [0, 5]}}`)
	}))
	t.Cleanup(srv.Close)
	client := New(Config{BaseURL: srv.URL, HTTPClient: srv.Client()})
	streams, err := client.GetStreams(context.Background(), "token", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(streams.LatLng) != 2 || streams.LatLng[1][1] != 2.6 || streams.Altitude[1] != 11 || streams.Time[1] != 5 || streams.Heartrate != nil {
		t.Errorf("unexpected streams %+v", streams)
	}
	if _, err := client.GetStreams(context.Background(), "token", 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// photosFixture is a response of the photos endpoint, which isn't in
// Strava's API reference: a photo with a location, one without, one still
// processing, a video, and entries that can't be used.
const photosFixture = `[
	{"unique_id": "a1b2", "athlete_id": 7, "activity_id": 1, "caption": " Summit! ", "type": 1, "source": 1,
		"urls": {"2048": "https://cdn.example/a1b2-1536x2048.jpg"}, "sizes": {"2048": [1536, 2048]},
		"location": [46.5, 7.25], "created_at": "2026-09-07T09:15:00Z", "created_at_local": "2026-09-07T11:15:00Z",
		"default_photo": true},
	{"unique_id": "c3d4", "caption": null, "urls": {"600": "https://cdn.example/c3d4-600.jpg", "100": "https://cdn.example/c3d4-100.jpg"},
		"location": [0, 0], "created_at": "not a time"},
	{"unique_id": "e5f6", "urls": {"2048": "https://cdn.example/assets/media/placeholder-photo.png"}},
	{"unique_id": "g7h8", "urls": {"2048": "https://cdn.example/g7h8.jpg"}, "video_url": "https://cdn.example/g7h8.mp4"},
	{"unique_id": "i9j0", "urls": "unexpected"},
	{"caption": "no ID", "urls": {"2048": "https://cdn.example/x.jpg"}}
]`

func TestGetActivityPhotos(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/activities/1/photos" || q.Get("size") != "2048" || q.Get("photo_sources") != "true" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, photosFixture)
	}))
	t.Cleanup(srv.Close)
	client := New(Config{BaseURL: srv.URL, HTTPClient: srv.Client()})
	photos, err := client.GetActivityPhotos(context.Background(), "token", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(photos) != 2 {
		t.Fatalf("got %d photos, want 2: %+v", len(photos), photos)
	}
	p := photos[0]
	if p.UniqueID != "a1b2" || p.Caption != "Summit!" || p.URL != "https://cdn.example/a1b2-1536x2048.jpg" ||
		p.Width != 1536 || p.Height != 2048 || !p.HasLocation || p.Lat != 46.5 || p.Lng != 7.25 ||
		!p.CreatedAt.Equal(time.Date(2026, 9, 7, 9, 15, 0, 0, time.UTC)) || len(p.Raw) == 0 {
		t.Errorf("unexpected photo %+v", p)
	}
	// Without the requested size, the largest is used. A 0, 0 location
	// means there is none.
	p = photos[1]
	if p.UniqueID != "c3d4" || p.Caption != "" || p.URL != "https://cdn.example/c3d4-600.jpg" ||
		p.Width != 0 || p.HasLocation || !p.CreatedAt.IsZero() {
		t.Errorf("unexpected photo %+v", p)
	}
	if _, err := client.GetActivityPhotos(context.Background(), "token", 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestPushSubscriptions(t *testing.T) {
	t.Parallel()
	var subs []*PushSubscription
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("client_id") != "id" || r.Form.Get("client_secret") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/push_subscriptions":
			_ = json.NewEncoder(w).Encode(subs)
		case r.Method == http.MethodPost && r.URL.Path == "/push_subscriptions":
			if r.PostForm.Get("verify_token") != "verify" {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"message": "Bad Request", "errors": [{"resource": "PushSubscription", "field": "callback url", "code": "not verifiable"}]}`)
				return
			}
			subs = append(subs, &PushSubscription{ID: int64(len(subs) + 1), CallbackURL: r.PostForm.Get("callback_url")})
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id": %d}`, len(subs))
		case r.Method == http.MethodDelete && r.URL.Path == "/push_subscriptions/1" && len(subs) == 1:
			subs = nil
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	client := New(Config{ClientID: "id", ClientSecret: "secret", BaseURL: srv.URL, HTTPClient: srv.Client()})
	ctx := context.Background()

	if _, err := client.CreatePushSubscription(ctx, "https://trail.example/strava/webhook", "wrong"); err == nil || !strings.Contains(err.Error(), "not verifiable") {
		t.Errorf("got %v, want Strava's explanation", err)
	}
	sub, err := client.CreatePushSubscription(ctx, "https://trail.example/strava/webhook", "verify")
	if err != nil {
		t.Fatalf("CreatePushSubscription: %v", err)
	}
	if sub.ID != 1 || sub.CallbackURL != "https://trail.example/strava/webhook" {
		t.Errorf("unexpected subscription %+v", sub)
	}
	listed, err := client.ListPushSubscriptions(ctx)
	if err != nil || len(listed) != 1 || *listed[0] != *sub {
		t.Fatalf("ListPushSubscriptions: got %v, %v", listed, err)
	}
	if err := client.DeletePushSubscription(ctx, 1); err != nil {
		t.Fatalf("DeletePushSubscription: %v", err)
	}
	if err := client.DeletePushSubscription(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
	if _, err := New(Config{ClientID: "id", ClientSecret: "wrong", BaseURL: srv.URL, HTTPClient: srv.Client()}).ListPushSubscriptions(ctx); err == nil {
		t.Error("wrong client secret should fail")
	}
}

func TestRefreshRevoked(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		status      int
		body        string
		wantRevoked bool
	}{
		"invalid refresh token": {http.StatusBadRequest, `{"message": "Bad Request", "errors": [{"resource": "RefreshToken", "field": "refresh_token", "code": "invalid"}]}`, true},
		"invalid grant":         {http.StatusBadRequest, `{"error": "invalid_grant"}`, true},
		// A misconfigured client must not make every connection look
		// revoked.
		"invalid client": {http.StatusBadRequest, `{"message": "Bad Request", "errors": [{"resource": "Application", "field": "client_id", "code": "invalid"}]}`, false},
		"unauthorized":   {http.StatusUnauthorized, `{"message": "Authorization Error", "errors": [{"resource": "Application", "field": "", "code": "invalid"}]}`, false},
		"server error":   {http.StatusInternalServerError, `oops`, false},
		"not json":       {http.StatusBadRequest, `Bad Request`, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			t.Cleanup(srv.Close)
			client := New(Config{OAuthBaseURL: srv.URL, HTTPClient: srv.Client()})
			_, err := client.Refresh(context.Background(), "refresh")
			if err == nil {
				t.Fatal("Refresh succeeded, want an error")
			}
			if got := errors.Is(err, ErrUnauthorized); got != tc.wantRevoked {
				t.Errorf("got %v, want revoked %v", err, tc.wantRevoked)
			}
		})
	}
}
