// Package garmin is a client for the Garmin Connect Developer Program's APIs:
// OAuth 2.0 with PKCE to connect a user's account, the Activity API to fetch
// the user's activities and their FIT files, and the Training API to put
// planned workouts on the user's Garmin Connect calendar, which syncs them to
// their watch.
//
// The URLs and the shapes of the requests follow Garmin's documentation of
// October 2026 (OAuth 2.0 PKCE 2.0, Activity API 2.1 and Training API V2
// 2.0), which is only available to registered developers. They're in this
// package, and the base URLs are configurable, so a change on Garmin's side
// is fixed here.
package garmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// Default URLs of Garmin's APIs.
const (
	DefaultAuthURL  = "https://connect.garmin.com/oauth2Confirm"
	DefaultTokenURL = "https://diauth.garmin.com/di-oauth2-service/oauth/token"
	DefaultAPIURL   = "https://apis.garmin.com"
)

// Permissions a user grants to the app, which they can change in Garmin
// Connect at any time.
const (
	// PermissionActivityExport lets the app read the user's activities.
	PermissionActivityExport = "ACTIVITY_EXPORT"
	// PermissionWorkoutImport lets the app put workouts on the user's
	// calendar.
	PermissionWorkoutImport = "WORKOUT_IMPORT"
)

// ErrUnauthorized is returned when Garmin rejects the tokens, for example
// because the user disconnected the app in Garmin Connect.
var ErrUnauthorized = errors.New("garmin authorization was revoked or expired, reconnect Garmin")

// ErrNotFound is returned for a resource that doesn't exist, or whose
// download link expired.
var ErrNotFound = errors.New("not found on Garmin")

// ErrRateLimited is returned when Garmin's rate limit is reached.
var ErrRateLimited = errors.New("garmin rate limit reached")

// ErrPermissionDenied is returned when the user hasn't given the app the
// permission a request needs, like WORKOUT_IMPORT, though the token is
// valid.
var ErrPermissionDenied = errors.New("the Garmin user hasn't allowed this; change the permissions in Garmin Connect, or reconnect Garmin")

// maxResponseSize bounds the JSON responses read into memory.
const maxResponseSize = 8 << 20

// maxFileSize bounds activity files, as fit.MaxFileSize does.
const maxFileSize = 32 << 20

// Config configures a Client.
type Config struct {
	ClientID, ClientSecret string
	// The URL Garmin redirects to after the user authorizes the app.
	RedirectURL string
	// The URLs of the APIs, Garmin's by default.
	AuthURL, TokenURL, APIURL string
	HTTPClient                *http.Client
}

// Client calls Garmin's APIs.
type Client struct {
	oauth      *oauth2.Config
	apiURL     string
	httpClient *http.Client
}

// NewClient creates a Client.
func NewClient(c Config) *Client {
	or := func(s, fallback string) string {
		if s == "" {
			return fallback
		}
		return s
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		oauth: &oauth2.Config{
			ClientID:     c.ClientID,
			ClientSecret: c.ClientSecret,
			RedirectURL:  c.RedirectURL,
			Endpoint: oauth2.Endpoint{
				AuthURL:  or(c.AuthURL, DefaultAuthURL),
				TokenURL: or(c.TokenURL, DefaultTokenURL),
				// Garmin's documentation lists the client credentials as
				// form parameters, but its examples send them with basic
				// authentication, so both are tried.
				AuthStyle: oauth2.AuthStyleAutoDetect,
			},
		},
		apiURL:     strings.TrimSuffix(or(c.APIURL, DefaultAPIURL), "/"),
		httpClient: httpClient,
	}
}

// Token is a user's OAuth tokens.
type Token struct {
	AccessToken, RefreshToken string
	// When the access token expires.
	Expiry time.Time
}

// AuthCodeURL returns the URL to send the user to, to authorize the app.
// verifier is the PKCE verifier, kept until the callback, from
// oauth2.GenerateVerifier.
func (c *Client) AuthCodeURL(state, verifier string) string {
	return c.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

// Exchange exchanges an authorization code for tokens.
func (c *Client) Exchange(ctx context.Context, code, verifier string) (*Token, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, c.httpClient)
	token, err := c.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	return &Token{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, Expiry: token.Expiry}, nil
}

