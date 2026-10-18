// Package trip implements the TripReportService, which writes and publishes
// trip reports, and serves their photos and public pages.
package trip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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
	mcpv1 "github.com/johanbrandhorst/trail-tools/internal/gen/mcp/v1"
	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
	tripsv1 "github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1/tripsv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/photos"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/track"
	"github.com/johanbrandhorst/trail-tools/internal/trips"
	"github.com/johanbrandhorst/trail-tools/internal/users"
	"gitlab.com/greyxor/slogor"
	"google.golang.org/protobuf/proto"
)

var _ tripsv1connect.TripReportServiceHandler = (*Service)(nil)

// TripRepository stores trip reports, photos and public profiles.
type TripRepository interface {
	GetProfile(ctx context.Context, userID pgtype.UUID) (*trips.Profile, error)
	GetProfileByHandle(ctx context.Context, handle string) (*trips.Profile, error)
	UpsertProfile(ctx context.Context, p *trips.Profile) (*trips.Profile, error)
	GetUserName(ctx context.Context, userID pgtype.UUID) (string, error)
	CreateReport(ctx context.Context, report *trips.Report, body string) (*trips.Report, error)
	GetReport(ctx context.Context, userID, reportID pgtype.UUID) (*trips.Report, error)
	GetReportBySlug(ctx context.Context, userID pgtype.UUID, slug string) (*trips.Report, error)
	ListReports(ctx context.Context, userID pgtype.UUID, filter trips.ListReportsFilter) ([]*trips.Report, error)
	UpdateReport(ctx context.Context, u *trips.ReportUpdate) (*trips.Report, error)
	SetReportPublished(ctx context.Context, userID, reportID pgtype.UUID, published bool) (*trips.Report, error)
	DeleteReport(ctx context.Context, userID, reportID pgtype.UUID) ([]string, error)
	AddPhoto(ctx context.Context, userID pgtype.UUID, p *trips.Photo, before trips.PhotoOrder) (*trips.Photo, error)
	ListPhotos(ctx context.Context, reportIDs ...pgtype.UUID) (map[pgtype.UUID][]*trips.Photo, error)
	GetPhoto(ctx context.Context, userID, photoID pgtype.UUID) (*trips.Photo, error)
	GetPhotoAccess(ctx context.Context, photoID pgtype.UUID) (*trips.PhotoAccess, error)
	UpdatePhotoCaption(ctx context.Context, userID, photoID pgtype.UUID, caption string) (*trips.Photo, error)
	UpdatePhotoLocation(ctx context.Context, userID, photoID pgtype.UUID, loc *trips.Location) (*trips.Photo, error)
	SetSections(ctx context.Context, userID, reportID pgtype.UUID, sections []*trips.Section) ([]*trips.Section, error)
	ListSections(ctx context.Context, reportIDs ...pgtype.UUID) (map[pgtype.UUID][]*trips.Section, error)
	ListExcerpts(ctx context.Context, reportIDs ...pgtype.UUID) (map[pgtype.UUID]string, error)
	DeletePhoto(ctx context.Context, userID, photoID pgtype.UUID) (string, error)
	PhotoUploadLinkRepository
}

// ActivityRepository gets the activities reports are made from.
type ActivityRepository interface {
	GetActivity(ctx context.Context, userID, activityID pgtype.UUID) (*activities.Activity, error)
	GetActivityStreams(ctx context.Context, userID, activityID pgtype.UUID) (*activitiesv1.ActivityStreams, error)
	ListActivityPhotos(ctx context.Context, userID, activityID pgtype.UUID) ([]activities.Photo, error)
}

// ActivitySyncer syncs an activity from Strava.
// PrivacyZoneRepository lists the privacy zones tracks and photos are hidden
// in on public pages.
type PrivacyZoneRepository interface {
	ListPrivacyZones(ctx context.Context, userID pgtype.UUID) ([]users.PrivacyZone, error)
}

type ActivitySyncer interface {
	SyncActivity(ctx context.Context, userID pgtype.UUID, stravaID int64) error
	SyncPending(ctx context.Context, userID, activityID pgtype.UUID) (synced bool, resumeTime time.Time, err error)
}

