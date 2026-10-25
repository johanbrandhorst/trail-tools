package plan

import (
	"testing"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/mapdata"
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
			byLocalDay := daytimeByLocalDay(tt.hours)
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
