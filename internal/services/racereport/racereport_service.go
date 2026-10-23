// Package racereport implements the RaceReportService.
package racereport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/blob"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/races/v1/racesv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/photos"
	"github.com/johanbrandhorst/trail-tools/internal/races"
	"github.com/johanbrandhorst/trail-tools/internal/services/raceplan"
	"github.com/johanbrandhorst/trail-tools/internal/services/trip"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/track"
	"github.com/johanbrandhorst/trail-tools/internal/trips"
	"github.com/johanbrandhorst/trail-tools/internal/users"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ racesv1connect.RaceReportServiceHandler = (*Service)(nil)

// RacePath is the path segment of race reports on public profiles:
// /u/<handle>/races/<slug>.
const RacePath = "races"

const (
	// maxTitleLength is the longest title in characters.
	maxTitleLength = 200
	// maxSectionLength is the longest section of a report in characters.
	maxSectionLength = 50_000
	// maxPlacementLength is the longest placement in characters.
	maxPlacementLength = 200
	// maxStopCheckpointLength is the longest name of the checkpoint where a
	// runner who did not finish stopped, in characters.
	maxStopCheckpointLength = 200
	// maxSlugAttempts is how many slugs are tried for a new report.
	maxSlugAttempts = 20
	// maxActivitiesPerDay is how many activities on the race date are
	// considered to find the race.
	maxActivitiesPerDay = 50
)

// ReportRepository stores race reports and races.
type ReportRepository interface {
	GetRace(ctx context.Context, userID, raceID pgtype.UUID) (*races.Race, error)
	CreateRaceReport(ctx context.Context, report *races.RaceReport) (*races.RaceReport, error)
	GetRaceReport(ctx context.Context, userID, reportID pgtype.UUID) (*races.RaceReport, error)
	GetRaceReportBySlug(ctx context.Context, userID pgtype.UUID, slug string) (*races.RaceReport, error)
	ListRaceReports(ctx context.Context, userID pgtype.UUID, filter races.RaceReportFilter) ([]*races.RaceReport, error)
	UpdateRaceReport(ctx context.Context, update *races.RaceReportUpdate) (*races.RaceReport, error)
	SetRaceReportPublished(ctx context.Context, userID, reportID pgtype.UUID, published bool) (*races.RaceReport, error)
	DeleteRaceReport(ctx context.Context, userID, reportID pgtype.UUID) ([]string, error)
	SetRaceReportTrack(ctx context.Context, userID, reportID pgtype.UUID, track []byte) error
	ListRaceReportSegments(ctx context.Context, reportID pgtype.UUID) ([]races.RaceReportSegment, error)
	SetRaceReportSegments(ctx context.Context, reportID pgtype.UUID, segments []races.RaceReportSegment) error
	AddRaceReportPhoto(ctx context.Context, userID pgtype.UUID, p *races.RaceReportPhoto, before races.RaceReportPhotoOrder) (*races.RaceReportPhoto, error)
	ListRaceReportPhotos(ctx context.Context, reportID pgtype.UUID) ([]*races.RaceReportPhoto, error)
	GetRaceReportPhoto(ctx context.Context, userID, photoID pgtype.UUID) (*races.RaceReportPhoto, error)
	GetRaceReportPhotoAccess(ctx context.Context, photoID pgtype.UUID) (*races.RaceReportPhotoAccess, error)
	UpdateRaceReportPhotoCaption(ctx context.Context, userID, photoID pgtype.UUID, caption string) (*races.RaceReportPhoto, error)
	UpdateRaceReportPhotoLocation(ctx context.Context, userID, photoID pgtype.UUID, loc *trips.Location) (*races.RaceReportPhoto, error)
	DeleteRaceReportPhoto(ctx context.Context, userID, photoID pgtype.UUID) (string, error)
	SetRaceReportPhotoLocations(ctx context.Context, reportID pgtype.UUID, photos []*races.RaceReportPhoto) error
}

