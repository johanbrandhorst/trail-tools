package strava

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/strava"
)

// SyncRepository stores synced activities and the sync state.
type SyncRepository interface {
	GetStravaConnection(ctx context.Context, userID pgtype.UUID) (*activities.StravaConnection, error)
	ListStravaConnections(ctx context.Context) ([]*activities.StravaConnection, error)
	UpdateStravaTokens(ctx context.Context, userID pgtype.UUID, accessToken, refreshToken string, expiry time.Time) error
	UpdateStravaSyncState(ctx context.Context, userID pgtype.UUID, state activities.SyncState, message string, resumeTime, lastSyncTime time.Time) error
	UpsertActivity(ctx context.Context, userID pgtype.UUID, a *activities.ActivitySummary) (pgtype.UUID, error)
	StoreListedActivities(ctx context.Context, userID pgtype.UUID, page []*activities.ActivitySummary, listBefore time.Time) error
	LatestActivityStartTime(ctx context.Context, userID pgtype.UUID) (time.Time, error)
	ListActivitiesWithoutStreams(ctx context.Context, userID pgtype.UUID, window time.Duration, limit int32) ([]activities.PendingStreams, error)
	GetActivity(ctx context.Context, userID, activityID pgtype.UUID) (*activities.Activity, error)
	RequestActivitySync(ctx context.Context, userID, activityID pgtype.UUID) error
	CountActivities(ctx context.Context, userID pgtype.UUID, window time.Duration) (total, pendingStreams int64, err error)
	StoreActivityDetail(ctx context.Context, userID, activityID pgtype.UUID, stravaID int64, detail *activities.Detail, streams *activitiesv1.ActivityStreams, bounds *geo.Bounds) error
	DeleteActivityByStravaID(ctx context.Context, userID pgtype.UUID, stravaID int64) error
}

// StravaClient is the Strava API.
type StravaClient interface {
	Refresh(ctx context.Context, refreshToken string) (*strava.Token, error)
	ListActivities(ctx context.Context, accessToken string, before time.Time, perPage int) ([]*strava.Activity, error)
	GetActivity(ctx context.Context, accessToken string, id int64) (*strava.Activity, error)
	GetStreams(ctx context.Context, accessToken string, id int64) (*strava.Streams, error)
	GetActivityPhotos(ctx context.Context, accessToken string, id int64) ([]*strava.Photo, error)
}

// Trigger starts a scheduled sync right away, e.g. by running the Cloud
// Scheduler job that calls SyncPath.
type Trigger interface {
	Run(ctx context.Context) error
}

const (
	// recentOverlap is how far before the latest synced activity an
	// incremental sync stops, to pick up recent edits.
	recentOverlap = 7 * 24 * time.Hour
	// autoSyncAge is the age of the last sync after which a background sync
	// starts automatically.
	autoSyncAge = 6 * time.Hour
	// autoSyncCheckInterval is how often to check for stale syncs.
	autoSyncCheckInterval = time.Hour
	// streamBatchSize is the number of activities fetched streams for between
	// progress updates, and in a user's turn of a scheduled sync.
	streamBatchSize = 25
	// eagerSyncAge is how far back from a user's latest activity syncs fetch
	// the details, photos and streams of activities. Older activities are
	// fetched when they're requested, e.g. opened, as fetching them all for a
	// long history would take days of Strava's rate limit, which all users
	// share.
	eagerSyncAge = 365 * 24 * time.Hour
	// staleSyncAge is how long a running sync goes without updating its
	// state before it is considered interrupted. Syncs update it after each
	// page of activities and each batch of streams.
	staleSyncAge = 3 * time.Minute
	// inlineSyncBudget is how long Request syncs for within the request when
	// syncs neither run in the background nor can be triggered.
	inlineSyncBudget = 45 * time.Second
)

