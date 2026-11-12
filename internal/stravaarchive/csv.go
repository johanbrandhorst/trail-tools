// Package stravaarchive reads the archive of their data that Strava gives
// its users: activities.csv, which lists the activities with Strava's
// stats, and the activities' original files, which the activities' samples
// come from. Its data comes from the user rather than Strava's API.
package stravaarchive

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Activity is an activity as activities.csv lists it.
type Activity struct {
	// Strava's ID of the activity, the same as its API's.
	StravaID    int64
	Name        string
	Description string
	// Strava's sport type, from the activity type the archive gives, like
	// "Run" for "Run" and "WeightTraining" for "Weight Training". The
	// archive gives the coarser, older types, so a trail run is a "Run".
	SportType string
	StartTime time.Time
	// The stats, zero if not given.
	ElapsedTimeSeconds  int32
	MovingTimeSeconds   int32
	DistanceMeters      float64
	ElevationGainMeters float64
	AverageSpeedMps     float64
	MaxSpeedMps         float64
	AverageHeartrateBpm float64
	MaxHeartrateBpm     float64
	// In strides per minute for running and walking, as Strava's API gives
	// it.
	AverageCadence float64
	RelativeEffort int32
	// The path of the activity's original file in the archive, like
	// "activities/22115185543.fit.gz", or empty for an activity without
	// one, like one entered by hand.
	Filename string
}

// column is a column of activities.csv.
type column struct {
	name string
	// Whether the last of the columns with the name is read, rather than
	// the first.
	last bool
	// Whether the column must be the raw values, which are only there when
	// the name is used twice: a single column of the name is in display
	// units, and isn't read.
	raw bool
}

// dateLayouts are the layouts of activities' dates, which are in UTC.
var dateLayouts = []string{"Jan 2, 2006, 3:04:05 PM", "Jan 2, 2006 3:04:05 PM", "2006-01-02 15:04:05"}

// ErrNotActivities is returned for a file that isn't a Strava archive's
// activities.csv.
var ErrNotActivities = errors.New("activities.csv isn't a Strava archive's list of activities")

// maxActivities bounds the activities read, far beyond any athlete's
// history, so a malformed file can't take the server's memory.
const maxActivities = 100_000

// ParseActivities reads activities.csv. It returns the activities, and the
// number of rows it skipped as they lack an ID or a start time it can read.
//
// Several column names are used twice: first for values in the athlete's
// display units, like kilometers or miles, then for the raw values, in
// meters and seconds, which are the ones read.
func ParseActivities(r io.Reader) (activities []Activity, skipped int, err error) {
	reader := csv.NewReader(r)
	// A row cut short shouldn't stop the others.
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	header, err := reader.Read()
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrNotActivities, err)
	}
	// The header may start with a byte order mark.
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}
	positions := map[string][]int{}
	for i, name := range header {
		name = strings.TrimSpace(name)
		positions[name] = append(positions[name], i)
	}
	index := func(c column) int {
		p := positions[c.name]
		switch {
		case len(p) == 0, c.raw && len(p) < 2:
			return -1
		case c.last:
			return p[len(p)-1]
		}
		return p[0]
	}
	required := []string{"Activity ID", "Activity Date", "Activity Name", "Activity Type", "Filename"}
	var missing []string
	for _, name := range required {
		if index(column{name: name}) < 0 {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, 0, fmt.Errorf("%w: it has no %s column", ErrNotActivities, strings.Join(missing, ", "))
	}
	var (
		id               = index(column{name: "Activity ID"})
		date             = index(column{name: "Activity Date"})
		name             = index(column{name: "Activity Name"})
		activityType     = index(column{name: "Activity Type"})
		description      = index(column{name: "Activity Description"})
		filename         = index(column{name: "Filename"})
		elapsed          = index(column{name: "Elapsed Time", last: true})
		distance         = index(column{name: "Distance", last: true, raw: true})
		maxHeartRate     = index(column{name: "Max Heart Rate", last: true})
		relativeEffort   = index(column{name: "Relative Effort", last: true})
		moving           = index(column{name: "Moving Time"})
		elevationGain    = index(column{name: "Elevation Gain"})
		averageSpeed     = index(column{name: "Average Speed"})
		maxSpeed         = index(column{name: "Max Speed"})
		averageHeartRate = index(column{name: "Average Heart Rate"})
		averageCadence   = index(column{name: "Average Cadence"})
	)
	for {
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("%w: %v", ErrNotActivities, err)
		}
		if len(activities) >= maxActivities {
			return nil, 0, fmt.Errorf("activities.csv lists more than %d activities", maxActivities)
		}
		field := func(i int) string {
			if i < 0 || i >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[i])
		}
		number := func(i int) float64 {
			v, err := strconv.ParseFloat(field(i), 64)
			if err != nil {
				return 0
			}
			return v
		}
		stravaID, err := strconv.ParseInt(field(id), 10, 64)
		start, ok := parseDate(field(date))
		if err != nil || stravaID <= 0 || !ok {
			skipped++
			continue
		}
		activities = append(activities, Activity{
			StravaID:            stravaID,
			Name:                field(name),
			Description:         field(description),
			SportType:           SportType(field(activityType)),
			StartTime:           start,
			ElapsedTimeSeconds:  int32(number(elapsed)),
			MovingTimeSeconds:   int32(number(moving)),
			DistanceMeters:      number(distance),
			ElevationGainMeters: number(elevationGain),
			AverageSpeedMps:     number(averageSpeed),
			MaxSpeedMps:         number(maxSpeed),
			AverageHeartrateBpm: number(averageHeartRate),
			MaxHeartrateBpm:     number(maxHeartRate),
			AverageCadence:      number(averageCadence),
			RelativeEffort:      int32(number(relativeEffort)),
			Filename:            field(filename),
		})
	}
	return activities, skipped, nil
}

// parseDate parses an activity's date, in UTC.
func parseDate(value string) (time.Time, bool) {
	// Newer formats put a narrow no-break space before AM and PM.
	value = strings.NewReplacer(" ", " ", " ", " ").Replace(value)
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// sportTypes are the archive's activity types whose sport type isn't the
// type without spaces and hyphens.
var sportTypes = map[string]string{
	"HIIT":       "HighIntensityIntervalTraining",
	"Rock Climb": "RockClimbing",
}

// SportType returns Strava's sport type of an activity type of the
// archive, like "WeightTraining" for "Weight Training".
func SportType(activityType string) string {
	if t, ok := sportTypes[activityType]; ok {
		return t
	}
	return strings.NewReplacer(" ", "", "-", "").Replace(activityType)
}
