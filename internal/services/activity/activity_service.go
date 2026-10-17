// Package activity implements the ActivityService, which queries synced
// activities.
package activity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1/activitiesv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ activitiesv1connect.ActivityServiceHandler = (*Service)(nil)

type ActivityRepository interface {
	ListActivities(ctx context.Context, userID pgtype.UUID, filter activities.ListActivitiesFilter) ([]*activities.Activity, error)
	GetActivity(ctx context.Context, userID, activityID pgtype.UUID) (*activities.Activity, error)
	GetActivityStreams(ctx context.Context, userID, activityID pgtype.UUID) (*activitiesv1.ActivityStreams, error)
	ListActivityPhotos(ctx context.Context, userID, activityID pgtype.UUID) ([]activities.Photo, error)
	ListActivityTotals(ctx context.Context, userID pgtype.UUID, startDate, endDate time.Time, sportTypes []string) ([]activities.ActivityTotals, error)
}

// Service implements API handlers for the activity service.
type Service struct {
	log        *slog.Logger
	report     *errreport.Reporter
	activities ActivityRepository
	// The URL of Strava's website, for links to activities.
	stravaWebURL string
}

// NewService creates a new Service. stravaWebURL is the URL of Strava's
// website, usually strava.WebURL, for links to activities.
func NewService(log *slog.Logger, report *errreport.Reporter, activities ActivityRepository, stravaWebURL string) *Service {
	return &Service{
		log:          log,
		report:       report,
		activities:   activities,
		stravaWebURL: stravaWebURL,
	}
}

const (
	defaultPageSize       = 50
	maxPageSize           = 500
	defaultStreamPoints   = 1000
	maxStreamPoints       = 20000
	maxSummaryPeriodYears = 5
)

var activitySorts = map[activitiesv1.ActivitySort]activities.Sort{
	activitiesv1.ActivitySort_ACTIVITY_SORT_UNSPECIFIED:    activities.SortRecent,
	activitiesv1.ActivitySort_ACTIVITY_SORT_DISTANCE:       activities.SortDistance,
	activitiesv1.ActivitySort_ACTIVITY_SORT_ELEVATION_GAIN: activities.SortElevationGain,
	activitiesv1.ActivitySort_ACTIVITY_SORT_MOVING_TIME:    activities.SortMovingTime,
}

func (s *Service) ListActivities(ctx context.Context, req *activitiesv1.ListActivitiesRequest) (*activitiesv1.ListActivitiesResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	sort, ok := activitySorts[req.GetSort()]
	if !ok {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "unknown sort %v", req.GetSort())
	}
	filter := activities.ListActivitiesFilter{
		SportTypes: req.GetSportTypes(),
		Sort:       sort,
		Limit:      defaultPageSize,
	}
	if size := req.GetPageSize(); size > 0 {
		filter.Limit = min(size, maxPageSize)
	}
	var err error
	if filter.StartDate, filter.EndDate, err = parseDateRange(req.GetStartDate(), req.GetEndDate()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if token := req.GetPageToken(); token != "" {
		offset, err := strconv.Atoi(token)
		if err != nil || offset < 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, "invalid page token")
		}
		filter.Offset = int32(offset)
	}
	// Fetch one extra to know whether there is another page.
	filter.Limit++
	list, err := s.activities.ListActivities(ctx, userID, filter)
	if err != nil {
		return nil, s.internalError(ctx, "failed to list activities", err)
	}
	var nextPageToken string
	if len(list) == int(filter.Limit) {
		list = list[:len(list)-1]
		nextPageToken = strconv.Itoa(int(filter.Offset) + len(list))
	}
	protoActivities := make([]*activitiesv1.Activity, len(list))
	for i, a := range list {
		protoActivities[i] = s.activityToProto(a)
	}
	return activitiesv1.ListActivitiesResponse_builder{
		Activities:    protoActivities,
		NextPageToken: nextPageToken,
	}.Build(), nil
}

