package trip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	tripsv1 "github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1"
	"github.com/johanbrandhorst/trail-tools/internal/photos"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/track"
	"github.com/johanbrandhorst/trail-tools/internal/trips"
	"gitlab.com/greyxor/slogor"
)

// photoDownloadTimeout bounds downloading a photo from Strava.
const photoDownloadTimeout = 30 * time.Second

func (s *Service) ImportStravaPhotos(ctx context.Context, req *tripsv1.ImportStravaPhotosRequest) (*tripsv1.ImportStravaPhotosResponse, error) {
	userID, reportID, err := s.parseReportID(ctx, req.GetReportId())
	if err != nil {
		return nil, err
	}
	report, err := s.repo.GetReport(ctx, userID, reportID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get trip report")
	}
	if !report.ActivityID.Valid {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the report wasn't made from an activity, so it has no photos on Strava")
	}
	activity, err := s.activities.GetActivity(ctx, userID, report.ActivityID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get activity")
	}
	result, err := s.importPhotos(ctx, report, activity)
	if err != nil {
		return nil, err
	}
	resp := tripsv1.ImportStravaPhotosResponse_builder{
		SkippedCount: result.skipped,
		FailedCount:  result.failed,
	}
	for _, p := range result.photos {
		resp.Photos = append(resp.Photos, s.photoToProto(p))
	}
	return resp.Build(), nil
}

// importStravaPhotos imports the photos of a new report's activity from
// Strava, if it has any, and returns the imported photos.
func (s *Service) importStravaPhotos(ctx context.Context, report *trips.Report) ([]*trips.Photo, error) {
	activity, err := s.activities.GetActivity(ctx, report.UserID, report.ActivityID)
	if err != nil {
		return nil, err
	}
	if activity.PhotoCount == 0 {
		// The activity may have photos from an earlier sync anyway.
		photos, err := s.activities.ListActivityPhotos(ctx, report.UserID, report.ActivityID)
		if err != nil || len(photos) == 0 {
			return nil, err
		}
	}
	result, err := s.importPhotos(ctx, report, activity)
	if err != nil {
		return nil, err
	}
	return result.photos, nil
}

// importResult is the outcome of importing a report's photos from Strava.
type importResult struct {
	photos          []*trips.Photo
	skipped, failed int32
}

// importPhotos imports the photos of the report's activity from Strava,
// skipping the ones it already has. It returns connect errors.
func (s *Service) importPhotos(ctx context.Context, report *trips.Report, activity *activities.Activity) (*importResult, error) {
	userID, reportID := report.UserID, report.ID
	// Photos may have been added on Strava since the activity was synced,
	// and Strava's image URLs may have expired, so sync the activity first.
	// Without Strava, the photos from the last sync are imported.
	if s.syncer != nil {
		if err := s.syncer.SyncActivity(ctx, userID, activity.StravaID); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.report.Report(ctx, "Failed to sync the activity before importing its photos", err)
		}
	}
	stravaPhotos, err := s.activities.ListActivityPhotos(ctx, userID, report.ActivityID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list the activity's photos")
	}
	existing, err := s.repo.ListPhotos(ctx, reportID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list photos")
	}
	imported := map[string]bool{}
	for _, p := range existing[reportID] {
		if p.StravaPhotoID != "" {
			imported[p.StravaPhotoID] = true
		}
	}
	count := len(existing[reportID])
	for _, p := range stravaPhotos {
		if !imported[p.StravaID] {
			count++
		}
	}
	if count > maxPhotosPerReport {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "a report can have at most %d photos", maxPhotosPerReport)
	}
	t, err := track.Decode(report.Track)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get track")
	}

	result := &importResult{}
	for _, p := range stravaPhotos {
		if imported[p.StravaID] {
			result.skipped++
			continue
		}
		log := s.log.With(slog.String("strava_photo_id", p.StravaID))
		data, err := s.downloadPhoto(ctx, p.URL)
		if err != nil {
			s.report.Report(ctx, "Failed to download a photo from Strava", err, slog.String("strava_photo_id", p.StravaID))
			result.failed++
			continue
		}
		upload := &PhotoUpload{Data: data, TakenTime: p.TakenTime}
		if p.Location != nil {
			upload.Location = geov1.LatLng_builder{Lat: p.Location.Lat, Lng: p.Location.Lng}.Build()
		}
		img, err := ProcessPhoto(ctx, upload)
		if connect.CodeOf(err) == connect.CodeInvalidArgument {
			log.WarnContext(ctx, "A photo from Strava isn't a supported image", slogor.Err(err))
			result.failed++
			continue
		}
		if err != nil {
			return nil, err
		}
		photo := &trips.Photo{
			ReportID:      reportID,
			Caption:       truncateRunes(p.Caption, MaxCaptionLength),
			TakenTime:     upload.TakenTime,
			StravaPhotoID: p.StravaID,
		}
		if photo.Location, err = PlacePhoto(report.StartTime, t, upload.Location, photo.TakenTime); err != nil {
			// Strava's location is invalid, so place the photo by time.
			photo.Location, _ = PlacePhoto(report.StartTime, t, nil, photo.TakenTime)
		}
		created, err := s.storePhoto(ctx, userID, photo, img)
		switch {
		case errors.Is(err, trips.ErrPhotoImported):
			// Imported meanwhile.
			result.skipped++
		case err != nil:
			return nil, err
		default:
			result.photos = append(result.photos, created)
		}
	}
	return result, nil
}

// downloadPhoto downloads a photo from Strava's CDN.
func (s *Service) downloadPhoto(ctx context.Context, imageURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, photoDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, photos.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read photo: %w", err)
	}
	if len(data) > photos.MaxBytes {
		return nil, fmt.Errorf("the photo is larger than %d MB", photos.MaxBytes>>20)
	}
	return data, nil
}

// truncateRunes returns s cut to at most n runes.
func truncateRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
