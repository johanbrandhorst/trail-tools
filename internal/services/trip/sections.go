package trip

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect/v2"
	tripsv1 "github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/track"
	"github.com/johanbrandhorst/trail-tools/internal/trips"
)

func (s *Service) SetTripSections(ctx context.Context, req *tripsv1.SetTripSectionsRequest) (*tripsv1.SetTripSectionsResponse, error) {
	userID, reportID, err := s.parseReportID(ctx, req.GetReportId())
	if err != nil {
		return nil, err
	}
	sections, err := sectionsFromProto(req.GetSections())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if _, err := s.repo.SetSections(ctx, userID, reportID, sections); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to set sections")
	}
	report, err := s.repo.GetReport(ctx, userID, reportID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get trip report")
	}
	result, err := s.reportWithStory(ctx, report)
	if err != nil {
		return nil, err
	}
	return tripsv1.SetTripSectionsResponse_builder{Report: result}.Build(), nil
}

// sectionsFromProto validates and converts the sections of a report.
func sectionsFromProto(sections []*tripsv1.TripSection) ([]*trips.Section, error) {
	switch {
	case len(sections) == 0:
		return nil, errors.New("a report needs at least one section")
	case len(sections) > maxSections:
		return nil, fmt.Errorf("a report can have at most %d sections", maxSections)
	}
	result := make([]*trips.Section, len(sections))
	length := 0
	for i, sec := range sections {
		title := strings.TrimSpace(sec.GetTitle())
		if err := validateTitle(title); err != nil {
			return nil, err
		}
		length += utf8.RuneCountInString(sec.GetBody())
		if length > maxBodyLength {
			return nil, fmt.Errorf("the text of the sections must be at most %d characters", maxBodyLength)
		}
		result[i] = &trips.Section{Title: title, Body: sec.GetBody()}
		if sec.GetId() != "" {
			id, err := store.StringToUUID(sec.GetId())
			if err != nil {
				return nil, fmt.Errorf("invalid section ID: %w", err)
			}
			result[i].ID = id
		}
		if sec.HasStartDistanceMeters() != sec.HasEndDistanceMeters() {
			return nil, errors.New("set both the start and end distance of a section, or neither")
		}
		if sec.HasStartDistanceMeters() {
			start, end := sec.GetStartDistanceMeters(), sec.GetEndDistanceMeters()
			if math.IsNaN(start) || math.IsInf(end, 0) || start < 0 || !(end > start) {
				return nil, errors.New("a section's end distance must be after its start distance, which can't be negative")
			}
			result[i].StartDistanceMeters, result[i].EndDistanceMeters, result[i].HasDistances = start, end, true
		}
		ids, err := store.UUIDsToPG(sec.GetPhotoIds())
		if err != nil {
			return nil, err
		}
		result[i].PhotoIDs = ids
	}
	if err := checkOverlaps(result); err != nil {
		return nil, err
	}
	return result, nil
}

// checkOverlaps returns an error if the stretches of two sections overlap.
// A stretch may end where another starts. Each part of the track belongs to
// at most one section, so the editor and the public page can show which
// section a point is about.
func checkOverlaps(sections []*trips.Section) error {
	var ranged []int
	for i, sec := range sections {
		if sec.HasDistances {
			ranged = append(ranged, i)
		}
	}
	slices.SortFunc(ranged, func(a, b int) int {
		return cmp.Compare(sections[a].StartDistanceMeters, sections[b].StartDistanceMeters)
	})
	for k := 1; k < len(ranged); k++ {
		prev, next := sections[ranged[k-1]], sections[ranged[k]]
		if prev.EndDistanceMeters > next.StartDistanceMeters {
			first, second := min(ranged[k-1], ranged[k]), max(ranged[k-1], ranged[k])
			return fmt.Errorf("the stretches of sections %d (%s) and %d (%s) overlap; "+
				"make one end where the other starts, or before",
				first+1, formatStretch(sections[first]), second+1, formatStretch(sections[second]))
		}
	}
	return nil
}

// formatStretch formats a section's stretch for error messages, e.g. "km
// 3.2-9.8".
func formatStretch(sec *trips.Section) string {
	return fmt.Sprintf("km %.1f-%.1f", sec.StartDistanceMeters/1000, sec.EndDistanceMeters/1000)
}

func (s *Service) SuggestTripSections(ctx context.Context, req *tripsv1.SuggestTripSectionsRequest) (*tripsv1.SuggestTripSectionsResponse, error) {
	userID, reportID, err := s.parseReportID(ctx, req.GetReportId())
	if err != nil {
		return nil, err
	}
	report, err := s.repo.GetReport(ctx, userID, reportID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get trip report")
	}
	t, err := track.Decode(report.Track)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get track")
	}
	if t == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "the report has no track to suggest sections from")
	}
	eles := make([]float64, len(t.Points))
	for i, p := range t.Points {
		eles[i] = p.Ele
	}
	segs, err := track.SuggestSegments(t.Dists, eles)
	if err != nil {
		if errors.Is(err, track.ErrNoProfile) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, "the report's track has no elevations to suggest sections from")
		}
		return nil, s.toConnectError(ctx, err, "failed to suggest sections")
	}
	photos, err := s.repo.ListPhotos(ctx, reportID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list photos")
	}
	return tripsv1.SuggestTripSectionsResponse_builder{
		Sections: suggestedSections(segs, photos[reportID]),
	}.Build(), nil
}

// suggestedSections returns a section for each segment, with the photos
// taken along it in the order they were taken. Photos without a place on
// the track go first in the first section, in their current order.
func suggestedSections(segs []*track.Segment, photos []*trips.Photo) []*tripsv1.TripSection {
	placed := slices.Clone(photos)
	slices.SortStableFunc(placed, func(a, b *trips.Photo) int {
		return cmp.Compare(photoDistance(a), photoDistance(b))
	})
	ids := make([][]string, len(segs))
	for _, p := range placed {
		i := 0
		if d := photoDistance(p); d >= 0 {
			// The last segment that starts at or before the photo, so photos
			// past the end go in the last one.
			for i+1 < len(segs) && segs[i+1].StartMeters <= d {
				i++
			}
		}
		ids[i] = append(ids[i], store.UUIDToString(p.ID))
	}
	sections := make([]*tripsv1.TripSection, len(segs))
	for i, seg := range segs {
		sections[i] = tripsv1.TripSection_builder{
			Title:               seg.Name,
			StartDistanceMeters: &seg.StartMeters,
			EndDistanceMeters:   &seg.EndMeters,
			PhotoIds:            ids[i],
		}.Build()
	}
	return sections
}

// photoDistance returns the distance along the track where a photo was
// taken, or -1 if it isn't known.
func photoDistance(p *trips.Photo) float64 {
	if p.Location == nil || !p.Location.HasTrackDistanceMeters {
		return -1
	}
	return p.Location.TrackDistanceMeters
}