func (s *Service) GetActivity(ctx context.Context, req *activitiesv1.GetActivityRequest) (*activitiesv1.GetActivityResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	activityID, err := store.StringToUUID(req.GetId())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid activity ID: %v", err).WithCause(err)
	}
	activity, err := s.activities.GetActivity(ctx, userID, activityID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, "activity not found")
		}
		return nil, s.internalError(ctx, "failed to get activity", err)
	}
	resp := activitiesv1.GetActivityResponse_builder{Activity: s.activityToProto(activity)}
	if req.GetIncludeStreams() && activity.StreamsSynced {
		streams, err := s.activities.GetActivityStreams(ctx, userID, activityID)
		switch {
		case err == nil:
			limit := defaultStreamPoints
			if n := int(req.GetMaxStreamPoints()); n > 0 {
				limit = min(n, maxStreamPoints)
			}
			resp.Streams = DownsampleStreams(streams, limit)
		case errors.Is(err, store.ErrNotFound):
			// The activity has no streams, e.g. a manual entry.
		default:
			return nil, s.internalError(ctx, "failed to get streams", err)
		}
	}
	photos, err := s.activities.ListActivityPhotos(ctx, userID, activityID)
	if err != nil {
		return nil, s.internalError(ctx, "failed to list photos", err)
	}
	for _, p := range photos {
		resp.Photos = append(resp.Photos, photoToProto(p))
	}
	return resp.Build(), nil
}

func (s *Service) SummarizeActivities(ctx context.Context, req *activitiesv1.SummarizeActivitiesRequest) (*activitiesv1.SummarizeActivitiesResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	startDate, endDate, err := parseDateRange(req.GetStartDate(), req.GetEndDate())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if startDate.IsZero() || endDate.IsZero() {
		return nil, connect.NewError(connect.CodeInvalidArgument, "start_date and end_date are required")
	}
	if endDate.After(startDate.AddDate(maxSummaryPeriodYears, 0, 0)) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "the date range must be at most %d years", maxSummaryPeriodYears)
	}
	monthly := req.GetInterval() == activitiesv1.SummaryInterval_SUMMARY_INTERVAL_MONTH
	periods := Periods(startDate, endDate, monthly)
	totals, err := s.activities.ListActivityTotals(ctx, userID, periods[0].Start, endDate, req.GetSportTypes())
	if err != nil {
		return nil, s.internalError(ctx, "failed to list activity totals", err)
	}
	summaries := make([]*activitiesv1.ActivitySummary, len(periods))
	p := 0
	for i, period := range periods {
		var count, movingTime int32
		var distance, elevation, longest float64
		for p < len(totals) && !totals[p].StartDateLocal.After(period.End) {
			t := totals[p]
			count++
			distance += t.DistanceMeters
			movingTime += t.MovingTimeSeconds
			elevation += t.ElevationGainMeters
			longest = max(longest, t.DistanceMeters)
			p++
		}
		summaries[i] = activitiesv1.ActivitySummary_builder{
			StartDate:             store.FormatDate(period.Start),
			EndDate:               store.FormatDate(period.End),
			ActivityCount:         count,
			DistanceMeters:        distance,
			MovingTimeSeconds:     movingTime,
			ElevationGainMeters:   elevation,
			LongestDistanceMeters: longest,
		}.Build()
	}
	return activitiesv1.SummarizeActivitiesResponse_builder{
		Summaries: summaries,
	}.Build(), nil
}

// Period is an inclusive range of dates.
type Period struct {
	Start, End time.Time
}

// Periods divides a date range into weeks starting on Monday, or calendar
// months. The first period starts at the beginning of the week or month
// containing start.
func Periods(start, end time.Time, monthly bool) []Period {
	var periods []Period
	if monthly {
		for t := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC); !t.After(end); t = t.AddDate(0, 1, 0) {
			periods = append(periods, Period{Start: t, End: t.AddDate(0, 1, -1)})
		}
		return periods
	}
	for t := WeekStart(start); !t.After(end); t = t.AddDate(0, 0, 7) {
		periods = append(periods, Period{Start: t, End: t.AddDate(0, 0, 6)})
	}
	return periods
}

// WeekStart returns the Monday of the week containing the date.
func WeekStart(date time.Time) time.Time {
	offset := (int(date.Weekday()) + 6) % 7
	return date.AddDate(0, 0, -offset)
}

