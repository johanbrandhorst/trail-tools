package track

import (
	"crypto/sha256"
	"encoding/binary"
	"math"

	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/users"
)

// Zone is a circle, like one around the user's home, within which tracks
// and photos are hidden on public pages.
type Zone struct {
	Center       geo.Point
	RadiusMeters float64
}

// The circle hidden around a privacy zone is centered between these
// fractions of the zone's radius away from the zone's center, and is larger
// by the largest of them, so it always covers the zone.
const (
	minOffsetFraction = 0.25
	maxOffsetFraction = 0.5
)

// HiddenZones returns the circles to hide on public pages for the user's
// privacy zones. Tracks are cut where they cross a circle, so the circle
// through the cuts of tracks from home, three of them being enough, would
// give away the zone's center if it were the circle hidden. Instead each
// circle is centered away from the zone's center, in a direction and by a
// distance derived from the user's secret seed and the zone, so it is the
// same on every page: different offsets could be averaged to find the
// center. Finding the hidden circle tells only that the zone's center is
// somewhere between a quarter and half of its radius from that circle's.
// Moving or resizing a zone moves its circle elsewhere.
func HiddenZones(zones []users.PrivacyZone) []Zone {
	out := make([]Zone, len(zones))
	for i, z := range zones {
		out[i] = hiddenZone(z.Center, float64(z.RadiusMeters), z.Seed)
	}
	return out
}

// hiddenZone returns the circle hidden for a zone, see HiddenZones.
func hiddenZone(center geo.Point, radiusMeters float64, seed [16]byte) Zone {
	h := sha256.New()
	h.Write(seed[:])
	for _, v := range []float64{center.Lat, center.Lng, radiusMeters} {
		h.Write(binary.BigEndian.AppendUint64(nil, math.Float64bits(v)))
	}
	sum := h.Sum(nil)
	// Two uniform numbers in [0, 1) from the hash's first 53 bits each.
	u1 := float64(binary.BigEndian.Uint64(sum[0:8])>>11) / (1 << 53)
	u2 := float64(binary.BigEndian.Uint64(sum[8:16])>>11) / (1 << 53)
	// Uniform over the area of the ring the center may be moved into, so
	// no part of it is likelier.
	minOffset, maxOffset := minOffsetFraction*radiusMeters, maxOffsetFraction*radiusMeters
	offset := math.Sqrt(minOffset*minOffset + u1*(maxOffset*maxOffset-minOffset*minOffset))
	bearing := 2 * math.Pi * u2
	return Zone{
		Center:       move(center, offset*math.Cos(bearing), offset*math.Sin(bearing)),
		RadiusMeters: radiusMeters + maxOffset,
	}
}

// move returns the point a number of meters north and east of p. It treats
// the earth as flat around p, which is close enough for the few kilometers
// zones are moved by.
func move(p geo.Point, northMeters, eastMeters float64) geo.Point {
	const metersPerDegree = 6371008.8 * math.Pi / 180
	// Near the poles a degree of longitude is too short to divide by.
	cos := math.Max(math.Cos(p.Lat*math.Pi/180), 1e-6)
	return geo.Point{
		Lat: math.Max(-90, math.Min(90, p.Lat+northMeters/metersPerDegree)),
		Lng: p.Lng + eastMeters/(metersPerDegree*cos),
		Ele: p.Ele,
	}
}

// InZones reports whether a point is within any of the zones.
func InZones(p geo.Point, zones []Zone) bool {
	for _, z := range zones {
		if geo.Distance(p, z.Center) < z.RadiusMeters {
			return true
		}
	}
	return false
}

// PublicPoints returns the track as route points without those within the
// zones, and the indexes of the returned points that start a new piece of
// the track after a hidden stretch, which mustn't be joined to the point
// before. Distances stay along the whole track.
func (t *Track) PublicPoints(zones []Zone) (points []*routesv1.RoutePoint, breaks []int32) {
	all := t.RoutePoints()
	if len(zones) == 0 {
		return all, nil
	}
	hidden := false
	for i, p := range t.Points {
		if InZones(p, zones) {
			hidden = true
			continue
		}
		if hidden && len(points) > 0 {
			breaks = append(breaks, int32(len(points)))
		}
		hidden = false
		points = append(points, all[i])
	}
	return points, breaks
}
