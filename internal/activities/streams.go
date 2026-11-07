package activities

import (
	"errors"
	"fmt"
	"math"

	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	"google.golang.org/protobuf/encoding/protowire"
)

// Streams are stored compactly: each series is quantized to the precision
// Strava records it at, and stored as the differences between consecutive
// values, as zigzag encoded varints in a field of a protobuf message. At a
// sample a second most differences take a byte or two, where the
// activitiesv1.ActivityStreams message takes eight bytes for each double.
// The message has no .proto file, as it's only read here.

// streamsVersion is the version of the encoding, stored in versionField so
// the encoding can change.
const (
	streamsVersion uint64           = 1
	versionField   protowire.Number = 15
)

// series is a series of the streams and its field.
type series struct {
	field protowire.Number
	// How many stored units make one unit of the series, e.g. 10 for tenths
	// of meters. Dividing a stored integer by it gives back exactly the
	// double parsed from Strava's decimal value.
	scale float64
	get   func(*activitiesv1.ActivityStreams) []float64
	set   func(*activitiesv1.ActivityStreams_builder, []float64)
}

// streamSeries are the series stored as doubles. Strava sends positions with
// six decimals, about 11 cm, distances, elevations and grades with one,
// speeds with three, and heart rates and cadences as whole numbers.
var streamSeries = []series{
	{
		field: 2, scale: 10,
		get: (*activitiesv1.ActivityStreams).GetDistanceMeters,
		set: func(b *activitiesv1.ActivityStreams_builder, v []float64) { b.DistanceMeters = v },
	},
	{
		field: 3, scale: 1e6,
		get: (*activitiesv1.ActivityStreams).GetLat,
		set: func(b *activitiesv1.ActivityStreams_builder, v []float64) { b.Lat = v },
	},
	{
		field: 4, scale: 1e6,
		get: (*activitiesv1.ActivityStreams).GetLng,
		set: func(b *activitiesv1.ActivityStreams_builder, v []float64) { b.Lng = v },
	},
	{
		field: 5, scale: 10,
		get: (*activitiesv1.ActivityStreams).GetAltitudeMeters,
		set: func(b *activitiesv1.ActivityStreams_builder, v []float64) { b.AltitudeMeters = v },
	},
	{
		field: 6, scale: 1,
		get: (*activitiesv1.ActivityStreams).GetHeartrateBpm,
		set: func(b *activitiesv1.ActivityStreams_builder, v []float64) { b.HeartrateBpm = v },
	},
	{
		field: 7, scale: 1,
		get: (*activitiesv1.ActivityStreams).GetCadenceSpm,
		set: func(b *activitiesv1.ActivityStreams_builder, v []float64) { b.CadenceSpm = v },
	},
	{
		field: 8, scale: 1000,
		get: (*activitiesv1.ActivityStreams).GetVelocityMps,
		set: func(b *activitiesv1.ActivityStreams_builder, v []float64) { b.VelocityMps = v },
	},
	{
		field: 9, scale: 10,
		get: (*activitiesv1.ActivityStreams).GetGradePercent,
		set: func(b *activitiesv1.ActivityStreams_builder, v []float64) { b.GradePercent = v },
	},
}

// timeField is the field of the time series, in whole seconds.
const timeField protowire.Number = 1

// maxStoredValue bounds the quantized values, so that they and their
// differences fit in an int64 and convert back to doubles exactly.
const maxStoredValue = 1 << 52

// encodeStreams encodes streams compactly, keeping their values to Strava's
// precision.
func encodeStreams(streams *activitiesv1.ActivityStreams) ([]byte, error) {
	b := protowire.AppendTag(nil, versionField, protowire.VarintType)
	b = protowire.AppendVarint(b, streamsVersion)
	times := streams.GetTimeSeconds()
	if len(times) > 0 {
		values := make([]float64, len(times))
		for i, t := range times {
			values[i] = float64(t)
		}
		b = appendSeries(b, timeField, values, 1)
	}
	for _, s := range streamSeries {
		values := s.get(streams)
		for _, v := range values {
			if scaled := math.Round(v * s.scale); math.IsNaN(scaled) || math.Abs(scaled) > maxStoredValue {
				return nil, fmt.Errorf("failed to encode streams: value %v of field %d is out of range", v, s.field)
			}
		}
		b = appendSeries(b, s.field, values, s.scale)
	}
	return b, nil
}

// appendSeries appends a series as the differences between its quantized
// values, unless it's empty.
func appendSeries(b []byte, field protowire.Number, values []float64, scale float64) []byte {
	if len(values) == 0 {
		return b
	}
	packed := make([]byte, 0, 2*len(values))
	var previous int64
	for _, v := range values {
		stored := int64(math.Round(v * scale))
		packed = protowire.AppendVarint(packed, protowire.EncodeZigZag(stored-previous))
		previous = stored
	}
	b = protowire.AppendTag(b, field, protowire.BytesType)
	return protowire.AppendBytes(b, packed)
}

// errBadStreams is returned for streams that can't be decoded.
var errBadStreams = errors.New("malformed streams")

// decodeStreams decodes streams encoded by encodeStreams.
func decodeStreams(data []byte) (*activitiesv1.ActivityStreams, error) {
	var b activitiesv1.ActivityStreams_builder
	for len(data) > 0 {
		field, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, fmt.Errorf("%w: %w", errBadStreams, protowire.ParseError(n))
		}
		data = data[n:]
		switch {
		case field == versionField && kind == protowire.VarintType:
			version, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return nil, fmt.Errorf("%w: %w", errBadStreams, protowire.ParseError(n))
			}
			if version != streamsVersion {
				return nil, fmt.Errorf("%w: unsupported version %d", errBadStreams, version)
			}
			data = data[n:]
		case kind == protowire.BytesType:
			packed, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return nil, fmt.Errorf("%w: %w", errBadStreams, protowire.ParseError(n))
			}
			data = data[n:]
			if field == timeField {
				values, err := decodeSeries(packed, 1)
				if err != nil {
					return nil, err
				}
				b.TimeSeconds = make([]int32, len(values))
				for i, v := range values {
					b.TimeSeconds[i] = int32(v)
				}
				continue
			}
			for _, s := range streamSeries {
				if s.field == field {
					values, err := decodeSeries(packed, s.scale)
					if err != nil {
						return nil, err
					}
					s.set(&b, values)
				}
			}
		default:
			// Skip fields of later versions.
			n := protowire.ConsumeFieldValue(field, kind, data)
			if n < 0 {
				return nil, fmt.Errorf("%w: %w", errBadStreams, protowire.ParseError(n))
			}
			data = data[n:]
		}
	}
	return b.Build(), nil
}

// decodeSeries decodes the differences of a series back into its values.
func decodeSeries(packed []byte, scale float64) ([]float64, error) {
	var values []float64
	var stored int64
	for len(packed) > 0 {
		v, n := protowire.ConsumeVarint(packed)
		if n < 0 {
			return nil, fmt.Errorf("%w: %w", errBadStreams, protowire.ParseError(n))
		}
		packed = packed[n:]
		stored += protowire.DecodeZigZag(v)
		values = append(values, float64(stored)/scale)
	}
	return values, nil
}