func parseDateRange(start, end string) (startDate, endDate time.Time, err error) {
	if start != "" {
		if startDate, err = store.ParseDate(start); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start_date: %w", err)
		}
	}
	if end != "" {
		if endDate, err = store.ParseDate(end); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end_date: %w", err)
		}
	}
	if !startDate.IsZero() && !endDate.IsZero() && endDate.Before(startDate) {
		return time.Time{}, time.Time{}, errors.New("end_date must not be before start_date")
	}
	return startDate, endDate, nil
}

func photoToProto(p activities.Photo) *activitiesv1.ActivityPhoto {
	b := activitiesv1.ActivityPhoto_builder{
		Id:      p.StravaID,
		Caption: p.Caption,
		Width:   p.Width,
		Height:  p.Height,
		Url:     p.URL,
	}
	if p.Location != nil {
		b.Location = activitiesv1.LatLng_builder{Lat: p.Location.Lat, Lng: p.Location.Lng}.Build()
	}
	if !p.TakenTime.IsZero() {
		b.TakenTime = timestamppb.New(p.TakenTime)
	}
	return b.Build()
}

func (s *Service) activityToProto(a *activities.Activity) *activitiesv1.Activity {
	stravaID := strconv.FormatInt(a.StravaID, 10)
	return activitiesv1.Activity_builder{
		Id:                  store.UUIDToString(a.ID),
		StravaId:            stravaID,
		StravaUrl:           s.stravaWebURL + "/activities/" + stravaID,
		Name:                a.Name,
		Description:         a.Description,
		SportType:           a.SportType,
		StartTime:           timestamppb.New(a.StartTime),
		StartDateLocal:      store.FormatDate(a.StartDateLocal),
		Timezone:            a.Timezone,
		DistanceMeters:      a.DistanceMeters,
		MovingTimeSeconds:   a.MovingTimeSeconds,
		ElapsedTimeSeconds:  a.ElapsedTimeSeconds,
		ElevationGainMeters: a.ElevationGainMeters,
		AverageSpeedMps:     a.AverageSpeedMps,
		MaxSpeedMps:         a.MaxSpeedMps,
		AverageHeartrateBpm: a.AverageHeartrateBpm,
		MaxHeartrateBpm:     a.MaxHeartrateBpm,
		AverageCadenceSpm:   a.AverageCadenceSpm,
		SummaryPolyline:     a.SummaryPolyline,
		HasStreams:          a.StreamsSynced,
		DeviceName:          a.DeviceName,
		RelativeEffort:      a.RelativeEffort,
		PhotoCount:          a.PhotoCount,
	}.Build()
}

// DownsampleStreams returns the streams with at most limit points.
func DownsampleStreams(streams *activitiesv1.ActivityStreams, limit int) *activitiesv1.ActivityStreams {
	n := 0
	for _, l := range []int{
		len(streams.GetTimeSeconds()), len(streams.GetDistanceMeters()), len(streams.GetLat()),
		len(streams.GetAltitudeMeters()), len(streams.GetHeartrateBpm()),
	} {
		n = max(n, l)
	}
	idxs := geo.Downsample(n, limit)
	pickFloats := func(values []float64) []float64 {
		if len(values) != n {
			return nil
		}
		out := make([]float64, len(idxs))
		for i, idx := range idxs {
			out[i] = values[idx]
		}
		return out
	}
	var times []int32
	if len(streams.GetTimeSeconds()) == n {
		times = make([]int32, len(idxs))
		for i, idx := range idxs {
			times[i] = streams.GetTimeSeconds()[idx]
		}
	}
	return activitiesv1.ActivityStreams_builder{
		TimeSeconds:    times,
		DistanceMeters: pickFloats(streams.GetDistanceMeters()),
		Lat:            pickFloats(streams.GetLat()),
		Lng:            pickFloats(streams.GetLng()),
		AltitudeMeters: pickFloats(streams.GetAltitudeMeters()),
		HeartrateBpm:   pickFloats(streams.GetHeartrateBpm()),
		CadenceSpm:     pickFloats(streams.GetCadenceSpm()),
		VelocityMps:    pickFloats(streams.GetVelocityMps()),
		GradePercent:   pickFloats(streams.GetGradePercent()),
	}.Build()
}

func (s *Service) internalError(ctx context.Context, msg string, err error) error {
	s.report.Report(ctx, msg, err)
	return connect.NewError(connect.CodeInternal, msg).WithCause(err)
}
