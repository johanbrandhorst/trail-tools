package activities

import (
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/fit"
	activitiesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/activities/v1"
	"github.com/bs-tools/trailrunn.ing/internal/geo"
)

// StreamsFromFIT returns an activity's samples from its FIT file, and the
// bounds of its track, if it has one. footSport doubles the cadence, as
// files record strides for running and walking. Files in other formats,
// like GPX, can be read into a fit.Activity's records to use this too. Streams are as long as the records;
// a value a record lacks is the previous record's, so streams stay aligned.
func StreamsFromFIT(a *fit.Activity, footSport bool) (*activitiesv1.ActivityStreams, *geo.Bounds) {
	if len(a.Records) == 0 {
		return nil, nil
	}
	var has struct{ position, altitude, heartRate, cadence, distance, speed bool }
	for _, r := range a.Records {
		has.position = has.position || r.HasPosition
		has.altitude = has.altitude || r.HasAltitude
		has.heartRate = has.heartRate || r.HasHeartRate
		has.cadence = has.cadence || r.HasCadence
		has.distance = has.distance || r.HasDistance
		has.speed = has.speed || r.HasSpeed
	}
	start := a.Records[0].Time
	n := len(a.Records)
	msg := activitiesv1.ActivityStreams_builder{TimeSeconds: make([]int32, n)}
	stream := func(on bool) []float64 {
		if !on {
			return nil
		}
		return make([]float64, n)
	}
	msg.DistanceMeters = stream(has.distance)
	msg.AltitudeMeters = stream(has.altitude)
	msg.HeartrateBpm = stream(has.heartRate)
	msg.CadenceSpm = stream(has.cadence)
	msg.VelocityMps = stream(has.speed)
	msg.Lat, msg.Lng = stream(has.position), stream(has.position)
	var points []geo.Point
	var last fit.Record
	for i, r := range a.Records {
		msg.TimeSeconds[i] = int32(r.Time.Sub(start) / time.Second)
		if r.HasPosition {
			last.Lat, last.Lng = r.Lat, r.Lng
			points = append(points, geo.Point{Lat: r.Lat, Lng: r.Lng})
		}
		if r.HasAltitude {
			last.AltitudeMeters = r.AltitudeMeters
		}
		if r.HasHeartRate {
			last.HeartRateBPM = r.HeartRateBPM
		}
		if r.HasCadence {
			last.Cadence = r.Cadence
			if footSport {
				last.Cadence *= 2
			}
		}
		if r.HasDistance {
			last.DistanceMeters = r.DistanceMeters
		}
		if r.HasSpeed {
			last.SpeedMPS = r.SpeedMPS
		}
		if has.position {
			msg.Lat[i], msg.Lng[i] = last.Lat, last.Lng
		}
		if has.altitude {
			msg.AltitudeMeters[i] = last.AltitudeMeters
		}
		if has.heartRate {
			msg.HeartrateBpm[i] = last.HeartRateBPM
		}
		if has.cadence {
			msg.CadenceSpm[i] = last.Cadence
		}
		if has.distance {
			msg.DistanceMeters[i] = last.DistanceMeters
		}
		if has.speed {
			msg.VelocityMps[i] = last.SpeedMPS
		}
	}
	bounds, ok := geo.BoundsOf(points)
	if !ok {
		return msg.Build(), nil
	}
	return msg.Build(), &bounds
}
