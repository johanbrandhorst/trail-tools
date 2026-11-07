package garmin

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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/activities"
	"github.com/bs-tools/trailrunn.ing/internal/authn"
	"github.com/bs-tools/trailrunn.ing/internal/errreport"
	"github.com/bs-tools/trailrunn.ing/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
	"gitlab.com/greyxor/slogor"
	"golang.org/x/oauth2"
)

const (
	// ConnectPath starts connecting a Garmin account.
	ConnectPath = "/garmin/connect"
	// CallbackPath is where Garmin redirects to after the user authorized
	// the app. It must be registered as the app's redirect URL in Garmin's
	// developer portal.
	CallbackPath    = "/garmin/callback"
	stateCookieName = "garmin_oauth_state"
	// stateLifetime is how long the user has to authorize the app on
	// Garmin's site. Signing in to Garmin for the first time, with a code
	// for two-factor authentication, and reading the consent screen can
	// take more than 10 minutes.
	stateLifetime = 30 * time.Minute
	// connectSetupTimeout bounds setting up a new connection before
	// redirecting back to the app: asking for the recent activities and
	// putting the upcoming workouts on the calendar. Whatever doesn't finish
	// happens with the next scheduled sync.
	connectSetupTimeout = 15 * time.Second
)

// OAuth connects users' Garmin accounts with OAuth 2.0 and PKCE.
type OAuth struct {
	log    *slog.Logger
	report *errreport.Reporter
	client Client
	repo   ActivityRepository
	syncer *Syncer
	// The key signing the OAuth state.
	stateKey []byte
	nowFn    func() time.Time
}

// NewOAuth creates an OAuth. stateKey signs the OAuth state and must be the
// same on every instance of the server, e.g. the Garmin client secret.
func NewOAuth(log *slog.Logger, report *errreport.Reporter, client Client, repo ActivityRepository, syncer *Syncer, stateKey []byte) *OAuth {
	return &OAuth{log: log, report: report, client: client, repo: repo, syncer: syncer, stateKey: stateKey, nowFn: time.Now}
}

// RegisterHandlers registers the OAuth handlers on the mux. authenticate
// must be the authn middleware.
func (o *OAuth) RegisterHandlers(mux *http.ServeMux, authenticate func(http.Handler) http.Handler) {
	mux.Handle("GET "+ConnectPath, authenticate(http.HandlerFunc(o.handleConnect)))
	// The callback is a cross site navigation from Garmin, so the strict
	// session cookie isn't sent. The user is identified by the signed state.
	mux.Handle("GET "+CallbackPath, http.HandlerFunc(o.handleCallback))
}

func (o *OAuth) handleConnect(w http.ResponseWriter, r *http.Request) {
	userID, ok := authn.GetUserID(r.Context())
	if !ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	nonce := rand.Text()
	// The PKCE verifier stays in the browser's cookie until the callback,
	// so no server state is needed between the two.
	verifier := oauth2.GenerateVerifier()
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    nonce + "." + verifier,
		Path:     CallbackPath,
		MaxAge:   int(stateLifetime.Seconds()),
		Secure:   true,
		HttpOnly: true,
		// Lax, so the cookie is sent on the redirect back from Garmin.
		SameSite: http.SameSiteLaxMode,
	})
	state := o.signState(userID, nonce, o.nowFn().Add(stateLifetime))
	http.Redirect(w, r, o.client.AuthCodeURL(state, verifier), http.StatusFound)
}

func (o *OAuth) handleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	cookie, err := r.Cookie(stateCookieName)
	if err != nil {
		o.redirectSettings(w, r, "error", "Connecting Garmin took more than 30 minutes, or was finished in another browser; please try again")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookieName, Path: CallbackPath, MaxAge: -1, Secure: true, HttpOnly: true})
	nonce, verifier, _ := strings.Cut(cookie.Value, ".")
	userID, err := o.verifyState(q.Get("state"), nonce)
	if err != nil {
		o.log.WarnContext(ctx, "Invalid Garmin OAuth state", slogor.Err(err))
		o.redirectSettings(w, r, "error", "The Garmin connection could not be verified, please try again")
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		o.redirectSettings(w, r, "error", "Garmin access was denied")
		return
	}
	token, err := o.client.Exchange(ctx, q.Get("code"), verifier)
	if err != nil {
		o.report.Report(ctx, "Failed to exchange Garmin code", err)
		o.redirectSettings(w, r, "error", "Failed to connect Garmin")
		return
	}
	garminUserID, err := o.client.UserID(ctx, token.AccessToken)
	if err != nil {
		o.report.Report(ctx, "Failed to get the Garmin user ID", err)
		o.redirectSettings(w, r, "error", "Failed to connect Garmin")
		return
	}
	permissions, err := o.client.Permissions(ctx, token.AccessToken)
	if err != nil {
		o.report.Report(ctx, "Failed to get the Garmin permissions", err)
		o.redirectSettings(w, r, "error", "Failed to connect Garmin")
		return
	}
	slices.Sort(permissions)
	conn, err := o.repo.UpsertGarminConnection(ctx, &activities.GarminConnection{
		UserID:       userID,
		GarminUserID: garminUserID,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		Expiry:       token.Expiry,
		Permissions:  permissions,
	})
	if errors.Is(err, activities.ErrGarminAccountInUse) {
		o.redirectSettings(w, r, "error", "This Garmin account is connected to another trailrunn.ing account; disconnect it there first")
		return
	}
	if err != nil {
		o.report.Report(ctx, "Failed to store Garmin connection", err)
		o.redirectSettings(w, r, "error", "Failed to connect Garmin")
		return
	}
	// Set up the connection before redirecting, so the settings page shows
	// the result. It continues with the next sync if it takes too long.
	setupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), connectSetupTimeout)
	defer cancel()
	o.syncer.Connected(setupCtx, conn)
	o.redirectSettings(w, r, "connected", "")
}

func (o *OAuth) redirectSettings(w http.ResponseWriter, r *http.Request, status, message string) {
	query := url.Values{"garmin": {status}}
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
	if nonce == "" || !hmac.Equal([]byte(parts[1]), []byte(nonce)) {
		return pgtype.UUID{}, errors.New("state does not match this browser")
	}
	expiry, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || o.nowFn().After(time.Unix(expiry, 0)) {
		return pgtype.UUID{}, errors.New("state expired")
	}
	return store.StringToUUID(parts[0])
}
