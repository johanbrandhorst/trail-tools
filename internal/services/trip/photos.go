package trip

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect/v2"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	tripsv1 "github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/photos"
	"github.com/johanbrandhorst/trail-tools/internal/track"
	"github.com/johanbrandhorst/trail-tools/internal/trips"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The photos of race reports are placed like those of trip reports, with the
// helpers here.

const (
	// MaxCaptionLength is the most characters of a photo's caption.
	MaxCaptionLength = 2000
	// PhotoUploadLinkLifetime is how long the links agents upload photos
	// with work: long enough to upload many photos, and short enough that a
	// link that leaked soon stops working.
	PhotoUploadLinkLifetime = time.Hour
	// localTimeLayout is the layout of local times without an offset.
	localTimeLayout = "2006-01-02T15:04:05"
)

// PhotoUpload is an uploaded photo, with what the uploader says about where
// and when it was taken. Browsers read that from the photo's metadata, but
// agents upload photos as they are, so the photo's metadata fills in what
// the uploader doesn't say.
type PhotoUpload struct {
	Data    []byte
	Caption string
	// The GPS position recorded in the photo, nil if unknown.
	Location *geov1.LatLng
	// When the photo was taken, zero if unknown, or else as a local time
	// without an offset (YYYY-MM-DDTHH:MM:SS), empty if unknown.
	TakenTime      time.Time
	TakenLocalTime string
}

// Validate checks the size of an uploaded photo and the length of its
// caption.
func (u *PhotoUpload) Validate() error {
	switch {
	case len(u.Data) == 0:
		return connect.NewError(connect.CodeInvalidArgument, "the photo is empty")
	case len(u.Data) > photos.MaxBytes:
		return connect.Errorf(connect.CodeInvalidArgument, "the photo must be at most %d MB", photos.MaxBytes>>20)
	case utf8.RuneCountInString(u.Caption) > MaxCaptionLength:
		return connect.Errorf(connect.CodeInvalidArgument, "the caption must be at most %d characters", MaxCaptionLength)
	}
	return nil
}

