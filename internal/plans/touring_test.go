package plans

import (
	"slices"
	"testing"

	"github.com/bs-tools/trailrunn.ing/internal/avalanche"
)

func TestTouringLimitsEvaluate(t *testing.T) {
	t.Parallel()
	rating := func(d avalanche.Danger) avalanche.Rating { return avalanche.Rating{Danger: d, Description: d.String()} }
	day := func(alpine, treeline avalanche.Danger) *avalanche.Day {
		return &avalanche.Day{Alpine: rating(alpine), Treeline: rating(treeline), BelowTreeline: rating(avalanche.DangerLow)}
	}
	limits := &TouringLimits{
		MaxDangerAlpine:          avalanche.DangerModerate,
		MaxDangerTreeline:        avalanche.DangerConsiderable,
		MaxWindGustsKMH:          60,
		MaxFreezingLevelMeters:   2500,
		MaxSnowfallPast72HoursCM: 30,
	}
	calm := TouringConditions{Avalanche: day(avalanche.DangerModerate, avalanche.DangerConsiderable), MaxWindGustsKMH: 30, MaxFreezingLevelMeters: 1500, SnowfallPast72HoursCM: 10}
	tests := map[string]struct {
		limits     *TouringLimits
		conditions func(c *TouringConditions)
		want       Verdict
		// The checks' verdicts, in order: alpine, treeline, gusts,
		// freezing level and snow.
		wantChecks []Verdict
		wantFirst  string
	}{
		"within": {
			limits:     limits,
			conditions: func(*TouringConditions) {},
			want:       VerdictGo,
			wantChecks: []Verdict{VerdictGo, VerdictGo, VerdictGo, VerdictGo, VerdictGo},
			wantFirst:  "Avalanche danger in the alpine is Moderate, within your limit of Moderate",
		},
		"danger above": {
			limits: limits,
			conditions: func(c *TouringConditions) {
				c.Avalanche = day(avalanche.DangerConsiderable, avalanche.DangerConsiderable)
			},
			want:       VerdictNoGo,
			wantChecks: []Verdict{VerdictNoGo, VerdictGo, VerdictGo, VerdictGo, VerdictGo},
			wantFirst:  "Avalanche danger in the alpine is Considerable, above your limit of Moderate",
		},
		"unrated": {
			limits: limits,
			conditions: func(c *TouringConditions) {
				c.Avalanche = &avalanche.Day{Alpine: avalanche.Rating{Description: "Early Season"}, Treeline: rating(avalanche.DangerLow)}
			},
			want:       VerdictCaution,
			wantChecks: []Verdict{VerdictCaution, VerdictGo, VerdictGo, VerdictGo, VerdictGo},
			wantFirst:  "Avalanche danger in the alpine isn't rated (Early Season), so it can't be checked against your limit of Moderate: judge it yourself",
		},
		"no forecast": {
			limits:     limits,
			conditions: func(c *TouringConditions) { c.Avalanche = nil },
			want:       VerdictCaution,
			wantChecks: []Verdict{VerdictCaution, VerdictGo, VerdictGo, VerdictGo},
		},
		"close to the gusts": {
			limits:     limits,
			conditions: func(c *TouringConditions) { c.MaxWindGustsKMH = 55 },
			want:       VerdictCaution,
			wantChecks: []Verdict{VerdictGo, VerdictGo, VerdictCaution, VerdictGo, VerdictGo},
		},
		"warm and snowy": {
			limits:     limits,
			conditions: func(c *TouringConditions) { c.MaxFreezingLevelMeters, c.SnowfallPast72HoursCM = 2800, 40 },
			want:       VerdictNoGo,
			wantChecks: []Verdict{VerdictGo, VerdictGo, VerdictGo, VerdictNoGo, VerdictNoGo},
		},
		"only gusts": {
			limits:     &TouringLimits{MaxWindGustsKMH: 60},
			conditions: func(c *TouringConditions) { c.Avalanche = nil; c.MaxWindGustsKMH = 70 },
			want:       VerdictNoGo,
			wantChecks: []Verdict{VerdictNoGo},
			wantFirst:  "Gusts up to 70 km/h, above your limit of 60 km/h",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := calm
			tt.conditions(&c)
			got, checks := tt.limits.Evaluate(c)
			if got != tt.want {
				t.Errorf("got verdict %v, want %v", got, tt.want)
			}
			var verdicts []Verdict
			for _, check := range checks {
				verdicts = append(verdicts, check.Verdict)
			}
			if !slices.Equal(verdicts, tt.wantChecks) {
				t.Errorf("got checks %+v, want verdicts %v", checks, tt.wantChecks)
			}
			if tt.wantFirst != "" && checks[0].Message != tt.wantFirst {
				t.Errorf("got first check %q, want %q", checks[0].Message, tt.wantFirst)
			}
		})
	}
}

func TestTouringLimitsValidate(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		limits TouringLimits
		valid  bool
	}{
		"empty":             {TouringLimits{}, true},
		"set":               {TouringLimits{MaxDangerAlpine: avalanche.DangerHigh, MaxWindGustsKMH: 60, MaxFreezingLevelMeters: 2500, MaxSnowfallPast72HoursCM: 30}, true},
		"danger too high":   {TouringLimits{MaxDangerTreeline: 6}, false},
		"gusts in m/s":      {TouringLimits{MaxWindGustsKMH: 5}, false},
		"freezing level":    {TouringLimits{MaxFreezingLevelMeters: 9000}, false},
		"negative snowfall": {TouringLimits{MaxSnowfallPast72HoursCM: -1}, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := tt.limits.Validate(); (err == nil) != tt.valid {
				t.Errorf("got %v, want valid %v", err, tt.valid)
			}
		})
	}
}
