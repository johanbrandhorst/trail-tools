package racereport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	mcpv1 "github.com/johanbrandhorst/trail-tools/internal/gen/mcp/v1"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/photos"
	"github.com/johanbrandhorst/trail-tools/internal/races"
	"github.com/johanbrandhorst/trail-tools/internal/services/trip"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/track"
	"github.com/johanbrandhorst/trail-tools/internal/trips"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// PhotoPath is the path the photos of race reports are served at:
	// /race-report-photos/<id>/<size>.jpg.
	PhotoPath = "/race-report-photos/"
	// PhotoUploadPath is the path of the links agents upload photos to race
	// reports with, followed by their secret token.
	PhotoUploadPath = "/race-report-photo-uploads/"
	// maxPhotosPerReport is the most photos a race report can have.
	maxPhotosPerReport = 200
)

func (s *Service) UploadRaceReportPhoto(ctx context.Context, req *racesv1.UploadRaceReportPhotoRequest) (*racesv1.UploadRaceReportPhotoResponse, error) {
	report, err := s.getReport(ctx, req.GetReportId())
	if err != nil {
		return nil, err
	}
	upload := &trip.PhotoUpload{
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
	created, err := s.addPhoto(ctx, report, upload)
	if err != nil {
		return nil, err
	}
	return racesv1.UploadRaceReportPhotoResponse_builder{Photo: s.photoToProto(created)}.Build(), nil
}

// addPhoto adds an uploaded photo to a race report, placed on the track and
// among the report's photos where and when it was taken.
func (s *Service) addPhoto(ctx context.Context, report *races.RaceReport, upload *trip.PhotoUpload) (*races.RaceReportPhoto, error) {
	if err := upload.Validate(); err != nil {
		return nil, err
	}
	existing, err := s.repo.ListRaceReportPhotos(ctx, report.ID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list race report photos")
	}
	if len(existing) >= maxPhotosPerReport {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "a race report can have at most %d photos", maxPhotosPerReport)
	}
	if err := s.ensureTrack(ctx, report); err != nil {
		return nil, err
	}
	// The activity's start places photos on the track by the time they were
	// taken, and its timezone gives the time of photos taken by cameras,
	// which record local times.
	var start time.Time
	timezone := "UTC"
	if report.ActivityID.Valid {
		activity, err := s.activities.GetActivity(ctx, report.UserID, report.ActivityID)
		switch {
		case err == nil:
			start, timezone = activity.StartTime, activity.Timezone
		case !errors.Is(err, store.ErrNotFound):
			return nil, s.toConnectError(ctx, err, "failed to get activity")
		}
	}
	img, err := trip.ProcessPhoto(ctx, upload)
	if err != nil {
		return nil, err
	}
	photo := &races.RaceReportPhoto{
		ReportID: report.ID,
		Caption:  strings.TrimSpace(upload.Caption),
		Width:    int32(img.Width),
		Height:   int32(img.Height),
	}
	if photo.TakenTime, err = upload.Taken(timezone); err != nil {
		return nil, err
	}
	t, err := track.Decode(report.Track)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to decode race report track")
	}
	if photo.Location, err = trip.PlacePhoto(start, t, upload.Location, photo.TakenTime); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if photo.ObjectKey, err = s.photoStore.Put(ctx, img); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to store photo")
	}
	created, err := s.repo.AddRaceReportPhoto(ctx, report.UserID, photo, takenBefore)
	if err != nil {
		s.deleteImages(ctx, photo.ObjectKey)
		return nil, s.toConnectError(ctx, err, "failed to add photo")
	}
	return created, nil
}

// replacePhotos places the photos of a report on its new track, after its
// activity changed, which started at start: on the track at the time they
// were taken, if the new activity covers it. Photos placed by hand, and
// those the new activity doesn't cover, stay where they are, with their
// distance along the new track. Without a track, photos keep their places
// without distances.
func (s *Service) replacePhotos(ctx context.Context, report *races.RaceReport, start time.Time) error {
	list, err := s.repo.ListRaceReportPhotos(ctx, report.ID)
	if err != nil {
		return s.toConnectError(ctx, err, "failed to list race report photos")
	}
	t, err := track.Decode(report.Track)
	if err != nil {
		return s.toConnectError(ctx, err, "failed to decode race report track")
	}
	var moved []*races.RaceReportPhoto
	for _, p := range list {
		loc := replacedLocation(p, start, t)
		if (loc == nil && p.Location == nil) || (loc != nil && p.Location != nil && *loc == *p.Location) {
			continue
		}
		p.Location = loc
		moved = append(moved, p)
	}
	if len(moved) == 0 {
		return nil
	}
	if err := s.repo.SetRaceReportPhotoLocations(ctx, report.ID, moved); err != nil {
		return s.toConnectError(ctx, err, "failed to move race report photos")
	}
	return nil
}

