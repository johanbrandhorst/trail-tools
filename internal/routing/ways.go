package routing

import (
	"bufio"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"slices"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/elevation"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// ImportOSM loads OpenStreetMap data into the routing graph as a region,
// with elevations from the DEM, and returns the number of edges imported.
// Importing a region again replaces it.
func ImportOSM(ctx context.Context, log *slog.Logger, db *store.DB, region string, bounds geo.Bounds, data *OSM, dem *elevation.DEM) (int, error) {
	start := time.Now()
	ways, nodes := data.Ways, data.Nodes
	elevations, err := dem.Elevations(ctx, nodes.Lats, nodes.Lngs)
	if err != nil {
		return 0, fmt.Errorf("failed to look up elevations: %w", err)
	}
	features, err := featureElevations(ctx, data.Features, dem)
	if err != nil {
		return 0, err
	}
	if bounds == (geo.Bounds{}) {
		bounds = geo.Bounds{MinLat: 90, MinLng: 180, MaxLat: -90, MaxLng: -180}
		for i := range nodes.IDs {
			if !math.IsNaN(nodes.Lats[i]) {
				bounds.MinLat, bounds.MaxLat = math.Min(bounds.MinLat, nodes.Lats[i]), math.Max(bounds.MaxLat, nodes.Lats[i])
				bounds.MinLng, bounds.MaxLng = math.Min(bounds.MinLng, nodes.Lngs[i]), math.Max(bounds.MaxLng, nodes.Lngs[i])
			}
		}
	}
	count, err := SaveRegion(ctx, db, region, bounds, BuildEdges(ways, nodes, elevations, bounds), features)
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, fmt.Errorf("there are no foot accessible ways within the bounds")
	}
	log.InfoContext(ctx, "Imported the region", slog.String("region", region), slog.Int("edges", count),
		slog.Int("points_of_interest", len(features)), slog.Duration("duration", time.Since(start)))
	return count, nil
}

// featureElevations returns the features with the elevations of those
// without an ele tag from the DEM.
func featureElevations(ctx context.Context, features []Feature, dem *elevation.DEM) ([]Feature, error) {
	features = slices.Clone(features)
	var lats, lngs []float64
	var missing []int
	for i, f := range features {
		if math.IsNaN(f.Point.Ele) {
			lats, lngs = append(lats, f.Point.Lat), append(lngs, f.Point.Lng)
			missing = append(missing, i)
		}
	}
	elevations, err := dem.Elevations(ctx, lats, lngs)
	if err != nil {
		return nil, fmt.Errorf("failed to look up the elevations of points of interest: %w", err)
	}
	for j, i := range missing {
		features[i].Point.Ele = math.Round(elevations[j])
	}
	return features, nil
}

// osmMagic starts OpenStreetMap data files.
const osmMagic = "TTOSM1"

// coordinateScale is the precision coordinates are stored with, about 10 cm.
const coordinateScale = 1e6

