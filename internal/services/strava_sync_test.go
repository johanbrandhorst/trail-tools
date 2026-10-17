package services_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	stravav1 "github.com/johanbrandhorst/trail-tools/internal/gen/strava/v1"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/strava"
)

// upsertSummaries stores the summaries of the fake Strava activities, as
// listed by a sync, without their details. It returns their IDs by Strava
// ID.
func (e *env) upsertSummaries() map[int64]pgtype.UUID {
	e.t.Helper()
	e.strava.mu.Lock()
	defer e.strava.mu.Unlock()
	ids := map[int64]pgtype.UUID{}
	for _, a := range e.strava.activities {
		ids[a.ID] = e.upsertSummary(a, a.TotalPhotoCount)
	}
	return ids
}

// upsertSummary stores the summary of an activity with a photo count, which
// marks it waiting for details again if it changed.
func (e *env) upsertSummary(a *strava.Activity, photoCount int32) pgtype.UUID {
	e.t.Helper()
	id, err := e.activities.UpsertActivity(context.Background(), e.userID, &activities.ActivitySummary{
		StravaID:       a.ID,
		Name:           a.Name,
		SportType:      a.SportType,
		StartTime:      a.StartDate,
		StartDateLocal: a.LocalDate(),
		Timezone:       a.TimezoneName(),
		PhotoCount:     photoCount,
		RawJSON:        a.Raw,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// pendingStravaIDs lists the Strava IDs of the activities waiting for
// details, in the order the backlog syncs them.
func (e *env) pendingStravaIDs() []int64 {
	e.t.Helper()
	pending, err := e.activities.ListActivitiesWithoutStreams(context.Background(), e.userID, 100)
	if err != nil {
		e.t.Fatal(err)
	}
	var ids []int64
	for _, p := range pending {
		ids = append(ids, p.StravaID)
	}
	return ids
}

func TestStravaRequestedActivitiesSyncFirst(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	ids := e.upsertSummaries()

	// Most recent first.
	if got := e.pendingStravaIDs(); !slices.Equal(got, []int64{1002, 1001, 1000}) {
		t.Fatalf("got pending %v, want most recent first", got)
	}
	// Requested activities come first.
	if err := e.activities.RequestActivitySync(ctx, e.userID, ids[1000]); err != nil {
		t.Fatal(err)
	}
	if got := e.pendingStravaIDs(); !slices.Equal(got, []int64{1000, 1002, 1001}) {
		t.Fatalf("got pending %v, want the requested activity first", got)
	}
	// Syncing the activity clears the request, so when it's waiting for
	// details again, e.g. as its photos changed, it's in date order.
	e.storeActivityDetail(ids[1000], nil, nil, nil)
	if got := e.pendingStravaIDs(); !slices.Equal(got, []int64{1002, 1001}) {
		t.Fatalf("got pending %v after syncing the requested activity", got)
	}
	e.strava.mu.Lock()
	a := e.strava.activities[0]
	e.strava.mu.Unlock()
	e.upsertSummary(a, a.TotalPhotoCount+1)
	if got := e.pendingStravaIDs(); !slices.Equal(got, []int64{1002, 1001, 1000}) {
		t.Fatalf("got pending %v, want the request cleared", got)
	}
	// Synced activities can't be requested.
	if err := e.activities.StoreActivityDetail(ctx, e.userID, ids[1002], 1002, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.activities.RequestActivitySync(ctx, e.userID, ids[1002]); err != nil {
		t.Fatal(err)
	}
	if got := e.pendingStravaIDs(); !slices.Equal(got, []int64{1001, 1000}) {
		t.Fatalf("got pending %v, want synced activities left out", got)
	}
}

func TestStravaSyncActivityOnDemand(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	ids := e.upsertSummaries()
	syncActivity := func(id pgtype.UUID) (*stravav1.SyncActivityResponse, error) {
		return e.stravaAPI.SyncActivity(ctx, stravav1.SyncActivityRequest_builder{ActivityId: store.UUIDToString(id)}.Build())
	}

	// Activities can't sync without a Strava connection.
	_, err := syncActivity(ids[1000])
	wantCode(t, err, connect.CodeFailedPrecondition)
	e.connectStrava()
	_, err = e.stravaAPI.SyncActivity(ctx, stravav1.SyncActivityRequest_builder{ActivityId: "nope"}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = syncActivity(pgtype.UUID{Bytes: [16]byte{1}, Valid: true})
	wantCode(t, err, connect.CodeNotFound)

	// The fake's first request for streams is rate limited. The activity is
	// requested, so the backlog syncs it first, before more recent ones.
	resp, err := syncActivity(ids[1000])
	if err != nil {
		t.Fatalf("SyncActivity: %v", err)
	}
	if resp.GetSynced() || !resp.HasResumeTime() {
		t.Errorf("got %v, want a rate limited sync with a resume time", resp)
	}
	if conn := e.waitForSync(); conn.GetSyncState() != stravav1.SyncState_SYNC_STATE_IDLE || conn.GetPendingStreamCount() != 0 {
		t.Fatalf("unexpected connection after the backlog %v", conn)
	}
	e.strava.mu.Lock()
	streamIDs, priorities := slices.Clone(e.strava.streamIDs), slices.Clone(e.strava.streamPriorities)
	e.strava.mu.Unlock()
	if !slices.Equal(streamIDs, []int64{1000, 1000, 1002, 1001}) {
		t.Errorf("got streams fetched for %v, want the requested activity first", streamIDs)
	}
	// The activity the user asked for may use the reserve, the backlog not.
	if !slices.Equal(priorities, []strava.Priority{strava.PriorityInteractive, strava.PriorityBackground, strava.PriorityBackground, strava.PriorityBackground}) {
		t.Errorf("got priorities %v, want interactive for the request only", priorities)
	}
	got, err := e.activity.GetActivity(ctx, activitiesv1.GetActivityRequest_builder{Id: store.UUIDToString(ids[1000])}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if !got.GetActivity().GetHasStreams() || got.GetActivity().GetDescription() != "Felt good" || len(got.GetPhotos()) != 2 {
		t.Errorf("unexpected synced activity %v", got)
	}

	// Synced activities aren't fetched again.
	resp, err = syncActivity(ids[1001])
	if err != nil || !resp.GetSynced() || resp.HasResumeTime() {
		t.Errorf("got %v, %v, want a synced activity", resp, err)
	}

	// An activity the backlog is fetching is fetched once: the request
	// waits for the backlog and finds it synced.
	e.strava.mu.Lock()
	a := e.strava.activities[1]
	e.strava.blockStreams = a.ID
	e.strava.streamsBlocked = make(chan struct{})
	e.strava.unblockStreams = make(chan struct{})
	streamCalls := len(e.strava.streamIDs)
	e.strava.mu.Unlock()
	e.upsertSummary(a, a.TotalPhotoCount+1)
	if _, err := e.stravaAPI.SyncStrava(ctx, stravav1.SyncStravaRequest_builder{}.Build()); err != nil {
		t.Fatal(err)
	}
	<-e.strava.streamsBlocked
	result := make(chan *stravav1.SyncActivityResponse)
	go func() {
		resp, err := syncActivity(ids[1001])
		if err != nil {
			t.Error(err)
		}
		result <- resp
	}()
	// Give the request time to wait for the backlog.
	time.Sleep(100 * time.Millisecond)
	close(e.strava.unblockStreams)
	if resp := <-result; !resp.GetSynced() {
		t.Errorf("got %v, want the activity synced by the backlog", resp)
	}
	e.waitForSync()
	e.strava.mu.Lock()
	streamIDs = slices.Clone(e.strava.streamIDs[streamCalls:])
	e.strava.mu.Unlock()
	if !slices.Equal(streamIDs, []int64{1001}) {
		t.Errorf("got streams fetched for %v, want the activity fetched once", streamIDs)
	}
}
