package routing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	"os"
	"runtime"
	"slices"

	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/qedus/osmpbf"
)

// footHighways are the highway types that can be run on. Busier roads are
// included so routes can connect trails, but cost more to route along.
var footHighways = map[string]bool{
	"path": true, "footway": true, "track": true, "steps": true, "bridleway": true,
	"pedestrian": true, "living_street": true, "cycleway": true, "service": true,
	"residential": true, "unclassified": true, "road": true,
	"tertiary": true, "tertiary_link": true, "secondary": true, "secondary_link": true,
	"primary": true, "primary_link": true,
}

// isFootWay reports whether a way can be run on, from its tags. Sidewalks,
// crossings, driveways and parking aisles are left out to keep the graph
// small, as the roads they belong to can be run on.
func isFootWay(tags map[string]string) bool {
	if !footHighways[tags["highway"]] || tags["area"] == "yes" {
		return false
	}
	switch tags["footway"] {
	case "sidewalk", "crossing", "traffic_island":
		return false
	}
	switch tags["service"] {
	case "driveway", "parking_aisle", "drive-through", "emergency_access":
		return false
	}
	foot := tags["foot"]
	switch foot {
	case "no", "private", "use_sidepath":
		return false
	case "yes", "designated", "permissive":
		return true
	}
	switch tags["access"] {
	case "no", "private":
		return false
	}
	return true
}

// Way is a foot accessible OpenStreetMap way.
type Way struct {
	ID    int64
	Nodes []int64
	// The tags that affect routing.
	Highway, SacScale string
	Terrain           geo.Terrain
	// The tags that describe the way, for finding trails.
	Name, Surface string
}

func wayFromOSM(w *osmpbf.Way) Way {
	return Way{
		ID:       w.ID,
		Nodes:    w.NodeIDs,
		Highway:  w.Tags["highway"],
		SacScale: w.Tags["sac_scale"],
		Terrain:  geo.ClassifyTerrain(w.Tags),
		Name:     w.Tags["name"],
		Surface:  w.Tags["surface"],
	}
}

// Feature is a point of interest from OpenStreetMap, such as a peak, a
// spring or a car park.
type Feature struct {
	// The OpenStreetMap ID of the node or way.
	ID int64
	// Whether the feature is a way, such as the outline of a car park,
	// rather than a node. A way is located at the center of its bounding
	// box.
	Way  bool
	Kind geo.FeatureKind
	Name string
	// The location. The elevation is NaN if it's unknown.
	Point geo.Point
}

// featureFromTags returns the feature an OpenStreetMap node or way is, if
// any, without its location.
func featureFromTags(id int64, way bool, tags map[string]string) (Feature, bool) {
	kind := geo.ClassifyFeature(tags)
	if kind == geo.FeatureNone {
		return Feature{}, false
	}
	f := Feature{ID: id, Way: way, Kind: kind, Name: tags["name"], Point: geo.Point{Ele: math.NaN()}}
	if ele, ok := geo.ParseElevation(tags["ele"]); ok {
		f.Point.Ele = ele
	}
	return f, true
}

// OSM is the OpenStreetMap data of an area: its foot accessible ways, the
// nodes they use, its points of interest and its forests.
type OSM struct {
	Ways     []Way
	Nodes    *Nodes
	Features []Feature
	// Not written by WriteOSM, as the demo region doesn't need them.
	Forests []Forest
}

// Nodes are the coordinates of OpenStreetMap nodes, sorted by ID.
type Nodes struct {
	IDs        []int64
	Lats, Lngs []float64
}

// find returns the index of a node, or -1 if it is missing.
func (n *Nodes) find(id int64) int {
	i, ok := slices.BinarySearch(n.IDs, id)
	if !ok {
		return -1
	}
	return i
}

// ReadPBF reads the foot accessible ways of an OpenStreetMap PBF extract,
// the coordinates of their nodes, its points of interest and its forests.
// The extract is read three times: first for the forest relations, which
// come last, then for the ways, and then for the nodes, which come before
// the ways that use them.
//
// If bounds are set, only ways with a node inside them, and points of
// interest inside them, are read. The first read then also finds the nodes
// inside the bounds, so memory grows with the area rather than the extract.
// Forests are read if one of their ways has a node inside the bounds, which
// misses the rare forest that surrounds the bounds without one.
func ReadPBF(ctx context.Context, path string, bounds geo.Bounds) (*OSM, error) {
	return readOSM(bounds, func(handle func(any)) error {
		return scanPBF(ctx, path, handle)
	})
}

