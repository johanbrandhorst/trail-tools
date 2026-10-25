package livetrack

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseURL(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		url  string
		want Session
		ok   bool
	}{
		{"https://livetrack.garmin.com/session/3e8c3a0e-8a57-4c4d-9e5f-0c8f3b2d6a11/token/9A8B7C6D5E4F", Session{"3e8c3a0e-8a57-4c4d-9e5f-0c8f3b2d6a11", "9A8B7C6D5E4F"}, true},
		{"  https://livetrack.garmin.com/session/3e8c3a0e-8a57-4c4d-9e5f-0c8f3b2d6a11/token/ABC/?utm=x ", Session{"3e8c3a0e-8a57-4c4d-9e5f-0c8f3b2d6a11", "ABC"}, true},
		{"https://livetrack.garmin.com/session/3e8c3a0e-8a57-4c4d-9e5f-0c8f3b2d6a11", Session{}, false},
		{"https://example.com/session/3e8c3a0e-8a57-4c4d-9e5f-0c8f3b2d6a11/token/ABC", Session{}, false},
		{"not a url", Session{}, false},
		{"https://livetrack.garmin.com/session/3e8c3a0e-8a57-4c4d-9e5f-0c8f3b2d6a11/token/" + strings.Repeat("A", 129), Session{}, false},
		{"https://livetrack.garmin.com/session/" + strings.Repeat("a", 65) + "/token/ABC", Session{}, false},
		{"https://livetrack.garmin.com/session/3e8c3a0e-8a57-4c4d-9e5f-0c8f3b2d6a11/token/ABC?" + strings.Repeat("x", MaxURLLength), Session{}, false},
	} {
		got, err := ParseURL(tt.url)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("ParseURL(%q) = %v, %v", tt.url, got, err)
		}
	}
}

// fakeLiveTrack serves a session with points every minute, requiring a CSRF
// token like Garmin does.
type fakeLiveTrack struct {
	mu       sync.Mutex
	start    time.Time
	points   int
	ended    bool
	pageSize int
	// The number of points at the start without a position, like those
	// recorded before the device has a GPS fix.
	positionless int
	pointCalls   int
	begins       []string
}

