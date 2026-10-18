package services_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	stravav1 "github.com/johanbrandhorst/trail-tools/internal/gen/strava/v1"
	tripsv1 "github.com/johanbrandhorst/trail-tools/internal/gen/trips/v1"
	stravasvc "github.com/johanbrandhorst/trail-tools/internal/services/strava"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/strava"
)

// allActivities is a window of activities' start times that has them all.
const allActivities = 100 * 365 * 24 * time.Hour

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
	pending, err := e.activities.ListActivitiesWithoutStreams(context.Background(), e.userID, allActivities, 100)
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

// scheduledSyncer returns a syncer that only syncs when it's called, as on
// Cloud Run.
func (e *env) scheduledSyncer() *stravasvc.Syncer {
	log := slog.New(slog.DiscardHandler)
	return stravasvc.NewSyncer(context.Background(), log, errreport.NewLocal(log), e.activities, e.strava, false, nil)
}

// streamsFetched returns the Strava IDs of the activities streams were
// fetched for, in order.
func (e *env) streamsFetched() []int64 {
	e.strava.mu.Lock()
	defer e.strava.mu.Unlock()
	return slices.Clone(e.strava.streamIDs)
}

func TestStravaSyncFetchesDetailsOfRecentActivities(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	e.connectStrava()
	e.strava.mu.Lock()
	// Don't start at the rate limit.
	e.strava.rateLimited = true
	latest := e.strava.activities[2].StartDate
	e.strava.mu.Unlock()
	// An activity from two years before the latest one.
	old := latest.AddDate(-2, 0, 0)
	e.strava.upsertActivity(&strava.Activity{
		ID: 900, Name: "Old run", SportType: "Run", StartDate: old, StartDateLocal: old.Format(time.RFC3339),
	})

	syncer := e.scheduledSyncer()
	if err := syncer.SyncDue(ctx); err != nil {
		t.Fatalf("SyncDue: %v", err)
	}
	if fetched := e.streamsFetched(); slices.Contains(fetched, 900) || len(fetched) != 3 {
		t.Errorf("fetched streams of %v, want the three recent activities", fetched)
	}
	// The old activity isn't waiting for its details, so the sync is done.
	resp, err := e.stravaAPI.GetStravaConnection(ctx, stravav1.GetStravaConnectionRequest_builder{}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if conn := resp.GetConnection(); conn.GetActivityCount() != 4 || conn.GetPendingStreamCount() != 0 || conn.GetSyncState() != stravav1.SyncState_SYNC_STATE_IDLE {
		t.Errorf("got %d activities, %d pending and state %v, want 4, none pending and idle",
			conn.GetActivityCount(), conn.GetPendingStreamCount(), conn.GetSyncState())
	}

	// Its details are fetched when it's linked to a report.
	pending, err := e.activities.ListActivitiesWithoutStreams(ctx, e.userID, allActivities, 10)
	if err != nil || len(pending) != 1 || pending[0].StravaID != 900 {
		t.Fatalf("got pending activities %v (%v), want the old one", pending, err)
	}
	if _, err := e.trips.CreateTripReport(ctx, tripsv1.CreateTripReportRequest_builder{ActivityId: store.UUIDToString(pending[0].ID)}.Build()); err != nil {
		t.Fatalf("CreateTripReport: %v", err)
	}
	a, err := e.activities.GetActivity(ctx, e.userID, pending[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !a.StreamsSynced || !slices.Contains(e.streamsFetched(), 900) {
		t.Error("the old activity wasn't synced when a trip report was created from it")
	}
}

func TestScheduledStravaSyncTakesTurns(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	e.connectStravaAs(e.userID, 7)
	e.connectStravaAs(e.otherUserID, 8)
	e.strava.mu.Lock()
	e.strava.rateLimited = true
	e.strava.activities = nil
	e.strava.mu.Unlock()
	// 30 activities, which both athletes get from the fake.
	start := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	for i := range 30 {
		day := start.AddDate(0, 0, i)
		e.strava.upsertActivity(&strava.Activity{
			ID: int64(i + 1), Name: "Run", SportType: "Run", StartDate: day, StartDateLocal: day.Format(time.RFC3339),
		})
	}

	if err := e.scheduledSyncer().SyncDue(ctx); err != nil {
		t.Fatalf("SyncDue: %v", err)
	}
	// Each user's first turn fetches a batch of 25, the newest first, then
	// each fetches the rest.
	fetched := e.streamsFetched()
	if len(fetched) != 60 || fetched[0] != 30 || fetched[25] != 30 || fetched[50] != 5 || fetched[55] != 5 {
		t.Errorf("fetched streams in the order %v, want a batch for each user in turn", fetched)
	}
	for _, userID := range []pgtype.UUID{e.userID, e.otherUserID} {
		conn, err := e.activities.GetStravaConnection(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		_, pending, err := e.activities.CountActivities(ctx, userID, allActivities)
		if err != nil {
			t.Fatal(err)
		}
		if conn.SyncState != activities.SyncStateIdle || pending != 0 {
			t.Errorf("got state %q with %d pending, want idle with none", conn.SyncState, pending)
		}
	}
}

// stravaConnection returns the user's Strava connection as stored.
func (e *env) stravaConnection(userID pgtype.UUID) *activities.StravaConnection {
	e.t.Helper()
	conn, err := e.activities.GetStravaConnection(context.Background(), userID)
	if err != nil {
		e.t.Fatal(err)
	}
	return conn
}

// exec runs a statement on the database, to set up a test.
func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(context.Background(), sql, args...); err != nil {
		e.t.Fatal(err)
	}
}

// activityIDs returns the IDs of the user's activities by Strava ID.
func (e *env) activityIDs() map[int64]pgtype.UUID {
	e.t.Helper()
	list, err := e.activities.ListActivities(context.Background(), e.userID, activities.ListActivitiesFilter{Limit: 100})
	if err != nil {
		e.t.Fatal(err)
	}
	ids := map[int64]pgtype.UUID{}
	for _, a := range list {
		ids[a.StravaID] = a.ID
	}
	return ids
}

func TestStravaSyncSkipsFailingActivities(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	e.connectStrava()
	e.strava.rateLimited = true
	e.strava.failStreams[1001] = true
	sync := func() *stravav1.StravaConnection {
		t.Helper()
		if _, err := e.stravaAPI.SyncStrava(ctx, stravav1.SyncStravaRequest_builder{}.Build()); err != nil {
			t.Fatalf("SyncStrava: %v", err)
		}
		return e.waitForSync()
	}
	streamCalls := func(id int64) int {
		e.strava.mu.Lock()
		defer e.strava.mu.Unlock()
		n := 0
		for _, got := range e.strava.streamIDs {
			if got == id {
				n++
			}
		}
		return n
	}

	// An activity that fails doesn't fail the sync: the others sync, and it
	// is retried by the next syncs, once each.
	for i := range activities.MaxActivitySyncFailures - 1 {
		conn := sync()
		if conn.GetSyncState() != stravav1.SyncState_SYNC_STATE_IDLE || conn.GetActivityCount() != 3 || conn.GetPendingStreamCount() != 1 {
			t.Fatalf("sync %d: got %v, want the other activities synced", i, conn)
		}
		if n := streamCalls(1001); n != i+1 {
			t.Fatalf("sync %d: got %d attempts, want one per sync", i, n)
		}
	}
	// After failing too often, it's skipped, and the sync says so.
	conn := sync()
	if conn.GetSyncState() != stravav1.SyncState_SYNC_STATE_IDLE || conn.GetPendingStreamCount() != 0 ||
		!strings.Contains(conn.GetSyncMessage(), "the details of 1 failed to sync") {
		t.Fatalf("got %v, want the failing activity skipped", conn)
	}
	sync()
	if n := streamCalls(1001); n != activities.MaxActivitySyncFailures {
		t.Errorf("got %d attempts, want the skipped activity left alone", n)
	}

	// Opening the activity syncs it again.
	e.strava.mu.Lock()
	e.strava.failStreams[1001] = false
	e.strava.mu.Unlock()
	resp, err := e.stravaAPI.SyncActivity(ctx, stravav1.SyncActivityRequest_builder{ActivityId: store.UUIDToString(e.activityIDs()[1001])}.Build())
	if err != nil || !resp.GetSynced() {
		t.Fatalf("got %v, %v, want the skipped activity synced", resp, err)
	}
	if conn := sync(); conn.GetPendingStreamCount() != 0 || strings.Contains(conn.GetSyncMessage(), "failed") {
		t.Errorf("got %v, want no failed activities", conn)
	}

	// Activities failing in a row fail the sync, as Strava is more likely
	// the problem than they are.
	e.strava.mu.Lock()
	for _, a := range e.strava.activities {
		e.strava.failStreams[a.ID] = true
	}
	e.strava.mu.Unlock()
	e.upsertSummaries()
	e.exec("update strava_activities set streams_synced = false where user_id = $1", e.userID)
	if conn := sync(); conn.GetSyncState() != stravav1.SyncState_SYNC_STATE_FAILED || !strings.Contains(conn.GetSyncMessage(), "in a row failed") {
		t.Errorf("got %v, want the sync failed", conn)
	}
}

func TestStravaFailedSyncRetries(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	syncer := e.scheduledSyncer()
	e.connectStrava()
	e.strava.rateLimited = true
	e.strava.listErr = errors.New("unexpected status 503 from Strava")
	listings := func() int {
		e.strava.mu.Lock()
		defer e.strava.mu.Unlock()
		return len(e.strava.listed)
	}
	retryNow := func() {
		e.exec("update strava_connections set resume_time = now() - interval '1 minute' where user_id = $1", e.userID)
	}

	// A failed sync is retried after a while, waiting longer with each
	// failure in a row.
	if err := syncer.SyncDue(ctx); err != nil {
		t.Fatal(err)
	}
	conn := e.stravaConnection(e.userID)
	if conn.SyncState != activities.SyncStateFailed || conn.SyncFailures != 1 || !strings.Contains(conn.SyncMessage, "503") {
		t.Fatalf("got %+v, want a failed sync", conn)
	}
	if wait := time.Until(conn.ResumeTime); wait < 14*time.Minute || wait > 16*time.Minute {
		t.Errorf("got a retry in %v, want 15 minutes", wait)
	}
	if err := syncer.SyncDue(ctx); err != nil || listings() != 1 {
		t.Fatalf("got %v and %d listings, want no retry yet", err, listings())
	}
	retryNow()
	if err := syncer.SyncDue(ctx); err != nil || listings() != 2 {
		t.Fatalf("got %v and %d listings, want a retry", err, listings())
	}
	conn = e.stravaConnection(e.userID)
	if wait := time.Until(conn.ResumeTime); conn.SyncFailures != 2 || wait < 29*time.Minute || wait > 31*time.Minute {
		t.Errorf("got %d failures and a retry in %v, want 2 and 30 minutes", conn.SyncFailures, wait)
	}

	// Once the sync succeeds, the failures are forgotten.
	e.strava.mu.Lock()
	e.strava.listErr = nil
	e.strava.mu.Unlock()
	retryNow()
	if err := syncer.SyncDue(ctx); err != nil {
		t.Fatal(err)
	}
	conn = e.stravaConnection(e.userID)
	if conn.SyncState != activities.SyncStateIdle || conn.SyncFailures != 0 || !conn.ResumeTime.IsZero() || conn.LastSyncTime.IsZero() {
		t.Fatalf("got %+v, want a finished sync", conn)
	}

	// A revoked connection waits for Strava to be reconnected, rather than
	// being retried.
	e.strava.mu.Lock()
	e.strava.revoked = true
	e.strava.mu.Unlock()
	e.exec("update strava_connections set expiry = now() - interval '1 hour' where user_id = $1", e.userID)
	syncer.SyncOnce(ctx, e.userID, false)
	conn = e.stravaConnection(e.userID)
	if conn.SyncState != activities.SyncStateFailed || !conn.ResumeTime.IsZero() || !strings.Contains(conn.SyncMessage, "reconnect Strava") {
		t.Fatalf("got %+v, want a sync waiting for Strava to be reconnected", conn)
	}
	e.exec("update strava_connections set last_sync_time = now() - interval '1 day' where user_id = $1", e.userID)
	if err := syncer.SyncDue(ctx); err != nil || e.stravaConnection(e.userID).SyncFailures != 1 {
		t.Fatalf("got %v, want no retry of a revoked connection", err)
	}

	// Reconnecting starts afresh.
	e.strava.mu.Lock()
	e.strava.revoked = false
	e.strava.mu.Unlock()
	e.connectStrava()
	conn = e.stravaConnection(e.userID)
	if conn.SyncState != activities.SyncStateIdle || conn.SyncFailures != 0 || !conn.ResumeTime.IsZero() || conn.SyncMessage != "" {
		t.Errorf("got %+v, want the failure reset on reconnecting", conn)
	}
}

func TestStravaTokenRefreshSerialized(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	e.connectStrava()
	// Like Strava may, each refresh replaces the refresh token, and the
	// replaced one is rejected as if access was revoked.
	e.strava.rateLimited = true
	e.strava.rotateTokens = true
	e.strava.validRefreshToken = "refresh"
	e.strava.refreshDelay = 100 * time.Millisecond
	stale := e.stravaConnection(e.userID)

	// Webhook events and syncs that find the token expired at the same time
	// refresh it once, and use the refreshed token.
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := e.syncer.SyncActivity(ctx, e.userID, 1000); err != nil {
				t.Errorf("SyncActivity: %v", err)
			}
		})
	}
	wg.Wait()
	if e.strava.refreshes != 1 {
		t.Errorf("got %d refreshes, want 1", e.strava.refreshes)
	}
	if conn := e.stravaConnection(e.userID); conn.RefreshToken != "refresh-1" {
		t.Errorf("got refresh token %q, want the replaced one stored", conn.RefreshToken)
	}

	// Checking the authorization with a connection read before the refresh
	// uses the current refresh token, rather than finding it revoked.
	authorized, err := e.syncer.Authorized(ctx, stale)
	if err != nil || !authorized {
		t.Errorf("got %v, %v, want the connection authorized", authorized, err)
	}
	if e.strava.refreshes != 2 || stale.RefreshToken != "refresh-2" {
		t.Errorf("got %d refreshes and refresh token %q, want the current one used", e.strava.refreshes, stale.RefreshToken)
	}
}

