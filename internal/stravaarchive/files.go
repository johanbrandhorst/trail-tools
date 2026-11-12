package stravaarchive

import (
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/fit"
	"github.com/bs-tools/trailrunn.ing/internal/geo"
)

// MaxFileBytes bounds an activity's file once decompressed: a day long
// activity recorded every second is a few tens of megabytes as GPX.
const MaxFileBytes = 64 << 20

// ErrUnsupportedFile is returned for a file in a format that isn't read.
var ErrUnsupportedFile = errors.New("only FIT, GPX and TCX files are read")

// DecodeFile decodes an activity's original file from the archive, named
// as activities.csv names it: a FIT, GPX or TCX file, gzipped if its name
// ends in ".gz". GPX and TCX files are read into a fit.Activity's records,
// so all are converted to samples alike. Their summary is left out: the
// archive's stats are Strava's.
func DecodeFile(filename string, data []byte) (*fit.Activity, error) {
	name := strings.ToLower(filename)
	if base, ok := strings.CutSuffix(name, ".gz"); ok {
		name = base
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("failed to decompress %s: %w", filename, err)
		}
		data, err = io.ReadAll(io.LimitReader(zr, MaxFileBytes+1))
		if err != nil {
			return nil, fmt.Errorf("failed to decompress %s: %w", filename, err)
		}
		if len(data) > MaxFileBytes {
			return nil, fmt.Errorf("%s is larger than %d MB", filename, MaxFileBytes>>20)
		}
	}
	switch {
	case strings.HasSuffix(name, ".fit"):
		return fit.DecodeActivity(data)
	case strings.HasSuffix(name, ".gpx"):
		return decodeGPX(data)
	case strings.HasSuffix(name, ".tcx"):
		return decodeTCX(data)
	}
	return nil, fmt.Errorf("%s: %w", filename, ErrUnsupportedFile)
}

// gpxFile is the part of a GPX file read: its track points, with the
// heart rate and cadence of Garmin's track point extension, which most
// devices and apps write. Elements are matched by their local names, so
// any namespace prefix works.
type gpxFile struct {
	Tracks []struct {
		Segments []struct {
			Points []struct {
				Lat     float64  `xml:"lat,attr"`
				Lng     float64  `xml:"lon,attr"`
				Ele     *float64 `xml:"ele"`
				Time    string   `xml:"time"`
				HR      *float64 `xml:"extensions>TrackPointExtension>hr"`
				Cadence *float64 `xml:"extensions>TrackPointExtension>cad"`
			} `xml:"trkpt"`
		} `xml:"trkseg"`
	} `xml:"trk"`
}

func decodeGPX(data []byte) (*fit.Activity, error) {
	var f gpxFile
	if err := xml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("failed to parse GPX: %w", err)
	}
	a := &fit.Activity{}
	for _, track := range f.Tracks {
		for _, segment := range track.Segments {
			for _, p := range segment.Points {
				t, err := time.Parse(time.RFC3339, strings.TrimSpace(p.Time))
				if err != nil {
					// Samples need a time.
					continue
				}
				r := fit.Record{Time: t}
				if p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180 && (p.Lat != 0 || p.Lng != 0) {
					r.HasPosition, r.Lat, r.Lng = true, p.Lat, p.Lng
				}
				if p.Ele != nil {
					r.HasAltitude, r.AltitudeMeters = true, *p.Ele
				}
				if p.HR != nil {
					r.HasHeartRate, r.HeartRateBPM = true, *p.HR
				}
				if p.Cadence != nil {
					r.HasCadence, r.Cadence = true, *p.Cadence
				}
				a.Records = append(a.Records, r)
			}
		}
	}
	return withRecords(a)
}

// tcxFile is the part of a TCX file read: its track points.
type tcxFile struct {
	Activities []struct {
		Laps []struct {
			Points []struct {
				Time      string   `xml:"Time"`
				Lat       *float64 `xml:"Position>LatitudeDegrees"`
				Lng       *float64 `xml:"Position>LongitudeDegrees"`
				Altitude  *float64 `xml:"AltitudeMeters"`
				Distance  *float64 `xml:"DistanceMeters"`
				HeartRate *float64 `xml:"HeartRateBpm>Value"`
				Cadence   *float64 `xml:"Cadence"`
				// Runs record their cadence in an extension.
				RunCadence *float64 `xml:"Extensions>TPX>RunCadence"`
				Speed      *float64 `xml:"Extensions>TPX>Speed"`
			} `xml:"Track>Trackpoint"`
		} `xml:"Lap"`
	} `xml:"Activities>Activity"`
}

func decodeTCX(data []byte) (*fit.Activity, error) {
	var f tcxFile
	if err := xml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("failed to parse TCX: %w", err)
	}
	a := &fit.Activity{}
	for _, activity := range f.Activities {
		for _, lap := range activity.Laps {
			for _, p := range lap.Points {
				t, err := time.Parse(time.RFC3339, strings.TrimSpace(p.Time))
				if err != nil {
					continue
				}
				r := fit.Record{Time: t}
				if p.Lat != nil && p.Lng != nil {
					r.HasPosition, r.Lat, r.Lng = true, *p.Lat, *p.Lng
				}
				if p.Altitude != nil {
					r.HasAltitude, r.AltitudeMeters = true, *p.Altitude
				}
				if p.Distance != nil {
					r.HasDistance, r.DistanceMeters = true, *p.Distance
				}
				if p.HeartRate != nil {
					r.HasHeartRate, r.HeartRateBPM = true, *p.HeartRate
				}
				switch {
				case p.RunCadence != nil:
					r.HasCadence, r.Cadence = true, *p.RunCadence
				case p.Cadence != nil:
					r.HasCadence, r.Cadence = true, *p.Cadence
				}
				if p.Speed != nil {
					r.HasSpeed, r.SpeedMPS = true, *p.Speed
				}
				a.Records = append(a.Records, r)
			}
		}
	}
	return withRecords(a)
}

// withRecords returns an activity read from a GPX or TCX file, which needs
// at least a sample. Records without a distance, as GPX's never have one,
// get the distance along the positions so far, which elevation profiles and
// paces need.
func withRecords(a *fit.Activity) (*fit.Activity, error) {
	if len(a.Records) == 0 {
		return nil, errors.New("the file has no samples with a time")
	}
	a.StartTime = a.Records[0].Time
	var total float64
	var last *geo.Point
	for i := range a.Records {
		r := &a.Records[i]
		if r.HasPosition {
			p := geo.Point{Lat: r.Lat, Lng: r.Lng}
			if last != nil {
				total += geo.Distance(*last, p)
			}
			last = &p
		}
		if r.HasDistance {
			total = r.DistanceMeters
		} else if last != nil {
			r.HasDistance, r.DistanceMeters = true, total
		}
	}
	return a, nil
}
