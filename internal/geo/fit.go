package geo

import (
	"errors"
	"io"
	"math"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/fit"
)

// CoursePointType is the kind of a course point, as numbered in the FIT
// profile. Only the kinds all devices know are used.
type CoursePointType uint8

// Course point types.
const (
	CoursePointGeneric CoursePointType = 0
	CoursePointSummit  CoursePointType = 1
	CoursePointWater   CoursePointType = 3
	CoursePointFood    CoursePointType = 4
)

// CoursePoint is a named point along a course, like an aid station, which
// devices list and announce on the way.
type CoursePoint struct {
	Name string
	// The distance along the course in meters.
	DistanceMeters float64
	// The planned elapsed seconds when the point is reached.
	ElapsedSeconds float64
	Type           CoursePointType
}

// FITCourse is a course to write as a FIT file.
type FITCourse struct {
	Name   string
	Points []Point
	// The planned elapsed seconds at each point, which devices race as a
	// virtual partner. They must not decrease.
	ElapsedSeconds []float64
	CoursePoints   []CoursePoint
	// When the course starts. Devices only use the times relative to it.
	Start time.Time
}

// FIT global message numbers.
const (
	fitLap         = 19
	fitRecord      = 20
	fitEvent       = 21
	fitCourse      = 31
	fitCoursePoint = 32
)

const (
	// fitNameSize is the size of course names, including the terminating
	// zero.
	fitNameSize = 32
	// fitPointNameSize is the size of course point names, including the
	// terminating zero. Devices show about this many characters.
	fitPointNameSize = 16
	// fitInvalidUint16 marks an unknown uint16 value, like an altitude.
	fitInvalidUint16 = 0xFFFF
)

func semicircles(degrees float64) int32 {
	return int32(math.Round(degrees * (1 << 31) / 180))
}

func fitAltitude(ele float64) uint16 {
	if math.IsNaN(ele) {
		return fitInvalidUint16
	}
	return uint16(math.Round((min(max(ele, -500), 12000) + 500) * 5))
}

// WriteFITCourse encodes a course as a FIT course file, which Garmin and
// other devices follow with turn by turn guidance, a virtual partner and
// alerts at its course points.
func WriteFITCourse(w io.Writer, c *FITCourse) error {
	if len(c.Points) < 2 {
		return errors.New("a course needs at least two points")
	}
	if len(c.ElapsedSeconds) != len(c.Points) {
		return errors.New("a course needs an elapsed time for each point")
	}
	dists := CumulativeDistances(c.Points)
	timeAt := func(i int) uint32 {
		return fit.Time(c.Start.Add(time.Duration(c.ElapsedSeconds[i] * float64(time.Second))))
	}
	first, last := c.Points[0], c.Points[len(c.Points)-1]
	start, end := timeAt(0), timeAt(len(c.Points)-1)
	elevations := make([]float64, len(c.Points))
	for i, p := range c.Points {
		elevations[i] = p.Ele
	}
	gain, loss := ElevationGainLoss(elevations)
	elapsedMillis := uint32(math.Round((c.ElapsedSeconds[len(c.Points)-1] - c.ElapsedSeconds[0]) * 1000))

	var f fit.Writer
	f.Define(0, fit.FileIDMessage,
		fit.EnumField(0),   // Type.
		fit.Uint16Field(1), // Manufacturer.
		fit.Uint16Field(2), // Product.
		fit.Uint32Field(4), // Time created.
	)
	// A course file from a development manufacturer.
	f.Data(0, uint8(fit.FileCourse), uint16(fit.DevelopmentManufacturer), uint16(0), start)
	f.Define(1, fitCourse,
		fit.EnumField(4),                // Sport.
		fit.StringField(5, fitNameSize), // Name.
	)
	f.Data(1, uint8(1), fit.StringValue(c.Name, fitNameSize))
	f.Define(2, fitLap,
		fit.Uint32Field(253), // Timestamp.
		fit.Uint32Field(2),   // Start time.
		fit.Sint32Field(3),   // Start latitude.
		fit.Sint32Field(4),   // Start longitude.
		fit.Sint32Field(5),   // End latitude.
		fit.Sint32Field(6),   // End longitude.
		fit.Uint32Field(7),   // Total elapsed time in milliseconds.
		fit.Uint32Field(8),   // Total timer time in milliseconds.
		fit.Uint32Field(9),   // Total distance in centimeters.
		fit.Uint16Field(21),  // Total ascent in meters.
		fit.Uint16Field(22),  // Total descent in meters.
	)
	f.Data(2, end, start,
		semicircles(first.Lat), semicircles(first.Lng), semicircles(last.Lat), semicircles(last.Lng),
		elapsedMillis, elapsedMillis, uint32(math.Round(dists[len(dists)-1]*100)),
		uint16(math.Round(gain)), uint16(math.Round(loss)))
	f.Define(3, fitEvent,
		fit.Uint32Field(253), // Timestamp.
		fit.EnumField(0),     // Event.
		fit.EnumField(1),     // Event type.
		fit.Uint8Field(4),    // Event group.
	)
	// The timer starts at the start, and stops at the finish.
	f.Data(3, start, uint8(0), uint8(0), uint8(0))
	f.Define(4, fitRecord,
		fit.Uint32Field(253), // Timestamp.
		fit.Sint32Field(0),   // Latitude.
		fit.Sint32Field(1),   // Longitude.
		fit.Uint16Field(2),   // Altitude.
		fit.Uint32Field(5),   // Distance in centimeters.
	)
	for i, p := range c.Points {
		f.Data(4, timeAt(i), semicircles(p.Lat), semicircles(p.Lng), fitAltitude(p.Ele), uint32(math.Round(dists[i]*100)))
	}
	f.Data(3, end, uint8(0), uint8(9), uint8(0))
	if len(c.CoursePoints) > 0 {
		f.Define(5, fitCoursePoint,
			fit.Uint32Field(1),                   // Timestamp.
			fit.Sint32Field(2),                   // Latitude.
			fit.Sint32Field(3),                   // Longitude.
			fit.Uint32Field(4),                   // Distance in centimeters.
			fit.EnumField(5),                     // Type.
			fit.StringField(6, fitPointNameSize), // Name.
		)
	}
	for _, cp := range c.CoursePoints {
		d := min(max(cp.DistanceMeters, 0), dists[len(dists)-1])
		p := pointAtDistance(c.Points, dists, d)
		at := fit.Time(c.Start.Add(time.Duration(cp.ElapsedSeconds * float64(time.Second))))
		f.Data(5, at, semicircles(p.Lat), semicircles(p.Lng), uint32(math.Round(d*100)), uint8(cp.Type), fit.StringValue(cp.Name, fitPointNameSize))
	}

	return f.WriteFile(w)
}

