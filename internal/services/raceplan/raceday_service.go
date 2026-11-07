package raceplan

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/livetrack"
	"github.com/johanbrandhorst/trail-tools/internal/protomask"
	"github.com/johanbrandhorst/trail-tools/internal/races"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// LiveTracker reads Garmin LiveTrack sessions.
type LiveTracker interface {
	Track(ctx context.Context, s livetrack.Session) (livetrack.Track, error)
}

// liveTrackTimeout limits how long a status request waits for LiveTrack.
const liveTrackTimeout = 10 * time.Second

func (s *Service) GetRaceDayStatus(ctx context.Context, req *racesv1.GetRaceDayStatusRequest) (*racesv1.GetRaceDayStatusResponse, error) {
	stored, r, err := s.raceDayPlan(ctx, req.GetPlanId(), req.GetShareToken())
	if err != nil {
		return nil, err
	}
	status, err := s.raceDayStatus(ctx, stored, r)
	if err != nil {
		return nil, err
	}
	return racesv1.GetRaceDayStatusResponse_builder{Status: status}.Build(), nil
}

func (s *Service) UpdateRaceDay(ctx context.Context, req *racesv1.UpdateRaceDayRequest) (*racesv1.UpdateRaceDayResponse, error) {
	stored, r, err := s.raceDayPlan(ctx, req.GetPlanId(), req.GetShareToken())
	if err != nil {
		return nil, err
	}
	fields, err := protomask.Fields((&racesv1.RaceDay{}).ProtoReflect().Descriptor(), req.GetUpdateMask())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	var update races.RaceDayFields
	for _, fd := range fields {
		switch fd.Name() {
		case "livetrack_url":
			update.LivetrackURL = true
		case "start_time":
			update.StartTime = true
		case "splits":
			update.Splits = true
		default:
			return nil, connect.Errorf(connect.CodeInternal, "unhandled race day field %q", fd.Name())
		}
	}
	// Only the updated fields are validated and written.
	raceDay := &racesv1.RaceDay{}
	if err := protomask.Apply(raceDay, req.GetRaceDay(), req.GetUpdateMask()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if err := normalizeRaceDay(raceDay, len(stored.Checkpoints)); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	updated, err := s.repo.UpdateRaceDay(ctx, stored.ID, raceDayFromProto(raceDay), update)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to update race day")
	}
	status, err := s.raceDayStatus(ctx, updated, r)
	if err != nil {
		return nil, err
	}
	return racesv1.UpdateRaceDayResponse_builder{Status: status}.Build(), nil
}

func (s *Service) SetCheckpointSplit(ctx context.Context, req *racesv1.SetCheckpointSplitRequest) (*racesv1.SetCheckpointSplitResponse, error) {
	stored, r, err := s.raceDayPlan(ctx, req.GetPlanId(), req.GetShareToken())
	if err != nil {
		return nil, err
	}
	split := req.GetSplit()
	if split == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "split is required")
	}
	if err := validateSplit(split, len(stored.Checkpoints)); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	// Only this checkpoint's split is written, so crew recording at different
	// checkpoints don't overwrite each other.
	updated, err := s.repo.SetCheckpointSplit(ctx, stored.ID, splitFromProto(split))
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to set checkpoint split")
	}
	status, err := s.raceDayStatus(ctx, updated, r)
	if err != nil {
		return nil, err
	}
	return racesv1.SetCheckpointSplitResponse_builder{Status: status}.Build(), nil
}