// Syncer syncs activities from Strava in the background. At most one sync
// runs per user.
type Syncer struct {
	log    *slog.Logger
	report *errreport.Reporter
	repo   SyncRepository
	client StravaClient
	nowFn  func() time.Time

	// Whether syncs run in background goroutines. Otherwise, syncs only run
	// within SyncOnce and SyncDue calls.
	background bool
	// Starts scheduled syncs when requested, if syncs don't run in the
	// background. May be nil.
	trigger Trigger

	mu      sync.Mutex
	ctx     context.Context
	running map[pgtype.UUID]bool
	// The activities being synced, by Strava ID, closed when done.
	claims map[int64]chan struct{}
	wg     sync.WaitGroup
}

// NewSyncer creates a new Syncer. Syncs are stopped when ctx is cancelled.
// If background is false, Start does nothing and syncs must be run with
// SyncOnce and SyncDue, e.g. from a scheduled job, which trigger, if not
// nil, runs when a sync is requested.
func NewSyncer(ctx context.Context, log *slog.Logger, report *errreport.Reporter, repo SyncRepository, client StravaClient, background bool, trigger Trigger) *Syncer {
	return &Syncer{
		log:        log,
		report:     report,
		repo:       repo,
		client:     client,
		nowFn:      time.Now,
		background: background,
		trigger:    trigger,
		ctx:        ctx,
		running:    map[pgtype.UUID]bool{},
		claims:     map[int64]chan struct{}{},
	}
}

// Background returns whether syncs run in the background.
func (s *Syncer) Background() bool {
	return s.background
}

// Start starts syncing the user's activities in the background, unless a
// sync is already running or background syncs are disabled. It returns
// whether a new sync was started.
func (s *Syncer) Start(userID pgtype.UUID, full bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.background || s.running[userID] || s.ctx.Err() != nil {
		return false
	}
	s.running[userID] = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.running, userID)
			s.mu.Unlock()
		}()
		s.sync(s.ctx, userID, syncOptions{full: full, wait: true})
	}()
	return true
}

// Request syncs the user's activities soon. In the background, if syncs run
// there. Otherwise it marks the connection due a sync and runs the trigger,
// so a scheduled sync runs it right away. Without a trigger, or if it
// fails, it syncs within the request for a while instead, and scheduled
// syncs continue from where it stops.
func (s *Syncer) Request(ctx context.Context, userID pgtype.UUID, full bool) error {
	if s.background {
		s.Start(userID, full)
		return nil
	}
	if s.trigger != nil {
		conn, err := s.repo.GetStravaConnection(ctx, userID)
		if err != nil {
			return err
		}
		if s.Active(conn) {
			return nil
		}
		if full {
			// List all activities, as if an earlier listing was interrupted.
			if err := s.repo.StoreListedActivities(ctx, userID, nil, s.nowFn()); err != nil {
				return err
			}
		}
		// A running sync is due, so the triggered sync runs it.
		if err := s.setState(ctx, userID, activities.SyncStateRunning, "Starting", time.Time{}); err != nil {
			return err
		}
		err = s.trigger.Run(ctx)
		if err == nil {
			return nil
		}
		s.report.Report(ctx, "Failed to trigger a Strava sync, syncing within the request instead", err)
	}
	syncCtx, cancel := context.WithTimeout(ctx, inlineSyncBudget)
	defer cancel()
	s.SyncOnce(syncCtx, userID, full)
	return nil
}

// Active returns whether a sync recorded as running is still running,
// rather than interrupted, e.g. by a restart or the end of its time budget.
func (s *Syncer) Active(conn *activities.StravaConnection) bool {
	return conn.SyncState == activities.SyncStateRunning &&
		(s.Running(conn.UserID) || s.nowFn().Sub(conn.SyncStateTime) < staleSyncAge)
}

// SyncOnce syncs the user's activities until done, the rate limit is
// reached or ctx is done, without waiting for rate limits to reset. It is
// used where background work isn't possible, such as on Cloud Run with
// request based billing. It returns false if a sync is already running.
func (s *Syncer) SyncOnce(ctx context.Context, userID pgtype.UUID, full bool) bool {
	_, ok := s.syncOnce(ctx, userID, syncOptions{full: full})
	return ok
}