// ActivityRepository gets activities.
type ActivityRepository interface {
	GetActivity(ctx context.Context, userID, activityID pgtype.UUID) (*activities.Activity, error)
	GetActivityStreams(ctx context.Context, userID, activityID pgtype.UUID) (*activitiesv1.ActivityStreams, error)
	ListActivities(ctx context.Context, userID pgtype.UUID, filter activities.ListActivitiesFilter) ([]*activities.Activity, error)
}

// ProfileRepository gets the public profiles reports are published on.
type ProfileRepository interface {
	GetProfile(ctx context.Context, userID pgtype.UUID) (*trips.Profile, error)
	GetProfileByHandle(ctx context.Context, handle string) (*trips.Profile, error)
}

// Plans returns what a race's plan planned, e.g. a raceplan.Service.
type Plans interface {
	PlanOutcome(ctx context.Context, userID, raceID pgtype.UUID) (*raceplan.PlanOutcome, error)
}

// PrivacyZoneRepository lists the privacy zones tracks are hidden in on
// public pages.
type PrivacyZoneRepository interface {
	ListPrivacyZones(ctx context.Context, userID pgtype.UUID) ([]users.PrivacyZone, error)
}

// ActivitySyncer syncs the details and streams of an activity from Strava.
type ActivitySyncer interface {
	SyncPending(ctx context.Context, userID, activityID pgtype.UUID) (synced bool, resumeTime time.Time, err error)
}

// Service implements API handlers for the race report service.
type Service struct {
	log        *slog.Logger
	report     *errreport.Reporter
	repo       ReportRepository
	activities ActivityRepository
	// Syncs activities from Strava, if Strava is configured. May be nil.
	syncer   ActivitySyncer
	profiles ProfileRepository
	plans    Plans
	zones    PrivacyZoneRepository
	// Stores the images of photos.
	photoStore *photos.Store
	// Stores the links agents upload photos with.
	links   trip.PhotoUploadLinkRepository
	baseURL string
	nowFn   func() time.Time
}

// NewService creates a new Service. syncer may be nil, if Strava isn't
// configured. Photos are stored in blobs, and the links agents upload them
// with in links. baseURL is the public URL of the server, used for the URLs
// of reports and photos.
func NewService(log *slog.Logger, report *errreport.Reporter, repo ReportRepository, activities ActivityRepository, syncer ActivitySyncer, profiles ProfileRepository, plans Plans, zones PrivacyZoneRepository, blobs blob.Store, links trip.PhotoUploadLinkRepository, baseURL string) *Service {
	return &Service{
		log:        log,
		report:     report,
		repo:       repo,
		activities: activities,
		syncer:     syncer,
		profiles:   profiles,
		plans:      plans,
		zones:      zones,
		photoStore: photos.NewStore(blobs, "race-report-photos"),
		links:      links,
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		nowFn:      time.Now,
	}
}

func (s *Service) CreateRaceReport(ctx context.Context, req *racesv1.CreateRaceReportRequest) (*racesv1.CreateRaceReportResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	raceID, err := store.StringToUUID(req.GetRaceId())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid race ID: %v", err).WithCause(err)
	}
	race, err := s.repo.GetRace(ctx, userID, raceID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get race")
	}
	report := &races.RaceReport{
		UserID:              userID,
		RaceID:              race.ID,
		Title:               race.Name,
		RaceName:            race.Name,
		RaceDate:            race.Date,
		DistanceMeters:      race.DistanceMeters,
		ElevationGainMeters: race.ElevationGainMeters,
	}
	activity, err := s.raceActivity(ctx, userID, race, req.GetActivityId())
	if err != nil {
		return nil, err
	}
	if activity != nil {
		report.ActivityID = activity.ID
		report.FinishSeconds = activity.ElapsedTimeSeconds
		if report.Track, err = s.activityTrack(ctx, userID, activity); err != nil {
			return nil, err
		}
	}
	base := trip.Slugify(report.Title)
	if base == "" {
		base = "race"
	}
	for i := 1; i <= maxSlugAttempts; i++ {
		report.Slug = base
		if i > 1 {
			report.Slug = fmt.Sprintf("%s-%d", base, i)
		}
		created, err := s.repo.CreateRaceReport(ctx, report)
		if errors.Is(err, races.ErrSlugTaken) {
			continue
		}
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to create race report")
		}
		reportProto, comparison, err := s.withComparison(ctx, created)
		if err != nil {
			return nil, err
		}
		return racesv1.CreateRaceReportResponse_builder{Report: reportProto, Comparison: comparison}.Build(), nil
	}
	return nil, connect.NewError(connect.CodeAlreadyExists, "too many race reports have this title")
}

