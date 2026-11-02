package garmin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/garmin"
	"gitlab.com/greyxor/slogor"
)

// WebhookPath is the path below which Garmin sends notifications, to a
// secret path under it.
const WebhookPath = "/garmin/webhook"

const (
	// webhookBudget is how long a notification is handled for before
	// responding. Garmin waits up to 30 seconds and sends the notification
	// again if it fails or times out; activity files not downloaded in time
	// are by the next scheduled sync.
	webhookBudget = 20 * time.Second
	// maxNotificationBytes limits the size of notifications. Push
	// notifications carry whole activity summaries, a few kilobytes each,
	// and backfills can send many at once.
	maxNotificationBytes = 4 << 20
)

// notification is a notification from Garmin. One endpoint receives every
// kind, so each is configured in Garmin's developer portal with the same
// URL. A notification has one of the fields.
type notification struct {
	// Ping notifications of new activities carry a callback URL to fetch
	// them from, and push notifications the summaries themselves.
	Activities []activityNotification `json:"activities"`
	// Activity files carry a callback URL to download the file from,
	// valid for a day.
	ActivityFiles []activityFileNotification `json:"activityFiles"`
	// Activities the user deleted on Garmin.
	Deletions []deletionNotification `json:"deletions"`
	// Users who disconnected the app on Garmin.
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
	ActivityID int64 `json:"activityId"`
}

type activityFileNotification struct {
	UserID      string `json:"userId"`
	ActivityID  int64  `json:"activityId"`
	FileType    string `json:"fileType"`
	CallbackURL string `json:"callbackURL"`
}

type deletionNotification struct {
	UserID     string `json:"userId"`
	ActivityID int64  `json:"activityId"`
}

type permissionsNotification struct {
	UserID      string   `json:"userId"`
	Permissions []string `json:"permissions"`
}

// Webhook receives Garmin's notifications. Garmin doesn't sign them, so
// they're authenticated by a secret in the URL, which only Garmin learns
// through the developer portal. Even so, a notification only points to
// data fetched from Garmin with the user's token, except a push
// notification's summary, which only ever updates an activity of the user
// it names.
type Webhook struct {
	log    *slog.Logger
	report *errreport.Reporter
	syncer *Syncer
	// The path with the secret.
	path string
	// The full URL to configure in Garmin's developer portal.
	callbackURL string
}

// minWebhookSecretLength is the shortest secret accepted in the webhook's
// path, so it can't be guessed.
const minWebhookSecretLength = 24

// NewWebhook creates a Webhook, served at baseURL, the public URL of
// WebhookPath, followed by the secret. Register it at its Pattern, and
// configure CallbackURL for each notification in Garmin's developer portal.
func NewWebhook(log *slog.Logger, report *errreport.Reporter, syncer *Syncer, baseURL, secret string) (*Webhook, error) {
	if len(secret) < minWebhookSecretLength || strings.ContainsAny(secret, "/?#%") {
		return nil, fmt.Errorf("the Garmin webhook secret must be at least %d characters, without /, ?, # or %%", minWebhookSecretLength)
	}
	return &Webhook{
		log:         log,
		report:      report,
		syncer:      syncer,
		path:        WebhookPath + "/" + secret,
		callbackURL: strings.TrimSuffix(baseURL, "/") + "/" + secret,
	}, nil
}

// Pattern is the pattern to register the webhook at on a http.ServeMux. It
// matches every path below WebhookPath, and the webhook rejects all but the
// secret one, comparing it in constant time.
func (h *Webhook) Pattern() string {
	return WebhookPath + "/"
}

// CallbackURL is the URL to configure for each notification in Garmin's
// developer portal. It contains the secret, so it must not be logged.
func (h *Webhook) CallbackURL() string {
	return h.callbackURL
}

func (h *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.URL.Path), []byte(h.path)) != 1 {
		h.log.DebugContext(r.Context(), "Rejecting a Garmin notification to the wrong path")
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxNotificationBytes+1))
	if err != nil {
		http.Error(w, "failed to read notification", http.StatusBadRequest)
		return
	}
	if len(body) > maxNotificationBytes {
		http.Error(w, "notification too large", http.StatusRequestEntityTooLarge)
		return
	}
	// Handled even if Garmin gives up waiting, so the work isn't repeated.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), webhookBudget)
	defer cancel()
	if err := h.handle(ctx, body); err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
			h.log.WarnContext(ctx, "Rejecting a malformed Garmin notification", slogor.Err(err))
			http.Error(w, "malformed notification", http.StatusBadRequest)
			return
		}
		// Garmin sends it again.
		h.report.Report(ctx, "Failed to handle a Garmin notification, waiting for Garmin to send it again", err)
		http.Error(w, "failed to handle notification", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handle handles a notification. It returns an error if it should be sent
// again.
func (h *Webhook) handle(ctx context.Context, body []byte) error {
	var n notification
	if err := json.Unmarshal(body, &n); err != nil {
		return err
	}
	var errs []error
	if len(n.Activities) > 0 {
		if err := h.handleActivities(ctx, body, n.Activities); err != nil {
			errs = append(errs, err)
		}
	}
	for _, d := range n.Deletions {
		if err := h.syncer.HandleDeletedActivities(ctx, d.UserID, []int64{d.ActivityID}); err != nil {
			errs = append(errs, err)
		}
	}
	for _, f := range n.ActivityFiles {
		if f.CallbackURL == "" || f.ActivityID == 0 || !strings.EqualFold(f.FileType, "FIT") {
			continue
		}
		// The file is recorded before it's downloaded, so a failed
		// download is retried by the scheduled sync instead of Garmin.
		if err := h.syncer.HandleActivityFile(ctx, f.UserID, f.ActivityID, f.CallbackURL); err != nil {
			errs = append(errs, err)
		}
	}
	for _, d := range n.Deregistrations {
		if err := h.syncer.HandleDeregistration(ctx, d.UserID); err != nil {
			errs = append(errs, err)
		}
	}
	for _, p := range n.UserPermissionsChange {
		if err := h.syncer.HandlePermissions(ctx, p.UserID, p.Permissions); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// handleActivities handles the activities of a notification: pings are
// fetched from their callback URL, and push notifications' summaries are
// decoded from the body again, as whole summaries.
func (h *Webhook) handleActivities(ctx context.Context, body []byte, notified []activityNotification) error {
	var pushed struct {
		Activities []*garmin.ActivitySummary `json:"activities"`
	}
	if err := json.Unmarshal(body, &pushed); err != nil {
		return err
	}
	var raw struct {
		Activities []json.RawMessage `json:"activities"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	var errs []error
	for i, a := range notified {
		var err error
		switch {
		case a.CallbackURL != "":
			err = h.syncer.HandleActivities(ctx, a.UserID, a.CallbackURL, nil)
		case a.ActivityID != 0:
			summary := pushed.Activities[i]
			summary.RawJSON = raw.Activities[i]
			err = h.syncer.HandleActivities(ctx, a.UserID, "", []*garmin.ActivitySummary{summary})
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