// syncOnce runs a sync unless one is already running for the user. It
// returns whether the sync stopped at the end of a turn, and whether it ran.
func (s *Syncer) syncOnce(ctx context.Context, userID pgtype.UUID, opts syncOptions) (more, ok bool) {
	s.mu.Lock()
	if s.running[userID] {
		s.mu.Unlock()
		return false, false
	}
	s.running[userID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, userID)
		s.mu.Unlock()
	}()
	return s.sync(ctx, userID, opts), true
}

// SyncDue syncs the connections that are due a sync until they're done, the
// rate limit is reached or ctx is done. A sync is due if it was interrupted,
// the rate limit has reset or the last sync is old.
//
// Users take turns, as Strava's rate limit is shared by all of them: the
// first turn lists a user's new activities and fetches a batch of the
// details and streams waiting, and later turns fetch a batch each. So one
// user's backlog doesn't hold up the others' new activities.
func (s *Syncer) SyncDue(ctx context.Context) error {
	conns, err := s.repo.ListStravaConnections(ctx)
	if err != nil {
		return err
	}
	var users []pgtype.UUID
	for _, conn := range conns {
		if s.due(conn) {
			users = append(users, conn.UserID)
		}
	}
	for first := true; len(users) > 0; first = false {
		var next []pgtype.UUID
		for _, userID := range users {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if more, _ := s.syncOnce(ctx, userID, syncOptions{turn: true, skipListing: !first}); more {
				next = append(next, userID)
			}
		}
		users = next
	}
	return nil
}

// due returns whether a connection is due a sync.
func (s *Syncer) due(conn *activities.StravaConnection) bool {
	now := s.nowFn()
	switch conn.SyncState {
	case activities.SyncStateRunning:
		return true
	case activities.SyncStateRateLimited:
		return now.After(conn.ResumeTime)
	case activities.SyncStateFailed:
		return false
	default:
		return now.Sub(conn.LastSyncTime) > autoSyncAge
	}
}

// Running returns whether a sync is running for the user.
func (s *Syncer) Running(userID pgtype.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[userID]
}

// Wait waits for all running syncs to stop.
func (s *Syncer) Wait() {
	s.wg.Wait()
}

