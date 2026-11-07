package garmin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/johanbrandhorst/trail-tools/internal/errreport"
)

// WebhookPath is the path below which Garmin sends notifications, to a
// secret path under it.
const WebhookPath = "/garmin/webhook"

// maxNotificationBytes limits the size of notifications. Push notifications
// carry whole activity summaries, a few kilobytes each, and backfills can
// send many at once.
const maxNotificationBytes = 4 << 20

// minWebhookSecretLength is the shortest secret accepted in the webhook's
// path, so it can't be guessed.
const minWebhookSecretLength = 24

// NotificationQueue queues Garmin's notifications.
type NotificationQueue interface {
	AddGarminNotification(ctx context.Context, body []byte) error
}

// Webhook receives Garmin's notifications. Garmin requires them to be
// answered before they're processed, so they're queued, and the syncer
// handles them after. Garmin doesn't sign them, so they're authenticated by
// a secret in the URL, which only Garmin learns through the developer
// portal, and the client ID Garmin sends with them. Even so, a notification
// only points to data fetched from Garmin with the user's token, except a
// push notification's summary, which only ever updates an activity of the
// user it names.
type Webhook struct {
	log      *slog.Logger
	report   *errreport.Reporter
	queue    NotificationQueue
	syncer   *Syncer
	clientID string
	// The path with the secret.
	path string
	// The full URL to configure in Garmin's developer portal.
	callbackURL string
}

// NewWebhook creates a Webhook, served at baseURL, the public URL of
// WebhookPath, followed by the secret. clientID is the app's, which Garmin
// sends in the garmin-client-id header. Register it at its Pattern, and
// configure CallbackURL for each notification in Garmin's developer portal.
func NewWebhook(log *slog.Logger, report *errreport.Reporter, queue NotificationQueue, syncer *Syncer, baseURL, secret, clientID string) (*Webhook, error) {
	if len(secret) < minWebhookSecretLength || strings.ContainsAny(secret, "/?#%") {
		return nil, fmt.Errorf("the Garmin webhook secret must be at least %d characters, without /, ?, # or %%", minWebhookSecretLength)
	}
	return &Webhook{
		log:         log,
		report:      report,
		queue:       queue,
		syncer:      syncer,
		clientID:    clientID,
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
	ctx := r.Context()
	if subtle.ConstantTimeCompare([]byte(r.URL.Path), []byte(h.path)) != 1 {
		h.log.DebugContext(ctx, "Rejecting a Garmin notification to the wrong path")
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Garmin sends the app's client ID with notifications.
	if id := r.Header.Get("garmin-client-id"); id != "" && subtle.ConstantTimeCompare([]byte(id), []byte(h.clientID)) != 1 {
		h.log.WarnContext(ctx, "Rejecting a Garmin notification for another client ID")
		http.Error(w, "unknown client ID", http.StatusForbidden)
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
	if !json.Valid(body) || !strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
		h.log.WarnContext(ctx, "Rejecting a malformed Garmin notification")
		http.Error(w, "malformed notification", http.StatusBadRequest)
		return
	}
	if err := h.queue.AddGarminNotification(ctx, body); err != nil {
		// Garmin sends it again within the hour.
		h.report.Report(ctx, "Failed to queue a Garmin notification, waiting for Garmin to send it again", err)
		http.Error(w, "failed to queue notification", http.StatusServiceUnavailable)
		return
	}
	// Starting the pass only asks the scheduler to run it, which is quick,
	// so it's done before answering, while the server runs.
	h.syncer.NotificationQueued(ctx)
	w.WriteHeader(http.StatusOK)
}
