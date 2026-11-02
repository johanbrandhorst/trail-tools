package fit

import (
	"errors"
	"time"
)

// Global message numbers of activity files.
const (
	sessionMessage = 18
	recordMessage  = 20
	setMessage     = 225
)

// semicirclesToDegrees converts FIT positions to degrees.
const semicirclesToDegrees = 180.0 / (1 << 31)

// Activity is what a FIT activity file says of an activity: its summary,
// the samples recorded during it, and the sets of a strength workout.
type Activity struct {
	// The FIT sport and sub sport of the first session.
	Sport, SubSport uint8
	StartTime       time.Time
	// Zero if not recorded.
	ElapsedTime, MovingTime time.Duration
	DistanceMeters          float64
	ElevationGainMeters     float64
	AverageHeartRateBPM     float64
	MaxHeartRateBPM         float64
	AverageSpeedMPS         float64
	MaxSpeedMPS             float64
	Records                 []Record
	Sets                    []Set
	// The names of the exercises of the sets, by category and name.
	ExerciseTitles map[[2]uint16]string
}

// Record is a sample recorded during an activity. Values that weren't
// recorded are zero, with their Has field false.
type Record struct {
	Time time.Time
	// Whether the position, altitude, heart rate, cadence, distance and
	// speed were recorded.
	HasPosition, HasAltitude, HasHeartRate, HasCadence, HasDistance, HasSpeed bool
	Lat, Lng                                                                  float64
	AltitudeMeters                                                            float64
	HeartRateBPM                                                              float64
	// Cadence in revolutions per minute: strides for running, so half the
	// steps.
	Cadence        float64
	DistanceMeters float64
	SpeedMPS       float64
}

// Set is a set of a strength workout, or the rest between sets.
type Set struct {
	StartTime time.Time
	Duration  time.Duration
	// Whether it's an active set, rather than rest.
	Active bool
	// Zero if not counted.
	Reps int
	// Zero for bodyweight.
	WeightKilograms float64
	// Whether the weight was shown in pounds.
	Pounds bool
	// The exercise's category and name in Garmin's catalog, or the
	// unknown category and the number of a custom exercise title. Zero
	// and false if not set.
	Category, Name uint16
	HasExercise    bool
	// The index of the workout step the set was of, if the activity
	// followed a workout, and false if not.
	WorkoutStep    int
	HasWorkoutStep bool
}

// Time converts a FIT timestamp.
func timeOf(ts uint64) time.Time {
	return Epoch.Add(time.Duration(ts) * time.Second)
}

// scaled returns an integer field divided by a scale, with an offset.
func scaled(m Message, num uint8, scale, offset float64) (float64, bool) {
	v, ok := m.Uint(num)
	if !ok {
		return 0, false
	}
	return float64(v)/scale - offset, true
}

// scaledSigned is scaled for a signed field.
func scaledSigned(m Message, num uint8, scale float64) (float64, bool) {
	v, ok := m.Int(num)
	if !ok {
		return 0, false
	}
	return float64(v) / scale, true
}

// first returns the first valid of several scaled fields, like the
// enhanced speed and the speed.
func first(values ...func() (float64, bool)) (float64, bool) {
	for _, v := range values {
		if x, ok := v(); ok {
			return x, true
		}
	}
	return 0, false
}