// raceDayPlan gets a plan by ID for its owner, or by share token for anyone.
func (s *Service) raceDayPlan(ctx context.Context, planID, shareToken string) (*races.RacePlan, *races.Race, error) {
	switch {
	case planID != "" && shareToken != "":
		return nil, nil, connect.NewError(connect.CodeInvalidArgument, "only one of plan_id and share_token can be set")
	case planID != "":
		return s.getPlan(ctx, planID)
	case shareToken != "":
		stored, r, err := s.repo.GetSharedRacePlan(ctx, shareToken)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, nil, connect.NewError(connect.CodeNotFound, "this race plan doesn't exist or is no longer shared")
			}
			return nil, nil, s.toConnectError(ctx, err, "failed to get shared race plan")
		}
		return stored, r, nil
	default:
		if _, ok := authn.GetUserID(ctx); !ok {
			return nil, nil, connect.NewError(connect.CodeUnauthenticated, "")
		}
		return nil, nil, connect.NewError(connect.CodeInvalidArgument, "plan_id or share_token is required")
	}
}

// raceDayStatus computes the race day status of a plan, fetching the
// runner's position from LiveTrack if linked.
func (s *Service) raceDayStatus(ctx context.Context, stored *races.RacePlan, r *races.Race) (*racesv1.RaceDayStatus, error) {
	plan := planToProto(stored, "")
	raceDay := plan.GetRaceDay()

	course, err := s.course(ctx, r)
	if err != nil {
		return nil, err
	}
	var start time.Time
	startSource := racesv1.StartTimeSource_START_TIME_SOURCE_UNSPECIFIED
	if raceDay.HasStartTime() {
		start = raceDay.GetStartTime().AsTime()
		startSource = racesv1.StartTimeSource_START_TIME_SOURCE_RECORDED
	}
	var position *racesv1.LiveTrackPosition
	var fix *LiveFix
	var liveErr error
	if url := raceDay.GetLivetrackUrl(); url != "" && s.liveTracker != nil {
		position, fix, liveErr = s.livePosition(ctx, course, url)
		if liveErr == nil && start.IsZero() && position.HasTime() {
			start = position.GetTime().AsTime().Add(-time.Duration(position.GetElapsedSeconds()) * time.Second)
			startSource = racesv1.StartTimeSource_START_TIME_SOURCE_LIVETRACK
		}
	}
	status := EstimateRaceDay(plan, start, fix, s.nowFn())
	status.SetStartTimeSource(startSource)
	if position != nil {
		status.SetLivetrack(position)
	}
	if liveErr != nil {
		status.SetLivetrackError(liveErr.Error())
	}
	// Prefer the actual position if LiveTrack is the latest progress and
	// recent, otherwise use the estimated distance on the course.
	switch {
	case status.GetEstimateSource() == racesv1.EstimateSource_ESTIMATE_SOURCE_LIVETRACK && s.nowFn().Sub(position.GetTime().AsTime()) < livePositionMaxAge:
		status.SetEstimatedLocation(position.GetLocation())
	case status.HasStartTime():
		if p, ok := PointAtDistance(course, status.GetEstimatedDistanceMeters()); ok {
			status.SetEstimatedLocation(geov1.LatLng_builder{Lat: p.Lat, Lng: p.Lng}.Build())
		}
	}
	return status, nil
}

// livePositionMaxAge is how old a LiveTrack position can be to be used as
// the runner's location.
const livePositionMaxAge = 5 * time.Minute

// livePosition fetches the runner's latest position from LiveTrack and
// matches it to the course.
func (s *Service) livePosition(ctx context.Context, course []geo.Point, url string) (*racesv1.LiveTrackPosition, *LiveFix, error) {
	session, err := livetrack.ParseURL(url)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, liveTrackTimeout)
	defer cancel()
	track, err := s.liveTracker.Track(ctx, session)
	if err != nil {
		return nil, nil, err
	}
	if track.Latest == nil {
		return nil, nil, errors.New("the LiveTrack session has no positions yet")
	}
	p := track.Latest
	courseDistance := SnapToCourse(course, geo.Point{Lat: p.Lat, Lng: p.Lng}, p.DistanceMeters)
	position := racesv1.LiveTrackPosition_builder{
		Time:                 timestamppb.New(p.Time),
		Location:             geov1.LatLng_builder{Lat: p.Lat, Lng: p.Lng}.Build(),
		ElevationMeters:      p.ElevationMeters,
		DistanceMeters:       p.DistanceMeters,
		CourseDistanceMeters: courseDistance,
		ElapsedSeconds:       int32(p.ElapsedSeconds),
		SpeedMps:             p.SpeedMPS,
		Ended:                track.Ended,
	}.Build()
	return position, &LiveFix{Time: p.Time, CourseDistanceMeters: courseDistance}, nil
}