// ProcessPhoto turns an uploaded photo into the images to store, and fills
// in where and when it was taken from its metadata. It returns Connect
// errors.
func ProcessPhoto(ctx context.Context, u *PhotoUpload) (*photos.Processed, error) {
	img, err := photos.Process(ctx, u.Data)
	if err != nil {
		if ctx.Err() != nil {
			return nil, connect.NewError(connect.CodeUnavailable, "the server was too busy to process the photo; try again").WithCause(err)
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	md := img.Metadata
	if u.Location == nil && md.HasLocation {
		u.Location = geov1.LatLng_builder{Lat: md.Lat, Lng: md.Lng}.Build()
	}
	if u.TakenTime.IsZero() && u.TakenLocalTime == "" {
		u.TakenTime, u.TakenLocalTime = md.TakenTime, md.TakenLocalTime
	}
	return img, nil
}

// Taken returns when an uploaded photo was taken, with a local time in the
// IANA timezone of the trip, or zero if it's unknown.
func (u *PhotoUpload) Taken(timezone string) (time.Time, error) {
	if !u.TakenTime.IsZero() || u.TakenLocalTime == "" {
		return u.TakenTime, nil
	}
	t, err := parseLocalTime(u.TakenLocalTime, timezone)
	if err != nil {
		return time.Time{}, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	return t, nil
}

// parseLocalTime parses a local time without an offset in the IANA
// timezone, falling back to UTC for unknown timezones.
func parseLocalTime(value, timezone string) (time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	t, err := time.ParseInLocation(localTimeLayout, value, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid taken_local_time %q, expected YYYY-MM-DDTHH:MM:SS", value)
	}
	return t, nil
}

// PlacePhoto returns where a photo was taken: on the track at the time it
// was taken if that was during the activity that started at startTime, or
// else at its GPS position if it has one. Photo GPS positions are often far
// off, e.g. from a phone in a pocket, while the track records where the
// activity went. It returns nil if neither is known.
func PlacePhoto(startTime time.Time, t *track.Track, gps *geov1.LatLng, takenTime time.Time) (*trips.Location, error) {
	if !takenTime.IsZero() && !startTime.IsZero() {
		if p, dist, ok := t.PositionAt(takenTime.Sub(startTime).Seconds()); ok {
			return &trips.Location{
				Lat:                    p.Lat,
				Lng:                    p.Lng,
				Source:                 trips.LocationTrackTime,
				TrackDistanceMeters:    dist,
				HasTrackDistanceMeters: true,
			}, nil
		}
	}
	if gps != nil {
		return LocationFromProto(gps, trips.LocationGPS, t)
	}
	return nil, nil
}

// TakenBefore reports whether a photo taken at time a and location aLoc was
// taken before one taken at b and bLoc: by the times they were taken, if
// both are known, or else by how far along the track they were taken. Times
// come first, as on an out-and-back the same place is passed twice, and a
// photo's GPS position may be matched to the wrong way.
func TakenBefore(a time.Time, aLoc *trips.Location, b time.Time, bLoc *trips.Location) bool {
	if !a.IsZero() && !b.IsZero() {
		return a.Before(b)
	}
	if aLoc != nil && aLoc.HasTrackDistanceMeters && bLoc != nil && bLoc.HasTrackDistanceMeters {
		return aLoc.TrackDistanceMeters < bLoc.TrackDistanceMeters
	}
	return false
}

// LocationFromProto validates a location and finds its distance along the
// track.
func LocationFromProto(ll *geov1.LatLng, source trips.LocationSource, t *track.Track) (*trips.Location, error) {
	lat, lng := ll.GetLat(), ll.GetLng()
	if math.IsNaN(lat) || math.IsNaN(lng) || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return nil, errors.New("the location must have a latitude between -90 and 90 and a longitude between -180 and 180")
	}
	loc := &trips.Location{Lat: lat, Lng: lng, Source: source}
	loc.TrackDistanceMeters, loc.HasTrackDistanceMeters = t.NearestDistance(geo.Point{Lat: lat, Lng: lng})
	return loc, nil
}

// PhotoSize returns the size of a photo's image to get.
func PhotoSize(size tripsv1.PhotoSize) (string, error) {
	switch size {
	case tripsv1.PhotoSize_PHOTO_SIZE_UNSPECIFIED, tripsv1.PhotoSize_PHOTO_SIZE_THUMBNAIL:
		return photos.SizeThumb, nil
	case tripsv1.PhotoSize_PHOTO_SIZE_LARGE:
		return photos.SizeLarge, nil
	}
	return "", connect.Errorf(connect.CodeInvalidArgument, "unknown size %v; use PHOTO_SIZE_THUMBNAIL or PHOTO_SIZE_LARGE", size)
}

// LocationSourceToProto returns the API's value of where a photo's location
// comes from.
func LocationSourceToProto(source trips.LocationSource) tripsv1.LocationSource {
	switch source {
	case trips.LocationGPS:
		return tripsv1.LocationSource_LOCATION_SOURCE_GPS
	case trips.LocationTrackTime:
		return tripsv1.LocationSource_LOCATION_SOURCE_TRACK_TIME
	case trips.LocationManual:
		return tripsv1.LocationSource_LOCATION_SOURCE_MANUAL
	}
	return tripsv1.LocationSource_LOCATION_SOURCE_UNSPECIFIED
}

// PhotoUploadLinkRepository stores the links agents upload photos with.
type PhotoUploadLinkRepository interface {
	CreatePhotoUploadLink(ctx context.Context, link *trips.PhotoUploadLink) (*trips.PhotoUploadLink, error)
	GetPhotoUploadLink(ctx context.Context, tokenSHA256 []byte) (*trips.PhotoUploadLink, error)
}

// CreatePhotoUploadLink stores a new link to upload photos to the report of
// link with, expiring PhotoUploadLinkLifetime after now, and returns it with
// its URL and secret token. The caller must check that the report is the
// user's.
func CreatePhotoUploadLink(ctx context.Context, links PhotoUploadLinkRepository, link *trips.PhotoUploadLink, url string, now time.Time) (*tripsv1.PhotoUpload, error) {
	token, hash := photos.NewUploadToken()
	link.TokenSHA256 = hash
	link.ExpireTime = now.Add(PhotoUploadLinkLifetime)
	created, err := links.CreatePhotoUploadLink(ctx, link)
	if err != nil {
		return nil, err
	}
	return tripsv1.PhotoUpload_builder{
		Url:        url,
		ExpireTime: timestamppb.New(created.ExpireTime),
		Token:      token,
	}.Build(), nil
}