// WriteOSM writes OpenStreetMap data gzipped, compactly: IDs and coordinates
// are stored as differences to the ones before, and node references as
// differences between node indexes.
func WriteOSM(w io.Writer, data *OSM) error {
	nodes := data.Nodes
	zw, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(zw)
	var buf [binary.MaxVarintLen64]byte
	varint := func(v int64) {
		n := binary.PutVarint(buf[:], v)
		_, _ = bw.Write(buf[:n])
	}
	str := func(s string) {
		varint(int64(len(s)))
		_, _ = bw.WriteString(s)
	}
	_, _ = bw.WriteString(osmMagic)

	varint(int64(len(nodes.IDs)))
	var prevID, prevLat, prevLng int64
	for i, id := range nodes.IDs {
		lat, lng := int64(math.Round(nodes.Lats[i]*coordinateScale)), int64(math.Round(nodes.Lngs[i]*coordinateScale))
		varint(id - prevID)
		varint(lat - prevLat)
		varint(lng - prevLng)
		prevID, prevLat, prevLng = id, lat, lng
	}

	sorted := slices.Clone(data.Ways)
	slices.SortFunc(sorted, func(a, b Way) int { return cmp.Compare(a.ID, b.ID) })
	varint(int64(len(sorted)))
	prevID = 0
	prevIndex := int64(0)
	for _, way := range sorted {
		varint(way.ID - prevID)
		prevID = way.ID
		str(way.Highway)
		str(way.SacScale)
		str(string(way.Terrain))
		str(way.Name)
		str(way.Surface)
		varint(int64(len(way.Nodes)))
		for _, id := range way.Nodes {
			index := int64(nodes.find(id))
			if index < 0 {
				return fmt.Errorf("way %d refers to the missing node %d", way.ID, id)
			}
			varint(index - prevIndex)
			prevIndex = index
		}
	}

	features := slices.Clone(data.Features)
	slices.SortFunc(features, func(a, b Feature) int { return cmp.Compare(a.ID, b.ID) })
	varint(int64(len(features)))
	prevID, prevLat, prevLng = 0, 0, 0
	for _, f := range features {
		lat, lng := int64(math.Round(f.Point.Lat*coordinateScale)), int64(math.Round(f.Point.Lng*coordinateScale))
		varint(f.ID - prevID)
		varint(lat - prevLat)
		varint(lng - prevLng)
		prevID, prevLat, prevLng = f.ID, lat, lng
		// Flags: 1 for a way, 2 if the elevation is known.
		flags := int64(0)
		if f.Way {
			flags |= 1
		}
		if !math.IsNaN(f.Point.Ele) {
			flags |= 2
		}
		varint(flags)
		if flags&2 != 0 {
			varint(int64(math.Round(f.Point.Ele)))
		}
		str(string(f.Kind))
		str(f.Name)
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return zw.Close()
}

// ReadOSM reads OpenStreetMap data written by WriteOSM.
func ReadOSM(r io.Reader) (*OSM, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read the OpenStreetMap data: %w", err)
	}
	br := bufio.NewReader(zr)
	magic := make([]byte, len(osmMagic))
	if _, err := io.ReadFull(br, magic); err != nil || string(magic) != osmMagic {
		return nil, errors.New("not an OpenStreetMap data file")
	}
	var readErr error
	varint := func() int64 {
		v, err := binary.ReadVarint(br)
		if err != nil && readErr == nil {
			readErr = err
		}
		return v
	}
	count := func() int {
		n := varint()
		if n < 0 || n > 1<<26 {
			if readErr == nil {
				readErr = errors.New("invalid count")
			}
			return 0
		}
		return int(n)
	}
	str := func() string {
		b := make([]byte, min(count(), 1<<10))
		if _, err := io.ReadFull(br, b); err != nil && readErr == nil {
			readErr = err
		}
		return string(b)
	}
	failed := func() (*OSM, error) {
		return nil, fmt.Errorf("failed to read the OpenStreetMap data: %w", readErr)
	}

	n := count()
	nodes := &Nodes{IDs: make([]int64, n), Lats: make([]float64, n), Lngs: make([]float64, n)}
	var id, lat, lng int64
	for i := range n {
		id += varint()
		lat += varint()
		lng += varint()
		nodes.IDs[i], nodes.Lats[i], nodes.Lngs[i] = id, float64(lat)/coordinateScale, float64(lng)/coordinateScale
	}
	ways := make([]Way, count())
	id = 0
	index := int64(0)
	for i := range ways {
		id += varint()
		way := Way{ID: id, Highway: str(), SacScale: str(), Terrain: geo.Terrain(str()), Name: str(), Surface: str()}
		way.Nodes = make([]int64, count())
		for j := range way.Nodes {
			index += varint()
			if index < 0 || index >= int64(n) {
				return nil, errors.New("a way refers to a missing node")
			}
			way.Nodes[j] = nodes.IDs[index]
		}
		ways[i] = way
		if readErr != nil {
			return failed()
		}
	}
	features := make([]Feature, count())
	id, lat, lng = 0, 0, 0
	for i := range features {
		id += varint()
		lat += varint()
		lng += varint()
		f := Feature{ID: id, Point: geo.Point{Lat: float64(lat) / coordinateScale, Lng: float64(lng) / coordinateScale, Ele: math.NaN()}}
		flags := varint()
		f.Way = flags&1 != 0
		if flags&2 != 0 {
			f.Point.Ele = float64(varint())
		}
		f.Kind = geo.FeatureKind(str())
		f.Name = str()
		features[i] = f
		if readErr != nil {
			return failed()
		}
	}
	if readErr != nil {
		return failed()
	}
	return &OSM{Ways: ways, Nodes: nodes, Features: features}, nil
}
