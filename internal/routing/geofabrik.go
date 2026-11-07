package routing

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

// DefaultGeofabrikIndexURL is the index of Geofabrik's OpenStreetMap
// extracts, with the area each covers.
const DefaultGeofabrikIndexURL = "https://download.geofabrik.de/index-v1.json"

// geofabrikIndexTTL is how long the index is cached. Geofabrik rarely adds
// extracts.
const geofabrikIndexTTL = 24 * time.Hour

const (
	// maxExtracts is the most extracts an import reads, as each is
	// downloaded and read in full.
	maxExtracts = 4
	// sampleSpacing is the spacing in degrees of the points sampled across
	// an area to find the extracts covering it, about a kilometer, so that
	// small countries such as Liechtenstein aren't missed.
	sampleSpacing = 0.01
	// maxSamples is the most points sampled along each side of an area,
	// spacing them further apart in large areas.
	maxSamples = 101
)

var (
	// ErrNoExtract is returned when no extract covers an area.
	ErrNoExtract = errors.New("no OpenStreetMap extract covers the area")
	// ErrTooManyExtracts is returned when an area needs more than
	// maxExtracts extracts.
	ErrTooManyExtracts = fmt.Errorf("the area spans more than %d OpenStreetMap extracts, select a smaller area", maxExtracts)
)

// Extract is an OpenStreetMap extract that can be imported.
type Extract struct {
	// The name, e.g. "Switzerland".
	Name string
	// The URL of the PBF file.
	URL string
}

// ring is a closed line of [lng, lat] positions.
type ring [][2]float64

// polygon is an outer ring followed by any holes in it.
type polygon []ring

// indexedExtract is an extract in the index with the area it covers.
type indexedExtract struct {
	Extract
	polygons []polygon
	// The bounding box of the polygons.
	bounds geo.Bounds
	// The size of the area, in square degrees at the equator.
	size float64
}

// contains reports whether the extract's area contains p.
func (x *indexedExtract) contains(p geo.Point) bool {
	if !x.bounds.Contains(p) {
		return false
	}
	for _, poly := range x.polygons {
		if !poly[0].contains(p) {
			continue
		}
		inHole := false
		for _, hole := range poly[1:] {
			if hole.contains(p) {
				inHole = true
				break
			}
		}
		if !inHole {
			return true
		}
	}
	return false
}

// contains reports whether p is inside the ring, by counting the edges a
// line from p to the east crosses.
func (r ring) contains(p geo.Point) bool {
	inside := false
	for i, j := 0, len(r)-1; i < len(r); j, i = i, i+1 {
		a, b := r[i], r[j]
		if (a[1] > p.Lat) != (b[1] > p.Lat) && p.Lng < a[0]+(p.Lat-a[1])*(b[0]-a[0])/(b[1]-a[1]) {
			inside = !inside
		}
	}
	return inside
}

// size returns the area of the ring in square degrees at the equator, in
// the sinusoidal projection, which preserves areas.
func (r ring) size() float64 {
	sum := 0.0
	for i, j := 0, len(r)-1; i < len(r); j, i = i, i+1 {
		xi := r[i][0] * math.Cos(r[i][1]*math.Pi/180)
		xj := r[j][0] * math.Cos(r[j][1]*math.Pi/180)
		sum += xj*r[i][1] - xi*r[j][1]
	}
	return math.Abs(sum) / 2
}

// Extracts finds Geofabrik extracts covering an area. The index is fetched
// when first needed and cached.
type Extracts struct {
	url    string
	client *http.Client

	mu       sync.Mutex
	extracts []indexedExtract
	fetched  time.Time
}

// NewExtracts creates a new Extracts using the index at url, fetched with
// client.
func NewExtracts(url string, client *http.Client) *Extracts {
	return &Extracts{url: url, client: client}
}

