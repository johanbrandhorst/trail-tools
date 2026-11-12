// Package garmin syncs users' Garmin Connect accounts: their activities,
// with the samples and strength sets from the watch's files, alongside
// Strava's, and their upcoming workouts onto their Garmin calendar.
package garmin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/activities"
	"github.com/bs-tools/trailrunn.ing/internal/errreport"
	"github.com/bs-tools/trailrunn.ing/internal/fit"
	"github.com/bs-tools/trailrunn.ing/internal/garmin"
	activitiesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/activities/v1"
	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/bs-tools/trailrunn.ing/internal/lactate"
	"github.com/bs-tools/trailrunn.ing/internal/plans"
	"github.com/bs-tools/trailrunn.ing/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
	"gitlab.com/greyxor/slogor"
)

// Client is Garmin's API.
type Client interface {
	AuthCodeURL(state, verifier string) string
	Exchange(ctx context.Context, code, verifier string) (*garmin.Token, error)
	Refresh(ctx context.Context, refreshToken string) (*garmin.Token, error)
	UserID(ctx context.Context, accessToken string) (string, error)
	Permissions(ctx context.Context, accessToken string) ([]string, error)
	Deregister(ctx context.Context, accessToken string) error
	BackfillActivities(ctx context.Context, accessToken string, start, end time.Time) error
	FetchActivities(ctx context.Context, accessToken, callbackURL string) ([]*garmin.ActivitySummary, error)
	DownloadFile(ctx context.Context, accessToken, callbackURL string) ([]byte, error)
	CreateWorkout(ctx context.Context, accessToken string, w *garmin.Workout) (int64, error)
	DeleteWorkout(ctx context.Context, accessToken string, id int64) error
	ScheduleWorkout(ctx context.Context, accessToken string, workoutID int64, date time.Time) (int64, error)
	CreateCourse(ctx context.Context, accessToken string, course *garmin.Course) (int64, error)
	DeleteCourse(ctx context.Context, accessToken string, id int64) error
}

// ActivityRepository stores Garmin connections and activities.
type ActivityRepository interface {
	UpsertGarminConnection(ctx context.Context, c *activities.GarminConnection) (*activities.GarminConnection, error)
	GetGarminConnection(ctx context.Context, userID pgtype.UUID) (*activities.GarminConnection, error)
	GetGarminConnectionByGarminUser(ctx context.Context, garminUserID string) (*activities.GarminConnection, error)
	ListGarminConnections(ctx context.Context) ([]*activities.GarminConnection, error)
	RefreshGarminTokens(ctx context.Context, userID pgtype.UUID, refresh func(stored *activities.GarminConnection) (*activities.GarminTokens, error)) (*activities.GarminConnection, error)
	UpdateGarminPermissions(ctx context.Context, userID pgtype.UUID, permissions []string) error
	FinishGarminWorkoutsSync(ctx context.Context, userID pgtype.UUID, at time.Time, message string) error
	SetGarminSyncMessage(ctx context.Context, userID pgtype.UUID, message string) error
	DeleteGarminConnection(ctx context.Context, userID pgtype.UUID) error
	UpsertGarminActivity(ctx context.Context, userID pgtype.UUID, a *activities.GarminActivity) (*activities.StoredGarminActivity, error)
	GetGarminActivity(ctx context.Context, userID pgtype.UUID, garminID int64) (*activities.StoredGarminActivity, error)
	StoreGarminActivityFile(ctx context.Context, userID, activityID pgtype.UUID, garminID int64, file []byte, streams *activitiesv1.ActivityStreams, bounds *geo.Bounds) error
	DeleteGarminActivity(ctx context.Context, userID pgtype.UUID, garminID int64) error
	AddGarminActivityFile(ctx context.Context, f *activities.GarminActivityFile) error
	ListGarminActivityFiles(ctx context.Context, userID pgtype.UUID, limit int32) ([]*activities.GarminActivityFile, error)
	FinishGarminActivityFile(ctx context.Context, userID pgtype.UUID, garminID int64) error
	RecordGarminActivityFileFailure(ctx context.Context, userID pgtype.UUID, garminID int64) error
	DeleteExpiredGarminActivityFiles(ctx context.Context) error
	ListGarminNotifications(ctx context.Context, limit int32) ([]*activities.GarminNotification, error)
	FinishGarminNotification(ctx context.Context, id int64) error
	RecordGarminNotificationFailure(ctx context.Context, id int64) (dropped bool, err error)
}