// readOSM reads OpenStreetMap data with scan, which passes each node, way
// and relation to handle, in the order of a PBF extract: nodes, then ways,
// then relations.
func readOSM(bounds geo.Bounds, scan func(handle func(any)) error) (*OSM, error) {
	bounded := bounds != (geo.Bounds{})
	var inside []int64
	var relations []*forestRelation
	// The forest relations each outer way belongs to.
	relationsOf := make(map[int64][]*forestRelation)
	err := scan(func(v any) {
		switch v := v.(type) {
		case *osmpbf.Node:
			if bounded && bounds.Contains(geo.Point{Lat: v.Lat, Lng: v.Lon}) {
				inside = append(inside, v.ID)
			}
		case *osmpbf.Relation:
			if f, ok := forestRelationFromOSM(v); ok {
				relations = append(relations, &f)
				for _, id := range f.ways {
					relationsOf[id] = append(relationsOf[id], &f)
				}
			}
		}
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(inside)
	entersBounds := func(nodes []int64) bool {
		for _, id := range nodes {
			if _, ok := slices.BinarySearch(inside, id); ok {
				return true
			}
		}
		return false
	}
	var ways []Way
	// Ways that are points of interest, such as car parks, with their nodes.
	var featureWays []Way
	var wayFeatures []Feature
	// Closed ways that are forests, and the outer ways of forest relations,
	// which are kept whether or not they enter the bounds, as the others of
	// their relation may.
	var forestWays []Way
	memberNodes := make(map[int64][]int64)
	err = scan(func(v any) {
		w, ok := v.(*osmpbf.Way)
		if !ok {
			return
		}
		enters := !bounded || entersBounds(w.NodeIDs)
		if rs := relationsOf[w.ID]; len(rs) > 0 {
			memberNodes[w.ID] = w.NodeIDs
			for _, r := range rs {
				r.entersBounds = r.entersBounds || enters
			}
		}
		if !enters {
			return
		}
		if isForest(w.Tags) && len(w.NodeIDs) >= 4 && w.NodeIDs[0] == w.NodeIDs[len(w.NodeIDs)-1] {
			forestWays = append(forestWays, Way{ID: w.ID, Nodes: w.NodeIDs})
		}
		if isFootWay(w.Tags) {
			ways = append(ways, wayFromOSM(w))
		}
		if f, ok := featureFromTags(w.ID, true, w.Tags); ok {
			featureWays = append(featureWays, Way{ID: w.ID, Nodes: w.NodeIDs})
			wayFeatures = append(wayFeatures, f)
		}
	})
	if err != nil {
		return nil, err
	}
	inside = nil
	// The rings of the forest relations that enter the bounds.
	type relationRing struct {
		id    int64
		nodes []int64
	}
	var relationRings []relationRing
	// The closed ways come first in forestWays, then the relation rings.
	closedForests := len(forestWays)
	for _, r := range relations {
		if !r.entersBounds {
			continue
		}
		members := make([][]int64, 0, len(r.ways))
		for _, id := range r.ways {
			members = append(members, memberNodes[id])
		}
		for _, ring := range assembleRings(members) {
			relationRings = append(relationRings, relationRing{id: r.id, nodes: ring})
			forestWays = append(forestWays, Way{Nodes: ring})
		}
	}
	// Free the members, which can be many, before reading the nodes.
	relationsOf, memberNodes = nil, nil
	nodes := newNodes(ways)
	featureNodes := newNodes(featureWays)
	forestNodes := newNodes(forestWays)
	var features []Feature
	err = scan(func(v any) {
		n, ok := v.(*osmpbf.Node)
		if !ok {
			return
		}
		if i := nodes.find(n.ID); i >= 0 {
			nodes.Lats[i], nodes.Lngs[i] = n.Lat, n.Lon
		}
		if i := featureNodes.find(n.ID); i >= 0 {
			featureNodes.Lats[i], featureNodes.Lngs[i] = n.Lat, n.Lon
		}
		if i := forestNodes.find(n.ID); i >= 0 {
			forestNodes.Lats[i], forestNodes.Lngs[i] = n.Lat, n.Lon
		}
		p := geo.Point{Lat: n.Lat, Lng: n.Lon}
		if f, ok := featureFromTags(n.ID, false, n.Tags); ok && (!bounded || bounds.Contains(p)) {
			f.Point.Lat, f.Point.Lng = p.Lat, p.Lng
			features = append(features, f)
		}
	})
	if err != nil {
		return nil, err
	}
	for i, w := range featureWays {
		f := wayFeatures[i]
		center, ok := featureNodes.center(w.Nodes)
		if ok && (!bounded || bounds.Contains(center)) {
			f.Point.Lat, f.Point.Lng = center.Lat, center.Lng
			features = append(features, f)
		}
	}
	var forests []Forest
	for _, w := range forestWays[:closedForests] {
		if ring, ok := ringPoints(w.Nodes, forestNodes); ok {
			forests = append(forests, Forest{ID: w.ID, Ring: ring})
		}
	}
	for _, r := range relationRings {
		if ring, ok := ringPoints(r.nodes, forestNodes); ok && overlaps(ring, bounds) {
			forests = append(forests, Forest{ID: r.id, Relation: true, Ring: ring})
		}
	}
	return &OSM{Ways: ways, Nodes: nodes, Features: features, Forests: forests}, nil
}

// newNodes returns the nodes used by ways, without coordinates.
func newNodes(ways []Way) *Nodes {
	var ids []int64
	for _, w := range ways {
		ids = append(ids, w.Nodes...)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	nodes := &Nodes{IDs: ids, Lats: make([]float64, len(ids)), Lngs: make([]float64, len(ids))}
	for i := range ids {
		nodes.Lats[i], nodes.Lngs[i] = math.NaN(), math.NaN()
	}
	return nodes
}

// center returns the center of the bounding box of nodes, leaving out those
// without coordinates. It returns false if none have coordinates.
func (n *Nodes) center(ids []int64) (geo.Point, bool) {
	b := geo.Bounds{MinLat: 90, MinLng: 180, MaxLat: -90, MaxLng: -180}
	found := false
	for _, id := range ids {
		i := n.find(id)
		if i < 0 || math.IsNaN(n.Lats[i]) {
			continue
		}
		found = true
		b.MinLat, b.MaxLat = math.Min(b.MinLat, n.Lats[i]), math.Max(b.MaxLat, n.Lats[i])
		b.MinLng, b.MaxLng = math.Min(b.MinLng, n.Lngs[i]), math.Max(b.MaxLng, n.Lngs[i])
	}
	return geo.Point{Lat: (b.MinLat + b.MaxLat) / 2, Lng: (b.MinLng + b.MaxLng) / 2}, found
}

// MergeExtracts merges the data read from an extract into that read from
// other extracts. Neighboring extracts overlap at their borders, so ways and
// features already read are left out. OpenStreetMap IDs are global, so nodes
// are kept once, with coordinates from whichever extract has them.
func MergeExtracts(data, more *OSM) *OSM {
	ways := data.Ways
	seen := make(map[int64]bool, len(ways))
	for _, w := range ways {
		seen[w.ID] = true
	}
	for _, w := range more.Ways {
		if !seen[w.ID] {
			seen[w.ID] = true
			ways = append(ways, w)
		}
	}
	features := data.Features
	type featureID struct {
		id  int64
		way bool
	}
	seenFeatures := make(map[featureID]bool, len(features))
	for _, f := range features {
		seenFeatures[featureID{f.ID, f.Way}] = true
	}
	for _, f := range more.Features {
		if id := (featureID{f.ID, f.Way}); !seenFeatures[id] {
			seenFeatures[id] = true
			features = append(features, f)
		}
	}
	forests := data.Forests
	type forestID struct {
		id       int64
		relation bool
	}
	seenForests := make(map[forestID]bool, len(forests))
	for _, f := range forests {
		seenForests[forestID{f.ID, f.Relation}] = true
	}
	for _, f := range more.Forests {
		// A relation has a forest for each ring, all kept from the first
		// extract that has it.
		if id := (forestID{f.ID, f.Relation}); !seenForests[id] {
			forests = append(forests, f)
		}
	}
	nodes, moreNodes := data.Nodes, more.Nodes
	if nodes == nil {
		nodes = &Nodes{}
	}
	// Merge the nodes, both sorted by ID.
	merged := &Nodes{
		IDs:  make([]int64, 0, len(nodes.IDs)+len(moreNodes.IDs)),
		Lats: make([]float64, 0, len(nodes.IDs)+len(moreNodes.IDs)),
		Lngs: make([]float64, 0, len(nodes.IDs)+len(moreNodes.IDs)),
	}
	add := func(n *Nodes, i int) {
		merged.IDs = append(merged.IDs, n.IDs[i])
		merged.Lats = append(merged.Lats, n.Lats[i])
		merged.Lngs = append(merged.Lngs, n.Lngs[i])
	}
	i, j := 0, 0
	for i < len(nodes.IDs) || j < len(moreNodes.IDs) {
		switch {
		case j == len(moreNodes.IDs) || i < len(nodes.IDs) && nodes.IDs[i] < moreNodes.IDs[j]:
			add(nodes, i)
			i++
		case i == len(nodes.IDs) || moreNodes.IDs[j] < nodes.IDs[i]:
			add(moreNodes, j)
			j++
		default:
			// The same node in both, missing from one if its way was cut
			// off at the edge of the extract.
			if math.IsNaN(nodes.Lats[i]) {
				add(moreNodes, j)
			} else {
				add(nodes, i)
			}
			i++
			j++
		}
	}
	return &OSM{Ways: ways, Nodes: merged, Features: features, Forests: forests}
}

func scanPBF(ctx context.Context, path string, handle func(any)) error {
	r, err := os.Open(path)
	if err != nil {
		return err
	}
	defer r.Close()
	d := osmpbf.NewDecoder(r)
	if err := d.Start(runtime.GOMAXPROCS(0)); err != nil {
		return fmt.Errorf("failed to read the OpenStreetMap extract: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		v, err := d.Decode()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to read the OpenStreetMap extract: %w", err)
		}
		handle(v)
	}
}

// Edge is a section of a way between two junctions or ends.
type Edge struct {
	Source, Target int64
	Points         []geo.Point
	Highway        string
	SacScale       string
	Terrain        geo.Terrain
	// The way the edge is a section of, and its tags.
	WayID         int64
	Name, Surface string
}

// simplifyMeters is the tolerance edge geometries are simplified with, to
// save storage.
const simplifyMeters = 1

// BuildEdges splits ways into edges at the nodes where they meet or end, and
// yields them one at a time to limit memory use. Ways without any node
// inside bounds are left out, and so are nodes missing from the extract.
// Points get their elevation from elevations, which has one entry per node,
// NaN if unknown.
func BuildEdges(ways []Way, nodes *Nodes, elevations []float64, bounds geo.Bounds) iter.Seq[Edge] {
	return func(yield func(Edge) bool) {
		buildEdges(ways, nodes, elevations, bounds, yield)
	}
}

func buildEdges(ways []Way, nodes *Nodes, elevations []float64, bounds geo.Bounds, yield func(Edge) bool) {
	// Junctions are nodes used more than once, by different ways or twice by
	// the same way, as in a loop.
	uses := make([]uint8, len(nodes.IDs))
	for _, w := range ways {
		for _, id := range w.Nodes {
			if i := nodes.find(id); i >= 0 && uses[i] < 2 {
				uses[i]++
			}
		}
	}
	for _, w := range ways {
		// The indexes of the way's nodes that have coordinates.
		var idxs []int
		inside := false
		for _, id := range w.Nodes {
			i := nodes.find(id)
			if i < 0 || math.IsNaN(nodes.Lats[i]) {
				continue
			}
			idxs = append(idxs, i)
			if bounds.Contains(geo.Point{Lat: nodes.Lats[i], Lng: nodes.Lngs[i]}) {
				inside = true
			}
		}
		if !inside || len(idxs) < 2 {
			continue
		}
		start := 0
		for k := 1; k < len(idxs); k++ {
			if k < len(idxs)-1 && uses[idxs[k]] < 2 {
				continue
			}
			edge := Edge{
				Source:   nodes.IDs[idxs[start]],
				Target:   nodes.IDs[idxs[k]],
				Highway:  w.Highway,
				SacScale: w.SacScale,
				Terrain:  w.Terrain,
				WayID:    w.ID,
				Name:     w.Name,
				Surface:  w.Surface,
			}
			for _, i := range idxs[start : k+1] {
				edge.Points = append(edge.Points, geo.Point{Lat: nodes.Lats[i], Lng: nodes.Lngs[i], Ele: elevations[i]})
			}
			edge.Points = simplify(edge.Points, simplifyMeters)
			if !yield(edge) {
				return
			}
			start = k
		}
	}
}

// simplify removes points within tolerance meters of the line through their
// neighbors (the Douglas-Peucker algorithm), keeping the ends.
func simplify(points []geo.Point, tolerance float64) []geo.Point {
	if len(points) < 3 {
		return points
	}
	worst, worstIndex := 0.0, 0
	last := len(points) - 1
	for i := 1; i < last; i++ {
		if d := segmentDistance(points[i], points[0], points[last]); d > worst {
			worst, worstIndex = d, i
		}
	}
	if worst <= tolerance {
		return []geo.Point{points[0], points[last]}
	}
	first := simplify(points[:worstIndex+1], tolerance)
	return append(first[:len(first)-1], simplify(points[worstIndex:], tolerance)...)
}

// segmentDistance returns the distance in meters from p to the segment from
// a to b, in a flat projection around p.
func segmentDistance(p, a, b geo.Point) float64 {
	scale := math.Cos(p.Lat * math.Pi / 180)
	ax, ay := (a.Lng-p.Lng)*scale*metersPerDegreeLat, (a.Lat-p.Lat)*metersPerDegreeLat
	bx, by := (b.Lng-p.Lng)*scale*metersPerDegreeLat, (b.Lat-p.Lat)*metersPerDegreeLat
	dx, dy := bx-ax, by-ay
	f := 0.0
	if length := dx*dx + dy*dy; length > 0 {
		f = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/length))
	}
	return math.Hypot(ax+f*dx, ay+f*dy)
}