// RunBackground resumes interrupted syncs and periodically syncs connections
// that haven't synced recently, until ctx is cancelled.
func (s *Syncer) RunBackground(ctx context.Context) {
	ticker := time.NewTicker(autoSyncCheckInterval)
	defer ticker.Stop()
	for {
		conns, err := s.repo.ListStravaConnections(ctx)
		if err != nil {
			s.report.Report(ctx, "Failed to list Strava connections", err)
		}
		for _, conn := range conns {
			// Rate limited syncs resume waiting for the limit to reset.
			if conn.SyncState == activities.SyncStateRateLimited || s.due(conn) {
				s.Start(conn.UserID, false)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// errRateLimited is returned when a sync stops at the rate limit rather than
// waiting for it to reset.
var errRateLimited = errors.New("rate limited")

// errTurnOver is returned when a sync stops at the end of its turn.
var errTurnOver = errors.New("turn over")

// syncOptions are how a sync runs.
type syncOptions struct {
	// Lists all activities, rather than the new ones.
	full bool
	// Waits for rate limits to reset, rather than stopping.
	wait bool
	// Stops after a batch of the activities waiting for details and
	// streams, for another user's turn.
	turn bool
	// Doesn't list activities, as the user's first turn did.
	skipListing bool
}

// sync runs a sync to completion, or to the end of its turn, recording
// progress and errors in the connection's sync state. It returns whether it
// stopped at the end of its turn, with more to sync.
func (s *Syncer) sync(ctx context.Context, userID pgtype.UUID, opts syncOptions) bool {
	user := slog.String("user_id", store.UUIDToString(userID))
	log, report := s.log.With(user), s.report.With(user)
	if !opts.skipListing {
		log.InfoContext(ctx, "Starting Strava sync", slog.Bool("full", opts.full))
	}
	err := s.runSync(ctx, log, userID, opts)
	if ctx.Err() != nil {
		// The server is shutting down or the time is up. Leave the state as
		// running so the sync resumes later.
		return false
	}
	if errors.Is(err, errTurnOver) {
		// The state is left as running, with the progress so far.
		return true
	}
	if errors.Is(err, errRateLimited) {
		// The rate limited state is already recorded.
		log.InfoContext(ctx, "Strava sync paused at the rate limit")
		return false
	}
	// Use a fresh context so the final state is recorded.
	stateCtx := context.WithoutCancel(ctx)
	if err != nil {
		report.Report(ctx, "Strava sync failed", err)
		if err := s.repo.UpdateStravaSyncState(stateCtx, userID, activities.SyncStateFailed, err.Error(), time.Time{}, time.Time{}); err != nil {
			report.Report(ctx, "Failed to record sync failure", err)
		}
		return false
	}
	total, _, err := s.repo.CountActivities(stateCtx, userID, eagerSyncAge)
	if err != nil {
		report.Report(ctx, "Failed to count activities", err)
	}
	message := fmt.Sprintf("Synced %d activities", total)
	if err := s.repo.UpdateStravaSyncState(stateCtx, userID, activities.SyncStateIdle, message, time.Time{}, s.nowFn()); err != nil {
		report.Report(ctx, "Failed to record sync completion", err)
	}
	log.InfoContext(ctx, "Finished Strava sync", slog.Int64("activities", total))
	return false
}

func (s *Syncer) runSync(ctx context.Context, log *slog.Logger, userID pgtype.UUID, opts syncOptions) error {
	conn, err := s.repo.GetStravaConnection(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get Strava connection: %w", err)
	}
	if !opts.skipListing {
		if err := s.listActivities(ctx, conn, opts); err != nil {
			return err
		}
	}

	// Fetch details and streams: activities users asked for first, then the
	// most recent first.
	for {
		pending, err := s.repo.ListActivitiesWithoutStreams(ctx, userID, eagerSyncAge, streamBatchSize)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		_, remaining, err := s.repo.CountActivities(ctx, userID, eagerSyncAge)
		if err != nil {
			return err
		}
		if err := s.setState(ctx, userID, activities.SyncStateRunning, fmt.Sprintf("Fetching activity details: %d remaining", remaining), time.Time{}); err != nil {
			return err
		}
		for _, p := range pending {
			_, err := s.syncPending(ctx, conn, p)
			var rateErr *strava.RateLimitError
			if opts.wait && errors.As(err, &rateErr) {
				// List the pending activities again after the reset, as
				// users may have requested some meanwhile.
				if err := s.waitForReset(ctx, conn, rateErr.ResetAt); err != nil {
					return err
				}
				break
			}
			if err != nil {
				return fmt.Errorf("failed to sync activity %d: %w", p.StravaID, err)
			}
		}
		if opts.turn {
			return errTurnOver
		}
	}
}

// listActivities lists the user's activities, storing their summaries.
func (s *Syncer) listActivities(ctx context.Context, conn *activities.StravaConnection, opts syncOptions) error {
	userID := conn.UserID
	if err := s.setState(ctx, userID, activities.SyncStateRunning, "Fetching activities", time.Time{}); err != nil {
		return err
	}

	// Fetch activity summaries, newest first, so recent activities show up
	// first. An unfinished listing continues where it stopped, to the oldest
	// activity. Otherwise a full sync lists all activities, and an
	// incremental sync stops at the ones synced before.
	before, since := conn.ListBefore, time.Time{}
	if before.IsZero() && !opts.full {
		latest, err := s.repo.LatestActivityStartTime(ctx, userID)
		if err != nil {
			return err
		}
		if !latest.IsZero() {
			since = latest.Add(-recentOverlap)
		}
	}
	fetched := 0
	for done := false; !done; {
		var list []*strava.Activity
		err := s.retry(ctx, conn, opts.wait, func() error {
			return s.withToken(ctx, conn, func(token string) error {
				var err error
				list, err = s.client.ListActivities(ctx, token, before, strava.MaxActivitiesPerPage)
				return err
			})
		})
		if err != nil {
			return fmt.Errorf("failed to list activities: %w", err)
		}
		var page []*activities.ActivitySummary
		reachedSince := false
		for _, a := range list {
			if a.StartDate.Before(since) {
				reachedSince = true
				break
			}
			if !before.IsZero() && !a.StartDate.Before(before) {
				// Listed already.
				continue
			}
			page = append(page, summaryFromStrava(a))
			before = a.StartDate
		}
		fetched += len(page)
		// Record how far the listing got with the activities, so an
		// interrupted listing continues from there.
		done = reachedSince || len(page) == 0
		listBefore := before
		if done {
			listBefore = time.Time{}
		}
		if err := s.repo.StoreListedActivities(ctx, userID, page, listBefore); err != nil {
			return err
		}
		if err := s.setState(ctx, userID, activities.SyncStateRunning, fmt.Sprintf("Fetched %d activities", fetched), time.Time{}); err != nil {
			return err
		}
	}
	return nil
}

// syncPending syncs the details, photos and streams of an activity waiting
// for them, unless another sync of the activity, which it waits for, synced
// them already. It returns whether the activity is synced.
func (s *Syncer) syncPending(ctx context.Context, conn *activities.StravaConnection, p activities.PendingStreams) (bool, error) {
	release, err := s.claim(ctx, p.StravaID)
	if err != nil {
		return false, err
	}
	defer release()
	a, err := s.repo.GetActivity(ctx, conn.UserID, p.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// Deleted meanwhile, e.g. by a webhook event.
		return false, nil
	case err != nil:
		return false, err
	case a.StreamsSynced:
		return true, nil
	}
	if err := s.syncDetails(ctx, conn, p); err != nil {
		return false, err
	}
	return true, nil
}

// claim claims an activity for syncing, so the backlog and syncs of single
// activities don't fetch the same activity at the same time. It waits while
// another sync holds the claim. The returned function releases the claim.
// Claims only hold within this server; on other servers, syncing an
// activity twice wastes requests but is harmless.
func (s *Syncer) claim(ctx context.Context, stravaID int64) (func(), error) {
	for {
		s.mu.Lock()
		busy, ok := s.claims[stravaID]
		if !ok {
			done := make(chan struct{})
			s.claims[stravaID] = done
			s.mu.Unlock()
			return func() {
				s.mu.Lock()
				delete(s.claims, stravaID)
				s.mu.Unlock()
				close(done)
			}, nil
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-busy:
		}
	}
}

// syncDetails fetches and stores the details, photos and streams of an
// activity. The details and streams are fetched at the same time, then the
// photos, if it has any.
func (s *Syncer) syncDetails(ctx context.Context, conn *activities.StravaConnection, p activities.PendingStreams) error {
	// Refresh the token if needed first, so the requests don't refresh it
	// concurrently.
	if err := s.withToken(ctx, conn, func(string) error { return nil }); err != nil {
		return err
	}
	var detail *strava.Activity
	var detailErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		detailErr = s.withToken(ctx, conn, func(token string) error {
			var err error
			detail, err = s.client.GetActivity(ctx, token, p.StravaID)
			return err
		})
	})
	streams, streamsErr := s.fetchStreams(ctx, conn, p.StravaID)
	wg.Wait()
	switch {
	case errors.Is(detailErr, strava.ErrNotFound):
		// The activity was deleted or made private on Strava.
		detail = nil
	case detailErr != nil:
		return detailErr
	}
	if streamsErr != nil {
		return streamsErr
	}
	var photos []activities.Photo
	if detail != nil {
		var err error
		if photos, err = s.fetchPhotos(ctx, conn, detail); err != nil {
			return err
		}
	}
	return s.store(ctx, conn.UserID, p, detail, photos, streams)
}

// fetchPhotos fetches the photos of an activity, if it has any.
func (s *Syncer) fetchPhotos(ctx context.Context, conn *activities.StravaConnection, detail *strava.Activity) ([]activities.Photo, error) {
	if detail.TotalPhotoCount == 0 {
		return nil, nil
	}
	var photos []*strava.Photo
	err := s.withToken(ctx, conn, func(token string) error {
		var err error
		photos, err = s.client.GetActivityPhotos(ctx, token, detail.ID)
		return err
	})
	if err != nil && !errors.Is(err, strava.ErrNotFound) {
		return nil, err
	}
	result := make([]activities.Photo, len(photos))
	for i, p := range photos {
		result[i] = activities.Photo{
			StravaID:  p.UniqueID,
			Caption:   p.Caption,
			URL:       p.URL,
			Width:     p.Width,
			Height:    p.Height,
			TakenTime: p.CreatedAt,
			RawJSON:   p.Raw,
		}
		if p.HasLocation {
			result[i].Location = &geo.Point{Lat: p.Lat, Lng: p.Lng}
		}
	}
	return result, nil
}

// fetchStreams fetches the streams of an activity. Manual activities have
// no streams, for which it returns nil.
func (s *Syncer) fetchStreams(ctx context.Context, conn *activities.StravaConnection, stravaID int64) (*strava.Streams, error) {
	var streams *strava.Streams
	err := s.withToken(ctx, conn, func(token string) error {
		var err error
		streams, err = s.client.GetStreams(ctx, token, stravaID)
		return err
	})
	if errors.Is(err, strava.ErrNotFound) {
		return nil, nil
	}
	return streams, err
}

// store stores the detail of one of the user's activities, which is nil if
// Strava no longer has it, with its photos, and its streams, which are nil
// if it has none, and marks it synced.
func (s *Syncer) store(ctx context.Context, userID pgtype.UUID, a activities.PendingStreams, detail *strava.Activity, photos []activities.Photo, streams *strava.Streams) error {
	var d *activities.Detail
	if detail != nil {
		d = &activities.Detail{Description: detail.Description, DeviceName: detail.DeviceName, RawJSON: detail.Raw, Photos: photos}
	}
	if streams == nil {
		return s.repo.StoreActivityDetail(ctx, userID, a.ID, a.StravaID, d, nil, nil)
	}
	msg, bounds := streamsFromStrava(streams, detail != nil && detail.IsFootSport())
	return s.repo.StoreActivityDetail(ctx, userID, a.ID, a.StravaID, d, msg, bounds)
}

// SyncActivity syncs one of the user's activities from Strava right away:
// its summary, details, photos and streams, or deletes it if it no longer
// exists on Strava. It is used for Strava's webhook events and before
// importing an activity's photos. Its requests are interactive, so they may
// use the rate limit reserve the backlog leaves. It doesn't wait for rate
// limits to reset, recording the connection as rate limited so a later sync
// continues instead. Syncing an activity again is harmless, so it can be
// retried.
func (s *Syncer) SyncActivity(ctx context.Context, userID pgtype.UUID, stravaID int64) error {
	ctx = strava.WithPriority(ctx, strava.PriorityInteractive)
	conn, err := s.repo.GetStravaConnection(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get Strava connection: %w", err)
	}
	// Unlike syncing a pending activity, the activity is fetched again even
	// if a concurrent sync just fetched it, as it may have changed.
	release, err := s.claim(ctx, stravaID)
	if err != nil {
		return err
	}
	defer release()
	var detail *strava.Activity
	err = s.withToken(ctx, conn, func(token string) error {
		var err error
		detail, err = s.client.GetActivity(ctx, token, stravaID)
		return err
	})
	switch {
	case errors.Is(err, strava.ErrNotFound):
		// The activity was deleted on Strava. With the activity:read_all
		// scope, private activities are found too.
		return s.repo.DeleteActivityByStravaID(ctx, userID, stravaID)
	case err != nil:
		return err
	}
	id, err := s.repo.UpsertActivity(ctx, userID, summaryFromStrava(detail))
	if err != nil {
		return err
	}
	// Fetch the photos and streams at the same time, to answer webhook
	// events quickly. Fetching the activity refreshed the token if needed,
	// so they don't refresh it concurrently.
	var photos []activities.Photo
	var photosErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		photos, photosErr = s.fetchPhotos(ctx, conn, detail)
	})
	streams, streamsErr := s.fetchStreams(ctx, conn, stravaID)
	wg.Wait()
	if err := errors.Join(photosErr, streamsErr); err != nil {
		return err
	}
	return s.store(ctx, userID, activities.PendingStreams{ID: id, StravaID: stravaID}, detail, photos, streams)
}

