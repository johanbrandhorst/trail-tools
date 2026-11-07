package route

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

const (
	// heatmapPaddingMeters is how far around the waypoints of a leg the
	// user's activities are preferred, as routes can stray from the direct
	// line.
	heatmapPaddingMeters = 2000
	// heatmapSimplifyMeters is the tolerance activity tracks are simplified
	// with before routing prefers them.
	heatmapSimplifyMeters = 10
	// metersPerDegreeLat is the approximate length of a degree of latitude.
	metersPerDegreeLat = 111320.0
)

// heatmap is the user's activity tracks around the legs routed with the
// heatmap profile, simplified and clipped to the area.
type heatmap struct {
	tracks [][]geo.Point
}

// loadHeatmap loads the user's activity tracks around the points.
func (s *Service) loadHeatmap(ctx context.Context, userID pgtype.UUID, points []geo.Point) (*heatmap, error) {
	bounds, _ := geo.BoundsOf(points)
	bounds = padBounds(bounds, heatmapPaddingMeters)
	// Routing prefers the tracks of the most recent activities if there are
	// too many to list.
	tracks, _, err := s.activities.ListTracksInBounds(ctx, userID, bounds, time.Time{}, time.Time{}, heatmapSimplifyMeters)
	if err != nil {
		return nil, fmt.Errorf("failed to list activity tracks: %w", err)
	}
	heat := &heatmap{}
	for _, track := range tracks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, part := range geo.Clip(track.Points, bounds) {
			heat.tracks = append(heat.tracks, geo.Simplify(part, heatmapSimplifyMeters))
		}
	}
	return heat, nil
}

// near returns the tracks passing near a leg. It is nil without a heatmap.
func (h *heatmap) near(from, to geo.Point) [][]geo.Point {
	if h == nil {
		return nil
	}
	bounds, _ := geo.BoundsOf([]geo.Point{from, to})
	bounds = padBounds(bounds, heatmapPaddingMeters)
	var tracks [][]geo.Point
	for _, track := range h.tracks {
		if trackBounds, ok := geo.BoundsOf(track); ok && overlaps(trackBounds, bounds) {
			tracks = append(tracks, track)
		}
	}
	return tracks
}

func overlaps(a, b geo.Bounds) bool {
	return a.MinLat <= b.MaxLat && b.MinLat <= a.MaxLat && a.MinLng <= b.MaxLng && b.MinLng <= a.MaxLng
}

// padBounds grows bounds by about padding meters on each side.
func padBounds(b geo.Bounds, padding float64) geo.Bounds {
	midLat := (b.MinLat + b.MaxLat) / 2
	padLat := padding / metersPerDegreeLat
	padLng := padding / (metersPerDegreeLat * math.Max(0.01, math.Cos(midLat*math.Pi/180)))
	return geo.Bounds{
		MinLat: math.Max(-90, b.MinLat-padLat),
		MinLng: math.Max(-180, b.MinLng-padLng),
		MaxLat: math.Min(90, b.MaxLat+padLat),
		MaxLng: math.Min(180, b.MaxLng+padLng),
	}
}
