package garmin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/garmin"
	"gitlab.com/greyxor/slogor"
)

// notification is a notification from Garmin. One endpoint receives every
// kind, so each is configured in Garmin's developer portal with the same
// URL. A notification usually has one of the fields.
type notification struct {
	// Push notifications carry activities' summaries, and ping
	// notifications, with Enterprise access, a callback URL to fetch them
	// from.
	Activities []activityNotification `json:"activities"`
	// Activities edited or created on Garmin Connect, like Activities.
	ManuallyUpdatedActivities []activityNotification `json:"manuallyUpdatedActivities"`
	// Activity files carry a callback URL to download the file from,
	// once, within a day.
	ActivityFiles []activityFileNotification `json:"activityFiles"`
	// Activities deleted on Garmin. Garmin's documentation describes no
	// such notification, so they're handled in case Garmin sends them.
	Deletions []deletionNotification `json:"deletions"`
	// Users who disconnected the app on Garmin, or whom the app
	// deregistered.
	Deregistrations []userNotification `json:"deregistrations"`
	// Users who changed the permissions they granted.
	UserPermissionsChange []permissionsNotification `json:"userPermissionsChange"`
}

type userNotification struct {
	UserID string `json:"userId"`
}

type activityNotification struct {
	UserID string `json:"userId"`
	// Set for a ping notification.
	CallbackURL string `json:"callbackURL"`
	// Set for a push notification, with the rest of the summary.
	ActivityID garmin.ID `json:"activityId"`
}

type activityFileNotification struct {
	UserID      string    `json:"userId"`
	ActivityID  garmin.ID `json:"activityId"`
	FileType    string    `json:"fileType"`
	CallbackURL string    `json:"callbackURL"`
}

type deletionNotification struct {
	UserID     string    `json:"userId"`
	ActivityID garmin.ID `json:"activityId"`
}

type permissionsNotification struct {
	UserID      string   `json:"userId"`
	Permissions []string `json:"permissions"`
}

// notificationsPerPass is how many queued notifications are read at a
// time, to bound the memory of a pass.
const notificationsPerPass = 20

// notificationQueue tracks the handling of queued notifications in this
// server, so notifications arriving together start one pass, and ones
// arriving during a pass are handled by it. The server runs as one
// instance, so local state is enough.
type notificationQueue struct {
	mu sync.Mutex
	// Whether a pass is handling notifications.
	running bool
	// Whether notifications arrived during the pass, so it lists them
	// again.
	more bool
	// Whether a pass was asked for and hasn't started yet.
	requested bool
}

// NotificationQueued starts handling a notification the webhook queued: it
// runs the scheduled sync's trigger, as the server only runs during
// requests, or else handles the queue in the background. A pass that's
// running or asked for already handles it.
func (s *Syncer) NotificationQueued(ctx context.Context) {
	q := &s.queue
	q.mu.Lock()
	switch {
	case q.running:
		q.more = true
		q.mu.Unlock()
		return
	case q.requested:
		q.mu.Unlock()
		return
	}
	q.requested = true
	q.mu.Unlock()
	if s.trigger == nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), backgroundPassTimeout)
			defer cancel()
			if err := s.HandleNotifications(ctx); err != nil && ctx.Err() == nil {
				s.report.Report(ctx, "Failed to handle Garmin notifications, retrying with the next sync", err)
			}
		}()
		return
	}
	if err := s.trigger.Run(ctx); err != nil {
		q.mu.Lock()
		q.requested = false
		q.mu.Unlock()
		s.report.Report(ctx, "Failed to start handling a Garmin notification, handling it with the next scheduled sync", err)
	}
}

// backgroundPassTimeout bounds handling notifications in the background.
const backgroundPassTimeout = 5 * time.Minute