// Trigger starts the scheduled sync right away, like a Cloud Scheduler job,
// for handling notifications on servers that only run during requests.
type Trigger interface {
	Run(ctx context.Context) error
}

// PlanRepository stores the workouts put on Garmin calendars.
type PlanRepository interface {
	ListUserWorkouts(ctx context.Context, userID pgtype.UUID, startDate, endDate time.Time) ([]*plans.Workout, error)
	ListGarminWorkouts(ctx context.Context, userID pgtype.UUID) ([]*plans.GarminWorkout, error)
	AddGarminWorkout(ctx context.Context, userID pgtype.UUID, w *plans.GarminWorkout) error
	RemoveGarminWorkout(ctx context.Context, userID, id pgtype.UUID) error
	RemoveGarminWorkouts(ctx context.Context, userID pgtype.UUID) error
	ListGarminCourses(ctx context.Context, userID pgtype.UUID) ([]*plans.GarminCourse, error)
	AddGarminCourse(ctx context.Context, userID pgtype.UUID, c *plans.GarminCourse) error
	RemoveGarminCourse(ctx context.Context, userID, id pgtype.UUID) error
	RemoveGarminCourses(ctx context.Context, userID pgtype.UUID) error
	SetGarminCourseSent(ctx context.Context, userID, routeID pgtype.UUID, sent bool) error
	GetWorkoutByActivity(ctx context.Context, userID, activityID pgtype.UUID) (*plans.Workout, error)
	SetStrengthLog(ctx context.Context, userID pgtype.UUID, w *plans.Workout, log *plans.StrengthLog) error
}

const (
	// calendarDays is how many days ahead workouts are put on the Garmin
	// calendar. Watches hold a limited number of workouts, and warn when
	// there are too many, so only the coming week's are kept on Garmin.
	calendarDays = 7
	// pastWorkoutDays is how many days a workout is kept on Garmin after
	// its date, in UTC. Today in UTC can already be tomorrow for users west
	// of Greenwich, whose workouts of the day must stay on the watch until
	// the day is over for them.
	pastWorkoutDays = 1
	// maxRemovalsPerSync bounds how many workouts a sync removes from
	// Garmin, each a request, so the first sync after the window shrank,
	// with many past workouts to remove, doesn't run long. The next syncs
	// remove the rest.
	maxRemovalsPerSync = 50
	// backfillDays is how far back activities are asked for when a user
	// connects. Garmin sends them through the notifications, and older ones
	// most likely came from Strava already.
	backfillDays = 30
	// filesPerSync is how many activity files a sync downloads at most, to
	// bound the memory and time a sync takes.
	filesPerSync = 20
	// tokenRefreshMargin is how long before expiry tokens are refreshed, as
	// Garmin recommends at least 10 minutes. Access tokens last a day.
	tokenRefreshMargin = 10 * time.Minute
)

// Syncer syncs users' Garmin accounts.
type Syncer struct {
	log        *slog.Logger
	report     *errreport.Reporter
	client     Client
	activities ActivityRepository
	plans      PlanRepository
	// The user's lactate tests, or nil without them.
	thresholds ThresholdRepository
	nowFn      func() time.Time
	// A *sync.Mutex per user, held while putting their workouts on the
	// calendar, so a change and the scheduled sync don't both send a
	// workout. The server runs as one instance, so a local lock is enough.
	workoutLocks sync.Map
	// Starts the scheduled sync to handle queued notifications, or nil to
	// handle them in the background.
	trigger Trigger
	queue   notificationQueue
	routes  RouteRepository
}

