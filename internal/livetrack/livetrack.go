// Package livetrack reads the position of a runner from a Garmin LiveTrack
// session.
//
// Garmin has no public LiveTrack API. This client uses the same endpoints as
// the LiveTrack web page: the page provides a CSRF token, which is sent with
// requests to the JSON API for the session's track points. Garmin can change
// these without notice.
package livetrack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the base URL of Garmin LiveTrack.
const DefaultBaseURL = "https://livetrack.garmin.com"

const (
	// cacheTTL is how long fetched positions are reused, so many viewers of
	// a race plan don't each poll Garmin.
	cacheTTL = 30 * time.Second
	// csrfTTL is how long a CSRF token is reused.
	csrfTTL = 10 * time.Minute
	// fetchTimeout limits how long an update of a session takes, all pages
	// included, so viewers waiting for it don't wait for long.
	fetchTimeout = 10 * time.Second
	// maxPages limits the pages of track points fetched in one update.
	maxPages = 50
	// maxSessions is how many sessions are cached. Anyone with a shared race
	// plan link can set its LiveTrack link, so without a limit the cache
	// could grow until the server runs out of memory. A race has far fewer
	// runners being followed at once.
	maxSessions = 1000
	// sessionIdleTTL is how long a session that nobody asks for stays
	// cached. It keeps the paging cursor of a runner followed on and off
	// during a race, so later fetches only get new points.
	sessionIdleTTL = 6 * time.Hour
	// userAgent identifies the client to Garmin.
	userAgent = "Mozilla/5.0 (compatible; trail-tools/0.1)"
)

// MaxURLLength is the maximum length of a LiveTrack link in bytes. Real
// links are about 100 bytes; the limit keeps arbitrarily long links out of
// the database and the cache.
const MaxURLLength = 512

// ErrInvalidURL is returned for links that aren't LiveTrack session links.
var ErrInvalidURL = errors.New("not a Garmin LiveTrack link; expected a link like https://livetrack.garmin.com/session/<id>/token/<token>")

// sessionPath matches the path of a LiveTrack session link. The ID is a
// UUID and the token a short string; the lengths are bounded so cache keys
// stay small.
var sessionPath = regexp.MustCompile(`^/session/([0-9a-fA-F-]{8,64})/token/([0-9A-Za-z_-]{1,128})/?$`)

// csrfMeta matches the CSRF token in the LiveTrack page.
var csrfMeta = regexp.MustCompile(`<meta\s+name="csrf-token"\s+content="([^"]+)"`)

// Session identifies a LiveTrack session.
type Session struct {
	ID    string
	Token string
}

// ParseURL parses a LiveTrack session link.
func ParseURL(raw string) (Session, error) {
	if len(raw) > MaxURLLength {
		return Session{}, ErrInvalidURL
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.HasPrefix(u.Host, "livetrack.garmin.") {
		return Session{}, ErrInvalidURL
	}
	m := sessionPath.FindStringSubmatch(u.Path)
	if m == nil {
		return Session{}, ErrInvalidURL
	}
	return Session{ID: m[1], Token: m[2]}, nil
}

// Point is a position recorded by the runner's device.
type Point struct {
	Time            time.Time
	Lat, Lng        float64
	ElevationMeters float64
	DistanceMeters  float64
	ElapsedSeconds  float64
	SpeedMPS        float64
}

// Track is the state of a LiveTrack session.
type Track struct {
	// When the session started, zero if unknown.
	Start time.Time
	// The most recent position, nil if there are no positions yet.
	Latest *Point
	// Whether the activity has ended.
	Ended bool
}

// Client reads LiveTrack sessions. It is safe for concurrent use.
type Client struct {
	baseURL    string
	httpClient *http.Client
	nowFn      func() time.Time

	// mu guards sessions and lastSweep. It is never held during requests,
	// so a slow Garmin doesn't hold up viewers of other sessions.
	mu       sync.Mutex
	sessions map[Session]*sessionState
	// When idle sessions were last evicted.
	lastSweep time.Time

	// csrfMu guards the CSRF token. It is only held to read or write it.
	csrfMu   sync.Mutex
	csrf     string
	csrfTime time.Time
}

// sessionState is the cached state of a session.
type sessionState struct {
	// lastUse is when the session was last asked for, guarded by Client.mu.
	lastUse time.Time

	// mu guards the fields below. It is held during the session's update,
	// so concurrent viewers of a session wait for one fetch.
	mu        sync.Mutex
	track     Track
	begin     string
	fetchTime time.Time
	err       error
}

// New creates a new Client that sends requests with httpClient. baseURL
// defaults to DefaultBaseURL.
func New(baseURL string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		httpClient: httpClient,
		nowFn:      time.Now,
		sessions:   map[Session]*sessionState{},
	}
}

