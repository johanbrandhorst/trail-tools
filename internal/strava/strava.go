// Package strava is a client for the Strava API.
package strava

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// Scope is the OAuth scope requested from Strava. Strava separates scopes with
// commas.
const Scope = "read,activity:read_all"

// DefaultBaseURL is the base URL of the Strava API.
const DefaultBaseURL = "https://www.strava.com/api/v3"

// WebURL is the URL of Strava's website, where activities are at
// /activities/<id> and athletes at /athletes/<id>.
const WebURL = "https://www.strava.com"

// ErrUnauthorized is returned when Strava rejects the access token, or the
// refresh token when refreshing it, for example because the user revoked
// access.
var ErrUnauthorized = errors.New("strava authorization was revoked or expired, reconnect Strava")

// ErrNotFound is returned when a resource does not exist.
var ErrNotFound = errors.New("not found on Strava")

// RateLimitError is returned when the Strava API rate limit is reached.
type RateLimitError struct {
	// When the rate limit resets.
	ResetAt time.Time
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("strava rate limit reached, resets at %s", e.ResetAt.Format(time.RFC3339))
}

// Priority is the priority of a request. Strava's rate limits apply to the
// whole application, so requests for a user waiting on the result must not
// be starved by syncing the backlog of every user's activities.
type Priority int

const (
	// PriorityBackground requests, such as syncing the backlog of
	// activities, stop a reserve short of the rate limits. This is the
	// default.
	PriorityBackground Priority = iota
	// PriorityInteractive requests, such as syncing an activity a user is
	// looking at or a webhook event, may use the reserve.
	PriorityInteractive
)

type priorityKey struct{}

// WithPriority returns a context whose requests have the priority.
func WithPriority(ctx context.Context, p Priority) context.Context {
	return context.WithValue(ctx, priorityKey{}, p)
}

// PriorityFrom returns the priority of a context's requests.
func PriorityFrom(ctx context.Context) Priority {
	p, _ := ctx.Value(priorityKey{}).(Priority)
	return p
}

// DefaultBackgroundReserve is the default fraction of each rate limit window
// that background requests leave for interactive requests.
const DefaultBackgroundReserve = 0.1

// Client is a Strava API client. It is safe for concurrent use. The client
// tracks the application's rate limit usage and returns a *RateLimitError
// without calling the API when the limit is nearly reached, or, for
// background requests, when only the reserve for interactive requests is
// left.
type Client struct {
	oauth      *oauth2.Config
	httpClient *http.Client
	baseURL    string

	mu      sync.Mutex
	usage   rateUsage
	nowFn   func() time.Time
	margin  int
	reserve float64
}

// rateUsage is the most recently reported rate limit usage.
type rateUsage struct {
	at                time.Time
	limit15, limitDay int
	used15, usedDay   int
	blockedUntil      time.Time
}

// Config configures a Client.
type Config struct {
	ClientID     string
	ClientSecret string
	// The URL Strava redirects to after authorization.
	RedirectURL string
	// Defaults to DefaultBaseURL.
	BaseURL string
	// Defaults to https://www.strava.com/oauth.
	OAuthBaseURL string
	// Sends the requests to Strava. Required.
	HTTPClient *http.Client
	// The fraction of each rate limit window, the 15 minute and the daily
	// one, that background requests leave for interactive requests.
	// Defaults to DefaultBackgroundReserve. Negative values disable the
	// reserve.
	BackgroundReserve float64
}

// New creates a new Client. It panics if conf has no HTTPClient.
func New(conf Config) *Client {
	if conf.HTTPClient == nil {
		panic("strava: Config.HTTPClient is required")
	}
	if conf.BaseURL == "" {
		conf.BaseURL = DefaultBaseURL
	}
	if conf.OAuthBaseURL == "" {
		conf.OAuthBaseURL = "https://www.strava.com/oauth"
	}
	switch {
	case conf.BackgroundReserve == 0:
		conf.BackgroundReserve = DefaultBackgroundReserve
	case conf.BackgroundReserve < 0:
		conf.BackgroundReserve = 0
	}
	return &Client{
		oauth: &oauth2.Config{
			ClientID:     conf.ClientID,
			ClientSecret: conf.ClientSecret,
			RedirectURL:  conf.RedirectURL,
			Scopes:       []string{Scope},
			Endpoint: oauth2.Endpoint{
				AuthURL:   conf.OAuthBaseURL + "/authorize",
				TokenURL:  conf.OAuthBaseURL + "/token",
				AuthStyle: oauth2.AuthStyleInParams,
			},
		},
		httpClient: conf.HTTPClient,
		baseURL:    conf.BaseURL,
		nowFn:      time.Now,
		// Leave a little headroom below the limit.
		margin:  2,
		reserve: conf.BackgroundReserve,
	}
}

// Token is a Strava OAuth token.
type Token struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
}

// Athlete is the Strava athlete that authorized the application.
type Athlete struct {
	ID        int64  `json:"id"`
	FirstName string `json:"firstname"`
	LastName  string `json:"lastname"`
}

// AuthCodeURL returns the URL to send the user to for authorization.
func (c *Client) AuthCodeURL(state string) string {
	return c.oauth.AuthCodeURL(state, oauth2.SetAuthURLParam("approval_prompt", "auto"))
}

// Exchange exchanges an authorization code for a token.
func (c *Client) Exchange(ctx context.Context, code string) (*Token, *Athlete, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, c.httpClient)
	token, err := c.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	var athlete Athlete
	if raw, ok := token.Extra("athlete").(map[string]any); ok {
		// Round trip through JSON to decode into the struct.
		data, err := json.Marshal(raw)
		if err == nil {
			_ = json.Unmarshal(data, &athlete)
		}
	}
	return &Token{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, Expiry: token.Expiry}, &athlete, nil
}

