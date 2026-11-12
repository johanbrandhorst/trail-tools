package stravaarchive

import (
	"bytes"
	"compress/gzip"
	"errors"
	"math"
	"os"
	"testing"
	"time"
)

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecodeFileFIT(t *testing.T) {
	t.Parallel()
	file, err := os.ReadFile("../fit/testdata/run.fit")
	if err != nil {
		t.Fatal(err)
	}
	a, err := DecodeFile("activities/1.fit.gz", gzipped(t, file))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if len(a.Records) != 11 || a.Sport != 1 {
		t.Errorf("got %d records of sport %d, want the run's 11", len(a.Records), a.Sport)
	}
}

func TestDecodeFileGPX(t *testing.T) {
	t.Parallel()
	gpx := `<?xml version="1.0"?>
<gpx version="1.1" creator="StravaGPX" xmlns="http://www.topografix.com/GPX/1/1"
  xmlns:gpxtpx="http://www.garmin.com/xmlschemas/TrackPointExtension/v1">
 <trk><name>Morning Run</name><trkseg>
  <trkpt lat="49.700000" lon="-123.150000"><ele>10.0</ele><time>2020-02-03T18:00:00Z</time>
   <extensions><gpxtpx:TrackPointExtension><gpxtpx:hr>120</gpxtpx:hr><gpxtpx:cad>84</gpxtpx:cad></gpxtpx:TrackPointExtension></extensions></trkpt>
  <trkpt lat="49.701000" lon="-123.150000"><ele>12.0</ele><time>2020-02-03T18:01:00Z</time></trkpt>
  <trkpt lat="49.702000" lon="-123.150000"><time>not a time</time></trkpt>
 </trkseg></trk>
</gpx>`
	a, err := DecodeFile("activities/2.gpx", []byte(gpx))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if len(a.Records) != 2 || !a.StartTime.Equal(time.Date(2020, 2, 3, 18, 0, 0, 0, time.UTC)) {
		t.Fatalf("got %d records from %v, want the 2 with a time", len(a.Records), a.StartTime)
	}
	first, second := a.Records[0], a.Records[1]
	if !first.HasHeartRate || first.HeartRateBPM != 120 || !first.HasCadence || first.Cadence != 84 || first.AltitudeMeters != 10 {
		t.Errorf("unexpected first record %+v", first)
	}
	// The distance comes from the positions: a thousandth of a degree of
	// latitude is about 111 m.
	if !second.HasDistance || math.Abs(second.DistanceMeters-111.2) > 1 {
		t.Errorf("got distance %v, want about 111 m", second.DistanceMeters)
	}
}

func TestDecodeFileTCX(t *testing.T) {
	t.Parallel()
	tcx := `<?xml version="1.0"?>
<TrainingCenterDatabase xmlns="http://www.garmin.com/xmlschemas/TrainingCenterDatabase/v2"
  xmlns:ns3="http://www.garmin.com/xmlschemas/ActivityExtension/v2">
 <Activities><Activity Sport="Running"><Lap><Track>
  <Trackpoint><Time>2015-06-01T07:00:00.000Z</Time><Position><LatitudeDegrees>49.7</LatitudeDegrees><LongitudeDegrees>-123.15</LongitudeDegrees></Position>
   <AltitudeMeters>5</AltitudeMeters><DistanceMeters>0</DistanceMeters><HeartRateBpm><Value>110</Value></HeartRateBpm>
   <Extensions><ns3:TPX><ns3:RunCadence>80</ns3:RunCadence></ns3:TPX></Extensions></Trackpoint>
  <Trackpoint><Time>2015-06-01T07:00:10.000Z</Time><DistanceMeters>30</DistanceMeters></Trackpoint>
 </Track></Lap></Activity></Activities>
</TrainingCenterDatabase>`
	a, err := DecodeFile("activities/3.tcx.gz", gzipped(t, []byte(tcx)))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if len(a.Records) != 2 {
		t.Fatalf("got %d records, want 2", len(a.Records))
	}
	first, second := a.Records[0], a.Records[1]
	if !first.HasPosition || first.HeartRateBPM != 110 || first.Cadence != 80 || second.DistanceMeters != 30 || second.HasPosition {
		t.Errorf("unexpected records %+v", a.Records)
	}
}

func TestDecodeFileRejects(t *testing.T) {
	t.Parallel()
	if _, err := DecodeFile("activities/4.pwx", []byte("x")); !errors.Is(err, ErrUnsupportedFile) {
		t.Errorf("got %v for a PWX file, want ErrUnsupportedFile", err)
	}
	if _, err := DecodeFile("activities/5.fit.gz", []byte("not gzip")); err == nil {
		t.Error("decoded a file that isn't gzipped")
	}
	if _, err := DecodeFile("activities/6.gpx", []byte(`<gpx><trk><trkseg></trkseg></trk></gpx>`)); err == nil {
		t.Error("decoded a GPX file without samples")
	}
}