// Track returns the current state of a session. Results are cached briefly,
// and later calls only fetch new track points.
func (c *Client) Track(ctx context.Context, s Session) (Track, error) {
	state := c.session(s)

	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.fetchTime.IsZero() && c.nowFn().Sub(state.fetchTime) < cacheTTL {
		return state.track, state.err
	}
	// The result is cached for all viewers, so it mustn't depend on this
	// caller: the fetch carries on if the caller gives up or set a short
	// deadline, which would otherwise be cached as the session's error.
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)
	defer cancel()
	state.err = c.update(fetchCtx, s, state)
	state.fetchTime = c.nowFn()
	return state.track, state.err
}

// session returns the cached state of a session, adding it to the cache if
// it's not there.
func (c *Client) session(s Session) *sessionState {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.nowFn()
	state, ok := c.sessions[s]
	if !ok {
		c.evict(now)
		state = &sessionState{}
		c.sessions[s] = state
	}
	state.lastUse = now
	return state
}

// evict removes sessions idle for longer than sessionIdleTTL, at most once
// per sessionIdleTTL, and the least recently used ones if the cache is still
// full, making room for a new session. c.mu must be held.
func (c *Client) evict(now time.Time) {
	if len(c.sessions) < maxSessions && now.Sub(c.lastSweep) < sessionIdleTTL {
		return
	}
	c.lastSweep = now
	for s, state := range c.sessions {
		if now.Sub(state.lastUse) > sessionIdleTTL {
			delete(c.sessions, s)
		}
	}
	for len(c.sessions) >= maxSessions {
		var oldest Session
		var oldestUse time.Time
		for s, state := range c.sessions {
			if oldestUse.IsZero() || state.lastUse.Before(oldestUse) {
				oldest, oldestUse = s, state.lastUse
			}
		}
		delete(c.sessions, oldest)
	}
}

// update fetches the session and any new track points.
func (c *Client) update(ctx context.Context, s Session, state *sessionState) error {
	if state.track.Start.IsZero() {
		start, end, err := c.sessionTimes(ctx, s)
		if err != nil {
			return err
		}
		state.track.Start = start
		state.track.Ended = !end.IsZero() && end.Before(c.nowFn())
	}
	for range maxPages {
		points, last, ended, err := c.trackPoints(ctx, s, state.begin)
		if err != nil {
			return err
		}
		if ended {
			state.track.Ended = true
		}
		if len(points) > 0 {
			latest := points[len(points)-1]
			state.track.Latest = &latest
		}
		// Ask for points after the latest one next time, like the LiveTrack
		// page does. The cursor follows all points, including those without
		// a position, as a page of only those would otherwise be fetched
		// again and again, and the points after it never.
		if last.IsZero() {
			return nil
		}
		begin := last.Add(time.Millisecond).UTC().Format("2006-01-02T15:04:05.000Z")
		if begin <= state.begin {
			// Garmin returned nothing new.
			return nil
		}
		state.begin = begin
	}
	return nil
}

type sessionResponse struct {
	Start     string `json:"start"`
	StartTime string `json:"startTime"`
	End       string `json:"end"`
	EndTime   string `json:"endTime"`
}

// sessionTimes returns the start and end time of a session. The end time is
// zero if the session hasn't ended.
func (c *Client) sessionTimes(ctx context.Context, s Session) (start, end time.Time, err error) {
	q := url.Values{"token": {s.Token}}
	body, err := c.get(ctx, "/api/v2/sessions/"+url.PathEscape(s.ID)+"?"+q.Encode())
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	var resp sessionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("failed to decode LiveTrack session: %w", err)
	}
	start = parseTime(firstNonEmpty(resp.StartTime, resp.Start))
	end = parseTime(firstNonEmpty(resp.EndTime, resp.End))
	return start, end, nil
}

type fitnessPointData struct {
	TotalDistanceMeters *float64 `json:"totalDistanceMeters"`
	DistanceMeters      *float64 `json:"distanceMeters"`
	TotalDurationSecs   *float64 `json:"totalDurationSecs"`
	DurationSecs        *float64 `json:"durationSecs"`
	SpeedMetersPerSec   *float64 `json:"speedMetersPerSec"`
	EventTypes          []string `json:"eventTypes"`
}

