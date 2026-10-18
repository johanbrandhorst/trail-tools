package racereport

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/races"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/track"
	"google.golang.org/protobuf/proto"
)

const (
	// maxSegments is the most segments a report can have.
	maxSegments = 50
	// maxSegmentsBodyLength is the most characters of text all the segments
	// of a report can have together.
	maxSegmentsBodyLength = 100_000
	// trackEndToleranceMeters is how far past the end of the track a segment
	// may end, for distances rounded in the app.
	trackEndToleranceMeters = 1
)

func (s *Service) SetRaceReportSegments(ctx context.Context, req *racesv1.SetRaceReportSegmentsRequest) (*racesv1.SetRaceReportSegmentsResponse, error) {
	report, err := s.getReport(ctx, req.GetReportId())
	if err != nil {
		return nil, err
	}
	if err := s.ensureTrack(ctx, report); err != nil {
		return nil, err
	}
	t, err := track.Decode(report.Track)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to decode race report track")
	}
	segments, err := segmentsFromProto(req.GetSegments(), t)
	if err != nil {
		return nil, err
	}
	if err := s.repo.SetRaceReportSegments(ctx, report.ID, segments); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to set race report segments")
	}
	// Setting the segments updated the report.
	updated, err := s.repo.GetRaceReport(ctx, report.UserID, report.ID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get race report")
	}
	profile, err := s.profile(ctx, report.UserID)
	if err != nil {
		return nil, err
	}
	reportProto := s.reportToProto(updated, profile)
	reportProto.SetSegments(segmentsToProto(segments))
	if err := s.withPhotos(ctx, reportProto, report.ID, nil); err != nil {
		return nil, err
	}
	return racesv1.SetRaceReportSegmentsResponse_builder{Report: reportProto}.Build(), nil
}

func (s *Service) SuggestRaceReportSegments(ctx context.Context, req *racesv1.SuggestRaceReportSegmentsRequest) (*racesv1.SuggestRaceReportSegmentsResponse, error) {
	report, err := s.getReport(ctx, req.GetReportId())
	if err != nil {
		return nil, err
	}
	if err := s.ensureTrack(ctx, report); err != nil {
		return nil, err
	}
	t, err := track.Decode(report.Track)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to decode race report track")
	}
	if t == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the report has no track to suggest segments from; set its activity first")
	}
	eles := make([]float64, len(t.Points))
	for i, p := range t.Points {
		eles[i] = p.Ele
	}
	suggested, err := track.SuggestSegments(t.Dists, eles)
	if errors.Is(err, track.ErrNoProfile) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the report's track has no elevations to suggest segments from")
	}
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to suggest race report segments")
	}
	segments := make([]*racesv1.RaceReportSegment, len(suggested))
	for i, seg := range suggested {
		segments[i] = racesv1.RaceReportSegment_builder{
			Title:               seg.Name,
			StartDistanceMeters: seg.StartMeters,
			EndDistanceMeters:   seg.EndMeters,
		}.Build()
	}
	return racesv1.SuggestRaceReportSegmentsResponse_builder{Segments: segments}.Build(), nil
}

// activityTrack returns the track to copy into a report from its activity:
// the positions of its streams, downsampled, or else its summary polyline,
// encoded. It returns nil if the activity has neither.
func (s *Service) activityTrack(ctx context.Context, userID pgtype.UUID, activity *activities.Activity) ([]byte, error) {
	if !activity.StreamsSynced {
		s.syncPending(ctx, userID, activity.ID)
	}
	var t *activitiesv1.ActivityStreams
	streams, err := s.activities.GetActivityStreams(ctx, userID, activity.ID)
	switch {
	case err == nil:
		t = track.FromStreams(streams)
	case !errors.Is(err, store.ErrNotFound):
		return nil, s.toConnectError(ctx, err, "failed to get activity streams")
	}
	if t == nil && activity.SummaryPolyline != "" {
		t = track.FromPolyline(activity.SummaryPolyline)
	}
	if t == nil {
		return nil, nil
	}
	encoded, err := proto.Marshal(t)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to encode track")
	}
	return encoded, nil
}