// normalizeRaceDay validates race day data and merges splits for the same
// checkpoint, sorting them by checkpoint and dropping empty ones.
func normalizeRaceDay(raceDay *racesv1.RaceDay, checkpoints int) error {
	if url := raceDay.GetLivetrackUrl(); url != "" {
		if len(url) > livetrack.MaxURLLength {
			return fmt.Errorf("livetrack_url must be at most %d bytes; paste the session link from the LiveTrack email or app", livetrack.MaxURLLength)
		}
		if _, err := livetrack.ParseURL(url); err != nil {
			return err
		}
	}
	if raceDay.HasStartTime() {
		if err := validateTime(raceDay.GetStartTime(), "start_time"); err != nil {
			return err
		}
	}
	byIndex := map[int32]*racesv1.CheckpointSplit{}
	for _, split := range raceDay.GetSplits() {
		if err := validateSplit(split, checkpoints); err != nil {
			return err
		}
		if !split.HasArrivalTime() && !split.HasDepartureTime() {
			continue
		}
		byIndex[split.GetCheckpointIndex()] = split
	}
	var splits []*racesv1.CheckpointSplit
	for _, split := range byIndex {
		splits = append(splits, split)
	}
	slices.SortFunc(splits, func(a, b *racesv1.CheckpointSplit) int {
		return int(a.GetCheckpointIndex() - b.GetCheckpointIndex())
	})
	raceDay.SetSplits(splits)
	return nil
}

// validateSplit validates the recorded times at a checkpoint of a plan with
// the given number of checkpoints.
func validateSplit(split *racesv1.CheckpointSplit, checkpoints int) error {
	i := split.GetCheckpointIndex()
	if i < 0 || int(i) >= checkpoints {
		return fmt.Errorf("checkpoint_index %d is out of range, the plan has %d checkpoints", i, checkpoints)
	}
	if int(i) == checkpoints-1 && split.HasDepartureTime() {
		return fmt.Errorf("checkpoint %d is the finish, which has no departure_time", i+1)
	}
	if split.HasArrivalTime() {
		if err := validateTime(split.GetArrivalTime(), fmt.Sprintf("checkpoint %d: arrival_time", i+1)); err != nil {
			return err
		}
	}
	if split.HasDepartureTime() {
		if err := validateTime(split.GetDepartureTime(), fmt.Sprintf("checkpoint %d: departure_time", i+1)); err != nil {
			return err
		}
	}
	if split.HasArrivalTime() && split.HasDepartureTime() && split.GetDepartureTime().AsTime().Before(split.GetArrivalTime().AsTime()) {
		return fmt.Errorf("checkpoint %d: departure_time must not be before arrival_time", i+1)
	}
	return nil
}

// Recorded race day times must be within these years. Anyone with a share
// link can record times, and times beyond what the database can store would
// otherwise fail as internal errors.
var (
	minRaceDayTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	maxRaceDayTime = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

// validateTime checks that a recorded race day time is valid, naming it
// field in errors.
func validateTime(ts *timestamppb.Timestamp, field string) error {
	if err := ts.CheckValid(); err != nil {
		return fmt.Errorf("%s is not a valid time: %w", field, err)
	}
	if t := ts.AsTime(); t.Before(minRaceDayTime) || !t.Before(maxRaceDayTime) {
		return fmt.Errorf("%s must be between the years %d and %d", field, minRaceDayTime.Year(), maxRaceDayTime.Year()-1)
	}
	return nil
}