type trackPoint struct {
	DateTime string `json:"dateTime"`
	Position struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"position"`
	Altitude *float64 `json:"altitude"`
	Speed    *float64 `json:"speed"`
	// The point data is either nested or at the top level.
	FitnessPointData *fitnessPointData `json:"fitnessPointData"`
	fitnessPointData
}

type trackPointsResponse struct {
	TrackPoints []trackPoint `json:"trackPoints"`
}

// trackPoints returns the track points with positions after begin, the time
// of the latest track point with or without a position (zero if there are
// none), and whether an END event was among them.
func (c *Client) trackPoints(ctx context.Context, s Session, begin string) (points []Point, last time.Time, ended bool, err error) {
	q := url.Values{"token": {s.Token}}
	if begin != "" {
		q.Set("begin", begin)
	}
	body, err := c.get(ctx, "/api/sessions/"+url.PathEscape(s.ID)+"/track-points/common?"+q.Encode())
	if err != nil {
		return nil, time.Time{}, false, err
	}
	var resp trackPointsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, time.Time{}, false, fmt.Errorf("failed to decode LiveTrack track points: %w", err)
	}
	for _, tp := range resp.TrackPoints {
		data := tp.fitnessPointData
		if tp.FitnessPointData != nil {
			data = *tp.FitnessPointData
		}
		for _, event := range data.EventTypes {
			if event == "END" {
				ended = true
			}
		}
		t := parseTime(tp.DateTime)
		if t.After(last) {
			last = t
		}
		if t.IsZero() || (tp.Position.Lat == 0 && tp.Position.Lon == 0) {
			continue
		}
		p := Point{
			Time:            t,
			Lat:             tp.Position.Lat,
			Lng:             tp.Position.Lon,
			ElevationMeters: deref(tp.Altitude),
			DistanceMeters:  deref(firstNonNil(data.TotalDistanceMeters, data.DistanceMeters)),
			ElapsedSeconds:  deref(firstNonNil(data.TotalDurationSecs, data.DurationSecs)),
			SpeedMPS:        deref(firstNonNil(data.SpeedMetersPerSec, tp.Speed)),
		}
		points = append(points, p)
	}
	return points, last, ended, nil
}

// get performs a request to the LiveTrack API with a CSRF token, refreshing
// the token once if it is rejected.
func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		csrf, err := c.csrfToken(ctx, attempt > 0)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Livetrack-Csrf-Token", csrf)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to reach Garmin LiveTrack: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read LiveTrack response: %w", err)
		}
		switch {
		case resp.StatusCode == http.StatusOK:
			return body, nil
		case resp.StatusCode == http.StatusForbidden && strings.Contains(string(body), `"errorCode":1009`):
			return nil, errors.New("the LiveTrack session is protected by an access code, which isn't supported")
		case resp.StatusCode == http.StatusForbidden && attempt == 0:
			// The CSRF token may have expired.
			continue
		case resp.StatusCode == http.StatusNotFound:
			return nil, errors.New("the LiveTrack session was not found; check the link")
		default:
			return nil, fmt.Errorf("unexpected status %d from Garmin LiveTrack", resp.StatusCode)
		}
	}
}

// csrfToken returns a CSRF token from the LiveTrack page. The lock isn't held
// while the page is fetched, so a slow fetch doesn't block other sessions'
// cached tokens; sessions refreshing at the same time may each fetch it.
func (c *Client) csrfToken(ctx context.Context, refresh bool) (string, error) {
	c.csrfMu.Lock()
	csrf, csrfTime := c.csrf, c.csrfTime
	c.csrfMu.Unlock()
	if !refresh && csrf != "" && c.nowFn().Sub(csrfTime) < csrfTTL {
		return csrf, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to reach Garmin LiveTrack: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("failed to read LiveTrack page: %w", err)
	}
	m := csrfMeta.FindSubmatch(body)
	if m == nil {
		return "", errors.New("could not read the Garmin LiveTrack page; Garmin may have changed it")
	}
	csrf = string(m[1])
	c.csrfMu.Lock()
	c.csrf, c.csrfTime = csrf, c.nowFn()
	c.csrfMu.Unlock()
	return csrf, nil
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonNil(values ...*float64) *float64 {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