// ensureTrack copies the track of a report's activity into a report made
// before reports had tracks.
func (s *Service) ensureTrack(ctx context.Context, report *races.RaceReport) error {
	if report.Track != nil || !report.ActivityID.Valid {
		return nil
	}
	activity, err := s.activities.GetActivity(ctx, report.UserID, report.ActivityID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return s.toConnectError(ctx, err, "failed to get activity")
	}
	t, err := s.activityTrack(ctx, report.UserID, activity)
	if err != nil || t == nil {
		return err
	}
	if err := s.repo.SetRaceReportTrack(ctx, report.UserID, report.ID, t); err != nil {
		return s.toConnectError(ctx, err, "failed to set race report track")
	}
	report.Track = t
	return nil
}

// withSegments sets the segments of a report on its proto.
func (s *Service) withSegments(ctx context.Context, report *racesv1.RaceReport, reportID pgtype.UUID) error {
	segments, err := s.repo.ListRaceReportSegments(ctx, reportID)
	if err != nil {
		return s.toConnectError(ctx, err, "failed to list race report segments")
	}
	report.SetSegments(segmentsToProto(segments))
	return nil
}

// segmentsFromProto validates segments along a report's track, which may be
// nil if the report has none.
func segmentsFromProto(in []*racesv1.RaceReportSegment, t *track.Track) ([]races.RaceReportSegment, error) {
	if len(in) > maxSegments {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "a report can have at most %d segments", maxSegments)
	}
	if len(in) > 0 && t == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the report has no track for segments; set its activity first")
	}
	segments := make([]races.RaceReportSegment, len(in))
	bodyLength := 0
	for i, seg := range in {
		title := strings.TrimSpace(seg.GetTitle())
		if utf8.RuneCountInString(title) > maxTitleLength {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "segment %d: the title must be at most %d characters", i+1, maxTitleLength)
		}
		bodyLength += utf8.RuneCountInString(seg.GetBody())
		start, end := seg.GetStartDistanceMeters(), seg.GetEndDistanceMeters()
		length := t.Dists[len(t.Dists)-1]
		switch {
		case math.IsNaN(start) || math.IsNaN(end) || start < 0 || end <= start:
			return nil, connect.Errorf(connect.CodeInvalidArgument, "segment %d: end_distance_meters must be after start_distance_meters, which must not be negative", i+1)
		case end > length+trackEndToleranceMeters:
			return nil, connect.Errorf(connect.CodeInvalidArgument, "segment %d: end_distance_meters must be within the track, which is %.0f meters long", i+1, length)
		case i > 0 && start < segments[i-1].StartDistanceMeters:
			return nil, connect.Errorf(connect.CodeInvalidArgument, "segment %d: segments must be in order along the track, by start_distance_meters", i+1)
		}
		segments[i] = races.RaceReportSegment{
			Title:               title,
			Body:                seg.GetBody(),
			StartDistanceMeters: start,
			EndDistanceMeters:   min(end, length),
		}
	}
	if bodyLength > maxSegmentsBodyLength {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "the segments' text must be at most %d characters in all", maxSegmentsBodyLength)
	}
	return segments, nil
}

func segmentsToProto(segments []races.RaceReportSegment) []*racesv1.RaceReportSegment {
	out := make([]*racesv1.RaceReportSegment, len(segments))
	for i, seg := range segments {
		out[i] = racesv1.RaceReportSegment_builder{
			Title:               seg.Title,
			Body:                seg.Body,
			StartDistanceMeters: seg.StartDistanceMeters,
			EndDistanceMeters:   seg.EndDistanceMeters,
		}.Build()
	}
	return out
}

// activitySyncBudget is how long a request waits for an activity's details
// and streams to sync.
const activitySyncBudget = 15 * time.Second

// syncPending syncs the details and streams of an activity waiting for them,
// as activities older than syncs fetch them for are, so a report gets its
// whole track. If that fails or takes too long, the report gets the
// activity's summary polyline instead, and the activity syncs later.
func (s *Service) syncPending(ctx context.Context, userID, activityID pgtype.UUID) {
	if s.syncer == nil {
		return
	}
	syncCtx, cancel := context.WithTimeout(ctx, activitySyncBudget)
	defer cancel()
	if _, _, err := s.syncer.SyncPending(syncCtx, userID, activityID); err != nil && syncCtx.Err() == nil {
		s.report.Report(ctx, "Failed to sync an activity for a race report, using its summary polyline", err)
	}
}
