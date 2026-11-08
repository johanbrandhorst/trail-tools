package geo

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/bs-tools/trailrunn.ing/internal/authn"
	"github.com/bs-tools/trailrunn.ing/internal/slope"
)

// SlopeTilePath is where slope angle tiles are served, as
// /tiles/slope/{z}/{x}/{y}.png.
const SlopeTilePath = "/tiles/slope/"

const (
	// maxSlopeRenders is how many slope tiles render at once. A map asks
	// for a screen of tiles together, and each takes a few megabytes while
	// it renders, so the others wait rather than fill the memory.
	maxSlopeRenders = 4
	// slopeTileMaxAgeSeconds is how long browsers keep a tile: the
	// elevation model doesn't change, so a month.
	slopeTileMaxAgeSeconds = 30 * 24 * 60 * 60
)

// slopeRenders limits the slope tiles rendering at once.
var slopeRenders = make(chan struct{}, maxSlopeRenders)

// SlopeTileHandler serves slope angle tiles for signed in users, rendered
// from the elevation model. Tiles outside the zoom levels slope renders at
// aren't found, so maps show nothing there.
func (s *Service) SlopeTileHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Rendering costs the server's time, so it isn't offered to
		// anyone on the internet.
		if _, ok := authn.GetUserID(r.Context()); !ok {
			http.Error(w, "sign in to see slope angles", http.StatusUnauthorized)
			return
		}
		z, x, y, err := parseTile(strings.TrimPrefix(r.URL.Path, SlopeTilePath))
		if err != nil || !slope.ValidTile(z, x, y) {
			http.Error(w, fmt.Sprintf("slope tiles are /tiles/slope/{z}/{x}/{y}.png at zooms %d to %d", slope.MinZoom, slope.MaxZoom), http.StatusNotFound)
			return
		}
		select {
		case slopeRenders <- struct{}{}:
			defer func() { <-slopeRenders }()
		case <-r.Context().Done():
			return
		}
		elevations, err := s.mapData.Elevations(r.Context(), slope.GridPoints(z, x, y))
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			s.report.Report(r.Context(), "Failed to look up the elevations of a slope tile", err,
				slog.Int("z", z), slog.Int("x", x), slog.Int("y", y))
			http.Error(w, "the elevation data is unavailable, try again later", http.StatusServiceUnavailable)
			return
		}
		data, err := slope.Render(z, x, y, elevations)
		if err != nil {
			s.report.Report(r.Context(), "Failed to render a slope tile", err)
			http.Error(w, "failed to render the tile", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		// Private, as only signed in users may get tiles.
		w.Header().Set("Cache-Control", fmt.Sprintf("private, max-age=%d", slopeTileMaxAgeSeconds))
		_, _ = w.Write(data)
	})
}

// parseTile parses a tile's path, "{z}/{x}/{y}.png".
func parseTile(path string) (z, x, y int, err error) {
	name, ok := strings.CutSuffix(path, ".png")
	if !ok {
		return 0, 0, 0, fmt.Errorf("not a PNG: %q", path)
	}
	parts := strings.Split(name, "/")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("not z/x/y: %q", path)
	}
	var values [3]int
	for i, part := range parts {
		values[i], err = strconv.Atoi(part)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("not z/x/y: %q", path)
		}
	}
	return values[0], values[1], values[2], nil
}