// raceActivity returns the activity with the ID, or if it's empty, the
// user's longest activity on the race date, or nil if there is none.
func (s *Service) raceActivity(ctx context.Context, userID pgtype.UUID, race *races.Race, id string) (*activities.Activity, error) {
	if id != "" {
		activityID, err := store.StringToUUID(id)
		if err != nil {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid activity ID: %v", err).WithCause(err)
		}
		activity, err := s.activities.GetActivity(ctx, userID, activityID)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to get activity")
		}
		return activity, nil
	}
	onDate, err := s.activities.ListActivities(ctx, userID, activities.ListActivitiesFilter{
		StartDate: race.Date,
		EndDate:   race.Date,
		Limit:     maxActivitiesPerDay,
	})
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list activities")
	}
	var longest *activities.Activity
	for _, a := range onDate {
		if longest == nil || a.DistanceMeters > longest.DistanceMeters {
			longest = a
		}
	}
	return longest, nil
}

func (s *Service) GetRaceReport(ctx context.Context, req *racesv1.GetRaceReportRequest) (*racesv1.GetRaceReportResponse, error) {
	report, err := s.getReport(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.ensureTrack(ctx, report); err != nil {
		return nil, err
	}
	reportProto, comparison, err := s.withComparison(ctx, report)
	if err != nil {
		return nil, err
	}
	if err := s.withSegments(ctx, reportProto, report.ID); err != nil {
		return nil, err
	}
	if err := s.withPhotos(ctx, reportProto, report.ID, nil); err != nil {
		return nil, err
	}
	resp := racesv1.GetRaceReportResponse_builder{Report: reportProto, Comparison: comparison}.Build()
	if req.GetIncludeTrack() {
		t, err := track.Decode(report.Track)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to decode race report track")
		}
		resp.SetTrack(t.RoutePoints())
	}
	return resp, nil
}

func (s *Service) ListRaceReports(ctx context.Context, req *racesv1.ListRaceReportsRequest) (*racesv1.ListRaceReportsResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	filter := races.RaceReportFilter{}
	if req.GetRaceId() != "" {
		raceID, err := store.StringToUUID(req.GetRaceId())
		if err != nil {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid race ID: %v", err).WithCause(err)
		}
		filter.RaceID = raceID
	}
	list, err := s.repo.ListRaceReports(ctx, userID, filter)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list race reports")
	}
	profile, err := s.profile(ctx, userID)
	if err != nil {
		return nil, err
	}
	reports := make([]*racesv1.RaceReport, len(list))
	for i, r := range list {
		reports[i] = s.reportToProto(r, profile)
	}
	return racesv1.ListRaceReportsResponse_builder{Reports: reports}.Build(), nil
}