// Service implements API handlers for the trip report service.
type Service struct {
	log        *slog.Logger
	report     *errreport.Reporter
	repo       TripRepository
	activities ActivityRepository
	syncer     ActivitySyncer
	photoStore *photos.Store
	zones      PrivacyZoneRepository
	baseURL    string
	nowFn      func() time.Time
	// Downloads photos from Strava.
	httpClient *http.Client
}

// NewService creates a new Service. syncer refreshes activities from Strava
// before importing their photos, and may be nil if Strava isn't configured.
// Photos are stored in blobs. baseURL is the public URL of the server, used
// for the URLs of reports and photos. Photos are downloaded from Strava with
// client.
// zones are the privacy zones tracks and photos are hidden in on public
// pages.
func NewService(log *slog.Logger, report *errreport.Reporter, repo TripRepository, activities ActivityRepository, syncer ActivitySyncer, blobs blob.Store, zones PrivacyZoneRepository, client *http.Client, baseURL string) *Service {
	return &Service{
		zones:      zones,
		log:        log,
		report:     report,
		repo:       repo,
		activities: activities,
		syncer:     syncer,
		photoStore: photos.NewStore(blobs, "trip-photos"),
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		nowFn:      time.Now,
		httpClient: client,
	}
}

const (
	// ProfilePath is the path of the public pages: /u/<handle> and
	// /u/<handle>/<slug>.
	ProfilePath = "/u/"
	// PhotoPath is the path photos are served at:
	// /photos/<id>/<size>.jpg.
	PhotoPath = "/photos/"
	// PhotoUploadPath is the path of the links agents upload photos to trip
	// reports with, followed by their secret token.
	PhotoUploadPath = "/trip-photo-uploads/"

	maxTitleLength = 200
	// maxBodyLength is the most characters of text of all of a report's
	// sections.
	maxBodyLength = 100_000
	// maxSections is the most sections a report can have.
	maxSections = 50
	// excerptLength is the length of the excerpts of reports in lists.
	excerptLength        = 220
	maxDisplayNameLength = 80
	maxPhotosPerReport   = 200
	// maxSlugAttempts is how many numbered slugs are tried when creating a
	// report whose slug is taken.
	maxSlugAttempts = 100
)

func (s *Service) CreateTripReport(ctx context.Context, req *tripsv1.CreateTripReportRequest) (*tripsv1.CreateTripReportResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	report := &trips.Report{
		UserID:   userID,
		Title:    strings.TrimSpace(req.GetTitle()),
		Date:     dateOf(s.nowFn().UTC()),
		Timezone: "UTC",
	}
	// The text of the report's first section.
	var body string
	if req.GetActivityId() != "" {
		activityID, err := store.StringToUUID(req.GetActivityId())
		if err != nil {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid activity ID: %v", err).WithCause(err)
		}
		if body, err = s.fillFromActivity(ctx, report, activityID); err != nil {
			return nil, err
		}
	}
	if report.Title == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "a title is required for a report without an activity")
	}
	if err := validateTitle(report.Title); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	base := Slugify(report.Title)
	if base == "" {
		base = "trip"
	}
	for i := 1; i <= maxSlugAttempts; i++ {
		report.Slug = base
		if i > 1 {
			suffix := fmt.Sprintf("-%d", i)
			report.Slug = strings.TrimRight(base[:min(len(base), maxSlugLength-len(suffix))], "-") + suffix
		}
		created, err := s.repo.CreateReport(ctx, report, body)
		if errors.Is(err, trips.ErrSlugTaken) {
			continue
		}
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to create trip report")
		}
		// A report includes the activity's photos on Strava from the start.
		// Failing to import them doesn't fail creating the report.
		if created.ActivityID.Valid {
			if _, err := s.importStravaPhotos(ctx, created); err != nil {
				s.log.WarnContext(ctx, "Failed to import the activity's photos from Strava", slogor.Err(err))
			}
		}
		result, err := s.reportWithStory(ctx, created)
		if err != nil {
			return nil, err
		}
		return tripsv1.CreateTripReportResponse_builder{Report: result}.Build(), nil
	}
	return nil, connect.NewError(connect.CodeAlreadyExists, "too many trip reports have this title; choose another title")
}

// activitySyncBudget is how long a request waits for an activity's details
// and streams to sync.
const activitySyncBudget = 15 * time.Second