// DecodeActivity decodes a FIT activity file.
func DecodeActivity(file []byte) (*Activity, error) {
	messages, err := Decode(file)
	if err != nil {
		return nil, err
	}
	a := &Activity{ExerciseTitles: map[[2]uint16]string{}}
	sessions := 0
	for _, m := range messages {
		switch m.Global {
		case sessionMessage:
			sessions++
			if sessions > 1 {
				// Multisport activities keep the summary of the first
				// session; they're rare for trail runners.
				continue
			}
			if v, ok := m.Uint(5); ok {
				a.Sport = uint8(v)
			}
			if v, ok := m.Uint(6); ok {
				a.SubSport = uint8(v)
			}
			if v, ok := m.Uint(2); ok {
				a.StartTime = timeOf(v)
			}
			if v, ok := m.Uint(7); ok {
				a.ElapsedTime = time.Duration(v) * time.Millisecond
			}
			if v, ok := m.Uint(8); ok {
				a.MovingTime = time.Duration(v) * time.Millisecond
			}
			a.DistanceMeters, _ = scaled(m, 9, 100, 0)
			a.ElevationGainMeters, _ = scaled(m, 22, 1, 0)
			a.AverageHeartRateBPM, _ = scaled(m, 16, 1, 0)
			a.MaxHeartRateBPM, _ = scaled(m, 17, 1, 0)
			a.AverageSpeedMPS, _ = first(
				func() (float64, bool) { return scaled(m, 124, 1000, 0) },
				func() (float64, bool) { return scaled(m, 14, 1000, 0) },
			)
			a.MaxSpeedMPS, _ = first(
				func() (float64, bool) { return scaled(m, 125, 1000, 0) },
				func() (float64, bool) { return scaled(m, 15, 1000, 0) },
			)
		case recordMessage:
			ts, ok := m.Uint(253)
			if !ok {
				continue
			}
			r := Record{Time: timeOf(ts)}
			lat, latOK := scaledSigned(m, 0, 1/semicirclesToDegrees)
			lng, lngOK := scaledSigned(m, 1, 1/semicirclesToDegrees)
			if latOK && lngOK {
				r.HasPosition, r.Lat, r.Lng = true, lat, lng
			}
			r.AltitudeMeters, r.HasAltitude = first(
				func() (float64, bool) { return scaled(m, 78, 5, 500) },
				func() (float64, bool) { return scaled(m, 2, 5, 500) },
			)
			r.HeartRateBPM, r.HasHeartRate = scaled(m, 3, 1, 0)
			r.Cadence, r.HasCadence = scaled(m, 4, 1, 0)
			r.DistanceMeters, r.HasDistance = scaled(m, 5, 100, 0)
			r.SpeedMPS, r.HasSpeed = first(
				func() (float64, bool) { return scaled(m, 73, 1000, 0) },
				func() (float64, bool) { return scaled(m, 6, 1000, 0) },
			)
			a.Records = append(a.Records, r)
		case setMessage:
			s := Set{}
			if v, ok := m.Uint(6); ok {
				s.StartTime = timeOf(v)
			}
			if v, ok := m.Uint(0); ok {
				s.Duration = time.Duration(v) * time.Millisecond
			}
			if v, ok := m.Uint(5); ok {
				s.Active = v == 1
			}
			if v, ok := m.Uint(3); ok {
				s.Reps = int(v)
			}
			// Set weights are in sixteenths of a kilogram.
			s.WeightKilograms, _ = scaled(m, 4, 16, 0)
			if v, ok := m.Uint(9); ok {
				s.Pounds = v == WeightUnitPounds
			}
			category, catOK := m.Uint(7)
			name, nameOK := m.Uint(8)
			if catOK && nameOK {
				s.Category, s.Name, s.HasExercise = uint16(category), uint16(name), true
			}
			if v, ok := m.Uint(11); ok {
				s.WorkoutStep, s.HasWorkoutStep = int(v), true
			}
			a.Sets = append(a.Sets, s)
		case exerciseTitleMessage:
			category, catOK := m.Uint(0)
			name, nameOK := m.Uint(1)
			if catOK && nameOK {
				a.ExerciseTitles[[2]uint16{uint16(category), uint16(name)}] = m.String(2)
			}
		}
	}
	if sessions == 0 {
		return nil, errNoSession
	}
	return a, nil
}

// errNoSession is returned for FIT files that aren't activities.
var errNoSession = errors.New("the file has no session: it isn't an activity")
