package strava

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Activity is an activity returned by the Strava API. Detailed fields are
// only set when getting a single activity.
type Activity struct {
	ID int64 `json:"id"`
	// The ID of the upload the activity came from, like "garmin_ping_123"
	// for an activity Garmin Connect uploaded.
	ExternalID         string    `json:"external_id"`
	Name               string    `json:"name"`
	SportType          string    `json:"sport_type"`
	StartDate          time.Time `json:"start_date"`
	StartDateLocal     string    `json:"start_date_local"`
	Timezone           string    `json:"timezone"`
	Distance           float64   `json:"distance"`
	MovingTime         int32     `json:"moving_time"`
	ElapsedTime        int32     `json:"elapsed_time"`
	TotalElevationGain float64   `json:"total_elevation_gain"`
	AverageSpeed       float64   `json:"average_speed"`
	MaxSpeed           float64   `json:"max_speed"`
	AverageHeartrate   float64   `json:"average_heartrate"`
	MaxHeartrate       float64   `json:"max_heartrate"`
	AverageCadence     float64   `json:"average_cadence"`
	SufferScore        float64   `json:"suffer_score"`
	TotalPhotoCount    int32     `json:"total_photo_count"`
	Map                struct {
		SummaryPolyline string `json:"summary_polyline"`
	} `json:"map"`
	// Detailed fields.
	Description string `json:"description"`
	DeviceName  string `json:"device_name"`

	// The raw JSON of the activity.
	Raw json.RawMessage `json:"-"`
}

// LocalDate returns the local date the activity started on.
func (a *Activity) LocalDate() time.Time {
	// The local start time is formatted like a UTC time, but is local.
	t, err := time.Parse(time.RFC3339, a.StartDateLocal)
	if err != nil {
		return time.Date(a.StartDate.Year(), a.StartDate.Month(), a.StartDate.Day(), 0, 0, 0, 0, time.UTC)
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// TimezoneName returns the IANA timezone name of the activity, e.g.
// "Europe/London".
func (a *Activity) TimezoneName() string {
	// Strava formats timezones like "(GMT+00:00) Europe/London".
	if _, name, ok := strings.Cut(a.Timezone, ") "); ok {
		return name
	}
	return a.Timezone
}

// IsFootSport returns whether the activity is a running or walking sport.
// Strava reports the cadence of these per leg.
func (a *Activity) IsFootSport() bool {
	switch a.SportType {
	case "Run", "TrailRun", "VirtualRun", "Walk", "Hike", "Snowshoe":
		return true
	}
	return false
}

// MaxActivitiesPerPage is the maximum page size of ListActivities.
const MaxActivitiesPerPage = 200

// ListActivities lists up to perPage of the athlete's activities that
// started before the provided time, or the latest ones if it is zero,
// newest first.
func (c *Client) ListActivities(ctx context.Context, accessToken string, before time.Time, perPage int) ([]*Activity, error) {
	q := url.Values{}
	if !before.IsZero() {
		q.Set("before", strconv.FormatInt(before.Unix(), 10))
	}
	q.Set("per_page", strconv.Itoa(perPage))
	body, err := c.get(ctx, accessToken, "/athlete/activities", q)
	if err != nil {
		return nil, err
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(body, &raws); err != nil {
		return nil, fmt.Errorf("failed to decode activities: %w", err)
	}
	activities := make([]*Activity, len(raws))
	for i, raw := range raws {
		if activities[i], err = decodeActivity(raw); err != nil {
			return nil, err
		}
	}
	return activities, nil
}

// GetActivity gets the details of an activity.
func (c *Client) GetActivity(ctx context.Context, accessToken string, id int64) (*Activity, error) {
	body, err := c.get(ctx, accessToken, "/activities/"+strconv.FormatInt(id, 10), nil)
	if err != nil {
		return nil, err
	}
	return decodeActivity(body)
}

func decodeActivity(raw json.RawMessage) (*Activity, error) {
	var a Activity
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("failed to decode activity: %w", err)
	}
	a.Raw = raw
	return &a, nil
}

// Streams are the data streams recorded during an activity. Streams that were
// not recorded are nil.
type Streams struct {
	Time           []int32
	Distance       []float64
	LatLng         [][2]float64
	Altitude       []float64
	Heartrate      []float64
	Cadence        []float64
	VelocitySmooth []float64
	GradeSmooth    []float64
}

type streamsResponse struct {
	Time           *struct{ Data []int32 }      `json:"time"`
	Distance       *struct{ Data []float64 }    `json:"distance"`
	LatLng         *struct{ Data [][2]float64 } `json:"latlng"`
	Altitude       *struct{ Data []float64 }    `json:"altitude"`
	Heartrate      *struct{ Data []float64 }    `json:"heartrate"`
	Cadence        *struct{ Data []float64 }    `json:"cadence"`
	VelocitySmooth *struct{ Data []float64 }    `json:"velocity_smooth"`
	GradeSmooth    *struct{ Data []float64 }    `json:"grade_smooth"`
}

// GetStreams gets the full resolution data streams of an activity. It returns
// ErrNotFound if the activity has no streams.
func (c *Client) GetStreams(ctx context.Context, accessToken string, id int64) (*Streams, error) {
	q := url.Values{}
	q.Set("keys", "time,distance,latlng,altitude,heartrate,cadence,velocity_smooth,grade_smooth")
	q.Set("key_by_type", "true")
	body, err := c.get(ctx, accessToken, "/activities/"+strconv.FormatInt(id, 10)+"/streams", q)
	if err != nil {
		return nil, err
	}
	var resp streamsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode streams: %w", err)
	}
	streams := &Streams{}
	if resp.Time != nil {
		streams.Time = resp.Time.Data
	}
	if resp.Distance != nil {
		streams.Distance = resp.Distance.Data
	}
	if resp.LatLng != nil {
		streams.LatLng = resp.LatLng.Data
	}
	if resp.Altitude != nil {
		streams.Altitude = resp.Altitude.Data
	}
	if resp.Heartrate != nil {
		streams.Heartrate = resp.Heartrate.Data
	}
	if resp.Cadence != nil {
		streams.Cadence = resp.Cadence.Data
	}
	if resp.VelocitySmooth != nil {
		streams.VelocitySmooth = resp.VelocitySmooth.Data
	}
	if resp.GradeSmooth != nil {
		streams.GradeSmooth = resp.GradeSmooth.Data
	}
	return streams, nil
}