func (s *Service) UpdateRaceReport(ctx context.Context, req *racesv1.UpdateRaceReportRequest) (*racesv1.UpdateRaceReportResponse, error) {
	current, err := s.getReport(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	update := &races.RaceReportUpdate{ID: current.ID, UserID: current.UserID}
	if req.HasTitle() {
		title := strings.TrimSpace(req.GetTitle())
		switch {
		case title == "":
			return nil, connect.NewError(connect.CodeInvalidArgument, "the title is required")
		case utf8.RuneCountInString(title) > maxTitleLength:
			return nil, connect.Errorf(connect.CodeInvalidArgument, "the title must be at most %d characters", maxTitleLength)
		}
		update.Title = &title
	}
	if req.HasSlug() {
		slug := req.GetSlug()
		if err := trip.ValidateSlug(slug); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
		if current.Published && current.Slug != slug {
			return nil, connect.NewError(connect.CodeFailedPrecondition, "the slug of a published report can't be changed, so that links to it keep working; unpublish the report first")
		}
		update.Slug = &slug
	}
	activityID := current.ActivityID
	// The start of the new activity, if it changes, which places photos on
	// its track.
	var activityStart time.Time
	if req.HasActivityId() {
		update.SetActivity = true
		activityID = pgtype.UUID{}
		if req.GetActivityId() != "" {
			id, err := store.StringToUUID(req.GetActivityId())
			if err != nil {
				return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid activity ID: %v", err).WithCause(err)
			}
			activity, err := s.activities.GetActivity(ctx, current.UserID, id)
			if err != nil {
				return nil, s.toConnectError(ctx, err, "failed to get activity")
			}
			activityID, activityStart = id, activity.StartTime
			// Segments keep their distances along the new activity's track.
			if update.Track, err = s.activityTrack(ctx, current.UserID, activity); err != nil {
				return nil, err
			}
		}
		update.ActivityID = activityID
	}
	if req.HasFinishSeconds() {
		finish := req.GetFinishSeconds()
		if finish < 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, "the finish time must not be negative")
		}
		update.FinishSeconds = &finish
	}
	if err := s.updateResult(ctx, current, activityID, req, update); err != nil {
		return nil, err
	}
	if req.HasPlacement() {
		placement := strings.TrimSpace(req.GetPlacement())
		if utf8.RuneCountInString(placement) > maxPlacementLength {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "the placement must be at most %d characters", maxPlacementLength)
		}
		update.Placement = &placement
	}
	for _, section := range []struct {
		name  string
		has   bool
		value string
		set   **string
	}{
		{"summary", req.HasSummary(), req.GetSummary(), &update.Summary},
		{"preparation", req.HasPreparation(), req.GetPreparation(), &update.Preparation},
		{"race day", req.HasRaceDay(), req.GetRaceDay(), &update.RaceDay},
		{"nutrition", req.HasNutrition(), req.GetNutrition(), &update.Nutrition},
		{"what went well", req.HasWentWell(), req.GetWentWell(), &update.WentWell},
		{"what went badly", req.HasWentBadly(), req.GetWentBadly(), &update.WentBadly},
		{"lessons", req.HasLessons(), req.GetLessons(), &update.Lessons},
	} {
		if !section.has {
			continue
		}
		if utf8.RuneCountInString(section.value) > maxSectionLength {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "the %s section must be at most %d characters", section.name, maxSectionLength)
		}
		*section.set = &section.value
	}
	updated, err := s.repo.UpdateRaceReport(ctx, update)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to update race report")
	}
	if update.SetActivity {
		if err := s.replacePhotos(ctx, updated, activityStart); err != nil {
			return nil, err
		}
	}
	reportProto, comparison, err := s.withComparison(ctx, updated)
	if err != nil {
		return nil, err
	}
	if err := s.withSegments(ctx, reportProto, updated.ID); err != nil {
		return nil, err
	}
	if err := s.withPhotos(ctx, reportProto, updated.ID, nil); err != nil {
		return nil, err
	}
	return racesv1.UpdateRaceReportResponse_builder{Report: reportProto, Comparison: comparison}.Build(), nil
}