// syncPending syncs the details and streams of an activity waiting for them,
// as activities older than syncs fetch them for are, so a report gets its
// description and whole track, and returns the activity again. If that
// fails or takes too long, it returns the activity as it was, and the report
// gets its summary polyline instead.
func (s *Service) syncPending(ctx context.Context, userID pgtype.UUID, a *activities.Activity) *activities.Activity {
	if s.syncer == nil {
		return a
	}
	syncCtx, cancel := context.WithTimeout(ctx, activitySyncBudget)
	defer cancel()
	synced, _, err := s.syncer.SyncPending(syncCtx, userID, a.ID)
	if err != nil && syncCtx.Err() == nil {
		s.report.Report(ctx, "Failed to sync an activity for a trip report, using its summary polyline", err)
	}
	if !synced {
		return a
	}
	updated, err := s.activities.GetActivity(ctx, userID, a.ID)
	if err != nil {
		s.report.Report(ctx, "Failed to get a synced activity, using it as it was", err)
		return a
	}
	return updated
}

// fillFromActivity copies the details of one of the user's activities into
// a new report, and returns its description.
func (s *Service) fillFromActivity(ctx context.Context, report *trips.Report, activityID pgtype.UUID) (string, error) {
	a, err := s.activities.GetActivity(ctx, report.UserID, activityID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", connect.NewError(connect.CodeNotFound, "activity not found")
		}
		return "", s.toConnectError(ctx, err, "failed to get activity")
	}
	if !a.StreamsSynced {
		a = s.syncPending(ctx, report.UserID, a)
	}
	report.ActivityID = a.ID
	if report.Title == "" {
		report.Title = strings.TrimSpace(a.Name)
	}
	report.Date = a.StartDateLocal
	if a.Timezone != "" {
		report.Timezone = a.Timezone
	}
	report.SportType = a.SportType
	report.StartTime = a.StartTime
	report.DistanceMeters = a.DistanceMeters
	report.MovingTimeSeconds = a.MovingTimeSeconds
	report.ElapsedTimeSeconds = a.ElapsedTimeSeconds
	report.ElevationGainMeters = a.ElevationGainMeters

	var t *activitiesv1.ActivityStreams
	if a.StreamsSynced {
		streams, err := s.activities.GetActivityStreams(ctx, report.UserID, activityID)
		switch {
		case err == nil:
			t = track.FromStreams(streams)
		case errors.Is(err, store.ErrNotFound):
			// The activity has no streams, e.g. a manual entry.
		default:
			return "", s.toConnectError(ctx, err, "failed to get streams")
		}
	}
	if t == nil {
		t = track.FromPolyline(a.SummaryPolyline)
	}
	if t != nil {
		report.Track, err = proto.Marshal(t)
		if err != nil {
			return "", s.toConnectError(ctx, err, "failed to encode track")
		}
	}
	return a.Description, nil
}

func (s *Service) GetTripReport(ctx context.Context, req *tripsv1.GetTripReportRequest) (*tripsv1.GetTripReportResponse, error) {
	userID, reportID, err := s.parseReportID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	report, err := s.repo.GetReport(ctx, userID, reportID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get trip report")
	}
	resp, err := s.reportWithStory(ctx, report)
	if err != nil {
		return nil, err
	}
	b := tripsv1.GetTripReportResponse_builder{Report: resp}
	if req.GetIncludeTrack() {
		t, err := track.Decode(report.Track)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to get track")
		}
		b.Track = t.RoutePoints()
	}
	return b.Build(), nil
}

func (s *Service) ListTripReports(ctx context.Context, req *tripsv1.ListTripReportsRequest) (*tripsv1.ListTripReportsResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	var filter trips.ListReportsFilter
	if req.GetActivityId() != "" {
		activityID, err := store.StringToUUID(req.GetActivityId())
		if err != nil {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid activity ID: %v", err).WithCause(err)
		}
		filter.ActivityID = activityID
	}
	reports, err := s.repo.ListReports(ctx, userID, filter)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list trip reports")
	}
	profile, err := s.profile(ctx, userID)
	if err != nil {
		return nil, err
	}
	result, err := s.reportsToProto(ctx, reports, profile, false)
	if err != nil {
		return nil, err
	}
	// Lists only show the cover photos. All of them would make the list too
	// large for agents to read: hundreds of kilobytes for a few dozen reports.
	for _, r := range result {
		r.SetPhotos(coverPhoto(r))
	}
	return tripsv1.ListTripReportsResponse_builder{Reports: result}.Build(), nil
}