// replacedLocation returns where a photo goes on a report's new track, t,
// of an activity that started at start. See replacePhotos.
func replacedLocation(p *races.RaceReportPhoto, start time.Time, t *track.Track) *trips.Location {
	if p.Location == nil || p.Location.Source != trips.LocationManual {
		// Without a GPS position, placing by time can't fail.
		if loc, _ := trip.PlacePhoto(start, t, nil, p.TakenTime); loc != nil {
			return loc
		}
	}
	if p.Location == nil {
		return nil
	}
	loc := *p.Location
	loc.TrackDistanceMeters, loc.HasTrackDistanceMeters = t.NearestDistance(geo.Point{Lat: loc.Lat, Lng: loc.Lng})
	return &loc
}

// takenBefore reports whether photo a was taken before photo b in the race.
func takenBefore(a, b *races.RaceReportPhoto) bool {
	return trip.TakenBefore(a.TakenTime, a.Location, b.TakenTime, b.Location)
}

func (s *Service) CreateRaceReportPhotoUpload(ctx context.Context, req *racesv1.CreateRaceReportPhotoUploadRequest) (*racesv1.CreateRaceReportPhotoUploadResponse, error) {
	report, err := s.getReport(ctx, req.GetReportId())
	if err != nil {
		return nil, err
	}
	link := &trips.PhotoUploadLink{UserID: report.UserID, RaceReportID: report.ID}
	upload, err := trip.CreatePhotoUploadLink(ctx, s.links, link, s.baseURL+PhotoUploadPath, s.nowFn())
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to create the upload link")
	}
	return racesv1.CreateRaceReportPhotoUploadResponse_builder{Upload: upload}.Build(), nil
}

// PhotoUploadHandler serves the links agents upload photos to race reports
// with, at PhotoUploadPath. See photos.UploadHandler.
func (s *Service) PhotoUploadHandler() http.Handler {
	return photos.UploadHandler(s.report, PhotoUploadPath, s.findUploadLink, s.uploadWithLink)
}

// findUploadLink returns the race report upload link with the token.
func (s *Service) findUploadLink(ctx context.Context, token string) (*trips.PhotoUploadLink, error) {
	link, err := s.links.GetPhotoUploadLink(ctx, photos.HashUploadToken(token))
	if errors.Is(err, store.ErrNotFound) || (err == nil && !link.RaceReportID.Valid) {
		return nil, connect.NewError(connect.CodeNotFound, "this upload link doesn't exist or has expired; create a new one with CreateRaceReportPhotoUpload")
	}
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get the upload link")
	}
	return link, nil
}

// uploadWithLink adds a photo uploaded with a link to its report.
func (s *Service) uploadWithLink(ctx context.Context, link *trips.PhotoUploadLink, u *photos.Upload) (proto.Message, error) {
	report, err := s.repo.GetRaceReport(ctx, link.UserID, link.RaceReportID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get race report")
	}
	created, err := s.addPhoto(ctx, report, &trip.PhotoUpload{Data: u.Data, Caption: u.Caption})
	if err != nil {
		return nil, err
	}
	return racesv1.UploadRaceReportPhotoResponse_builder{Photo: s.photoToProto(created)}.Build(), nil
}

func (s *Service) UpdateRaceReportPhoto(ctx context.Context, req *racesv1.UpdateRaceReportPhotoRequest) (*racesv1.UpdateRaceReportPhotoResponse, error) {
	userID, photoID, err := s.parsePhotoID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if req.HasLocation() && req.GetClearLocation() {
		return nil, connect.NewError(connect.CodeInvalidArgument, "set either location or clear_location, not both")
	}
	if req.HasCaption() && utf8.RuneCountInString(req.GetCaption()) > trip.MaxCaptionLength {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "the caption must be at most %d characters", trip.MaxCaptionLength)
	}
	photo, err := s.repo.GetRaceReportPhoto(ctx, userID, photoID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get race report photo")
	}
	if req.HasCaption() {
		photo, err = s.repo.UpdateRaceReportPhotoCaption(ctx, userID, photoID, strings.TrimSpace(req.GetCaption()))
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to update race report photo")
		}
	}
	switch {
	case req.HasLocation():
		report, err := s.repo.GetRaceReport(ctx, userID, photo.ReportID)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to get race report")
		}
		t, err := track.Decode(report.Track)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to decode race report track")
		}
		loc, err := trip.LocationFromProto(req.GetLocation(), trips.LocationManual, t)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
		photo, err = s.repo.UpdateRaceReportPhotoLocation(ctx, userID, photoID, loc)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to update race report photo")
		}
	case req.GetClearLocation():
		photo, err = s.repo.UpdateRaceReportPhotoLocation(ctx, userID, photoID, nil)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to update race report photo")
		}
	}
	return racesv1.UpdateRaceReportPhotoResponse_builder{Photo: s.photoToProto(photo)}.Build(), nil
}