// pointAtDistance returns the point at a distance along the points.
func pointAtDistance(points []Point, dists []float64, d float64) Point {
	for i := 1; i < len(points); i++ {
		if dists[i] < d {
			continue
		}
		segment := dists[i] - dists[i-1]
		if segment <= 0 {
			return points[i]
		}
		return Interpolate(points[i-1], points[i], (d-dists[i-1])/segment)
	}
	return points[len(points)-1]
}

// ElapsedSeconds estimates the elapsed seconds at each point of a track:
// distance at a speed on the flat, plus elevation gain at a climbing rate if
// it is set, like Naismith's rule. Elevations are smoothed first, so noise
// doesn't count as climbing.
func ElapsedSeconds(points []Point, speedKMH, climbMetersPerHour float64) []float64 {
	dists := CumulativeDistances(points)
	smoothed := SmoothElevations(points)
	elapsed := make([]float64, len(points))
	speedMPS := speedKMH * 1000 / 3600
	for i := 1; i < len(points); i++ {
		dt := (dists[i] - dists[i-1]) / speedMPS
		if climb := smoothed[i] - smoothed[i-1]; climbMetersPerHour > 0 && climb > 0 {
			dt += climb / climbMetersPerHour * 3600
		}
		elapsed[i] = elapsed[i-1] + dt
	}
	return elapsed
}

// elevationSmoothingWindow is the number of points elevations are averaged
// over.
const elevationSmoothingWindow = 5

// SmoothElevations returns the elevations averaged over a small window,
// ignoring unknown elevations. It is NaN where all are unknown.
func SmoothElevations(points []Point) []float64 {
	smoothed := make([]float64, len(points))
	for i := range points {
		// Shrink the window near the ends so the start and end elevations
		// are kept.
		half := min(elevationSmoothingWindow/2, i, len(points)-1-i)
		var sum float64
		var n int
		for j := i - half; j <= i+half; j++ {
			if !math.IsNaN(points[j].Ele) {
				sum += points[j].Ele
				n++
			}
		}
		smoothed[i] = math.NaN()
		if n > 0 {
			smoothed[i] = sum / float64(n)
		}
	}
	return smoothed
}