// Refresh refreshes a token.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (*Token, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, c.httpClient)
	src := c.oauth.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken, Expiry: time.Unix(1, 0)})
	token, err := src.Token()
	if err != nil {
		var retrieveErr *oauth2.RetrieveError
		if errors.As(err, &retrieveErr) && refreshTokenRejected(retrieveErr) {
			return nil, ErrUnauthorized
		}
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}
	return &Token{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, Expiry: token.Expiry}, nil
}

// fault is the body of Strava's error responses.
type fault struct {
	Message string `json:"message"`
	Errors  []struct {
		Resource string `json:"resource"`
		Field    string `json:"field"`
		Code     string `json:"code"`
	} `json:"errors"`
}

// refreshTokenRejected returns whether the token endpoint rejected the
// refresh token itself, as once the athlete revokes access, rather than
// failing otherwise. Strava responds to many problems with a 400, such as
// an invalid client ID or secret, which would make every connection look
// revoked, so only the refresh token being invalid counts: Strava reports
// it as an invalid "refresh_token" field, and OAuth servers as an
// invalid_grant.
func refreshTokenRejected(err *oauth2.RetrieveError) bool {
	if err.ErrorCode == "invalid_grant" {
		return true
	}
	var f fault
	if json.Unmarshal(err.Body, &f) != nil {
		return false
	}
	for _, e := range f.Errors {
		if e.Field == "refresh_token" && e.Code == "invalid" {
			return true
		}
	}
	return false
}

// get performs an authenticated GET request and returns the response body.
func (c *Client) get(ctx context.Context, accessToken, path string, query url.Values) ([]byte, error) {
	if err := c.checkRateLimit(PriorityFrom(ctx)); err != nil {
		return nil, err
	}
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	c.recordUsage(resp)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusTooManyRequests:
		return nil, c.blockUntilReset()
	default:
		return nil, fmt.Errorf("unexpected status %d from Strava: %s", resp.StatusCode, strings.TrimSpace(string(body[:min(len(body), 256)])))
	}
}

// checkRateLimit returns a *RateLimitError if the last reported usage is
// close to the limit in the current window. Background requests stop a
// reserve short of the limit, to leave room for interactive requests.
func (c *Client) checkRateLimit(p Priority) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.nowFn()
	if now.Before(c.usage.blockedUntil) {
		return &RateLimitError{ResetAt: c.usage.blockedUntil}
	}
	if c.usage.at.IsZero() {
		return nil
	}
	if c.usage.limitDay > 0 && sameDay(c.usage.at, now) {
		if err := c.checkWindow(c.usage.usedDay, c.usage.limitDay, p, nextDay(now)); err != nil {
			return err
		}
	}
	if c.usage.limit15 > 0 && sameWindow(c.usage.at, now) {
		if err := c.checkWindow(c.usage.used15, c.usage.limit15, p, nextWindow(now)); err != nil {
			return err
		}
	}
	return nil
}

// checkWindow returns a *RateLimitError if a request of the priority would
// use too much of a rate limit window. Reaching the margin below the limit
// blocks all requests until the window resets, while reaching the reserve
// only rejects background requests.
func (c *Client) checkWindow(used, limit int, p Priority, reset time.Time) error {
	if used >= limit-c.margin {
		c.usage.blockedUntil = reset
		return &RateLimitError{ResetAt: reset}
	}
	reserve := int(math.Ceil(float64(limit) * c.reserve))
	if p == PriorityBackground && used >= limit-c.margin-reserve {
		return &RateLimitError{ResetAt: reset}
	}
	return nil
}

// blockUntilReset blocks requests until the next window after a 429
// response.
func (c *Client) blockUntilReset() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.nowFn()
	reset := nextWindow(now)
	if c.usage.limitDay > 0 && c.usage.usedDay >= c.usage.limitDay {
		reset = nextDay(now)
	}
	c.usage.blockedUntil = reset
	return &RateLimitError{ResetAt: reset}
}

// recordUsage records the rate limit usage reported in a response. Read
// limits are preferred over overall limits as all requests are reads.
func (c *Client) recordUsage(resp *http.Response) {
	limit, usage := resp.Header.Get("X-ReadRateLimit-Limit"), resp.Header.Get("X-ReadRateLimit-Usage")
	if limit == "" || usage == "" {
		limit, usage = resp.Header.Get("X-RateLimit-Limit"), resp.Header.Get("X-RateLimit-Usage")
	}
	limit15, limitDay, ok1 := parsePair(limit)
	used15, usedDay, ok2 := parsePair(usage)
	if !ok1 || !ok2 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.usage.at = c.nowFn()
	c.usage.limit15, c.usage.limitDay = limit15, limitDay
	c.usage.used15, c.usage.usedDay = used15, usedDay
}

func parsePair(s string) (int, int, bool) {
	a, b, ok := strings.Cut(s, ",")
	if !ok {
		return 0, 0, false
	}
	x, err1 := strconv.Atoi(strings.TrimSpace(a))
	y, err2 := strconv.Atoi(strings.TrimSpace(b))
	return x, y, err1 == nil && err2 == nil
}

// Strava's rate limit windows reset every 15 minutes on the quarter hour, and
// daily at midnight UTC.
const rateWindow = 15 * time.Minute

func sameWindow(a, b time.Time) bool {
	return a.UTC().Truncate(rateWindow).Equal(b.UTC().Truncate(rateWindow))
}

func nextWindow(t time.Time) time.Time {
	return t.UTC().Truncate(rateWindow).Add(rateWindow)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.UTC().Date()
	by, bm, bd := b.UTC().Date()
	return ay == by && am == bm && ad == bd
}

func nextDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)
}