// coverPhoto returns the report's cover photo in a list, or none if it has
// none.
func coverPhoto(r *tripsv1.TripReport) []*tripsv1.TripPhoto {
	for _, p := range r.GetPhotos() {
		if p.GetId() == r.GetCoverPhotoId() {
			return []*tripsv1.TripPhoto{p}
		}
	}
	return nil
}

func (s *Service) UpdateTripReport(ctx context.Context, req *tripsv1.UpdateTripReportRequest) (*tripsv1.UpdateTripReportResponse, error) {
	userID, reportID, err := s.parseReportID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	update := &trips.ReportUpdate{ID: reportID, UserID: userID}
	if req.HasTitle() {
		title := strings.TrimSpace(req.GetTitle())
		if title == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, "the title is required")
		}
		if err := validateTitle(title); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
		update.Title = &title
	}
	if req.HasSlug() {
		slug := req.GetSlug()
		if err := ValidateSlug(slug); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
		current, err := s.repo.GetReport(ctx, userID, reportID)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to get trip report")
		}
		if current.Published && current.Slug != slug {
			return nil, connect.NewError(connect.CodeFailedPrecondition, "the slug of a published report can't be changed, so that links to it keep working; unpublish the report first")
		}
		update.Slug = &slug
	}
	if req.HasDate() {
		date, err := store.ParseDate(req.GetDate())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
		update.Date = &date
	}
	if req.HasCoverPhotoId() {
		var cover pgtype.UUID
		if id := req.GetCoverPhotoId(); id != "" {
			cover, err = store.StringToUUID(id)
			if err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, "cover_photo_id is not a valid ID")
			}
		}
		update.CoverPhotoID = &cover
	}
	if req.HasTags() {
		update.Tags, err = tagNames(req.GetTags().GetTags())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
	}
	report, err := s.repo.UpdateReport(ctx, update)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to update trip report")
	}
	result, err := s.reportWithStory(ctx, report)
	if err != nil {
		return nil, err
	}
	return tripsv1.UpdateTripReportResponse_builder{Report: result}.Build(), nil
}

func (s *Service) DeleteTripReport(ctx context.Context, req *tripsv1.DeleteTripReportRequest) (*tripsv1.DeleteTripReportResponse, error) {
	userID, reportID, err := s.parseReportID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	keys, err := s.repo.DeleteReport(ctx, userID, reportID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete trip report")
	}
	for _, key := range keys {
		s.deleteImages(ctx, key)
	}
	return tripsv1.DeleteTripReportResponse_builder{}.Build(), nil
}

func (s *Service) PublishTripReport(ctx context.Context, req *tripsv1.PublishTripReportRequest) (*tripsv1.PublishTripReportResponse, error) {
	userID, reportID, err := s.parseReportID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.GetReport(ctx, userID, reportID); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get trip report")
	}
	profile, err := s.profile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "choose a public handle in Settings before publishing, so the report has an address")
	}
	report, err := s.repo.SetReportPublished(ctx, userID, reportID, true)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to publish trip report")
	}
	result, err := s.reportWithStory(ctx, report)
	if err != nil {
		return nil, err
	}
	return tripsv1.PublishTripReportResponse_builder{Report: result}.Build(), nil
}

func (s *Service) UnpublishTripReport(ctx context.Context, req *tripsv1.UnpublishTripReportRequest) (*tripsv1.UnpublishTripReportResponse, error) {
	userID, reportID, err := s.parseReportID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	report, err := s.repo.SetReportPublished(ctx, userID, reportID, false)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to unpublish trip report")
	}
	result, err := s.reportWithStory(ctx, report)
	if err != nil {
		return nil, err
	}
	return tripsv1.UnpublishTripReportResponse_builder{Report: result}.Build(), nil
}

func (s *Service) UploadTripPhoto(ctx context.Context, req *tripsv1.UploadTripPhotoRequest) (*tripsv1.UploadTripPhotoResponse, error) {
	userID, reportID, err := s.parseReportID(ctx, req.GetReportId())
	if err != nil {
		return nil, err
	}
	upload := &PhotoUpload{
		Data:           req.GetData(),
		Caption:        req.GetCaption(),
		TakenLocalTime: req.GetTakenLocalTime(),
	}
	if req.HasLocation() {
		upload.Location = req.GetLocation()
	}
	if req.HasTakenTime() {
		upload.TakenTime = req.GetTakenTime().AsTime()
	}
	created, err := s.addPhoto(ctx, userID, reportID, upload)
	if err != nil {
		return nil, err
	}
	return tripsv1.UploadTripPhotoResponse_builder{Photo: s.photoToProto(created)}.Build(), nil
}