// SyncPending syncs the details, photos and streams of one of the user's
// activities that is waiting for them right away, for a user looking at the
// activity. Its requests are interactive, so they may use the rate limit
// reserve the backlog leaves. It returns whether the activity is synced. If
// the rate limit is reached anyway, or ctx is done first, the activity is
// marked so the backlog syncs it before the other activities, and it
// returns when the rate limit resets, if known.
func (s *Syncer) SyncPending(ctx context.Context, userID, activityID pgtype.UUID) (synced bool, resumeTime time.Time, err error) {
	ctx = strava.WithPriority(ctx, strava.PriorityInteractive)
	conn, err := s.repo.GetStravaConnection(ctx, userID)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("failed to get Strava connection: %w", err)
	}
	a, err := s.repo.GetActivity(ctx, userID, activityID)
	if err != nil {
		return false, time.Time{}, err
	}
	if a.StreamsSynced {
		return true, time.Time{}, nil
	}
	_, err = s.syncPending(ctx, conn, activities.PendingStreams{ID: a.ID, StravaID: a.StravaID})
	var rateErr *strava.RateLimitError
	switch {
	case err == nil:
		return true, time.Time{}, nil
	case errors.As(err, &rateErr):
		resumeTime = rateErr.ResetAt
	case ctx.Err() != nil:
	default:
		return false, time.Time{}, err
	}
	stateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.repo.RequestActivitySync(stateCtx, userID, activityID); err != nil {
		return false, time.Time{}, err
	}
	// Make sure a sync picks up the request. A rate limited connection is
	// due a sync once the limit resets, and a running sync lists requested
	// activities first.
	if rateErr == nil {
		return false, time.Time{}, s.SyncSoon(stateCtx, userID)
	}
	s.Start(userID, false)
	return false, resumeTime, nil
}

