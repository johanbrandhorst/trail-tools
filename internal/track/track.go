// Package track holds the tracks of activities copied into reports, which
// show them on a map and elevation profile, and suggests segments of them.
package track

import (
	"fmt"
	"math"
	"time"

	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/services/activity"
	"google.golang.org/protobuf/proto"
)

// MaxPoints is the number of points of the track copied into a report.
const MaxPoints = 2000

// TimeTolerance is how long before the start or after the end of the
// track a photo can be taken and still be placed at the start or end.
const TimeTolerance = 10 * time.Minute

// FromStreams returns the track to store in a report from an
// activity's streams: the time, distance, position and altitude,
// downsampled. It returns nil if the streams have no positions.
func FromStreams(streams *activitiesv1.ActivityStreams) *activitiesv1.ActivityStreams {
	n := len(streams.GetLat())
	if n < 2 || len(streams.GetLng()) != n {
		return nil
	}
	reduced := activitiesv1.ActivityStreams_builder{
		Lat: streams.GetLat(),
		Lng: streams.GetLng(),
	}.Build()
	if len(streams.GetTimeSeconds()) == n {
		reduced.SetTimeSeconds(streams.GetTimeSeconds())
	}
	if len(streams.GetDistanceMeters()) == n {
		reduced.SetDistanceMeters(streams.GetDistanceMeters())
	}
	if len(streams.GetAltitudeMeters()) == n {
		reduced.SetAltitudeMeters(streams.GetAltitudeMeters())
	}
	return activity.DownsampleStreams(reduced, MaxPoints)
}

// FromPolyline returns a track from an encoded polyline, for
// activities without streams. It returns nil if the polyline has less than
// two points.
func FromPolyline(polyline string) *activitiesv1.ActivityStreams {
	points, err := geo.DecodePolyline(polyline)
	if err != nil || len(points) < 2 {
		return nil
	}
	lats := make([]float64, len(points))
	lngs := make([]float64, len(points))
	for i, p := range points {
		lats[i], lngs[i] = p.Lat, p.Lng
	}
	return activitiesv1.ActivityStreams_builder{
		Lat:            lats,
		Lng:            lngs,
		DistanceMeters: geo.CumulativeDistances(points),
	}.Build()
}

// track is a decoded report track.
type Track struct {
	Points []geo.Point
	// The cumulative distance at each point in meters.
	Dists []float64
	// The elapsed time at each point in seconds, nil if unknown.
	Times []int32
}

// Decode decodes a stored track. It returns nil for an empty track.
func Decode(data []byte) (*Track, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var streams activitiesv1.ActivityStreams
	if err := proto.Unmarshal(data, &streams); err != nil {
		return nil, fmt.Errorf("failed to decode track: %w", err)
	}
	return New(&streams), nil
}

// New returns the track of streams, or nil if it has less than two
// positions.
func New(streams *activitiesv1.ActivityStreams) *Track {
	n := len(streams.GetLat())
	if n < 2 || len(streams.GetLng()) != n {
		return nil
	}
	t := &Track{Points: make([]geo.Point, n)}
	hasAltitude := len(streams.GetAltitudeMeters()) == n
	for i := range n {
		t.Points[i] = geo.Point{Lat: streams.GetLat()[i], Lng: streams.GetLng()[i], Ele: math.NaN()}
		if hasAltitude {
			t.Points[i].Ele = streams.GetAltitudeMeters()[i]
		}
	}
	if len(streams.GetDistanceMeters()) == n {
		t.Dists = streams.GetDistanceMeters()
	} else {
		t.Dists = geo.CumulativeDistances(t.Points)
	}
	if len(streams.GetTimeSeconds()) == n {
		t.Times = streams.GetTimeSeconds()
	}
	return t
}

// PositionAt returns the position on the track at an elapsed time in
// seconds, interpolated between the recorded points, and the distance along
// the track there. Times up to TimeTolerance before the start or after
// the end are placed at the start or end. It returns false if the track has
// no times, or the time is outside it.
func (t *Track) PositionAt(elapsed float64) (geo.Point, float64, bool) {
	if t == nil || len(t.Times) == 0 {
		return geo.Point{}, 0, false
	}
	first, last := float64(t.Times[0]), float64(t.Times[len(t.Times)-1])
	tolerance := TimeTolerance.Seconds()
	switch {
	case elapsed < first-tolerance || elapsed > last+tolerance:
		return geo.Point{}, 0, false
	case elapsed <= first:
		return t.Points[0], t.Dists[0], true
	case elapsed >= last:
		return t.Points[len(t.Points)-1], t.Dists[len(t.Dists)-1], true
	}
	// Find the first point at or after the time.
	lo, hi := 0, len(t.Times)-1
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if float64(t.Times[mid]) < elapsed {
			lo = mid
		} else {
			hi = mid
		}
	}
	span := float64(t.Times[hi] - t.Times[lo])
	f := 0.0
	if span > 0 {
		f = (elapsed - float64(t.Times[lo])) / span
	}
	p := geo.Interpolate(t.Points[lo], t.Points[hi], f)
	return p, t.Dists[lo] + f*(t.Dists[hi]-t.Dists[lo]), true
}

// NearestDistance returns the distance along the track of the point
// nearest to p.
func (t *Track) NearestDistance(p geo.Point) (float64, bool) {
	if t == nil {
		return 0, false
	}
	i, _ := geo.NearestIndex(t.Points, p)
	if i < 0 {
		return 0, false
	}
	return t.Dists[i], true
}

// RoutePoints returns the track as route points. Unknown elevations are 0.
func (t *Track) RoutePoints() []*routesv1.RoutePoint {
	if t == nil {
		return nil
	}
	points := make([]*routesv1.RoutePoint, len(t.Points))
	for i, p := range t.Points {
		ele := p.Ele
		if math.IsNaN(ele) {
			ele = 0
		}
		points[i] = routesv1.RoutePoint_builder{
			Lat:             p.Lat,
			Lng:             p.Lng,
			ElevationMeters: ele,
			DistanceMeters:  t.Dists[i],
		}.Build()
	}
	return points
}