// updateResult adds the changes of the request to whether the runner
// finished, and where and when they stopped if not, to update. Marking a
// report as a DNF moves its finish time to the stop time, and defaults the
// stop distance to that of the activity, unless they're set; marking it as
// finished clears the stop.
func (s *Service) updateResult(ctx context.Context, current *races.RaceReport, activityID pgtype.UUID, req *racesv1.UpdateRaceReportRequest, update *races.RaceReportUpdate) error {
	dnf := current.DidNotFinish
	if req.HasDidNotFinish() {
		dnf = req.GetDidNotFinish()
		update.DidNotFinish = &dnf
	}
	setsStop := req.HasStopDistanceMeters() || req.HasStopCheckpoint() || req.HasStopSeconds()
	if !dnf {
		if setsStop {
			return connect.NewError(connect.CodeInvalidArgument, "where and when the runner stopped can only be set if they did not finish; set did_not_finish too")
		}
		if current.DidNotFinish {
			update.StopDistanceMeters = new(0.0)
			update.StopCheckpoint = new("")
			update.StopSeconds = new(int32(0))
		}
		return nil
	}

	if update.FinishSeconds != nil && *update.FinishSeconds != 0 {
		return connect.NewError(connect.CodeInvalidArgument, "a race that wasn't finished has no finish time; set the stop time instead")
	}
	if req.HasStopDistanceMeters() {
		meters := req.GetStopDistanceMeters()
		switch {
		case math.IsNaN(meters) || meters < 0:
			return connect.NewError(connect.CodeInvalidArgument, "the stop distance must not be negative")
		case current.DistanceMeters > 0 && meters > current.DistanceMeters:
			return connect.Errorf(connect.CodeInvalidArgument, "the stop distance must not be beyond the race distance of %.0f meters", current.DistanceMeters)
		}
		update.StopDistanceMeters = &meters
	}
	if req.HasStopCheckpoint() {
		checkpoint := strings.TrimSpace(req.GetStopCheckpoint())
		if utf8.RuneCountInString(checkpoint) > maxStopCheckpointLength {
			return connect.Errorf(connect.CodeInvalidArgument, "the stop checkpoint must be at most %d characters", maxStopCheckpointLength)
		}
		update.StopCheckpoint = &checkpoint
	}
	if req.HasStopSeconds() {
		seconds := req.GetStopSeconds()
		if seconds < 0 {
			return connect.NewError(connect.CodeInvalidArgument, "the stop time must not be negative")
		}
		update.StopSeconds = &seconds
	}
	if current.DidNotFinish {
		return nil
	}

	// The report becomes a DNF: its finish time, e.g. the elapsed time of
	// the activity, is when the runner stopped, and the activity's distance
	// where.
	if update.StopSeconds == nil && update.FinishSeconds == nil && current.FinishSeconds > 0 {
		update.StopSeconds = &current.FinishSeconds
	}
	update.FinishSeconds = new(int32(0))
	if update.StopDistanceMeters == nil && activityID.Valid {
		activity, err := s.activities.GetActivity(ctx, current.UserID, activityID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return s.toConnectError(ctx, err, "failed to get activity")
		}
		if activity != nil && activity.DistanceMeters > 0 {
			meters := activity.DistanceMeters
			if current.DistanceMeters > 0 {
				meters = min(meters, current.DistanceMeters)
			}
			update.StopDistanceMeters = &meters
		}
	}
	return nil
}

func (s *Service) DeleteRaceReport(ctx context.Context, req *racesv1.DeleteRaceReportRequest) (*racesv1.DeleteRaceReportResponse, error) {
	report, err := s.getReport(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	keys, err := s.repo.DeleteRaceReport(ctx, report.UserID, report.ID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete race report")
	}
	for _, key := range keys {
		s.deleteImages(ctx, key)
	}
	return racesv1.DeleteRaceReportResponse_builder{}.Build(), nil
}

func (s *Service) PublishRaceReport(ctx context.Context, req *racesv1.PublishRaceReportRequest) (*racesv1.PublishRaceReportResponse, error) {
	report, err := s.getReport(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	profile, err := s.profile(ctx, report.UserID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "set up a public profile with a handle in Settings before publishing")
	}
	published, err := s.repo.SetRaceReportPublished(ctx, report.UserID, report.ID, true)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to publish race report")
	}
	return racesv1.PublishRaceReportResponse_builder{Report: s.reportToProto(published, profile)}.Build(), nil
}

func (s *Service) UnpublishRaceReport(ctx context.Context, req *racesv1.UnpublishRaceReportRequest) (*racesv1.UnpublishRaceReportResponse, error) {
	report, err := s.getReport(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	unpublished, err := s.repo.SetRaceReportPublished(ctx, report.UserID, report.ID, false)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to unpublish race report")
	}
	profile, err := s.profile(ctx, report.UserID)
	if err != nil {
		return nil, err
	}
	return racesv1.UnpublishRaceReportResponse_builder{Report: s.reportToProto(unpublished, profile)}.Build(), nil
}

