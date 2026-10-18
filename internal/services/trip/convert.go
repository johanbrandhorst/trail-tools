package trip

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	tripsv1 "github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1"
	"github.com/johanbrandhorst/trail-tools/internal/photos"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/trips"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// story is the content of a report: its sections and photos in order, and
// the excerpt shown in lists.
type story struct {
	// Nil in lists.
	sections []*trips.Section
	photos   []*trips.Photo
	excerpt  string
}

// newStory returns the story of a report with its sections and photos.
func newStory(sections []*trips.Section, photos []*trips.Photo) *story {
	st := &story{sections: sections, photos: photos}
	for _, sec := range sections {
		if strings.TrimSpace(sec.Body) != "" {
			st.excerpt = sec.Body
			break
		}
	}
	return st
}

// reportToProto converts a report with its story. profile is the author's
// profile, nil if the author has none.
func (s *Service) reportToProto(r *trips.Report, st *story, profile *trips.Profile) *tripsv1.TripReport {
	stats := tripsv1.TripStats_builder{
		SportType:           r.SportType,
		Timezone:            r.Timezone,
		DistanceMeters:      r.DistanceMeters,
		MovingTimeSeconds:   r.MovingTimeSeconds,
		ElapsedTimeSeconds:  r.ElapsedTimeSeconds,
		ElevationGainMeters: r.ElevationGainMeters,
	}
	if !r.StartTime.IsZero() {
		stats.StartTime = timestamppb.New(r.StartTime)
	}
	b := tripsv1.TripReport_builder{
		Id:         store.UUIDToString(r.ID),
		ActivityId: store.UUIDToString(r.ActivityID),
		Title:      r.Title,
		Slug:       r.Slug,
		Excerpt:    Excerpt(st.excerpt, excerptLength),
		Date:       store.FormatDate(r.Date),
		Stats:      stats.Build(),
		Published:  r.Published,
		HasTrack:   r.HasTrack,
		Tags:       tagsToProto(r.Tags),
		CreateTime: timestamppb.New(r.CreateTime),
		UpdateTime: timestamppb.New(r.UpdateTime),
	}
	if !r.PublishTime.IsZero() {
		b.PublishTime = timestamppb.New(r.PublishTime)
	}
	if profile != nil {
		b.Url = s.reportURL(profile.Handle, r.Slug)
	}
	if cover := trips.CoverPhoto(r, st.photos); cover != nil {
		b.CoverPhotoId = store.UUIDToString(cover.ID)
	}
	photoIDs := map[pgtype.UUID][]string{}
	for _, p := range st.photos {
		b.Photos = append(b.Photos, s.photoToProto(p))
		photoIDs[p.SectionID] = append(photoIDs[p.SectionID], store.UUIDToString(p.ID))
	}
	for _, sec := range st.sections {
		b.Sections = append(b.Sections, sectionToProto(sec, photoIDs[sec.ID]))
	}
	return b.Build()
}

// tagName returns the stored name of a tag, e.g. "beginner_friendly".
func tagName(tag tripsv1.TripTag) string {
	return strings.ToLower(strings.TrimPrefix(tag.String(), "TRIP_TAG_"))
}

func tagsToProto(names []string) []tripsv1.TripTag {
	tags := make([]tripsv1.TripTag, 0, len(names))
	for _, name := range names {
		if value, ok := tripsv1.TripTag_value["TRIP_TAG_"+strings.ToUpper(name)]; ok {
			tags = append(tags, tripsv1.TripTag(value))
		}
	}
	return tags
}

// exclusiveTags are groups of tags of which a report has at most one.
var exclusiveTags = [][]tripsv1.TripTag{
	{tripsv1.TripTag_TRIP_TAG_BEGINNER_FRIENDLY, tripsv1.TripTag_TRIP_TAG_MODERATE, tripsv1.TripTag_TRIP_TAG_CHALLENGING},
	{tripsv1.TripTag_TRIP_TAG_LOOP, tripsv1.TripTag_TRIP_TAG_OUT_AND_BACK, tripsv1.TripTag_TRIP_TAG_POINT_TO_POINT},
}

// tagNames validates tags and returns their stored names, without
// duplicates and in the order of TripTag.
func tagNames(tags []tripsv1.TripTag) ([]string, error) {
	set := make(map[tripsv1.TripTag]bool, len(tags))
	for _, tag := range tags {
		if _, ok := tripsv1.TripTag_name[int32(tag)]; !ok || tag == tripsv1.TripTag_TRIP_TAG_UNSPECIFIED {
			return nil, fmt.Errorf("unknown tag %d", tag)
		}
		set[tag] = true
	}
	for _, group := range exclusiveTags {
		var found []string
		for _, tag := range group {
			if set[tag] {
				found = append(found, tagName(tag))
			}
		}
		if len(found) > 1 {
			return nil, fmt.Errorf("a report has at most one of the tags %s", strings.Join(found, " and "))
		}
	}
	names := []string{}
	for _, tag := range slices.Sorted(maps.Keys(set)) {
		names = append(names, tagName(tag))
	}
	return names, nil
}

// sectionToProto converts a section with the IDs of its photos.
func sectionToProto(sec *trips.Section, photoIDs []string) *tripsv1.TripSection {
	b := tripsv1.TripSection_builder{
		Id:       store.UUIDToString(sec.ID),
		Title:    sec.Title,
		Body:     sec.Body,
		PhotoIds: photoIDs,
	}
	if sec.HasDistances {
		b.StartDistanceMeters = &sec.StartDistanceMeters
		b.EndDistanceMeters = &sec.EndDistanceMeters
	}
	return b.Build()
}

func (s *Service) photoToProto(p *trips.Photo) *tripsv1.TripPhoto {
	id := store.UUIDToString(p.ID)
	b := tripsv1.TripPhoto_builder{
		Id:           id,
		Caption:      p.Caption,
		Width:        p.Width,
		Height:       p.Height,
		Url:          s.photoURL(id, photos.SizeLarge),
		ThumbnailUrl: s.photoURL(id, photos.SizeThumb),
	}
	if loc := p.Location; loc != nil {
		b.Location = geov1.LatLng_builder{Lat: loc.Lat, Lng: loc.Lng}.Build()
		b.LocationSource = LocationSourceToProto(loc.Source)
		if loc.HasTrackDistanceMeters {
			b.TrackDistanceMeters = &loc.TrackDistanceMeters
		}
	}
	if !p.TakenTime.IsZero() {
		b.TakenTime = timestamppb.New(p.TakenTime)
	}
	return b.Build()
}

func (s *Service) profileToProto(p *trips.Profile) *tripsv1.Profile {
	return tripsv1.Profile_builder{
		Handle:      p.Handle,
		DisplayName: p.DisplayName,
		Url:         s.profileURL(p.Handle),
	}.Build()
}

func (s *Service) profileURL(handle string) string {
	return s.baseURL + ProfilePath + handle
}

func (s *Service) reportURL(handle, slug string) string {
	return s.baseURL + ProfilePath + handle + "/" + slug
}

func (s *Service) photoURL(id, size string) string {
	return s.baseURL + PhotoPath + id + "/" + size + ".jpg"
}
