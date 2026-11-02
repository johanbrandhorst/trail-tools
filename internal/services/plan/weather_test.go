package plan

import (
	"slices"
	"testing"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/mapdata"
	"github.com/johanbrandhorst/trail-tools/internal/plans"
)

func TestDaytimeByLocalDay(t *testing.T) {
	t.Parallel()
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	// hoursIn returns the hours of three days from local midnight on October
	// 24, 2026, in a time zone, with the offset of each hour as the forecast
	// gives it.
	hoursIn := func(loc *time.Location) []mapdata.Weather {
		start := time.Date(2026, 10, 24, 0, 0, 0, 0, loc).UTC()
		var hours []mapdata.Weather
		for i := range 72 {
			h := start.Add(time.Duration(i) * time.Hour)
			_, offsetSeconds := h.In(loc).Zone()
			hours = append(hours, mapdata.Weather{Time: h, UTCOffset: time.Duration(offsetSeconds) * time.Second})
		}
		return hours
	}
	tests := map[string]struct {
		hours []mapdata.Weather
		// wantFirst are the UTC times of the first daytime hour of each day.
		wantFirst map[string]time.Time
	}{
		// Daylight saving time ends at 01:00 UTC on October 25, so 06:00
		// local is 04:00 UTC before it and 05:00 UTC after it.
		"daylight saving time ends": {
			hours: hoursIn(berlin),
			wantFirst: map[string]time.Time{
				"2026-10-24": time.Date(2026, 10, 24, 4, 0, 0, 0, time.UTC),
				"2026-10-25": time.Date(2026, 10, 25, 5, 0, 0, 0, time.UTC),
				"2026-10-26": time.Date(2026, 10, 26, 5, 0, 0, 0, time.UTC),
			},
		},
		// At +5:45, 06:00 local is 00:15 UTC.
		"fractional offset": {
			hours: hoursIn(time.FixedZone("Asia/Kathmandu", 5*3600+45*60)),
			wantFirst: map[string]time.Time{
				"2026-10-24": time.Date(2026, 10, 24, 0, 15, 0, 0, time.UTC),
				"2026-10-25": time.Date(2026, 10, 25, 0, 15, 0, 0, time.UTC),
				"2026-10-26": time.Date(2026, 10, 26, 0, 15, 0, 0, time.UTC),
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			daytime := map[string][]mapdata.Weather{}
			for day, hours := range byLocalDay(tt.hours) {
				if hours = slices.DeleteFunc(hours, func(h mapdata.Weather) bool { return !isDaytime(h) }); len(hours) > 0 {
					daytime[day] = hours
				}
			}
			byLocalDay := daytime
			if len(byLocalDay) != len(tt.wantFirst) {
				t.Errorf("got %d days, want %d", len(byLocalDay), len(tt.wantFirst))
			}
			for day, wantFirst := range tt.wantFirst {
				hours := byLocalDay[day]
				// The daytime is from the start of 06:00 to the end of 20:00,
				// 15 hours in local time whatever the offset.
				wantCount := daytimeLastHour - daytimeFirstHour + 1
				if len(hours) != wantCount {
					t.Errorf("%s: got %d hours, want %d", day, len(hours), wantCount)
					continue
				}
				if !hours[0].Time.Equal(wantFirst) {
					t.Errorf("%s: got first hour %v, want %v", day, hours[0].Time, wantFirst)
				}
				if wantLast := wantFirst.Add(time.Duration(wantCount-1) * time.Hour); !hours[len(hours)-1].Time.Equal(wantLast) {
					t.Errorf("%s: got last hour %v, want %v", day, hours[len(hours)-1].Time, wantLast)
				}
			}
		})
	}
}

func TestDuringWorkout(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("Europe/Zurich", 3600)
	var hours []mapdata.Weather
	for h := range 24 {
		hours = append(hours, mapdata.Weather{Time: time.Date(2026, 11, 5, h, 0, 0, 0, zone), UTCOffset: time.Hour})
	}
	date := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	tests := map[string]struct {
		workout            *plans.Workout
		wantFirst          int
		wantCount          int
		wantStart, wantEnd time.Time
	}{
		// From 07:00 to 08:00 plus a 90 minute run: until 09:30.
		"window and duration": {
			workout:   &plans.Workout{Date: date, StartTime: "07:00", StartWindowMinutes: 60, TargetDurationSeconds: 5400},
			wantFirst: 7, wantCount: 3,
			wantStart: time.Date(2026, 11, 5, 7, 0, 0, 0, zone),
			wantEnd:   time.Date(2026, 11, 5, 9, 30, 0, 0, zone),
		},
		// Without a duration, the workout takes an hour; a start within
		// an hour counts that hour.
		"no duration": {
			workout:   &plans.Workout{Date: date, StartTime: "05:30", StartWindowMinutes: 30},
			wantFirst: 5, wantCount: 2,
			wantStart: time.Date(2026, 11, 5, 5, 30, 0, 0, zone),
			wantEnd:   time.Date(2026, 11, 5, 7, 0, 0, 0, zone),
		},
		"no start time": {workout: &plans.Workout{Date: date}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			during, start, end := duringWorkout(tt.workout, hours)
			if len(during) != tt.wantCount {
				t.Fatalf("got %d hours, want %d", len(during), tt.wantCount)
			}
			if tt.wantCount == 0 {
				return
			}
			if got := during[0].Time.In(zone).Hour(); got != tt.wantFirst {
				t.Errorf("got first hour %d, want %d", got, tt.wantFirst)
			}
			if !start.Equal(tt.wantStart) || !end.Equal(tt.wantEnd) {
				t.Errorf("got %v to %v, want %v to %v", start, end, tt.wantStart, tt.wantEnd)
			}
		})
	}
}
