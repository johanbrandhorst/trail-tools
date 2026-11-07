package activity

import (
	"context"
	"errors"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	"github.com/johanbrandhorst/trail-tools/internal/lactate"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

const (
	// maxZoneWeeks is the longest date range heart rate zones are summarized
	// over. Each activity's streams are read from storage, which takes about
	// as long as a database round trip, and holds them in memory while they're
	// counted.
	maxZoneWeeks = 26
	// zoneConcurrency is how many activities' streams are read at once.
	zoneConcurrency = 8
	// maxSampleGapSeconds is the longest gap between two samples that counts
	// as time in a zone. Longer gaps are pauses, which recordings skip.
	maxSampleGapSeconds = 60
)

// ThresholdRepository lists the user's lactate tests, whose thresholds
// heart rate zones default to.
type ThresholdRepository interface {
	ListTests(ctx context.Context, userID pgtype.UUID) ([]*lactate.Test, error)
}

// zoneThresholds are the heart rates the zones are divided at, in bpm. The
// anaerobic threshold is 0 if unknown.
type zoneThresholds struct {
	aerobic, anaerobic int32
}

// thresholds returns the thresholds of a summary request, defaulting to
// those of the user's latest lactate tests that found them.
func (s *Service) thresholds(ctx context.Context, userID pgtype.UUID, req *activitiesv1.SummarizeActivitiesRequest) (zoneThresholds, error) {
	t := zoneThresholds{aerobic: req.GetAerobicThresholdHeartRateBpm(), anaerobic: req.GetAnaerobicThresholdHeartRateBpm()}
	if t.aerobic == 0 || t.anaerobic == 0 {
		tests, err := s.thresholdTests.ListTests(ctx, userID)
		if err != nil {
			return t, s.internalError(ctx, "failed to list lactate tests", err)
		}
		// The tests are listed with the latest first.
		for _, test := range tests {
			if t.aerobic == 0 {
				t.aerobic = test.HeartRateAt(lactate.AerobicThresholdMmolPerLiter)
			}
			if t.anaerobic == 0 {
				t.anaerobic = test.HeartRateAt(lactate.AnaerobicThresholdMmolPerLiter)
			}
		}
	}
	switch {
	case t.aerobic <= 0:
		return t, connect.NewError(connect.CodeFailedPrecondition,
			"no lactate test found an aerobic threshold; set aerobic_threshold_heart_rate_bpm for the heart rate zones")
	case t.anaerobic != 0 && t.anaerobic <= t.aerobic:
		return t, connect.Errorf(connect.CodeInvalidArgument,
			"the anaerobic threshold, %d bpm, must be above the aerobic threshold, %d bpm", t.anaerobic, t.aerobic)
	}
	return t, nil
}

// zoneTimes returns the time in heart rate zones of each activity, by the
// index of its totals, reading the streams of those that have them.
func (s *Service) zoneTimes(ctx context.Context, userID pgtype.UUID, totals []activities.ActivityTotals, t zoneThresholds) ([]*zoneTime, error) {
	times := make([]*zoneTime, len(totals))
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	sem := make(chan struct{}, zoneConcurrency)
	for i, total := range totals {
		if !total.HasStreams {
			times[i] = &zoneTime{noHeartRate: total.MovingTimeSeconds}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			streams, err := s.activities.GetActivityStreams(ctx, userID, total.ID)
			if errors.Is(err, store.ErrNotFound) {
				times[i] = &zoneTime{noHeartRate: total.MovingTimeSeconds}
				return
			}
			if err != nil {
				mu.Lock()
				firstErr = cmpErr(firstErr, err)
				mu.Unlock()
				return
			}
			times[i] = countZones(streams, t, total.MovingTimeSeconds)
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, s.internalError(ctx, "failed to read activity streams", firstErr)
	}
	return times, nil
}

func cmpErr(first, err error) error {
	if first != nil {
		return first
	}
	return err
}

// zoneTime is the time in each zone, in seconds.
type zoneTime struct {
	below, between, above, noHeartRate int32
}

func (z *zoneTime) add(other *zoneTime) {
	z.below += other.below
	z.between += other.between
	z.above += other.above
	z.noHeartRate += other.noHeartRate
}

func (z *zoneTime) toProto() *activitiesv1.HeartRateZones {
	return activitiesv1.HeartRateZones_builder{
		BelowAerobicThresholdSeconds:   z.below,
		BetweenThresholdsSeconds:       z.between,
		AboveAnaerobicThresholdSeconds: z.above,
		NoHeartRateSeconds:             z.noHeartRate,
	}.Build()
}

// countZones counts the time in each zone of an activity's streams: each
// sample's time since the one before goes to the zone of its heart rate.
// An activity without heart rate counts its moving time as none.
func countZones(streams *activitiesv1.ActivityStreams, t zoneThresholds, movingTimeSeconds int32) *zoneTime {
	times, heartRates := streams.GetTimeSeconds(), streams.GetHeartrateBpm()
	if len(heartRates) == 0 || len(heartRates) != len(times) {
		return &zoneTime{noHeartRate: movingTimeSeconds}
	}
	z := &zoneTime{}
	for i := 1; i < len(times); i++ {
		dt := times[i] - times[i-1]
		if dt <= 0 || dt > maxSampleGapSeconds {
			continue
		}
		switch hr := heartRates[i]; {
		case hr <= 0:
			z.noHeartRate += dt
		case hr < float64(t.aerobic):
			z.below += dt
		case t.anaerobic == 0 || hr < float64(t.anaerobic):
			z.between += dt
		default:
			z.above += dt
		}
	}
	return z
}

// zoneWeeksLimit returns the latest end date heart rate zones can be
// summarized to from a start date.
func zoneWeeksLimit(start time.Time) time.Time {
	return start.AddDate(0, 0, 7*maxZoneWeeks-1)
}