// Find returns the extracts to read to import the bounds: at points across
// the bounds, the smallest extract whose area contains each, as importing it
// reads the least data. Areas across borders need an extract on each side.
// Points no extract contains, such as at sea, are left out.
//
// Extracts are matched on their outlines rather than their bounding boxes,
// which overlap: Switzerland's contains Liechtenstein, which its extract
// doesn't. The extracts are sorted by how much of the bounds they cover,
// most first.
func (e *Extracts) Find(ctx context.Context, bounds geo.Bounds) ([]Extract, error) {
	extracts, err := e.index(ctx)
	if err != nil {
		return nil, err
	}
	// The extracts that may cover part of the bounds, smallest first.
	var candidates []*indexedExtract
	for i := range extracts {
		b := extracts[i].bounds
		if b.MinLat <= bounds.MaxLat && b.MaxLat >= bounds.MinLat && b.MinLng <= bounds.MaxLng && b.MaxLng >= bounds.MinLng {
			candidates = append(candidates, &extracts[i])
		}
	}
	slices.SortStableFunc(candidates, func(a, b *indexedExtract) int { return cmp.Compare(a.size, b.size) })
	samples := map[*indexedExtract]int{}
	latSteps, lngSteps := sampleSteps(bounds.MaxLat-bounds.MinLat), sampleSteps(bounds.MaxLng-bounds.MinLng)
	for i := range latSteps + 1 {
		for j := range lngSteps + 1 {
			p := geo.Point{
				Lat: bounds.MinLat + (bounds.MaxLat-bounds.MinLat)*float64(i)/float64(latSteps),
				Lng: bounds.MinLng + (bounds.MaxLng-bounds.MinLng)*float64(j)/float64(lngSteps),
			}
			for _, x := range candidates {
				if x.contains(p) {
					samples[x]++
					break
				}
			}
		}
	}
	if len(samples) == 0 {
		return nil, ErrNoExtract
	}
	if len(samples) > maxExtracts {
		return nil, ErrTooManyExtracts
	}
	found := make([]*indexedExtract, 0, len(samples))
	for x := range samples {
		found = append(found, x)
	}
	slices.SortFunc(found, func(a, b *indexedExtract) int {
		return cmp.Or(cmp.Compare(samples[b], samples[a]), cmp.Compare(a.Name, b.Name))
	})
	result := make([]Extract, len(found))
	for i, x := range found {
		result[i] = x.Extract
	}
	return result, nil
}

// sampleSteps returns the number of steps between the points sampled along
// a side of an area, span degrees long. The ends are sampled too.
func sampleSteps(span float64) int {
	return max(1, min(maxSamples-1, int(math.Ceil(span/sampleSpacing))))
}

func (e *Extracts) index(ctx context.Context) ([]indexedExtract, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.extracts != nil && time.Since(e.fetched) < geofabrikIndexTTL {
		return e.extracts, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch the extract index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch the extract index: status %d", resp.StatusCode)
	}
	extracts, err := parseGeofabrikIndex(resp.Body)
	if err != nil {
		return nil, err
	}
	e.extracts, e.fetched = extracts, time.Now()
	return extracts, nil
}

// parseGeofabrikIndex parses Geofabrik's GeoJSON index of extracts, whose
// areas are Polygon or MultiPolygon geometries.
func parseGeofabrikIndex(r io.Reader) ([]indexedExtract, error) {
	var index struct {
		Features []struct {
			Properties struct {
				Name string `json:"name"`
				URLs struct {
					PBF string `json:"pbf"`
				} `json:"urls"`
			} `json:"properties"`
			Geometry struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.NewDecoder(r).Decode(&index); err != nil {
		return nil, fmt.Errorf("failed to parse the extract index: %w", err)
	}
	var extracts []indexedExtract
	for _, f := range index.Features {
		if f.Properties.URLs.PBF == "" {
			continue
		}
		var polygons []polygon
		switch f.Geometry.Type {
		case "Polygon":
			var p polygon
			if err := json.Unmarshal(f.Geometry.Coordinates, &p); err != nil {
				continue
			}
			polygons = []polygon{p}
		case "MultiPolygon":
			if err := json.Unmarshal(f.Geometry.Coordinates, &polygons); err != nil {
				continue
			}
		}
		x := indexedExtract{
			// Some names have their English translation on a new line, e.g.
			// "Østlandet<br />(Eastern Norway)".
			Extract: Extract{Name: strings.ReplaceAll(f.Properties.Name, "<br />", " "), URL: f.Properties.URLs.PBF},
			bounds:  geo.Bounds{MinLat: 90, MinLng: 180, MaxLat: -90, MaxLng: -180},
		}
		for _, p := range polygons {
			if len(p) == 0 || len(p[0]) < 3 {
				continue
			}
			x.polygons = append(x.polygons, p)
			for _, pos := range p[0] {
				x.bounds.MinLat, x.bounds.MaxLat = math.Min(x.bounds.MinLat, pos[1]), math.Max(x.bounds.MaxLat, pos[1])
				x.bounds.MinLng, x.bounds.MaxLng = math.Min(x.bounds.MinLng, pos[0]), math.Max(x.bounds.MaxLng, pos[0])
			}
			x.size += p[0].size()
			for _, hole := range p[1:] {
				x.size -= hole.size()
			}
		}
		if len(x.polygons) == 0 {
			continue
		}
		extracts = append(extracts, x)
	}
	return extracts, nil
}
