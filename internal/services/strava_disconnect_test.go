package services_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/bs-tools/trailrunn.ing/internal/activities"
	activitiesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/activities/v1"
	stravav1 "github.com/bs-tools/trailrunn.ing/internal/gen/strava/v1"
)

func TestStravaDisconnect(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// Agreeing to keep the activities needs a connection.
	_, err := e.stravaAPI.SetStravaKeepActivities(ctx, stravav1.SetStravaKeepActivitiesRequest_builder{KeepActivities: true}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)

	// The user syncs their three runs from Strava.
	sync := func() {
		t.Helper()
		// Connecting authorizes the app again.
		e.strava.mu.Lock()
		e.strava.revoked = false
		e.strava.mu.Unlock()
		e.connectStrava()
		if _, err := e.stravaAPI.SyncStrava(ctx, stravav1.SyncStravaRequest_builder{}.Build()); err != nil {
			t.Fatalf("SyncStrava: %v", err)
		}
		if conn := e.waitForSync(); conn.GetActivityCount() != 3 || conn.GetKeepActivities() {
			t.Fatalf("got connection %v, want 3 activities, not kept by default", conn)
		}
	}
	sync()
	listed := func() map[string]*activitiesv1.Activity {
		t.Helper()
		resp, err := e.activity.ListActivities(ctx, activitiesv1.ListActivitiesRequest_builder{}.Build())
		if err != nil {
			t.Fatalf("ListActivities: %v", err)
		}
		byName := map[string]*activitiesv1.Activity{}
		for _, a := range resp.GetActivities() {
			byName[a.GetName()] = a
		}
		return byName
	}

	// Disconnecting with the user's agreement keeps the activities, and
	// revokes the app's access on Strava.
	resp, err := e.stravaAPI.DisconnectStrava(ctx, stravav1.DisconnectStravaRequest_builder{KeepActivities: true}.Build())
	if err != nil {
		t.Fatalf("DisconnectStrava: %v", err)
	}
	if conn := resp.GetConnection(); conn.GetConnected() || conn.GetDeletingActivities() {
		t.Errorf("got connection %v after disconnecting, want none, deleting nothing", conn)
	}
	if got := len(listed()); got != 3 {
		t.Errorf("got %d activities after disconnecting with agreement, want 3", got)
	}
	e.strava.mu.Lock()
	deauthorizations := e.strava.deauthorizations
	e.strava.mu.Unlock()
	if deauthorizations != 1 {
		t.Errorf("revoked access on Strava %d times, want once", deauthorizations)
	}
	_, err = e.stravaAPI.DisconnectStrava(ctx, stravav1.DisconnectStravaRequest_builder{}.Build())
	wantCode(t, err, connect.CodeNotFound)

	// Connected again, one run is on Garmin Connect too, and one was
	// imported from the user's Strava archive.
	sync()
	before := listed()
	onGarmin := before["Run 1"]
	if _, err := e.activities.UpsertGarminActivity(ctx, e.userID, &activities.GarminActivity{
		GarminActivityID: 5001, Name: "Garmin run", SportType: "TrailRun",
		StartTime: onGarmin.GetStartTime().AsTime().Add(30 * time.Second), RawJSON: []byte(`{}`),
	}); err != nil {
		t.Fatalf("UpsertGarminActivity: %v", err)
	}
	if _, err := e.db.Exec(ctx, "update strava_activities set from_strava_archive = true where strava_id = 1002"); err != nil {
		t.Fatal(err)
	}
	onlyStrava := before["Run 0"]
	if !e.exists(e.responseKey("1000", "summary")) || !e.exists(e.activityKey(mustUUID(t, onlyStrava.GetId()), "streams.pb")) {
		t.Fatal("the synced activity's data isn't stored")
	}

	// Disconnecting without the agreement deletes what came from Strava.
	_, err = e.stravaAPI.DisconnectStrava(ctx, stravav1.DisconnectStravaRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("DisconnectStrava: %v", err)
	}
	e.waitForPurge()
	after := listed()
	// The run only on Strava is gone, with its data.
	if _, ok := after["Run 0"]; ok {
		t.Error("the activity only on Strava was kept")
	}
	if e.exists(e.responseKey("1000", "summary")) || e.exists(e.activityKey(mustUUID(t, onlyStrava.GetId()), "streams.pb")) {
		t.Error("the deleted activity's data was kept")
	}
	// The run on Garmin stays, without Strava's ID or samples.
	if got, ok := after["Run 1"]; !ok || got.GetStravaId() != "" || got.GetGarminActivityId() != "5001" || got.GetHasStreams() {
		t.Errorf("got activity %v, want it on Garmin only, without Strava's samples", got)
	}
	// The run from the archive stays, without Strava's responses.
	if got, ok := after["Run 2"]; !ok || got.GetStravaId() != "1002" {
		t.Errorf("got activity %v, want the archived activity kept", got)
	}
	for _, id := range []string{"1001", "1002"} {
		if e.exists(e.responseKey(id, "summary")) || e.exists(e.responseKey(id, "detail")) {
			t.Errorf("Strava's responses about activity %s were kept", id)
		}
	}

	// Agreeing beforehand keeps them when access is revoked on Strava,
	// where there's no asking.
	sync()
	keep, err := e.stravaAPI.SetStravaKeepActivities(ctx, stravav1.SetStravaKeepActivitiesRequest_builder{KeepActivities: true}.Build())
	if err != nil {
		t.Fatalf("SetStravaKeepActivities: %v", err)
	}
	if !keep.GetConnection().GetKeepActivities() {
		t.Errorf("got connection %v, want the agreement recorded", keep.GetConnection())
	}
	conn, err := e.activities.GetStravaConnection(ctx, e.userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.activities.DisconnectStrava(ctx, e.userID, conn.KeepActivities); err != nil {
		t.Fatalf("DisconnectStrava: %v", err)
	}
	e.waitForPurge()
	names := slices.Sorted(func(yield func(string) bool) {
		for name := range listed() {
			if !yield(name) {
				return
			}
		}
	})
	if !slices.Equal(names, []string{"Run 0", "Run 1", "Run 2"}) {
		t.Errorf("got activities %v, want all kept", names)
	}
}

// waitForPurge waits until the user's activities from Strava aren't being
// deleted.
func (e *env) waitForPurge() {
	e.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := e.stravaAPI.GetStravaConnection(context.Background(), stravav1.GetStravaConnectionRequest_builder{}.Build())
		if err != nil {
			e.t.Fatal(err)
		}
		if !resp.GetConnection().GetDeletingActivities() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatal("the Strava activities weren't deleted")
}