func TestScheduledStravaSyncOrder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	e.connectStravaAs(e.userID, 7)
	e.connectStravaAs(e.otherUserID, 8)
	e.strava.rateLimited = true
	// The user's latest sync started an hour ago, and the other user never
	// synced, so the other user goes first.
	e.exec("update strava_connections set sync_start_time = now() - interval '1 hour' where user_id = $1", e.userID)
	if err := e.scheduledSyncer().SyncDue(ctx); err != nil {
		t.Fatalf("SyncDue: %v", err)
	}
	first, second := e.stravaConnection(e.otherUserID), e.stravaConnection(e.userID)
	if first.SyncState != activities.SyncStateIdle || second.SyncState != activities.SyncStateIdle {
		t.Fatalf("got states %q and %q, want both synced", first.SyncState, second.SyncState)
	}
	if !first.SyncStartTime.Before(second.SyncStartTime) {
		t.Errorf("got start times %v and %v, want the user who waited longest first", first.SyncStartTime, second.SyncStartTime)
	}
	// Connections are listed by when their latest sync started, so users a
	// run ends before go first in the next.
	conns, err := e.activities.ListStravaConnections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var order []pgtype.UUID
	for _, conn := range conns {
		if conn.UserID == e.otherUserID || conn.UserID == e.userID {
			order = append(order, conn.UserID)
		}
	}
	if !slices.Equal(order, []pgtype.UUID{e.otherUserID, e.userID}) {
		t.Errorf("got order %v, want the connection whose sync started first first", order)
	}
}