func (s *Service) DeleteRaceReportPhoto(ctx context.Context, req *racesv1.DeleteRaceReportPhotoRequest) (*racesv1.DeleteRaceReportPhotoResponse, error) {
	userID, photoID, err := s.parsePhotoID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	key, err := s.repo.DeleteRaceReportPhoto(ctx, userID, photoID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete race report photo")
	}
	s.deleteImages(ctx, key)
	return racesv1.DeleteRaceReportPhotoResponse_builder{}.Build(), nil
}

func (s *Service) GetRaceReportPhoto(ctx context.Context, req *racesv1.GetRaceReportPhotoRequest) (*racesv1.GetRaceReportPhotoResponse, error) {
	userID, photoID, err := s.parsePhotoID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	size, err := trip.PhotoSize(req.GetSize())
	if err != nil {
		return nil, err
	}
	photo, err := s.repo.GetRaceReportPhoto(ctx, userID, photoID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get race report photo")
	}
	data, err := s.photoStore.Get(ctx, photo.ObjectKey, size)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to read the photo's image")
	}
	return racesv1.GetRaceReportPhotoResponse_builder{
		Photo: s.photoToProto(photo),
		Image: mcpv1.Image_builder{MimeType: "image/jpeg", Data: data}.Build(),
	}.Build(), nil
}

// PhotoHandler serves the photos of race reports at
// /race-report-photos/<id>/<size>.jpg, where size is "large" or "thumb". It
// must be mounted at PhotoPath behind the authn middleware. Photos of
// published reports are served to anyone, photos of drafts only to their
// author.
func (s *Service) PhotoHandler() http.Handler {
	return s.photoStore.Handler(s.report, PhotoPath, func(ctx context.Context, photoID pgtype.UUID) (*photos.Access, error) {
		a, err := s.repo.GetRaceReportPhotoAccess(ctx, photoID)
		if err != nil {
			return nil, err
		}
		return &photos.Access{ObjectKey: a.ObjectKey, CreateTime: a.CreateTime, UserID: a.UserID, Published: a.Published}, nil
	})
}

// withPhotos sets the photos of a report on its proto. With zones, the
// locations of photos taken in them are left out, for public pages.
func (s *Service) withPhotos(ctx context.Context, report *racesv1.RaceReport, reportID pgtype.UUID, zones []track.Zone) error {
	list, err := s.repo.ListRaceReportPhotos(ctx, reportID)
	if err != nil {
		return s.toConnectError(ctx, err, "failed to list race report photos")
	}
	protos := make([]*racesv1.RaceReportPhoto, len(list))
	for i, p := range list {
		if p.Location != nil && track.InZones(geo.Point{Lat: p.Location.Lat, Lng: p.Location.Lng}, zones) {
			hidden := *p
			hidden.Location = nil
			p = &hidden
		}
		protos[i] = s.photoToProto(p)
	}
	report.SetPhotos(protos)
	return nil
}

func (s *Service) photoToProto(p *races.RaceReportPhoto) *racesv1.RaceReportPhoto {
	id := store.UUIDToString(p.ID)
	b := racesv1.RaceReportPhoto_builder{
		Id:           id,
		Caption:      p.Caption,
		Width:        p.Width,
		Height:       p.Height,
		Url:          s.photoURL(id, photos.SizeLarge),
		ThumbnailUrl: s.photoURL(id, photos.SizeThumb),
	}
	if loc := p.Location; loc != nil {
		b.Location = geov1.LatLng_builder{Lat: loc.Lat, Lng: loc.Lng}.Build()
		b.LocationSource = trip.LocationSourceToProto(loc.Source)
		if loc.HasTrackDistanceMeters {
			b.TrackDistanceMeters = &loc.TrackDistanceMeters
		}
	}
	if !p.TakenTime.IsZero() {
		b.TakenTime = timestamppb.New(p.TakenTime)
	}
	return b.Build()
}

func (s *Service) photoURL(id, size string) string {
	return s.baseURL + PhotoPath + id + "/" + size + ".jpg"
}

// deleteImages deletes the images of a photo. Failures are reported, as the
// photo is already gone.
func (s *Service) deleteImages(ctx context.Context, objectKey string) {
	if err := s.photoStore.Delete(ctx, objectKey); err != nil {
		s.report.Report(ctx, "Failed to delete race report photo images", err, slog.String("key", objectKey))
	}
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