// addPhoto adds an uploaded photo to one of the user's reports, placed on
// the track and in the story where and when it was taken.
func (s *Service) addPhoto(ctx context.Context, userID, reportID pgtype.UUID, upload *PhotoUpload) (*trips.Photo, error) {
	if err := upload.Validate(); err != nil {
		return nil, err
	}
	report, err := s.repo.GetReport(ctx, userID, reportID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get trip report")
	}
	existing, err := s.repo.ListPhotos(ctx, reportID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list photos")
	}
	if len(existing[reportID]) >= maxPhotosPerReport {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "a report can have at most %d photos", maxPhotosPerReport)
	}
	img, err := ProcessPhoto(ctx, upload)
	if err != nil {
		return nil, err
	}
	photo := &trips.Photo{
		ReportID: reportID,
		Caption:  strings.TrimSpace(upload.Caption),
	}
	if photo.TakenTime, err = upload.Taken(report.Timezone); err != nil {
		return nil, err
	}
	t, err := track.Decode(report.Track)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get track")
	}
	if photo.Location, err = PlacePhoto(report.StartTime, t, upload.Location, photo.TakenTime); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	return s.storePhoto(ctx, userID, photo, img)
}

// takenBefore reports whether photo a was taken before photo b on the trip.
func takenBefore(a, b *trips.Photo) bool {
	return TakenBefore(a.TakenTime, a.Location, b.TakenTime, b.Location)
}

// storePhoto stores a photo's images and adds it to its section of the
// report, in the order it was taken. It returns trips.ErrPhotoImported for a
// photo from Strava the report already has, and otherwise connect errors.
func (s *Service) storePhoto(ctx context.Context, userID pgtype.UUID, photo *trips.Photo, img *photos.Processed) (*trips.Photo, error) {
	photo.Width, photo.Height = int32(img.Width), int32(img.Height)
	key, err := s.photoStore.Put(ctx, img)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to store photo")
	}
	photo.ObjectKey = key
	created, err := s.repo.AddPhoto(ctx, userID, photo, takenBefore)
	if err != nil {
		s.deleteImages(ctx, photo.ObjectKey)
		if errors.Is(err, trips.ErrPhotoImported) {
			return nil, err
		}
		return nil, s.toConnectError(ctx, err, "failed to add photo")
	}
	return created, nil
}

func (s *Service) CreateTripPhotoUpload(ctx context.Context, req *tripsv1.CreateTripPhotoUploadRequest) (*tripsv1.CreateTripPhotoUploadResponse, error) {
	userID, reportID, err := s.parseReportID(ctx, req.GetReportId())
	if err != nil {
		return nil, err
	}
	report, err := s.repo.GetReport(ctx, userID, reportID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get trip report")
	}
	link := &trips.PhotoUploadLink{UserID: userID, TripReportID: report.ID}
	upload, err := CreatePhotoUploadLink(ctx, s.repo, link, s.baseURL+PhotoUploadPath, s.nowFn())
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to create the upload link")
	}
	return tripsv1.CreateTripPhotoUploadResponse_builder{Upload: upload}.Build(), nil
}

// PhotoUploadHandler serves the links agents upload photos to trip reports
// with, at PhotoUploadPath. See photos.UploadHandler.
func (s *Service) PhotoUploadHandler() http.Handler {
	return photos.UploadHandler(s.report, PhotoUploadPath, s.findUploadLink, s.uploadWithLink)
}

// findUploadLink returns the trip report upload link with the token.
func (s *Service) findUploadLink(ctx context.Context, token string) (*trips.PhotoUploadLink, error) {
	link, err := s.repo.GetPhotoUploadLink(ctx, photos.HashUploadToken(token))
	if errors.Is(err, store.ErrNotFound) || (err == nil && !link.TripReportID.Valid) {
		return nil, connect.NewError(connect.CodeNotFound, "this upload link doesn't exist or has expired; create a new one with CreateTripPhotoUpload")
	}
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get the upload link")
	}
	return link, nil
}