func TestStravaSyncPhotosListedWithoutCount(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	e.strava.mu.Lock()
	e.strava.listWithoutPhotos = true
	e.strava.mu.Unlock()
	e.connectStrava()
	sync := func(full bool) {
		t.Helper()
		if _, err := e.stravaAPI.SyncStrava(ctx, stravav1.SyncStravaRequest_builder{Full: full}.Build()); err != nil {
			t.Fatal(err)
		}
		e.waitForSync()
	}
	photoCount := func() int32 {
		t.Helper()
		list, err := e.activity.ListActivities(ctx, activitiesv1.ListActivitiesRequest_builder{}.Build())
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range list.GetActivities() {
			if a.GetStravaId() == "1000" {
				got, err := e.activity.GetActivity(ctx, activitiesv1.GetActivityRequest_builder{Id: a.GetId()}.Build())
				if err != nil {
					t.Fatal(err)
				}
				if got.GetActivity().GetPhotoCount() != a.GetPhotoCount() {
					t.Errorf("got photo count %d, want %d as listed", got.GetActivity().GetPhotoCount(), a.GetPhotoCount())
				}
				return a.GetPhotoCount()
			}
		}
		t.Fatal("the activity with photos wasn't synced")
		return 0
	}

	// The photos are synced with the details, so the activity has them
	// although the list said it had none.
	sync(false)
	if got := photoCount(); got != 2 {
		t.Errorf("got photo count %d, want the 2 synced photos", got)
	}
	// Listing the activity again doesn't fetch its details again, nor
	// forget its photos.
	e.strava.mu.Lock()
	streamCalls := len(e.strava.streamIDs)
	e.strava.mu.Unlock()
	sync(true)
	e.strava.mu.Lock()
	refetched := slices.Clone(e.strava.streamIDs[streamCalls:])
	e.strava.mu.Unlock()
	if len(refetched) != 0 {
		t.Errorf("got details fetched again for %v", refetched)
	}
	if got := photoCount(); got != 2 {
		t.Errorf("got photo count %d after listing again, want 2", got)
	}
}