// NewSyncer creates a Syncer. trigger starts the scheduled sync, which
// handles queued notifications; if nil, they're handled in the background.
func NewSyncer(log *slog.Logger, report *errreport.Reporter, client Client, activityRepo ActivityRepository, planRepo PlanRepository, routeRepo RouteRepository, thresholdRepo ThresholdRepository, trigger Trigger) *Syncer {
	return &Syncer{log: log, report: report, client: client, activities: activityRepo, plans: planRepo, routes: routeRepo, thresholds: thresholdRepo, nowFn: time.Now, trigger: trigger}
}

// ThresholdRepository lists the user's lactate tests, for the heart rates of
// structured runs' targets relative to their thresholds, and of the stages
// of planned lactate tests.
type ThresholdRepository interface {
	ListTests(ctx context.Context, userID pgtype.UUID) ([]*lactate.Test, error)
}

// userThresholds returns the heart rates of the user's aerobic and
// anaerobic thresholds, from their latest lactate tests that found them, 0
// if unknown, and the first stage of their latest test with stages.
func (s *Syncer) userThresholds(ctx context.Context, userID pgtype.UUID) (thresholds, error) {
	var t thresholds
	if s.thresholds == nil {
		return t, nil
	}
	tests, err := s.thresholds.ListTests(ctx, userID)
	if err != nil {
		return t, err
	}
	// The tests are listed with the latest first.
	for _, test := range tests {
		if t.aerobic == 0 {
			t.aerobic = test.HeartRateAt(lactate.AerobicThresholdMmolPerLiter)
		}
		if t.anaerobic == 0 {
			t.anaerobic = test.HeartRateAt(lactate.AnaerobicThresholdMmolPerLiter)
		}
		if t.lactateStart == 0 && len(test.Stages) > 0 {
			t.lactateStart = test.Stages[0].HeartRateBPM
		}
	}
	return t, nil
}

// token returns a fresh access token of a connection, refreshing it if it
// expires soon.
func (s *Syncer) token(ctx context.Context, conn *activities.GarminConnection) (string, error) {
	if conn.Expiry.After(s.nowFn().Add(tokenRefreshMargin)) {
		return conn.AccessToken, nil
	}
	refreshed, err := s.activities.RefreshGarminTokens(ctx, conn.UserID, func(stored *activities.GarminConnection) (*activities.GarminTokens, error) {
		// Another request may have refreshed them meanwhile.
		if stored.Expiry.After(s.nowFn().Add(tokenRefreshMargin)) {
			return nil, nil
		}
		t, err := s.client.Refresh(ctx, stored.RefreshToken)
		if err != nil {
			return nil, err
		}
		return &activities.GarminTokens{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, Expiry: t.Expiry}, nil
	})
	if err != nil {
		return "", err
	}
	*conn = *refreshed
	return conn.AccessToken, nil
}

// Connected sets up a newly connected account: it asks Garmin for the
// recent activities, which arrive through the notifications, and puts the
// upcoming workouts on the calendar.
func (s *Syncer) Connected(ctx context.Context, conn *activities.GarminConnection) {
	log := s.log.With(slog.String("user_id", store.UUIDToString(conn.UserID)))
	token, err := s.token(ctx, conn)
	if err != nil {
		s.report.Report(ctx, "Failed to set up a Garmin connection", err)
		return
	}
	if slices.Contains(conn.Permissions, garmin.PermissionActivityExport) {
		now := s.nowFn()
		if err := s.client.BackfillActivities(ctx, token, now.AddDate(0, 0, -backfillDays), now); err != nil {
			// Garmin only backfills for apps with Enterprise access.
			log.WarnContext(ctx, "Failed to ask Garmin for recent activities, leaving them out", slogor.Err(err))
		}
	}
	if err := s.SyncUser(ctx, conn.UserID); err != nil {
		log.WarnContext(ctx, "Failed to sync a new Garmin connection", slogor.Err(err))
	}
}

