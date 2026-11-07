package strava

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/strava"
	"gitlab.com/greyxor/slogor"
)

const (
	connectPath     = "/strava/connect"
	callbackPath    = "/strava/callback"
	stateCookieName = "strava_oauth_state"
	stateLifetime   = 10 * time.Minute
	// connectSyncTimeout bounds starting the first sync after connecting,
	// which may run the scheduled sync's trigger, so the user isn't kept
	// waiting for the redirect.
	connectSyncTimeout = 10 * time.Second
)

// CallbackPath is the path Strava redirects to after authorization.
const CallbackPath = callbackPath

// OAuthClient is the Strava OAuth client.
type OAuthClient interface {
	AuthCodeURL(state string) string
	Exchange(ctx context.Context, code string) (*strava.Token, *strava.Athlete, error)
}

// OAuth implements the Strava OAuth flow.
type OAuth struct {
	log    *slog.Logger
	report *errreport.Reporter
	client OAuthClient
	repo   ConnectionRepository
	syncer *Syncer
	// The key used to sign the OAuth state.
	stateKey []byte
}

// NewOAuth creates a new OAuth. stateKey signs the OAuth state parameter
// and must be the same on all instances of the server, e.g. the Strava client
// secret.
func NewOAuth(log *slog.Logger, report *errreport.Reporter, client OAuthClient, repo ConnectionRepository, syncer *Syncer, stateKey []byte) *OAuth {
	return &OAuth{
		log:      log,
		report:   report,
		client:   client,
		repo:     repo,
		syncer:   syncer,
		stateKey: stateKey,
	}
}

// RegisterHandlers registers the OAuth handlers on the mux. authenticate must
// be the authn middleware.
func (o *OAuth) RegisterHandlers(mux *http.ServeMux, authenticate func(http.Handler) http.Handler) {
	mux.Handle("GET "+connectPath, authenticate(http.HandlerFunc(o.handleConnect)))
	// The callback is a cross site navigation from Strava, so the strict
	// session cookie is not sent. The user is identified by the signed state
	// instead.
	mux.Handle("GET "+callbackPath, http.HandlerFunc(o.handleCallback))
}

func (o *OAuth) handleConnect(w http.ResponseWriter, r *http.Request) {
	userID, ok := authn.GetUserID(r.Context())
	if !ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	nonce := rand.Text()
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    nonce,
		Path:     callbackPath,
		MaxAge:   int(stateLifetime.Seconds()),
		Secure:   true,
		HttpOnly: true,
		// Lax, so the cookie is sent on the redirect back from Strava.
		SameSite: http.SameSiteLaxMode,
	})
	state := o.signState(userID, nonce, timeNow().Add(stateLifetime))
	http.Redirect(w, r, o.client.AuthCodeURL(state), http.StatusFound)
}

func (o *OAuth) handleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	cookie, err := r.Cookie(stateCookieName)
	if err != nil {
		o.redirectSettings(w, r, "error", "The Strava connection expired, please try again")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookieName, Path: callbackPath, MaxAge: -1, Secure: true, HttpOnly: true})
	userID, err := o.verifyState(q.Get("state"), cookie.Value)
	if err != nil {
		o.log.WarnContext(ctx, "Invalid Strava OAuth state", slogor.Err(err))
		o.redirectSettings(w, r, "error", "The Strava connection could not be verified, please try again")
		return
	}
	if q.Get("error") != "" {
		o.redirectSettings(w, r, "error", "Strava access was denied")
		return
	}
	if scope := q.Get("scope"); !strings.Contains(scope, "activity:read") {
		o.redirectSettings(w, r, "error", "Access to activities is required, please allow it when connecting Strava")
		return
	}
	token, athlete, err := o.client.Exchange(ctx, q.Get("code"))
	if err != nil {
		o.report.Report(ctx, "Failed to exchange Strava code", err)
		o.redirectSettings(w, r, "error", "Failed to connect Strava")
		return
	}
	name := strings.TrimSpace(athlete.FirstName + " " + athlete.LastName)
	if _, err := o.repo.UpsertStravaConnection(ctx, &activities.StravaConnection{
		UserID:       userID,
		AthleteID:    athlete.ID,
		AthleteName:  name,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		Expiry:       token.Expiry,
	}); err != nil {
		o.report.Report(ctx, "Failed to store Strava connection", err)
		o.redirectSettings(w, r, "error", "Failed to connect Strava")
		return
	}
	// Start syncing right away, or with the next scheduled sync if syncs
	// can't run now. The connection is stored either way.
	syncCtx, cancel := context.WithTimeout(ctx, connectSyncTimeout)
	defer cancel()
	if err := o.syncer.SyncConnected(syncCtx, userID); err != nil {
		o.report.Report(ctx, "Failed to start syncing a new Strava connection, syncing with the next scheduled sync", err)
	}
	o.redirectSettings(w, r, "connected", "")
}

func (o *OAuth) redirectSettings(w http.ResponseWriter, r *http.Request, status, message string) {
	query := url.Values{"strava": {status}}
	if message != "" {
		query.Set("message", message)
	}
	http.Redirect(w, r, "/settings?"+query.Encode(), http.StatusFound)
}

// signState creates an OAuth state identifying the user, bound to the
// browser by the nonce in the state cookie.
func (o *OAuth) signState(userID pgtype.UUID, nonce string, expiry time.Time) string {
	payload := store.UUIDToString(userID) + "|" + nonce + "|" + strconv.FormatInt(expiry.Unix(), 10)
	mac := hmac.New(sha256.New, o.stateKey)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifyState verifies an OAuth state and returns the user it identifies.
func (o *OAuth) verifyState(state, nonce string) (pgtype.UUID, error) {
	encodedPayload, encodedMAC, ok := strings.Cut(state, ".")
	if !ok {
		return pgtype.UUID{}, errors.New("malformed state")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("malformed state payload: %w", err)
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(encodedMAC)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("malformed state signature: %w", err)
	}
	mac := hmac.New(sha256.New, o.stateKey)
	mac.Write(payload)
	if !hmac.Equal(gotMAC, mac.Sum(nil)) {
		return pgtype.UUID{}, errors.New("invalid state signature")
	}
	parts := strings.Split(string(payload), "|")
	if len(parts) != 3 {
		return pgtype.UUID{}, errors.New("malformed state payload")
	}
	if !hmac.Equal([]byte(parts[1]), []byte(nonce)) {
		return pgtype.UUID{}, errors.New("state does not match this browser")
	}
	expiry, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || timeNow().After(time.Unix(expiry, 0)) {
		return pgtype.UUID{}, errors.New("state expired")
	}
	return store.StringToUUID(parts[0])
}