// uploadWithLink adds a photo uploaded with a link to its report.
func (s *Service) uploadWithLink(ctx context.Context, link *trips.PhotoUploadLink, u *photos.Upload) (proto.Message, error) {
	created, err := s.addPhoto(ctx, link.UserID, link.TripReportID, &PhotoUpload{Data: u.Data, Caption: u.Caption})
	if err != nil {
		return nil, err
	}
	return tripsv1.UploadTripPhotoResponse_builder{Photo: s.photoToProto(created)}.Build(), nil
}

func (s *Service) UpdateTripPhoto(ctx context.Context, req *tripsv1.UpdateTripPhotoRequest) (*tripsv1.UpdateTripPhotoResponse, error) {
	userID, photoID, err := s.parsePhotoID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if req.HasLocation() && req.GetClearLocation() {
		return nil, connect.NewError(connect.CodeInvalidArgument, "set either location or clear_location, not both")
	}
	if req.HasCaption() && utf8.RuneCountInString(req.GetCaption()) > MaxCaptionLength {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "the caption must be at most %d characters", MaxCaptionLength)
	}
	photo, err := s.repo.GetPhoto(ctx, userID, photoID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get photo")
	}
	if req.HasCaption() {
		photo, err = s.repo.UpdatePhotoCaption(ctx, userID, photoID, strings.TrimSpace(req.GetCaption()))
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to update photo")
		}
	}
	switch {
	case req.HasLocation():
		report, err := s.repo.GetReport(ctx, userID, photo.ReportID)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to get trip report")
		}
		t, err := track.Decode(report.Track)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to get track")
		}
		loc, err := LocationFromProto(req.GetLocation(), trips.LocationManual, t)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
		photo, err = s.repo.UpdatePhotoLocation(ctx, userID, photoID, loc)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to update photo")
		}
	case req.GetClearLocation():
		photo, err = s.repo.UpdatePhotoLocation(ctx, userID, photoID, nil)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to update photo")
		}
	}
	return tripsv1.UpdateTripPhotoResponse_builder{Photo: s.photoToProto(photo)}.Build(), nil
}

func (s *Service) GetTripPhoto(ctx context.Context, req *tripsv1.GetTripPhotoRequest) (*tripsv1.GetTripPhotoResponse, error) {
	userID, photoID, err := s.parsePhotoID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	size, err := PhotoSize(req.GetSize())
	if err != nil {
		return nil, err
	}
	photo, err := s.repo.GetPhoto(ctx, userID, photoID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get photo")
	}
	data, err := s.photoStore.Get(ctx, photo.ObjectKey, size)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to read the photo's image")
	}
	return tripsv1.GetTripPhotoResponse_builder{
		Photo: s.photoToProto(photo),
		Image: mcpv1.Image_builder{MimeType: "image/jpeg", Data: data}.Build(),
	}.Build(), nil
}

func (s *Service) DeleteTripPhoto(ctx context.Context, req *tripsv1.DeleteTripPhotoRequest) (*tripsv1.DeleteTripPhotoResponse, error) {
	userID, photoID, err := s.parsePhotoID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	key, err := s.repo.DeletePhoto(ctx, userID, photoID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete photo")
	}
	s.deleteImages(ctx, key)
	return tripsv1.DeleteTripPhotoResponse_builder{}.Build(), nil
}

func (s *Service) GetProfile(ctx context.Context, req *tripsv1.GetProfileRequest) (*tripsv1.GetProfileResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	profile, err := s.profile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if profile != nil {
		return tripsv1.GetProfileResponse_builder{
			Profile:              s.profileToProto(profile),
			SuggestedDisplayName: profile.DisplayName,
		}.Build(), nil
	}
	name, err := s.repo.GetUserName(ctx, userID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, s.toConnectError(ctx, err, "failed to get user")
	}
	return tripsv1.GetProfileResponse_builder{SuggestedDisplayName: name}.Build(), nil
}

