package services_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	activitiesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/activities/v1"
	stravav1 "github.com/bs-tools/trailrunn.ing/internal/gen/strava/v1"
)

// archiveCSV is an activities.csv with the columns imports read, the
// distances in kilometers first and then in meters, as Strava's archives
// have them.
const archiveCSV = `Activity ID,Activity Date,Activity Name,Activity Type,Activity Description,Elapsed Time,Distance,Filename,Elapsed Time,Moving Time,Distance,Elevation Gain,Average Heart Rate
`

func TestStravaArchiveImport(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	fitFile, err := os.ReadFile("../fit/testdata/run.fit")
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(fitFile); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	gpx := []byte(`<gpx><trk><trkseg>
<trkpt lat="49.70" lon="-123.15"><ele>10</ele><time>2026-09-11T08:00:00Z</time></trkpt>
<trkpt lat="49.71" lon="-123.15"><ele>40</ele><time>2026-09-11T08:10:00Z</time></trkpt>
</trkseg></trk></gpx>`)

	// The archive has a trail run from a watch, a manual entry, and a run
	// that's on Strava already, recorded as GPX. Strava isn't connected.
	csv := archiveCSV +
		`9001,"Nov 5, 2026, 7:00:00 AM",Valley run,Run,"Easy,
with friends",600,1.5,activities/111.fit.gz,600.0,600.0,1500.0,42.0,140.0
9002,"Feb 3, 2020, 6:00:00 PM",Treadmill,Run,,1800,5.0,,1800.0,1800.0,5000.0,0.0,
1002,"Sep 15, 2026, 8:00:00 AM",Run 2 from the archive,Run,,3700,12.0,activities/333.gpx,3700.0,3600.0,12000.0,500.0,
`
	start, err := e.stravaAPI.StartStravaArchiveImport(ctx, stravav1.StartStravaArchiveImportRequest_builder{ActivitiesCsv: csv}.Build())
	if err != nil {
		t.Fatalf("StartStravaArchiveImport: %v", err)
	}
	if got := start.GetActivities(); len(got) != 3 || got[0].GetStravaId() != "9001" || got[0].GetFilename() != "activities/111.fit.gz" {
		t.Fatalf("got activities %v, want the 3, most recent first", got)
	}
	importActivity := func(stravaID string, file []byte) *stravav1.ImportStravaArchiveActivityResponse {
		t.Helper()
		resp, err := e.stravaAPI.ImportStravaArchiveActivity(ctx, stravav1.ImportStravaArchiveActivityRequest_builder{StravaId: stravaID, File: file}.Build())
		if err != nil {
			t.Fatalf("ImportStravaArchiveActivity(%s): %v", stravaID, err)
		}
		return resp
	}
	valley := importActivity("9001", gz.Bytes())
	treadmill := importActivity("9002", nil)
	// An activity is imported once.
	_, err = e.stravaAPI.ImportStravaArchiveActivity(ctx, stravav1.ImportStravaArchiveActivityRequest_builder{StravaId: "9001"}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)

	get := func(id string) *activitiesv1.GetActivityResponse {
		t.Helper()
		resp, err := e.activity.GetActivity(ctx, activitiesv1.GetActivityRequest_builder{Id: id, IncludeStreams: true}.Build())
		if err != nil {
			t.Fatalf("GetActivity: %v", err)
		}
		return resp
	}
	// The watch's file gives the samples, and the archive the stats. The
	// file has no time zone, so the local date is from the position: 07:00
	// UTC is the evening before near Squamish.
	got := get(valley.GetActivityId())
	if a := got.GetActivity(); a.GetName() != "Valley run" || a.GetDescription() != "Easy,\nwith friends" || a.GetStravaId() != "9001" ||
		a.GetDistanceMeters() != 1500 || a.GetElevationGainMeters() != 42 || !a.GetHasStreams() || a.GetStartDateLocal() != "2026-11-04" {
		t.Errorf("unexpected imported activity %v", a)
	}
	if n := len(got.GetStreams().GetHeartrateBpm()); n != 11 {
		t.Errorf("got %d heart rate samples, want the file's 11", n)
	}
	if a := get(treadmill.GetActivityId()).GetActivity(); a.GetName() != "Treadmill" || a.GetDistanceMeters() != 5000 || a.GetHasStreams() {
		t.Errorf("unexpected manual activity %v", a)
	}

	// Starting again skips what's imported.
	again, err := e.stravaAPI.StartStravaArchiveImport(ctx, stravav1.StartStravaArchiveImportRequest_builder{ActivitiesCsv: csv}.Build())
	if err != nil {
		t.Fatalf("StartStravaArchiveImport: %v", err)
	}
	if again.GetAlreadyImportedCount() != 2 || len(again.GetActivities()) != 1 || again.GetActivities()[0].GetStravaId() != "1002" {
		t.Errorf("got %v, want only the run not imported yet", again)
	}

	// Strava is connected and syncs its three runs: run 2 is the archive's.
	e.connectStrava()
	if _, err := e.stravaAPI.SyncStrava(ctx, stravav1.SyncStravaRequest_builder{}.Build()); err != nil {
		t.Fatalf("SyncStrava: %v", err)
	}
	e.waitForSync()
	// Importing it keeps the sport type and time zone Strava's API gave it,
	// and takes the archive's samples.
	run2 := importActivity("1002", gpx)
	if a := get(run2.GetActivityId()).GetActivity(); a.GetName() != "Run 2 from the archive" || a.GetSportType() != "TrailRun" ||
		a.GetTimezone() != "Europe/London" || !a.GetHasStreams() {
		t.Errorf("unexpected activity imported over a synced one %v", a)
	}

	// Disconnecting without keeping the activities from Strava keeps those
	// from the archive.
	if _, err := e.stravaAPI.DisconnectStrava(ctx, stravav1.DisconnectStravaRequest_builder{}.Build()); err != nil {
		t.Fatalf("DisconnectStrava: %v", err)
	}
	e.waitForPurge()
	list, err := e.activity.ListActivities(ctx, activitiesv1.ListActivitiesRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	var names []string
	for _, a := range list.GetActivities() {
		names = append(names, a.GetName())
	}
	if strings.Join(names, ", ") != "Valley run, Run 2 from the archive, Treadmill" {
		t.Errorf("got activities %v, want the archive's three", names)
	}

	// A file that isn't an archive's activities.csv is refused.
	_, err = e.stravaAPI.StartStravaArchiveImport(ctx, stravav1.StartStravaArchiveImportRequest_builder{ActivitiesCsv: "name,date\nRun,today\n"}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
}