// HandleActivities stores the summaries of a user's activities, fetched
// from a notification's callback URL, or sent in a push notification.
func (s *Syncer) HandleActivities(ctx context.Context, garminUserID, callbackURL string, pushed []*garmin.ActivitySummary) error {
	conn, err := s.activities.GetGarminConnectionByGarminUser(ctx, garminUserID)
	if errors.Is(err, store.ErrNotFound) {
		// The user disconnected.
		return nil
	}
	if err != nil {
		return err
	}
	summaries := pushed
	if callbackURL != "" {
		token, err := s.token(ctx, conn)
		if err != nil {
			return err
		}
		if summaries, err = s.client.FetchActivities(ctx, token, callbackURL); err != nil {
			return err
		}
	}
	for _, summary := range summaries {
		if summary.ActivityID == 0 {
			continue
		}
		if _, err := s.activities.UpsertGarminActivity(ctx, conn.UserID, activityFromSummary(summary)); err != nil {
			return err
		}
	}
	return nil
}

// HandleDeletedActivities removes activities deleted on Garmin.
func (s *Syncer) HandleDeletedActivities(ctx context.Context, garminUserID string, activityIDs []int64) error {
	conn, err := s.activities.GetGarminConnectionByGarminUser(ctx, garminUserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, id := range activityIDs {
		if err := s.activities.DeleteGarminActivity(ctx, conn.UserID, id); err != nil {
			return err
		}
	}
	return nil
}

// fileLinkLifetime is how long Garmin's links to activity files work.
const fileLinkLifetime = 24 * time.Hour

// HandleActivityFile records an activity file Garmin offered, and downloads
// it if it's needed, within ctx. It only returns an error if the file
// couldn't be recorded: a failed download is retried by the next sync.
func (s *Syncer) HandleActivityFile(ctx context.Context, garminUserID string, garminActivityID int64, callbackURL string) error {
	conn, err := s.activities.GetGarminConnectionByGarminUser(ctx, garminUserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	f := &activities.GarminActivityFile{
		UserID:           conn.UserID,
		GarminActivityID: garminActivityID,
		CallbackURL:      callbackURL,
		ExpireTime:       s.nowFn().Add(fileLinkLifetime),
	}
	// Recorded first, so a file that isn't downloaded now is by the next
	// sync.
	if err := s.activities.AddGarminActivityFile(ctx, f); err != nil {
		return err
	}
	if err := s.processFile(ctx, conn, f); err != nil {
		// Recorded, so it's not lost: Garmin needn't send it again.
		s.log.WarnContext(ctx, "Failed to download a Garmin activity file, downloading it with the next sync",
			slog.Int64("garmin_activity_id", garminActivityID), slogor.Err(err))
	}
	return nil
}

// processFile downloads an activity's file if the activity needs it: if it
// has no samples yet, or it's strength training, whose sets are only in
// the file. Otherwise the file isn't downloaded, to save the data: the
// activity's samples came from Strava. A file of an activity whose summary
// hasn't arrived yet waits for it.
func (s *Syncer) processFile(ctx context.Context, conn *activities.GarminConnection, f *activities.GarminActivityFile) error {
	stored, err := s.activities.GetGarminActivity(ctx, conn.UserID, f.GarminActivityID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	strength := isStrength(stored.SportType)
	if stored.HasSamples && !strength {
		return s.activities.FinishGarminActivityFile(ctx, conn.UserID, f.GarminActivityID)
	}
	token, err := s.token(ctx, conn)
	if err != nil {
		return err
	}
	file, err := s.client.DownloadFile(ctx, token, f.CallbackURL)
	if errors.Is(err, garmin.ErrNotFound) {
		// The link expired.
		return s.activities.FinishGarminActivityFile(ctx, conn.UserID, f.GarminActivityID)
	}
	if err != nil {
		if recordErr := s.activities.RecordGarminActivityFileFailure(ctx, conn.UserID, f.GarminActivityID); recordErr != nil {
			return errors.Join(err, recordErr)
		}
		return err
	}
	decoded, err := fit.DecodeActivity(file)
	if err != nil {
		// A file that doesn't decode won't later either.
		s.report.Report(ctx, "Failed to decode a Garmin activity file, leaving the activity without its samples", err,
			slog.Int64("garmin_activity_id", f.GarminActivityID))
		return s.activities.FinishGarminActivityFile(ctx, conn.UserID, f.GarminActivityID)
	}
	var streams *activitiesv1.ActivityStreams
	var bounds *geo.Bounds
	if !stored.HasSamples {
		streams, bounds = activities.StreamsFromFIT(decoded, isFootSport(stored.SportType))
	}
	if err := s.activities.StoreGarminActivityFile(ctx, conn.UserID, stored.ID, f.GarminActivityID, file, streams, bounds); err != nil {
		return err
	}
	if strength {
		if err := s.logStrengthSets(ctx, conn.UserID, stored.ID, decoded); err != nil {
			s.report.Report(ctx, "Failed to log a strength workout from the watch's sets, leaving it to the user", err)
		}
	}
	return s.activities.FinishGarminActivityFile(ctx, conn.UserID, f.GarminActivityID)
}

// logStrengthSets logs the strength workout the activity completed with
// the sets the watch recorded, unless the user logged it already: their
// log, with how it felt, comes first.
func (s *Syncer) logStrengthSets(ctx context.Context, userID, activityID pgtype.UUID, a *fit.Activity) error {
	w, err := s.plans.GetWorkoutByActivity(ctx, userID, activityID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if w.Type != plans.WorkoutTypeStrength || w.Log != nil || len(w.Exercises) == 0 {
		return nil
	}
	log := strengthLogFromSets(w, a)
	if log == nil {
		return nil
	}
	return s.plans.SetStrengthLog(ctx, userID, w, log)
}

// SyncUser downloads the user's waiting activity files and puts their
// upcoming workouts on their Garmin calendar.
func (s *Syncer) SyncUser(ctx context.Context, userID pgtype.UUID) error {
	conn, err := s.activities.GetGarminConnection(ctx, userID)
	if err != nil {
		return err
	}
	return s.sync(ctx, conn)
}

func (s *Syncer) sync(ctx context.Context, conn *activities.GarminConnection) error {
	// Refresh tokens expire after three months, unless they're used, so
	// they're refreshed with the access tokens, daily, even for users whose
	// syncs need no requests.
	if _, err := s.token(ctx, conn); err != nil {
		if errors.Is(err, garmin.ErrUnauthorized) {
			return errors.Join(err, s.activities.SetGarminSyncMessage(ctx, conn.UserID, "Garmin rejected the connection; reconnect Garmin."))
		}
		return err
	}
	files, err := s.activities.ListGarminActivityFiles(ctx, conn.UserID, filesPerSync)
	if err != nil {
		return err
	}
	var errs []error
	for _, f := range files {
		if err := s.processFile(ctx, conn, f); err != nil {
			errs = append(errs, err)
			if errors.Is(err, garmin.ErrUnauthorized) || errors.Is(err, garmin.ErrRateLimited) {
				break
			}
		}
	}
	if syncsCalendar(conn) {
		message, err := s.syncCalendar(ctx, conn)
		if err != nil {
			errs = append(errs, err)
			message = "Putting workouts or courses on Garmin failed; it's retried within the hour."
			if errors.Is(err, garmin.ErrUnauthorized) {
				message = "Garmin rejected the connection; reconnect Garmin."
			}
			if errors.Is(err, garmin.ErrPermissionDenied) {
				message = "Garmin doesn't allow putting workouts or courses on it; allow it in Garmin Connect, or reconnect Garmin."
			}
		}
		if recordErr := s.activities.FinishGarminWorkoutsSync(ctx, conn.UserID, s.nowFn(), message); recordErr != nil {
			errs = append(errs, recordErr)
		}
	}
	return errors.Join(errs...)
}

// syncsCalendar reports whether the user lets the app put workouts or
// courses on their Garmin account.
func syncsCalendar(conn *activities.GarminConnection) bool {
	return slices.Contains(conn.Permissions, garmin.PermissionWorkoutImport) || slices.Contains(conn.Permissions, garmin.PermissionCourseImport)
}

// lockCalendar locks what the app puts on a user's Garmin account, so syncs
// and changes the user asks for don't put the same workout or course there
// twice, and returns the function unlocking it.
func (s *Syncer) lockCalendar(userID pgtype.UUID) func() {
	lock, _ := s.workoutLocks.LoadOrStore(userID, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	return lock.(*sync.Mutex).Unlock
}

// syncCalendar puts the user's workouts of the coming days on their Garmin
// calendar, and the routes of their runs and races as courses, as far as
// they allow, and returns what it did.
func (s *Syncer) syncCalendar(ctx context.Context, conn *activities.GarminConnection) (string, error) {
	unlock := s.lockCalendar(conn.UserID)
	defer unlock()
	today := s.nowFn().UTC().Truncate(24 * time.Hour)
	workouts, err := s.plans.ListUserWorkouts(ctx, conn.UserID, today, today.AddDate(0, 0, calendarDays))
	if err != nil {
		return "", err
	}
	token, err := s.token(ctx, conn)
	if err != nil {
		return "", err
	}
	var messages []string
	if slices.Contains(conn.Permissions, garmin.PermissionWorkoutImport) {
		message, err := s.syncWorkouts(ctx, conn, token, today, workouts)
		if err != nil {
			return "", err
		}
		messages = append(messages, message)
	}
	if slices.Contains(conn.Permissions, garmin.PermissionCourseImport) {
		message, err := s.syncCourses(ctx, conn, token, workouts)
		if err != nil {
			return "", err
		}
		if message != "" {
			messages = append(messages, message)
		}
	}
	if len(messages) == 0 {
		return "Garmin is up to date.", nil
	}
	return strings.Join(messages, " "), nil
}

// syncWorkouts puts the user's runs and strength workouts of the coming
// days on their Garmin calendar, replacing those that changed and removing
// those deleted, moved away or past, and returns what it did. Workouts that
// didn't change aren't sent again.
func (s *Syncer) syncWorkouts(ctx context.Context, conn *activities.GarminConnection, token string, today time.Time, workouts []*plans.Workout) (string, error) {
	onGarmin, err := s.plans.ListGarminWorkouts(ctx, conn.UserID)
	if err != nil {
		return "", err
	}
	byWorkout := map[pgtype.UUID]*plans.GarminWorkout{}
	var stale []*plans.GarminWorkout
	for _, g := range onGarmin {
		switch {
		case !g.WorkoutID.Valid:
			// The workout was deleted.
			stale = append(stale, g)
		case g.WorkoutDate.Before(today):
			// Past workouts are removed, so they don't fill the watch;
			// the activities done for them stay on Garmin.
			if g.WorkoutDate.Before(today.AddDate(0, 0, -pastWorkoutDays)) {
				stale = append(stale, g)
			}
		case g.WorkoutDate.After(today.AddDate(0, 0, calendarDays)):
			// Ones moved further ahead are removed until they're near
			// again.
			stale = append(stale, g)
		default:
			byWorkout[g.WorkoutID] = g
		}
	}
	t, err := s.userThresholds(ctx, conn.UserID)
	if err != nil {
		return "", err
	}
	added, removed := 0, 0
	keep := map[pgtype.UUID]bool{}
	for _, w := range workouts {
		gw, ok := trainingWorkout(w, t)
		if !ok || w.Log != nil || w.CompletedActivityID.Valid {
			continue
		}
		hash := contentHash(gw, w.Date)
		existing := byWorkout[w.ID]
		if existing != nil && existing.ContentHash == hash {
			keep[existing.ID] = true
			continue
		}
		if existing != nil {
			if err := s.removeWorkout(ctx, conn.UserID, token, existing); err != nil {
				return "", err
			}
		}
		id, err := s.client.CreateWorkout(ctx, token, gw)
		if err != nil {
			return "", err
		}
		scheduleID, err := s.client.ScheduleWorkout(ctx, token, id, w.Date)
		if err != nil {
			// Not left unscheduled on Garmin, nor forgotten here.
			_ = s.client.DeleteWorkout(ctx, token, id)
			return "", err
		}
		if err := s.plans.AddGarminWorkout(ctx, conn.UserID, &plans.GarminWorkout{
			WorkoutID: w.ID, GarminWorkoutID: id, GarminScheduleID: scheduleID, ContentHash: hash,
		}); err != nil {
			return "", err
		}
		added++
	}
	// Workouts no longer planned, or done, are removed from the calendar.
	for id, g := range byWorkout {
		if !keep[id] {
			stillPlanned := slices.ContainsFunc(workouts, func(w *plans.Workout) bool {
				_, ok := trainingWorkout(w, t)
				return w.ID == id && ok && w.Log == nil && !w.CompletedActivityID.Valid
			})
			if !stillPlanned {
				stale = append(stale, g)
			}
		}
	}
	for _, g := range stale[:min(len(stale), maxRemovalsPerSync)] {
		if err := s.removeWorkout(ctx, conn.UserID, token, g); err != nil {
			return "", err
		}
		removed++
	}
	switch {
	case added == 0 && removed == 0:
		return "The Garmin calendar is up to date.", nil
	default:
		return fmt.Sprintf("Put %d workouts on the Garmin calendar and removed %d.", added, removed), nil
	}
}

func (s *Syncer) removeWorkout(ctx context.Context, userID pgtype.UUID, token string, g *plans.GarminWorkout) error {
	// The user may have deleted it on Garmin already.
	if err := s.client.DeleteWorkout(ctx, token, g.GarminWorkoutID); err != nil && !errors.Is(err, garmin.ErrNotFound) {
		return err
	}
	return s.plans.RemoveGarminWorkout(ctx, userID, g.ID)
}

// SyncDue syncs every connection: the files waiting to download, and the
// workouts of the coming days, for running from a scheduler.
func (s *Syncer) SyncDue(ctx context.Context) error {
	if err := s.HandleNotifications(ctx); err != nil {
		s.report.Report(ctx, "Failed to handle Garmin notifications, retrying with the next sync", err)
	}
	if err := s.activities.DeleteExpiredGarminActivityFiles(ctx); err != nil {
		return err
	}
	conns, err := s.activities.ListGarminConnections(ctx)
	if err != nil {
		return err
	}
	for _, conn := range conns {
		if ctx.Err() != nil {
			return nil
		}
		if err := s.sync(ctx, conn); err != nil {
			s.report.Report(ctx, "Scheduled Garmin sync failed, retrying within the hour", err,
				slog.String("user_id", store.UUIDToString(conn.UserID)))
		}
	}
	return nil
}

// Disconnect removes the workouts put on the user's Garmin calendar,
// deregisters the app from their account, and deletes the connection. The
// connection is deleted even if Garmin fails, so users can always
// disconnect; what failed on Garmin is reported.
func (s *Syncer) Disconnect(ctx context.Context, userID pgtype.UUID) error {
	conn, err := s.activities.GetGarminConnection(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.cleanUpGarmin(ctx, conn); err != nil && !errors.Is(err, garmin.ErrUnauthorized) {
		// Unauthorized means the user disconnected on Garmin already.
		s.report.Report(ctx, "Failed to remove workouts from Garmin while disconnecting, leaving them there", err)
	}
	return s.forget(ctx, userID)
}

// cleanUpGarmin removes the upcoming workouts put on the user's Garmin
// calendar, keeping past ones as the record of what was done, and
// deregisters the app.
func (s *Syncer) cleanUpGarmin(ctx context.Context, conn *activities.GarminConnection) error {
	token, err := s.token(ctx, conn)
	if err != nil {
		return err
	}
	onGarmin, err := s.plans.ListGarminWorkouts(ctx, conn.UserID)
	if err != nil {
		return err
	}
	today := s.nowFn().UTC().Truncate(24 * time.Hour)
	for _, g := range onGarmin {
		if g.WorkoutDate.IsZero() || !g.WorkoutDate.Before(today) {
			if err := s.client.DeleteWorkout(ctx, token, g.GarminWorkoutID); err != nil && !errors.Is(err, garmin.ErrNotFound) {
				return err
			}
		}
	}
	if err := s.removeAllCourses(ctx, conn.UserID, token); err != nil {
		return err
	}
	return s.client.Deregister(ctx, token)
}

// forget deletes a connection and the record of its workouts.
func (s *Syncer) forget(ctx context.Context, userID pgtype.UUID) error {
	if err := s.plans.RemoveGarminCourses(ctx, userID); err != nil {
		return err
	}
	if err := s.plans.RemoveGarminWorkouts(ctx, userID); err != nil {
		return err
	}
	return s.activities.DeleteGarminConnection(ctx, userID)
}

// HandleDeregistration forgets a user who disconnected the app on Garmin.
func (s *Syncer) HandleDeregistration(ctx context.Context, garminUserID string) error {
	conn, err := s.activities.GetGarminConnectionByGarminUser(ctx, garminUserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.forget(ctx, conn.UserID)
}

// HandlePermissions records the permissions a user changed on Garmin.
func (s *Syncer) HandlePermissions(ctx context.Context, garminUserID string, permissions []string) error {
	conn, err := s.activities.GetGarminConnectionByGarminUser(ctx, garminUserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	slices.Sort(permissions)
	return s.activities.UpdateGarminPermissions(ctx, conn.UserID, permissions)
}

// backgroundSyncInterval is how often a server without a scheduler syncs
// Garmin, as often as the scheduled sync runs.
const backgroundSyncInterval = time.Hour

// RunBackground syncs every connection every backgroundSyncInterval until
// ctx is done, for servers that run between requests, unlike Cloud Run.
func (s *Syncer) RunBackground(ctx context.Context) {
	ticker := time.NewTicker(backgroundSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.SyncDue(ctx); err != nil && ctx.Err() == nil {
				s.report.Report(ctx, "Background Garmin sync failed, retrying within the hour", err)
			}
		}
	}
}

// workoutsChangedTimeout bounds putting changed workouts on the Garmin
// calendar before responding to the change. Whatever doesn't finish is done
// by the next scheduled sync.
const workoutsChangedTimeout = 10 * time.Second

// WorkoutsChanged puts the user's changed workouts on their Garmin calendar
// right away, if any of the dates are within the calendar's window, or
// unknown, so a workout planned for today reaches the watch. It's a
// plan.WorkoutsChangedFunc.
func (s *Syncer) WorkoutsChanged(ctx context.Context, userID pgtype.UUID, dates []time.Time) {
	today := s.nowFn().UTC().Truncate(24 * time.Hour)
	inWindow := len(dates) == 0 || slices.ContainsFunc(dates, func(d time.Time) bool {
		return !d.Before(today) && !d.After(today.AddDate(0, 0, calendarDays))
	})
	if !inWindow {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workoutsChangedTimeout)
	defer cancel()
	conn, err := s.activities.GetGarminConnection(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		s.report.Report(ctx, "Failed to get a Garmin connection, putting changed workouts on the calendar with the next sync", err)
		return
	}
	if !syncsCalendar(conn) {
		return
	}
	message, err := s.syncCalendar(ctx, conn)
	if err != nil {
		s.log.WarnContext(ctx, "Failed to put changed workouts on the Garmin calendar, retrying with the next sync",
			slog.String("user_id", store.UUIDToString(userID)), slogor.Err(err))
		return
	}
	if err := s.activities.FinishGarminWorkoutsSync(ctx, userID, s.nowFn(), message); err != nil {
		s.report.Report(ctx, "Failed to record a Garmin workouts sync", err)
	}
}
