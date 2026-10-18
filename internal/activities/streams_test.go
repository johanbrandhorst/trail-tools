package activities

import (
	"math"
	"math/rand/v2"
	"testing"

	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	"google.golang.org/protobuf/proto"
)

// round rounds v to decimals, as Strava's values are.
func round(v float64, decimals int) float64 {
	scale := math.Pow(10, float64(decimals))
	return math.Round(v*scale) / scale
}

// stravaStreams returns the streams of a run of the given length, recorded
// every second, with values as precise as Strava's.
func stravaStreams(seconds int) *activitiesv1.ActivityStreams {
	r := rand.New(rand.NewPCG(1, 2))
	var b activitiesv1.ActivityStreams_builder
	lat, lng, altitude, distance := 49.7016, -123.1558, 10.0, 0.0
	heading := 0.0
	for i := range seconds {
		speed := 2.5 + r.Float64()
		heading += r.NormFloat64() * 0.1
		lat += speed * math.Cos(heading) / 111_320
		lng += speed * math.Sin(heading) / (111_320 * math.Cos(lat*math.Pi/180))
		distance += speed
		grade := 10 * math.Sin(float64(i)/600)
		altitude += speed * grade / 100
		b.TimeSeconds = append(b.TimeSeconds, int32(i))
		b.DistanceMeters = append(b.DistanceMeters, round(distance, 1))
		b.Lat = append(b.Lat, round(lat, 6))
		b.Lng = append(b.Lng, round(lng, 6))
		b.AltitudeMeters = append(b.AltitudeMeters, round(altitude, 1))
		b.HeartrateBpm = append(b.HeartrateBpm, float64(140+r.IntN(10)))
		b.CadenceSpm = append(b.CadenceSpm, float64(2*(85+r.IntN(5))))
		b.VelocityMps = append(b.VelocityMps, round(speed, 3))
		b.GradePercent = append(b.GradePercent, round(grade, 1))
	}
	return b.Build()
}

func TestStreamsRoundTrip(t *testing.T) {
	for name, streams := range map[string]*activitiesv1.ActivityStreams{
		"run":   stravaStreams(4 * 60 * 60),
		"empty": {},
		// A treadmill run has no positions.
		"no positions": activitiesv1.ActivityStreams_builder{
			TimeSeconds:    []int32{0, 1, 3},
			DistanceMeters: []float64{0, 2.7, 8.1},
			HeartrateBpm:   []float64{90, 95, 101},
		}.Build(),
		"negative": activitiesv1.ActivityStreams_builder{
			Lat:            []float64{-33.856784, -33.856801},
			Lng:            []float64{151.215297, 151.215313},
			AltitudeMeters: []float64{-12.5, -3.2},
			GradePercent:   []float64{-25.3, 4.1},
		}.Build(),
	} {
		t.Run(name, func(t *testing.T) {
			data, err := encodeStreams(streams)
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeStreams(data)
			if err != nil {
				t.Fatal(err)
			}
			// The values are exactly those encoded, not just close to them.
			if !proto.Equal(streams, got) {
				t.Errorf("decoded streams differ from the encoded ones")
			}
		})
	}
}

func TestStreamsSize(t *testing.T) {
	streams := stravaStreams(4 * 60 * 60)
	compact, err := encodeStreams(streams)
	if err != nil {
		t.Fatal(err)
	}
	full, err := proto.Marshal(streams)
	if err != nil {
		t.Fatal(err)
	}
	samples := len(streams.GetTimeSeconds())
	t.Logf("%d samples: %d bytes (%.1f a sample), %d as a message (%.1f a sample)",
		samples, len(compact), float64(len(compact))/float64(samples), len(full), float64(len(full))/float64(samples))
	if len(compact)*4 > len(full) {
		t.Errorf("compact streams are %d bytes, want at most a quarter of the message's %d", len(compact), len(full))
	}
}

func TestStreamsErrors(t *testing.T) {
	if _, err := encodeStreams(activitiesv1.ActivityStreams_builder{Lat: []float64{math.NaN()}}.Build()); err == nil {
		t.Error("encoding NaN succeeded, want an error")
	}
	if _, err := encodeStreams(activitiesv1.ActivityStreams_builder{DistanceMeters: []float64{math.Inf(1)}}.Build()); err == nil {
		t.Error("encoding infinity succeeded, want an error")
	}
	data, err := encodeStreams(stravaStreams(10))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeStreams(data[:len(data)-1]); err == nil {
		t.Error("decoding truncated streams succeeded, want an error")
	}
	if _, err := decodeStreams([]byte{15 << 3, 2}); err == nil {
		t.Error("decoding version 2 succeeded, want an error")
	}
}