func (s *Service) ListPublicRaceReports(ctx context.Context, req *racesv1.ListPublicRaceReportsRequest) (*racesv1.ListPublicRaceReportsResponse, error) {
	// Published reports don't require authentication.
	profile, err := s.profiles.GetProfileByHandle(ctx, req.GetHandle())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, "this profile doesn't exist")
		}
		return nil, s.toConnectError(ctx, err, "failed to get profile")
	}
	list, err := s.repo.ListRaceReports(ctx, profile.UserID, races.RaceReportFilter{PublishedOnly: true})
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list race reports")
	}
	reports := make([]*racesv1.RaceReport, len(list))
	for i, r := range list {
		reports[i] = s.reportToProto(r, profile)
	}
	return racesv1.ListPublicRaceReportsResponse_builder{Reports: reports}.Build(), nil
}

func (s *Service) GetPublicRaceReport(ctx context.Context, req *racesv1.GetPublicRaceReportRequest) (*racesv1.GetPublicRaceReportResponse, error) {
	// Published reports don't require authentication.
	profile, report, err := s.PublicReport(ctx, req.GetHandle(), req.GetSlug())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, "this race report doesn't exist or isn't published")
		}
		return nil, s.toConnectError(ctx, err, "failed to get race report")
	}
	t, err := track.Decode(report.Track)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to decode race report track")
	}
	comparison, err := s.comparison(ctx, report, t)
	if err != nil {
		return nil, err
	}
	reportProto := s.reportToProto(report, profile)
	if err := s.withSegments(ctx, reportProto, report.ID); err != nil {
		return nil, err
	}
	// The track is hidden in the author's privacy zones, and so are the
	// locations of the photos taken there.
	zoneList, err := s.zones.ListPrivacyZones(ctx, report.UserID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list privacy zones")
	}
	zones := track.HiddenZones(zoneList)
	if err := s.withPhotos(ctx, reportProto, report.ID, zones); err != nil {
		return nil, err
	}
	resp := racesv1.GetPublicRaceReportResponse_builder{
		Report:     reportProto,
		Comparison: comparison,
		AuthorName: profile.DisplayName,
	}.Build()
	if t != nil {
		points, breaks := t.PublicPoints(zones)
		resp.SetTrack(points)
		resp.SetTrackBreaks(breaks)
	}
	return resp, nil
}

// PublicReport gets a race report by its author's handle and slug, if it is
// published or the caller is its author. Otherwise it returns
// store.ErrNotFound, so drafts can't be told apart from missing reports.
func (s *Service) PublicReport(ctx context.Context, handle, slug string) (*trips.Profile, *races.RaceReport, error) {
	profile, err := s.profiles.GetProfileByHandle(ctx, handle)
	if err != nil {
		return nil, nil, err
	}
	report, err := s.repo.GetRaceReportBySlug(ctx, profile.UserID, slug)
	if err != nil {
		return nil, nil, err
	}
	if !report.Published {
		if userID, ok := authn.GetUserID(ctx); !ok || userID != report.UserID {
			return nil, nil, store.ErrNotFound
		}
	}
	return profile, report, nil
}

// withComparison returns a report and its comparison with the race plan.
func (s *Service) withComparison(ctx context.Context, report *races.RaceReport) (*racesv1.RaceReport, *racesv1.RaceComparison, error) {
	profile, err := s.profile(ctx, report.UserID)
	if err != nil {
		return nil, nil, err
	}
	t, err := track.Decode(report.Track)
	if err != nil {
		return nil, nil, s.toConnectError(ctx, err, "failed to decode race report track")
	}
	comparison, err := s.comparison(ctx, report, t)
	if err != nil {
		return nil, nil, err
	}
	return s.reportToProto(report, profile), comparison, nil
}