func (f *fakeLiveTrack) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/":
		fmt.Fprint(w, `<html><head><meta name="csrf-token" content="csrf-123"/></head></html>`)
		return
	case r.Header.Get("Livetrack-Csrf-Token") != "csrf-123":
		w.WriteHeader(http.StatusForbidden)
		return
	case r.URL.Query().Get("token") != "TOKEN":
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"errors":[{"type":"NOT_FOUND","errorCode":1001}]}`)
		return
	case r.URL.Path == "/api/v2/sessions/abcdef12-0000":
		fmt.Fprintf(w, `{"sessionId":"abcdef12-0000","startTime":%q}`, f.start.Format(time.RFC3339))
	case r.URL.Path == "/api/sessions/abcdef12-0000/track-points/common":
		f.pointCalls++
		begin := r.URL.Query().Get("begin")
		f.begins = append(f.begins, begin)
		var items []string
		for i := range f.points {
			pt := f.start.Add(time.Duration(i) * time.Minute)
			if begin != "" {
				b, _ := time.Parse(time.RFC3339Nano, begin)
				if pt.Before(b) {
					continue
				}
			}
			if len(items) == f.pageSize {
				break
			}
			events := `[]`
			if f.ended && i == f.points-1 {
				events = `["END"]`
			}
			lat, lon := 46+float64(i)*0.001, 7.0
			if i < f.positionless {
				lat, lon = 0, 0
			}
			items = append(items, fmt.Sprintf(`{"dateTime":%q,"position":{"lat":%f,"lon":%f},"altitude":%d,
				"fitnessPointData":{"totalDistanceMeters":%d,"totalDurationSecs":%d,"speedMetersPerSec":2.5,"eventTypes":%s}}`,
				pt.Format(time.RFC3339), lat, lon, 1000+i, i*150, i*60, events))
		}
		fmt.Fprintf(w, `{"trackPoints":[%s]}`, strings.Join(items, ","))
	default:
		http.NotFound(w, r)
	}
}

func TestTrack(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 11, 6, 0, 0, 0, time.UTC)
	fake := &fakeLiveTrack{start: start, points: 5, pageSize: 3}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	client := New(srv.URL, srv.Client())
	now := start.Add(10 * time.Minute)
	client.nowFn = func() time.Time { return now }
	ctx := context.Background()
	session := Session{ID: "abcdef12-0000", Token: "TOKEN"}

	track, err := client.Track(ctx, session)
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	if !track.Start.Equal(start) || track.Ended || track.Latest == nil {
		t.Fatalf("unexpected track %+v", track)
	}
	latest := track.Latest
	if !latest.Time.Equal(start.Add(4*time.Minute)) || latest.DistanceMeters != 600 || latest.ElapsedSeconds != 240 ||
		latest.ElevationMeters != 1004 || latest.SpeedMPS != 2.5 {
		t.Errorf("unexpected latest point %+v", latest)
	}

	// Within the cache TTL nothing is fetched.
	calls := fake.pointCalls
	if _, err := client.Track(ctx, session); err != nil || fake.pointCalls != calls {
		t.Errorf("expected a cached result, got %d calls, %v", fake.pointCalls-calls, err)
	}

	// Later, only new points are fetched.
	fake.mu.Lock()
	fake.points = 7
	fake.ended = true
	fake.mu.Unlock()
	now = now.Add(time.Minute)
	track, err = client.Track(ctx, session)
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	if !track.Ended || track.Latest.DistanceMeters != 900 {
		t.Errorf("unexpected track after update %+v", track)
	}
	if last := fake.begins[len(fake.begins)-1]; !strings.HasPrefix(last, "2026-10-11T06:06:00.001") {
		t.Errorf("unexpected begin %q", last)
	}

	_, err = client.Track(ctx, Session{ID: "abcdef12-0000", Token: "WRONG"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not found error, got %v", err)
	}
}

func TestTrackPositionlessPage(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 11, 6, 0, 0, 0, time.UTC)
	// The first page has no positions, as the device had no GPS fix yet.
	fake := &fakeLiveTrack{start: start, points: 5, pageSize: 3, positionless: 3}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	client := New(srv.URL, srv.Client())
	client.nowFn = func() time.Time { return start.Add(10 * time.Minute) }

	track, err := client.Track(context.Background(), Session{ID: "abcdef12-0000", Token: "TOKEN"})
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	// The cursor moves past the page without positions to the points after
	// it.
	if track.Latest == nil || !track.Latest.Time.Equal(start.Add(4*time.Minute)) {
		t.Errorf("got latest point %+v, want the point at 6:04", track.Latest)
	}
}

func TestTrackCanceled(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 11, 6, 0, 0, 0, time.UTC)
	fake := &fakeLiveTrack{start: start, points: 5, pageSize: 3}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	client := New(srv.URL, srv.Client())
	client.nowFn = func() time.Time { return start.Add(10 * time.Minute) }
	session := Session{ID: "abcdef12-0000", Token: "TOKEN"}

	// A viewer who gave up still gets the session fetched...
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	track, err := client.Track(canceled, session)
	if err != nil || track.Latest == nil {
		t.Fatalf("got %+v, %v, want the latest point", track, err)
	}
	// ...and cached for the next viewer.
	fake.mu.Lock()
	calls := fake.pointCalls
	fake.mu.Unlock()
	track, err = client.Track(context.Background(), session)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if err != nil || track.Latest == nil || fake.pointCalls != calls {
		t.Errorf("got %+v, %v after %d calls, want the cached latest point", track, err, fake.pointCalls-calls)
	}
}

func TestTrackEviction(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 11, 6, 0, 0, 0, time.UTC)
	fake := &fakeLiveTrack{start: start, points: 1, pageSize: 3}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	client := New(srv.URL, srv.Client())
	now := start
	client.nowFn = func() time.Time { return now }
	ctx := context.Background()

	// Unknown sessions fill the cache, but never beyond its limit.
	for i := range maxSessions + 10 {
		now = now.Add(time.Second)
		_, _ = client.Track(ctx, Session{ID: "abcdef12-0000", Token: fmt.Sprintf("T%d", i)})
	}
	client.mu.Lock()
	n := len(client.sessions)
	_, oldest := client.sessions[Session{ID: "abcdef12-0000", Token: "T0"}]
	_, newest := client.sessions[Session{ID: "abcdef12-0000", Token: fmt.Sprintf("T%d", maxSessions+9)}]
	client.mu.Unlock()
	if n > maxSessions || oldest || !newest {
		t.Errorf("got %d sessions (oldest cached %v, newest %v), want at most %d with the least recently used evicted", n, oldest, newest, maxSessions)
	}

	// Sessions nobody asks for are evicted once idle.
	now = now.Add(sessionIdleTTL + time.Minute)
	_, _ = client.Track(ctx, Session{ID: "abcdef12-0000", Token: "TOKEN"})
	client.mu.Lock()
	n = len(client.sessions)
	client.mu.Unlock()
	if n != 1 {
		t.Errorf("got %d sessions after they went idle, want 1", n)
	}
}