func (s *Service) UpdateProfile(ctx context.Context, req *tripsv1.UpdateProfileRequest) (*tripsv1.UpdateProfileResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	handle := strings.ToLower(strings.TrimSpace(req.GetProfile().GetHandle()))
	if err := validateHandle(handle); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	displayName := strings.Join(strings.Fields(req.GetProfile().GetDisplayName()), " ")
	if displayName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "the display name is required")
	}
	if utf8.RuneCountInString(displayName) > maxDisplayNameLength {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "the display name must be at most %d characters", maxDisplayNameLength)
	}
	profile, err := s.repo.UpsertProfile(ctx, &trips.Profile{UserID: userID, Handle: handle, DisplayName: displayName})
	if err != nil {
		if errors.Is(err, trips.ErrHandleTaken) {
			return nil, connect.NewError(connect.CodeAlreadyExists, "this handle is taken; choose another one")
		}
		return nil, s.toConnectError(ctx, err, "failed to update profile")
	}
	return tripsv1.UpdateProfileResponse_builder{Profile: s.profileToProto(profile)}.Build(), nil
}

func (s *Service) GetPublicProfile(ctx context.Context, req *tripsv1.GetPublicProfileRequest) (*tripsv1.GetPublicProfileResponse, error) {
	// Public profiles don't require authentication.
	profile, err := s.repo.GetProfileByHandle(ctx, req.GetHandle())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, "there is no profile with this handle")
		}
		return nil, s.toConnectError(ctx, err, "failed to get profile")
	}
	reports, err := s.repo.ListReports(ctx, profile.UserID, trips.ListReportsFilter{PublishedOnly: true})
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list trip reports")
	}
	result, err := s.reportsToProto(ctx, reports, profile, true)
	if err != nil {
		return nil, err
	}
	return tripsv1.GetPublicProfileResponse_builder{
		Profile: s.profileToProto(profile),
		Reports: result,
	}.Build(), nil
}

func (s *Service) GetPublicTripReport(ctx context.Context, req *tripsv1.GetPublicTripReportRequest) (*tripsv1.GetPublicTripReportResponse, error) {
	// Published reports don't require authentication.
	profile, report, err := s.publicReport(ctx, req.GetHandle(), req.GetSlug())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, "this trip report doesn't exist or isn't published")
		}
		return nil, s.toConnectError(ctx, err, "failed to get trip report")
	}
	photos, err := s.repo.ListPhotos(ctx, report.ID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list photos")
	}
	t, err := track.Decode(report.Track)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get track")
	}
	// Hide the track in the author's privacy zones, and the photos taken
	// there.
	zoneList, err := s.zones.ListPrivacyZones(ctx, report.UserID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list privacy zones")
	}
	zones := track.HiddenZones(zoneList)
	var points []*routesv1.RoutePoint
	var breaks []int32
	if t != nil {
		points, breaks = t.PublicPoints(zones)
	}
	visible := hidePhotoLocations(photos[report.ID], func(loc *trips.Location) bool {
		return track.InZones(geo.Point{Lat: loc.Lat, Lng: loc.Lng}, zones)
	})
	sections, err := s.repo.ListSections(ctx, report.ID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list sections")
	}
	return tripsv1.GetPublicTripReportResponse_builder{
		Report:      s.reportToProto(report, newStory(sections[report.ID], visible), profile),
		Author:      s.profileToProto(profile),
		Track:       points,
		TrackBreaks: breaks,
	}.Build(), nil
}

// publicReport gets a report by its author's handle and slug, if it is
// published or the caller is its author. Otherwise it returns
// store.ErrNotFound, so drafts can't be told apart from missing reports.
func (s *Service) publicReport(ctx context.Context, handle, slug string) (*trips.Profile, *trips.Report, error) {
	profile, err := s.repo.GetProfileByHandle(ctx, handle)
	if err != nil {
		return nil, nil, err
	}
	report, err := s.repo.GetReportBySlug(ctx, profile.UserID, slug)
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

// profile returns the user's public profile, or nil if the user has none.
func (s *Service) profile(ctx context.Context, userID pgtype.UUID) (*trips.Profile, error) {
	profile, err := s.repo.GetProfile(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, s.toConnectError(ctx, err, "failed to get profile")
	}
	return profile, nil
}

// reportWithStory converts a report of the authenticated user, with its
// sections and photos.
func (s *Service) reportWithStory(ctx context.Context, report *trips.Report) (*tripsv1.TripReport, error) {
	sections, err := s.repo.ListSections(ctx, report.ID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list sections")
	}
	photos, err := s.repo.ListPhotos(ctx, report.ID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list photos")
	}
	profile, err := s.profile(ctx, report.UserID)
	if err != nil {
		return nil, err
	}
	return s.reportToProto(report, newStory(sections[report.ID], photos[report.ID]), profile), nil
}

// reportsToProto converts reports of one author for lists, with their
// photos and excerpts but without their sections. Public lists leave out
// where the photos were taken: lists don't show it, and leaving it out saves
// hiding the photos in the author's privacy zones.
func (s *Service) reportsToProto(ctx context.Context, reports []*trips.Report, profile *trips.Profile, public bool) ([]*tripsv1.TripReport, error) {
	ids := make([]pgtype.UUID, len(reports))
	for i, r := range reports {
		ids[i] = r.ID
	}
	photos, err := s.repo.ListPhotos(ctx, ids...)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list photos")
	}
	excerpts, err := s.repo.ListExcerpts(ctx, ids...)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list excerpts")
	}
	result := make([]*tripsv1.TripReport, len(reports))
	for i, r := range reports {
		reportPhotos := photos[r.ID]
		if public {
			reportPhotos = hidePhotoLocations(reportPhotos, func(*trips.Location) bool { return true })
		}
		result[i] = s.reportToProto(r, &story{photos: reportPhotos, excerpt: excerpts[r.ID]}, profile)
	}
	return result, nil
}