// SyncSoon makes sure the user's activities are synced soon, e.g. after
// syncing an activity from a webhook event failed: right away with
// background syncs, otherwise by the next scheduled sync.
func (s *Syncer) SyncSoon(ctx context.Context, userID pgtype.UUID) error {
	if s.Start(userID, false) || s.Running(userID) {
		return nil
	}
	// Connections recorded as running are due a sync.
	return s.setState(ctx, userID, activities.SyncStateRunning, "Waiting for the next sync", time.Time{})
}

// Authorized returns whether Strava still authorizes the connection, by
// refreshing its token, which fails once the athlete revokes access.
func (s *Syncer) Authorized(ctx context.Context, conn *activities.StravaConnection) (bool, error) {
	token, err := s.client.Refresh(ctx, conn.RefreshToken)
	switch {
	case errors.Is(err, strava.ErrUnauthorized):
		return false, nil
	case err != nil:
		return false, err
	}
	if err := s.repo.UpdateStravaTokens(ctx, conn.UserID, token.AccessToken, token.RefreshToken, token.Expiry); err != nil {
		return false, err
	}
	return true, nil
}

// withToken calls fn with a valid access token, refreshing it if needed. If
// the rate limit is reached, it records the state and returns an error
// wrapping both errRateLimited and the *strava.RateLimitError.
func (s *Syncer) withToken(ctx context.Context, conn *activities.StravaConnection, fn func(token string) error) error {
	if s.nowFn().Add(5 * time.Minute).After(conn.Expiry) {
		token, err := s.client.Refresh(ctx, conn.RefreshToken)
		if err != nil {
			return err
		}
		if err := s.repo.UpdateStravaTokens(ctx, conn.UserID, token.AccessToken, token.RefreshToken, token.Expiry); err != nil {
			return err
		}
		conn.AccessToken, conn.RefreshToken, conn.Expiry = token.AccessToken, token.RefreshToken, token.Expiry
	}
	err := fn(conn.AccessToken)
	var rateErr *strava.RateLimitError
	if !errors.As(err, &rateErr) {
		return err
	}
	message := fmt.Sprintf("Paused until the Strava rate limit resets at %s", rateErr.ResetAt.UTC().Format("15:04 MST"))
	if err := s.setState(ctx, conn.UserID, activities.SyncStateRateLimited, message, rateErr.ResetAt); err != nil {
		return err
	}
	return fmt.Errorf("%w: %w", errRateLimited, rateErr)
}