// comparison compares a report with its race plan. The actual times come
// from the report's track t, which may be nil, if it has times. That's the
// activity's streams downsampled, which is close enough for times at
// checkpoints, and saves reading and decoding the whole streams on every
// view of a public report. Only tracks without times, e.g. from activities
// whose streams hadn't synced, fall back to the streams.
func (s *Service) comparison(ctx context.Context, report *races.RaceReport, t *track.Track) (*racesv1.RaceComparison, error) {
	var outcome *raceplan.PlanOutcome
	if report.RaceID.Valid {
		var err error
		if outcome, err = s.plans.PlanOutcome(ctx, report.UserID, report.RaceID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, s.toConnectError(ctx, err, "failed to get the race plan")
		}
	}
	var rec *recording
	switch {
	case outcome == nil:
		// Without a plan there's nothing to compare the times with.
	case t != nil && t.Times != nil:
		rec = trackRecording(t)
	case report.ActivityID.Valid:
		streams, err := s.activities.GetActivityStreams(ctx, report.UserID, report.ActivityID)
		switch {
		case err == nil:
			rec = newRecording(streams)
		case !errors.Is(err, store.ErrNotFound):
			return nil, s.toConnectError(ctx, err, "failed to get activity streams")
		}
	}
	return compare(outcome, report, rec), nil
}

// getReport gets one of the authenticated user's race reports by ID.
func (s *Service) getReport(ctx context.Context, id string) (*races.RaceReport, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	reportID, err := store.StringToUUID(id)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid race report ID: %v", err).WithCause(err)
	}
	report, err := s.repo.GetRaceReport(ctx, userID, reportID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get race report")
	}
	return report, nil
}

// profile returns the user's public profile, or nil if the user has none.
func (s *Service) profile(ctx context.Context, userID pgtype.UUID) (*trips.Profile, error) {
	profile, err := s.profiles.GetProfile(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, s.toConnectError(ctx, err, "failed to get profile")
	}
	return profile, nil
}

// ReportURL returns the public URL of a race report.
func (s *Service) ReportURL(handle, slug string) string {
	return s.baseURL + trip.ProfilePath + handle + "/" + RacePath + "/" + slug
}

func (s *Service) reportToProto(r *races.RaceReport, profile *trips.Profile) *racesv1.RaceReport {
	b := racesv1.RaceReport_builder{
		Id:                  store.UUIDToString(r.ID),
		RaceId:              store.UUIDToString(r.RaceID),
		ActivityId:          store.UUIDToString(r.ActivityID),
		Title:               r.Title,
		Slug:                r.Slug,
		RaceName:            r.RaceName,
		RaceDate:            store.FormatDate(r.RaceDate),
		DistanceMeters:      r.DistanceMeters,
		ElevationGainMeters: r.ElevationGainMeters,
		FinishSeconds:       r.FinishSeconds,
		Placement:           r.Placement,
		DidNotFinish:        r.DidNotFinish,
		StopDistanceMeters:  r.Stop.DistanceMeters,
		StopCheckpoint:      r.Stop.Checkpoint,
		StopSeconds:         r.Stop.Seconds,
		Summary:             r.Sections.Summary,
		Preparation:         r.Sections.Preparation,
		RaceDay:             r.Sections.RaceDay,
		Nutrition:           r.Sections.Nutrition,
		WentWell:            r.Sections.WentWell,
		WentBadly:           r.Sections.WentBadly,
		Lessons:             r.Sections.Lessons,
		HasTrack:            r.Track != nil,
		Published:           r.Published,
		CreateTime:          timestamppb.New(r.CreateTime),
		UpdateTime:          timestamppb.New(r.UpdateTime),
	}
	if !r.PublishTime.IsZero() {
		b.PublishTime = timestamppb.New(r.PublishTime)
	}
	if profile != nil {
		b.Url = s.ReportURL(profile.Handle, r.Slug)
	}
	return b.Build()
}

func (s *Service) toConnectError(ctx context.Context, err error, msg string) error {
	var connectErr *connect.Error
	switch {
	case errors.As(err, &connectErr):
		return err
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, "not found")
	case errors.Is(err, races.ErrSlugTaken):
		return connect.NewError(connect.CodeAlreadyExists, "another of your race reports has this slug; choose another one")
	default:
		s.report.Report(ctx, msg, err)
		return connect.NewError(connect.CodeInternal, msg).WithCause(err)
	}
}
