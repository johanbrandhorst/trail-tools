package routing

import (
	"math"
	"slices"

	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/qedus/osmpbf"
)

// Forest is the outline of a forest or wood from OpenStreetMap: a closed
// way, or an outer ring of a multipolygon relation. Edges whose midpoint is
// inside a forest are marked as in forest, for warnings about high winds.
type Forest struct {
	// The OpenStreetMap ID of the way or relation. A relation with several
	// outer rings has a Forest for each.
	ID       int64
	Relation bool
	// The outline, closed: its last point is its first.
	Ring []geo.Point
}

// isForest reports whether an OpenStreetMap way or relation is a forest or
// wood, from its tags. Both tags are used for areas of trees, managed or not.
func isForest(tags map[string]string) bool {
	return tags["landuse"] == "forest" || tags["natural"] == "wood"
}

// forestRelation is a forest multipolygon relation being read.
type forestRelation struct {
	id int64
	// The IDs of its outer ways.
	ways []int64
	// Whether one of its ways enters the bounds of the import.
	entersBounds bool
}

// forestRelationFromOSM returns the forest an OpenStreetMap relation is, if
// any, with its outer ways. Inner ways, the clearings in a forest, are left
// out: they are usually small, and warning about a run that skirts one is
// better than missing a run in the forest around it.
func forestRelationFromOSM(r *osmpbf.Relation) (forestRelation, bool) {
	if r.Tags["type"] != "multipolygon" || !isForest(r.Tags) {
		return forestRelation{}, false
	}
	f := forestRelation{id: r.ID}
	for _, m := range r.Members {
		if m.Type == osmpbf.WayType && (m.Role == "outer" || m.Role == "") {
			f.ways = append(f.ways, m.ID)
		}
	}
	return f, len(f.ways) > 0
}

// assembleRings joins the ways of a multipolygon, as lists of node IDs, into
// closed rings by their end nodes, as long outlines are split into several
// ways. Ways that don't close a ring, like those cut off at the edge of an
// extract, are left out.
func assembleRings(ways [][]int64) [][]int64 {
	used := make([]bool, len(ways))
	var rings [][]int64
	for i, w := range ways {
		if used[i] || len(w) < 2 {
			continue
		}
		used[i] = true
		ring := slices.Clone(w)
		for ring[0] != ring[len(ring)-1] {
			end := ring[len(ring)-1]
			found := false
			for j, other := range ways {
				if used[j] || len(other) < 2 {
					continue
				}
				switch end {
				case other[0]:
					ring = append(ring, other[1:]...)
				case other[len(other)-1]:
					for k := len(other) - 2; k >= 0; k-- {
						ring = append(ring, other[k])
					}
				default:
					continue
				}
				used[j], found = true, true
				break
			}
			if !found {
				break
			}
		}
		// A ring has at least three distinct nodes and its first again.
		if len(ring) >= 4 && ring[0] == ring[len(ring)-1] {
			rings = append(rings, ring)
		}
	}
	return rings
}

// ringPoints returns the coordinates of a ring of nodes. It returns false if
// a node is missing from the extract, as the ring then can't be closed.
func ringPoints(ids []int64, nodes *Nodes) ([]geo.Point, bool) {
	points := make([]geo.Point, len(ids))
	for i, id := range ids {
		j := nodes.find(id)
		if j < 0 || math.IsNaN(nodes.Lats[j]) {
			return nil, false
		}
		points[i] = geo.Point{Lat: nodes.Lats[j], Lng: nodes.Lngs[j]}
	}
	return points, true
}

// overlaps reports whether the bounding box of points overlaps bounds, or
// bounds are zero.
func overlaps(points []geo.Point, bounds geo.Bounds) bool {
	if bounds == (geo.Bounds{}) {
		return true
	}
	b, ok := geo.BoundsOf(points)
	return ok && b.MinLat <= bounds.MaxLat && b.MaxLat >= bounds.MinLat && b.MinLng <= bounds.MaxLng && b.MaxLng >= bounds.MinLng
}