// retry calls fn, and if wait is set and fn reached the rate limit, waits
// for the limit to reset and calls it again.
func (s *Syncer) retry(ctx context.Context, conn *activities.StravaConnection, wait bool, fn func() error) error {
	for {
		err := fn()
		var rateErr *strava.RateLimitError
		if !wait || !errors.As(err, &rateErr) {
			return err
		}
		if err := s.waitForReset(ctx, conn, rateErr.ResetAt); err != nil {
			return err
		}
	}
}

// waitForReset waits for the rate limit to reset and records the sync as
// running again. Syncs wait outside of claims on activities, so syncs of
// single activities aren't held up by a waiting sync.
func (s *Syncer) waitForReset(ctx context.Context, conn *activities.StravaConnection, resetAt time.Time) error {
	timer := time.NewTimer(time.Until(resetAt) + 5*time.Second)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
	}
	return s.setState(ctx, conn.UserID, activities.SyncStateRunning, "Resuming", time.Time{})
}

func (s *Syncer) setState(ctx context.Context, userID pgtype.UUID, state activities.SyncState, message string, resumeTime time.Time) error {
	return s.repo.UpdateStravaSyncState(ctx, userID, state, message, resumeTime, time.Time{})
}

func summaryFromStrava(a *strava.Activity) *activities.ActivitySummary {
	cadence := a.AverageCadence
	if a.IsFootSport() {
		cadence *= 2
	}
	return &activities.ActivitySummary{
		StravaID:            a.ID,
		Name:                a.Name,
		SportType:           a.SportType,
		StartTime:           a.StartDate,
		StartDateLocal:      a.LocalDate(),
		Timezone:            a.TimezoneName(),
		DistanceMeters:      a.Distance,
		MovingTimeSeconds:   a.MovingTime,
		ElapsedTimeSeconds:  a.ElapsedTime,
		ElevationGainMeters: a.TotalElevationGain,
		AverageSpeedMps:     a.AverageSpeed,
		MaxSpeedMps:         a.MaxSpeed,
		AverageHeartrateBpm: a.AverageHeartrate,
		MaxHeartrateBpm:     a.MaxHeartrate,
		AverageCadenceSpm:   cadence,
		SummaryPolyline:     a.Map.SummaryPolyline,
		RelativeEffort:      int32(a.SufferScore),
		PhotoCount:          a.TotalPhotoCount,
		RawJSON:             a.Raw,
	}
}

