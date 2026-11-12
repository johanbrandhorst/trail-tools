package stravaarchive

import (
	"encoding/csv"
	"errors"
	"strings"
	"testing"
	"time"
)

// header is the header of a real activities.csv, from an archive exported
// in November 2026.
const header = `Activity ID,Activity Date,Activity Name,Activity Type,Activity Description,Elapsed Time,Distance,Max Heart Rate,Relative Effort,Commute,Activity Private Note,Activity Gear,Filename,Athlete Weight,Bike Weight,Elapsed Time,Moving Time,Distance,Max Speed,Average Speed,Elevation Gain,Elevation Loss,Elevation Low,Elevation High,Max Grade,Average Grade,Average Positive Grade,Average Negative Grade,Max Cadence,Average Cadence,Max Heart Rate,Average Heart Rate,Max Watts,Average Watts,Calories,Max Temperature,Average Temperature,Relative Effort,Total Work,Number of Runs,Uphill Time,Downhill Time,Other Time,Perceived Exertion,Type,Start Time,Weighted Average Power,Power Count,Prefer Perceived Exertion,Perceived Relative Effort,Commute,Total Weight Lifted,From Upload,Grade Adjusted Distance,Weather Observation Time,Weather Condition,Weather Temperature,Apparent Temperature,Dewpoint,Humidity,Weather Pressure,Wind Speed,Wind Gust,Wind Bearing,Precipitation Intensity,Sunrise Time,Sunset Time,Moon Phase,Bike,Gear,Precipitation Probability,Precipitation Type,Cloud Cover,Weather Visibility,UV Index,Weather Ozone,Jump Count,Total Grit,Average Flow,Flagged,Average Elapsed Speed,Dirt Distance,Newly Explored Distance,Newly Explored Dirt Distance,Activity Count,Total Steps,Carbon Saved,Pool Length,Training Load,Intensity,Average Grade Adjusted Pace,Timer Time,Total Cycles,Recovery,With Pet,Competition,Long Run,For a Cause,With Kid,Downhill Distance,Total Sets,Total Reps,Media,Tag`

// weightTraining is a real row of that archive.
const weightTraining = `20948227852,"Nov 11, 2026, 3:04:35 PM",Morning Weight Training,Weight Training,,1909,0,122.0,3,false,,,activities/22115185543.fit.gz,,,1909.0,1909.0,0.0,0.0,0.0,0.0,0.0,,,0.0,0.0,,,,,122.0,94.0,,,150.0,,25.1,3.0,,,,,,,,,,,,,0.0,0.0,1.0,,,,,,,,,,,,,,,,,,,,,,,,,,,,0.0,0.0,,,,,0.0,,,,,,,,,,,,,,24,0,"",""`

// row returns a row with values by column, the first column of each name
// unless it ends in "#2", for the second.
func row(t *testing.T, values map[string]string) string {
	t.Helper()
	names := strings.Split(header, ",")
	fields := make([]string, len(names))
	seen := map[string]int{}
	for i, name := range names {
		seen[name]++
		key := name
		if seen[name] == 2 {
			key += "#2"
		}
		fields[i] = values[key]
	}
	var b strings.Builder
	w := csv.NewWriter(&b)
	if err := w.Write(fields); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	return strings.TrimSuffix(b.String(), "\n")
}

func TestParseActivities(t *testing.T) {
	t.Parallel()
	run := row(t, map[string]string{
		"Activity ID": "20941719160", "Activity Date": "Nov 11, 2026, 1:42:00 AM", "Activity Name": "Tempo Tuesday",
		"Activity Type": "Run", "Activity Description": "3x 1mile @ 5k pace\n\nStill some stiffness in left glute.",
		// The first columns are in the athlete's units, kilometers here.
		"Elapsed Time": "5506", "Distance": "14.24", "Max Heart Rate": "188.0", "Relative Effort": "37",
		"Filename":       "activities/22100000000.fit.gz",
		"Elapsed Time#2": "5506.0", "Moving Time": "4543.0", "Distance#2": "14244.8", "Elevation Gain": "24.0",
		"Max Heart Rate#2": "188.0", "Average Heart Rate": "146.1", "Average Cadence": "83.4", "Relative Effort#2": "37.0",
		"Average Speed": "3.136", "Max Speed": "5.78",
	})
	manual := row(t, map[string]string{
		"Activity ID": "123", "Activity Date": "Feb 3, 2020, 6:00:00 PM", "Activity Name": "Treadmill",
		"Activity Type": "Run", "Elapsed Time#2": "1800.0", "Distance#2": "5000.0",
	})
	broken := row(t, map[string]string{"Activity ID": "456", "Activity Date": "yesterday", "Activity Name": "Broken"})
	file := "\ufeff" + header + "\n" + weightTraining + "\n" + run + "\n" + manual + "\n" + broken + "\n"

	activities, skipped, err := ParseActivities(strings.NewReader(file))
	if err != nil {
		t.Fatalf("ParseActivities: %v", err)
	}
	if skipped != 1 || len(activities) != 3 {
		t.Fatalf("got %d activities and %d skipped, want 3 and the one without a date skipped", len(activities), skipped)
	}
	strength, tempo, treadmill := activities[0], activities[1], activities[2]
	if strength.StravaID != 20948227852 || strength.SportType != "WeightTraining" || strength.Filename != "activities/22115185543.fit.gz" ||
		!strength.StartTime.Equal(time.Date(2026, 11, 11, 15, 4, 35, 0, time.UTC)) || strength.ElapsedTimeSeconds != 1909 ||
		strength.AverageHeartrateBpm != 94 || strength.MaxHeartrateBpm != 122 || strength.RelativeEffort != 3 {
		t.Errorf("unexpected weight training %+v", strength)
	}
	// The raw distance is read, in meters, and the description keeps its
	// lines.
	if tempo.DistanceMeters != 14244.8 || tempo.MovingTimeSeconds != 4543 || tempo.ElevationGainMeters != 24 ||
		tempo.Description != "3x 1mile @ 5k pace\n\nStill some stiffness in left glute." || tempo.AverageCadence != 83.4 ||
		!tempo.StartTime.Equal(time.Date(2026, 11, 11, 1, 42, 0, 0, time.UTC)) {
		t.Errorf("unexpected run %+v", tempo)
	}
	if treadmill.Filename != "" || treadmill.DistanceMeters != 5000 || treadmill.ElapsedTimeSeconds != 1800 {
		t.Errorf("unexpected manual activity %+v", treadmill)
	}
}

func TestParseActivitiesRejects(t *testing.T) {
	t.Parallel()
	for name, file := range map[string]string{
		"empty":       "",
		"not strava":  "id,name\n1,Run\n",
		"no filename": "Activity ID,Activity Date,Activity Name,Activity Type\n1,\"Jan 1, 2020, 1:00:00 PM\",Run,Run\n",
	} {
		if _, _, err := ParseActivities(strings.NewReader(file)); !errors.Is(err, ErrNotActivities) {
			t.Errorf("%s: got %v, want ErrNotActivities", name, err)
		}
	}
}

func TestSportType(t *testing.T) {
	t.Parallel()
	for activityType, want := range map[string]string{
		"Run": "Run", "Weight Training": "WeightTraining", "Backcountry Ski": "BackcountrySki",
		"E-Bike Ride": "EBikeRide", "HIIT": "HighIntensityIntervalTraining",
	} {
		if got := SportType(activityType); got != want {
			t.Errorf("SportType(%q) = %q, want %q", activityType, got, want)
		}
	}
}