// Refresh refreshes tokens. Garmin rotates refresh tokens, so the returned
// refresh token replaces the old one.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (*Token, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, c.httpClient)
	token, err := c.oauth.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken, Expiry: time.Unix(1, 0)}).Token()
	if err != nil {
		var retrieveErr *oauth2.RetrieveError
		if errors.As(err, &retrieveErr) && (retrieveErr.ErrorCode == "invalid_grant" || retrieveErr.Response.StatusCode == http.StatusUnauthorized) {
			return nil, ErrUnauthorized
		}
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}
	return &Token{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, Expiry: token.Expiry}, nil
}

// do sends a request with the access token and decodes a JSON response into
// out, if not nil.
func (c *Client) do(ctx context.Context, accessToken, method, rawURL string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to encode request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	if err := statusError(resp.StatusCode, data); err != nil {
		return err
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	return nil
}

func statusError(status int, body []byte) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ErrUnauthorized
	case status == http.StatusNotFound || status == http.StatusGone:
		return ErrNotFound
	case status == http.StatusTooManyRequests:
		return ErrRateLimited
	case status == http.StatusPreconditionFailed:
		return ErrPermissionDenied
	default:
		return fmt.Errorf("unexpected status %d from Garmin: %s", status, strings.TrimSpace(string(body[:min(len(body), 256)])))
	}
}

// UserID returns the Garmin user ID of the tokens' user, which notifications
// identify the user by.
func (c *Client) UserID(ctx context.Context, accessToken string) (string, error) {
	var out struct {
		UserID string `json:"userId"`
	}
	if err := c.do(ctx, accessToken, http.MethodGet, c.apiURL+"/partner-gateway/rest/user/id", nil, &out); err != nil {
		return "", fmt.Errorf("failed to get user ID: %w", err)
	}
	if out.UserID == "" {
		return "", errors.New("garmin returned no user ID")
	}
	return out.UserID, nil
}

// Permissions returns the permissions the user granted.
func (c *Client) Permissions(ctx context.Context, accessToken string) ([]string, error) {
	var out struct {
		Permissions []string `json:"permissions"`
	}
	if err := c.do(ctx, accessToken, http.MethodGet, c.apiURL+"/partner-gateway/rest/user/permissions", nil, &out); err != nil {
		return nil, fmt.Errorf("failed to get permissions: %w", err)
	}
	slices.Sort(out.Permissions)
	return out.Permissions, nil
}

// Deregister disconnects the user from the app, so Garmin stops sending
// their data. Garmin requires it when the user disconnects in the app.
func (c *Client) Deregister(ctx context.Context, accessToken string) error {
	err := c.do(ctx, accessToken, http.MethodDelete, c.apiURL+"/partner-gateway/rest/user/registration", nil, nil)
	if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrUnauthorized) {
		return fmt.Errorf("failed to deregister: %w", err)
	}
	return nil
}

// maxBackfillRange is the longest range Garmin backfills in one request.
const maxBackfillRange = 30 * 24 * time.Hour

// BackfillActivities asks Garmin to send notifications of the activities
// that started in a range, as for new activities. Garmin answers
// asynchronously, through the notification endpoints, so no activity data
// is downloaded until notifications ask for it. Garmin offers it with
// Enterprise access only, and returns an error otherwise.
func (c *Client) BackfillActivities(ctx context.Context, accessToken string, start, end time.Time) error {
	for from := start; from.Before(end); from = from.Add(maxBackfillRange) {
		to := from.Add(maxBackfillRange)
		if to.After(end) {
			to = end
		}
		q := url.Values{
			"summaryStartTimeInSeconds": {strconv.FormatInt(from.Unix(), 10)},
			"summaryEndTimeInSeconds":   {strconv.FormatInt(to.Unix(), 10)},
		}
		err := c.do(ctx, accessToken, http.MethodGet, c.apiURL+"/wellness-api/rest/backfill/activities?"+q.Encode(), nil, nil)
		// Garmin answers a range it backfilled before with a conflict.
		if err != nil && !strings.Contains(err.Error(), "status 409") {
			return fmt.Errorf("failed to request backfill: %w", err)
		}
	}
	return nil
}

