package garmin

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/garmin"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

func TestOAuthState(t *testing.T) {
	t.Parallel()
	o := &OAuth{stateKey: []byte("secret"), nowFn: time.Now}
	userID, err := store.StringToUUID("8f6c1f54-4b1e-4f4e-9a0e-2f0b0a8f6c1f")
	if err != nil {
		t.Fatal(err)
	}
	state := o.signState(userID, "nonce", time.Now().Add(time.Minute))
	got, err := o.verifyState(state, "nonce")
	if err != nil || got != userID {
		t.Fatalf("verifyState: got %v, %v", got, err)
	}
	for name, tc := range map[string]struct {
		o            *OAuth
		state, nonce string
	}{
		"another browser":   {o, state, "other-nonce"},
		"no cookie":         {o, state, ""},
		"invalid signature": {&OAuth{stateKey: []byte("other"), nowFn: time.Now}, state, "nonce"},
		"expired":           {o, o.signState(userID, "nonce", time.Now().Add(-time.Minute)), "nonce"},
		"malformed":         {o, "garbage", "nonce"},
	} {
		if _, err := tc.o.verifyState(tc.state, tc.nonce); err == nil {
			t.Errorf("%s: state accepted", name)
		}
	}
}

// fakeOAuthClient checks that the code is exchanged with the PKCE verifier
// of the browser's cookie. Its other methods panic.
type fakeOAuthClient struct {
	Client
	mu        sync.Mutex
	verifier  string
	backfills int
}

func (f *fakeOAuthClient) Exchange(_ context.Context, code, verifier string) (*garmin.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verifier = verifier
	return &garmin.Token{AccessToken: "access", RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour)}, nil
}

func (f *fakeOAuthClient) UserID(context.Context, string) (string, error) {
	return "garmin-user", nil
}

func (f *fakeOAuthClient) Permissions(context.Context, string) ([]string, error) {
	return []string{garmin.PermissionActivityExport}, nil
}

func (f *fakeOAuthClient) BackfillActivities(context.Context, string, time.Time, time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.backfills++
	return nil
}

// fakeConnectionRepo stores one connection, with no activity files. Its
// other methods panic.
type fakeConnectionRepo struct {
	ActivityRepository
	mu   sync.Mutex
	conn *activities.GarminConnection
}

func (f *fakeConnectionRepo) UpsertGarminConnection(_ context.Context, c *activities.GarminConnection) (*activities.GarminConnection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.conn = c
	return c, nil
}

func (f *fakeConnectionRepo) GetGarminConnection(context.Context, pgtype.UUID) (*activities.GarminConnection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conn, nil
}

func (f *fakeConnectionRepo) ListGarminActivityFiles(context.Context, pgtype.UUID, int32) ([]*activities.GarminActivityFile, error) {
	return nil, nil
}

func TestOAuthCallback(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	report := errreport.NewLocal(log)
	client := &fakeOAuthClient{}
	repo := &fakeConnectionRepo{}
	o := NewOAuth(log, report, client, repo, NewSyncer(log, report, client, repo, nil), []byte("secret"))
	userID, err := store.StringToUUID("8f6c1f54-4b1e-4f4e-9a0e-2f0b0a8f6c1f")
	if err != nil {
		t.Fatal(err)
	}

	// Garmin redirects back with a code after the user authorized the app.
	q := url.Values{"state": {o.signState(userID, "nonce", time.Now().Add(time.Minute))}, "code": {"code"}}
	req := httptest.NewRequest(http.MethodGet, CallbackPath+"?"+q.Encode(), nil)
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "nonce.the-verifier"})
	rec := httptest.NewRecorder()
	o.handleCallback(rec, req)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || loc != "/settings?garmin=connected" {
		t.Fatalf("got %d to %q, want a redirect to the connected settings", rec.Code, loc)
	}
	if client.verifier != "the-verifier" {
		t.Errorf("exchanged the code with verifier %q, want the cookie's", client.verifier)
	}
	if repo.conn == nil || repo.conn.UserID != userID || repo.conn.GarminUserID != "garmin-user" || len(repo.conn.Permissions) != 1 {
		t.Fatalf("got connection %+v, want it stored", repo.conn)
	}
	// The recent activities are asked for once connected.
	if client.backfills != 1 {
		t.Errorf("got %d backfills, want 1", client.backfills)
	}

	// A denied authorization stores nothing.
	repo.conn = nil
	q.Set("error", "access_denied")
	req = httptest.NewRequest(http.MethodGet, CallbackPath+"?"+q.Encode(), nil)
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "nonce.the-verifier"})
	rec = httptest.NewRecorder()
	o.handleCallback(rec, req)
	if loc := rec.Header().Get("Location"); repo.conn != nil || loc != "/settings?garmin=error&message=Garmin+access+was+denied" {
		t.Errorf("got a redirect to %q and connection %+v after a denial", loc, repo.conn)
	}
}

func TestWebhookSecret(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, secret := range []string{"", "short", "a-secret-of-enough-length/with-a-slash"} {
		if _, err := NewWebhook(log, errreport.NewLocal(log), nil, "https://trail.example"+WebhookPath, secret); err == nil {
			t.Errorf("secret %q accepted", secret)
		}
	}
	h, err := NewWebhook(log, errreport.NewLocal(log), nil, "https://trail.example"+WebhookPath, "a-secret-of-enough-length")
	if err != nil {
		t.Fatal(err)
	}
	if got := h.CallbackURL(); got != "https://trail.example/garmin/webhook/a-secret-of-enough-length" {
		t.Errorf("got callback URL %q", got)
	}
}