// hidePhotoLocations returns the photos with the locations removed of those
// taken where hide reports true, for public pages. It copies the photos it
// changes, as the others may share them.
func hidePhotoLocations(photos []*trips.Photo, hide func(*trips.Location) bool) []*trips.Photo {
	visible := make([]*trips.Photo, len(photos))
	for i, p := range photos {
		visible[i] = p
		if p.Location != nil && hide(p.Location) {
			hidden := *p
			hidden.Location = nil
			visible[i] = &hidden
		}
	}
	return visible
}

// deleteImages deletes the images of a photo. Failures are reported, as the
// photo is already gone.
func (s *Service) deleteImages(ctx context.Context, objectKey string) {
	if err := s.photoStore.Delete(ctx, objectKey); err != nil {
		s.report.Report(ctx, "Failed to delete photo images", err, slog.String("key", objectKey))
	}
}

// parseReportID returns the authenticated user's ID and the parsed report
// ID.
func (s *Service) parseReportID(ctx context.Context, id string) (userID, reportID pgtype.UUID, err error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return userID, reportID, connect.NewError(connect.CodeUnauthenticated, "")
	}
	reportID, err = store.StringToUUID(id)
	if err != nil {
		return userID, reportID, connect.Errorf(connect.CodeInvalidArgument, "invalid trip report ID: %v", err).WithCause(err)
	}
	return userID, reportID, nil
}

// parsePhotoID returns the authenticated user's ID and the parsed photo ID.
func (s *Service) parsePhotoID(ctx context.Context, id string) (userID, photoID pgtype.UUID, err error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return userID, photoID, connect.NewError(connect.CodeUnauthenticated, "")
	}
	photoID, err = store.StringToUUID(id)
	if err != nil {
		return userID, photoID, connect.Errorf(connect.CodeInvalidArgument, "invalid photo ID: %v", err).WithCause(err)
	}
	return userID, photoID, nil
}

func validateTitle(title string) error {
	if utf8.RuneCountInString(title) > maxTitleLength {
		return fmt.Errorf("the title must be at most %d characters", maxTitleLength)
	}
	return nil
}

// dateOf returns the date of a time, at midnight UTC.
func dateOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func (s *Service) toConnectError(ctx context.Context, err error, msg string) error {
	var connectErr *connect.Error
	switch {
	case errors.As(err, &connectErr):
		return err
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, "not found")
	case errors.Is(err, trips.ErrSectionsMismatch):
		return connect.NewError(connect.CodeInvalidArgument, "the sections with an ID must be the report's sections, each listed once")
	case errors.Is(err, trips.ErrPhotosMismatch):
		return connect.NewError(connect.CodeInvalidArgument, "the photos must be the report's photos, each listed once")
	case errors.Is(err, trips.ErrNotReportPhoto):
		return connect.NewError(connect.CodeInvalidArgument, "the cover photo must be one of the report's photos")
	case errors.Is(err, trips.ErrSlugTaken):
		return connect.NewError(connect.CodeAlreadyExists, "another of your trip reports has this slug; choose another one")
	default:
		s.report.Report(ctx, msg, err)
		return connect.NewError(connect.CodeInternal, msg).WithCause(err)
	}
}