// ID is an ID Garmin sends as a JSON number, or in places as a string.
type ID int64

// UnmarshalJSON decodes a number or a string of one.
func (id *ID) UnmarshalJSON(data []byte) error {
	text := strings.Trim(string(data), `"`)
	if text == "" || text == "null" {
		*id = 0
		return nil
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid Garmin ID %s: %w", data, err)
	}
	*id = ID(n)
	return nil
}

// ActivitySummary is an activity's summary, as the Activity API returns it.
type ActivitySummary struct {
	SummaryID                         string  `json:"summaryId"`
	ActivityID                        ID      `json:"activityId"`
	ActivityName                      string  `json:"activityName"`
	ActivityType                      string  `json:"activityType"`
	StartTimeInSeconds                int64   `json:"startTimeInSeconds"`
	StartTimeOffsetInSeconds          int64   `json:"startTimeOffsetInSeconds"`
	DurationInSeconds                 int32   `json:"durationInSeconds"`
	MovingDurationInSeconds           int32   `json:"movingDurationInSeconds"`
	DistanceInMeters                  float64 `json:"distanceInMeters"`
	TotalElevationGainInMeters        float64 `json:"totalElevationGainInMeters"`
	AverageHeartRateInBeatsPerMinute  float64 `json:"averageHeartRateInBeatsPerMinute"`
	MaxHeartRateInBeatsPerMinute      float64 `json:"maxHeartRateInBeatsPerMinute"`
	AverageSpeedInMetersPerSecond     float64 `json:"averageSpeedInMetersPerSecond"`
	MaxSpeedInMetersPerSecond         float64 `json:"maxSpeedInMetersPerSecond"`
	AverageRunCadenceInStepsPerMinute float64 `json:"averageRunCadenceInStepsPerMinute"`
	DeviceName                        string  `json:"deviceName"`
	Manual                            bool    `json:"manual"`
	// The raw response, kept in blob storage.
	RawJSON json.RawMessage `json:"-"`
}

// StartTime returns when the activity started.
func (s *ActivitySummary) StartTime() time.Time {
	return time.Unix(s.StartTimeInSeconds, 0).UTC()
}

// FetchActivities fetches the activity summaries a notification's callback
// URL points to.
func (c *Client) FetchActivities(ctx context.Context, accessToken, callbackURL string) ([]*ActivitySummary, error) {
	if err := c.checkCallback(callbackURL); err != nil {
		return nil, err
	}
	var raw []json.RawMessage
	if err := c.do(ctx, accessToken, http.MethodGet, callbackURL, nil, &raw); err != nil {
		return nil, fmt.Errorf("failed to fetch activities: %w", err)
	}
	result := make([]*ActivitySummary, 0, len(raw))
	for _, r := range raw {
		s := &ActivitySummary{RawJSON: r}
		if err := json.Unmarshal(r, s); err != nil {
			return nil, fmt.Errorf("failed to decode activity: %w", err)
		}
		result = append(result, s)
	}
	return result, nil
}

// DownloadFile downloads an activity file a notification's callback URL
// points to. The link expires after a day, after which it returns
// ErrNotFound.
func (c *Client) DownloadFile(ctx context.Context, accessToken, callbackURL string) ([]byte, error) {
	if err := c.checkCallback(callbackURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, callbackURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	if err := statusError(resp.StatusCode, data); err != nil {
		return nil, err
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("the activity file is over %d bytes", maxFileSize)
	}
	return data, nil
}

// checkCallback checks that a callback URL from a notification is Garmin's,
// so a forged notification can't make the server send the user's token
// elsewhere.
func (c *Client) checkCallback(callbackURL string) error {
	u, err := url.Parse(callbackURL)
	if err != nil {
		return fmt.Errorf("invalid callback URL: %w", err)
	}
	api, _ := url.Parse(c.apiURL)
	if u.Scheme != api.Scheme || u.Host != api.Host {
		return fmt.Errorf("callback URL %q isn't Garmin's", u.Redacted())
	}
	return nil
}