// HandleNotifications handles the queued notifications, until none are
// left or ctx is done. It returns an error if the queue can't be read; a
// notification that fails is tried again by a later pass.
func (s *Syncer) HandleNotifications(ctx context.Context) error {
	q := &s.queue
	q.mu.Lock()
	if q.running {
		q.more = true
		q.mu.Unlock()
		return nil
	}
	q.running, q.requested, q.more = true, false, false
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		q.running = false
		q.mu.Unlock()
	}()
	// Notifications that failed in this pass aren't tried again in it.
	failed := map[int64]bool{}
	for ctx.Err() == nil {
		queued, err := s.activities.ListGarminNotifications(ctx, notificationsPerPass+int32(len(failed)))
		if err != nil {
			return err
		}
		handled := 0
		for _, n := range queued {
			if failed[n.ID] || ctx.Err() != nil {
				continue
			}
			handled++
			if err := s.handleNotification(ctx, n.Body); err != nil {
				failed[n.ID] = true
				dropped, recordErr := s.activities.RecordGarminNotificationFailure(context.WithoutCancel(ctx), n.ID)
				switch {
				case recordErr != nil:
					return errors.Join(err, recordErr)
				case dropped:
					s.report.Report(ctx, "Failed to handle a Garmin notification for a day, dropping it", err)
				default:
					s.log.WarnContext(ctx, "Failed to handle a Garmin notification, retrying with the next sync", slogor.Err(err))
				}
				continue
			}
			if err := s.activities.FinishGarminNotification(context.WithoutCancel(ctx), n.ID); err != nil {
				return err
			}
		}
		if handled > 0 {
			continue
		}
		// None left, unless more arrived meanwhile.
		q.mu.Lock()
		more := q.more
		q.more = false
		q.mu.Unlock()
		if !more {
			return nil
		}
	}
	return nil
}

// handleNotification handles a notification. It returns an error if it
// should be tried again.
func (s *Syncer) handleNotification(ctx context.Context, body []byte) error {
	var n notification
	if err := json.Unmarshal(body, &n); err != nil {
		// The webhook only queues valid JSON, so it's of an unexpected
		// shape, which won't change.
		s.report.Report(ctx, "Failed to decode a Garmin notification, dropping it", err)
		return nil
	}
	var errs []error
	if err := s.handleActivities(ctx, body, "activities", n.Activities); err != nil {
		errs = append(errs, err)
	}
	if err := s.handleActivities(ctx, body, "manuallyUpdatedActivities", n.ManuallyUpdatedActivities); err != nil {
		errs = append(errs, err)
	}
	for _, d := range n.Deletions {
		if err := s.HandleDeletedActivities(ctx, d.UserID, []int64{int64(d.ActivityID)}); err != nil {
			errs = append(errs, err)
		}
	}
	for _, f := range n.ActivityFiles {
		if f.CallbackURL == "" || f.ActivityID == 0 || !strings.EqualFold(f.FileType, "FIT") {
			continue
		}
		// The file is recorded before it's downloaded, so a failed
		// download is retried by the scheduled sync.
		if err := s.HandleActivityFile(ctx, f.UserID, int64(f.ActivityID), f.CallbackURL); err != nil {
			errs = append(errs, err)
		}
	}
	for _, d := range n.Deregistrations {
		if err := s.HandleDeregistration(ctx, d.UserID); err != nil {
			errs = append(errs, err)
		}
	}
	for _, p := range n.UserPermissionsChange {
		if err := s.HandlePermissions(ctx, p.UserID, p.Permissions); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// handleActivities handles the activities of a notification under key:
// pings are fetched from their callback URL, and push notifications'
// summaries are decoded from the body again, as whole summaries.
func (s *Syncer) handleActivities(ctx context.Context, body []byte, key string, notified []activityNotification) error {
	if len(notified) == 0 {
		return nil
	}
	var lists map[string][]json.RawMessage
	if err := json.Unmarshal(body, &lists); err != nil {
		return err
	}
	raw := lists[key]
	var errs []error
	for i, a := range notified {
		var err error
		switch {
		case a.CallbackURL != "":
			err = s.HandleActivities(ctx, a.UserID, a.CallbackURL, nil)
		case a.ActivityID != 0 && i < len(raw):
			summary := &garmin.ActivitySummary{RawJSON: raw[i]}
			if err = json.Unmarshal(raw[i], summary); err == nil {
				err = s.HandleActivities(ctx, a.UserID, "", []*garmin.ActivitySummary{summary})
			}
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