// streamsFromStrava converts streams to an activitiesv1.ActivityStreams
// message and returns the bounds of the GPS track, if any.
func streamsFromStrava(streams *strava.Streams, footSport bool) (*activitiesv1.ActivityStreams, *geo.Bounds) {
	msg := activitiesv1.ActivityStreams_builder{
		TimeSeconds:    streams.Time,
		DistanceMeters: streams.Distance,
		AltitudeMeters: streams.Altitude,
		HeartrateBpm:   streams.Heartrate,
		VelocityMps:    streams.VelocitySmooth,
		GradePercent:   streams.GradeSmooth,
	}
	if len(streams.Cadence) > 0 {
		msg.CadenceSpm = make([]float64, len(streams.Cadence))
		for i, c := range streams.Cadence {
			if footSport {
				c *= 2
			}
			msg.CadenceSpm[i] = c
		}
	}
	var points []geo.Point
	if len(streams.LatLng) > 0 {
		msg.Lat = make([]float64, len(streams.LatLng))
		msg.Lng = make([]float64, len(streams.LatLng))
		points = make([]geo.Point, len(streams.LatLng))
		for i, ll := range streams.LatLng {
			msg.Lat[i], msg.Lng[i] = ll[0], ll[1]
			points[i] = geo.Point{Lat: ll[0], Lng: ll[1]}
		}
	}
	bounds, ok := geo.BoundsOf(points)
	if !ok {
		return msg.Build(), nil
	}
	return msg.Build(), &bounds
}
