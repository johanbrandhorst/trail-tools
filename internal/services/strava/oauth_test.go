package strava

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
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/strava"
)

func TestOAuthState(t *testing.T) {
	o := &OAuth{stateKey: []byte("secret")}
	userID, err := store.StringToUUID("8f6c1f54-4b1e-4f4e-9a0e-2f0b0a8f6c1f")
	if err != nil {
		t.Fatal(err)
	}
	state := o.signState(userID, "nonce", time.Now().Add(time.Minute))
	got, err := o.verifyState(state, "nonce")
	if err != nil || got != userID {
		t.Fatalf("verifyState: got %v, %v", got, err)
	}
	if _, err := o.verifyState(state, "other-nonce"); err == nil {
		t.Error("state from another browser should be rejected")
	}
	if _, err := (&OAuth{stateKey: []byte("other")}).verifyState(state, "nonce"); err == nil {
		t.Error("state with an invalid signature should be rejected")
	}
	expired := o.signState(userID, "nonce", time.Now().Add(-time.Minute))
	if _, err := o.verifyState(expired, "nonce"); err == nil {
		t.Error("expired state should be rejected")
	}
	if _, err := o.verifyState("garbage", "nonce"); err == nil {
		t.Error("malformed state should be rejected")
	}
}

// fakeOAuthClient exchanges any code for a token of athlete 7.
type fakeOAuthClient struct{}

func (fakeOAuthClient) AuthCodeURL(state string) string {
	return "https://strava.example/authorize?state=" + state
}

func (fakeOAuthClient) Exchange(context.Context, string) (*strava.Token, *strava.Athlete, error) {
	return &strava.Token{AccessToken: "access", RefreshToken: "refresh", Expiry: time.Now().Add(6 * time.Hour)},
		&strava.Athlete{ID: 7, FirstName: "Test", LastName: "Runner"}, nil
}

// fakeConnectionRepo records the stored connection and sync states. Its
// other methods panic.
type fakeConnectionRepo struct {
	SyncRepository
	mu     sync.Mutex
	conn   *activities.StravaConnection
	states []activities.SyncState
}

func (f *fakeConnectionRepo) UpsertStravaConnection(_ context.Context, conn *activities.StravaConnection) (*activities.StravaConnection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.conn = conn
	return conn, nil
}

func (f *fakeConnectionRepo) DeleteStravaConnection(context.Context, pgtype.UUID) error {
	panic("unexpected DeleteStravaConnection")
}

func (f *fakeConnectionRepo) UpdateStravaSyncState(_ context.Context, _ pgtype.UUID, state activities.SyncState, _ string, _, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states = append(f.states, state)
	return nil
}

// fakeTrigger counts its runs.
type fakeTrigger struct {
	mu   sync.Mutex
	runs int
}

func (f *fakeTrigger) Run(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs++
	return nil
}

func TestOAuthCallbackStartsScheduledSync(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	report := errreport.NewLocal(log)
	repo := &fakeConnectionRepo{}
	trigger := &fakeTrigger{}
	syncer := NewSyncer(t.Context(), log, report, repo, nil, false, trigger)
	o := NewOAuth(log, report, fakeOAuthClient{}, repo, syncer, []byte("secret"))
	userID, err := store.StringToUUID("8f6c1f54-4b1e-4f4e-9a0e-2f0b0a8f6c1f")
	if err != nil {
		t.Fatal(err)
	}

	// Strava redirects back with a code after the user authorized access.
	q := url.Values{
		"state": {o.signState(userID, "nonce", time.Now().Add(time.Minute))},
		"code":  {"code"},
		"scope": {"read,activity:read_all"},
	}
	req := httptest.NewRequest(http.MethodGet, callbackPath+"?"+q.Encode(), nil)
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "nonce"})
	rec := httptest.NewRecorder()
	o.handleCallback(rec, req)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || loc != "/settings?strava=connected" {
		t.Fatalf("got %d to %q, want a redirect to the connected settings", rec.Code, loc)
	}
	if repo.conn == nil || repo.conn.UserID != userID || repo.conn.AthleteID != 7 {
		t.Fatalf("got connection %+v, want it stored", repo.conn)
	}
	// Syncs don't run in the background, so the connection is marked due a
	// scheduled sync, which the trigger runs right away.
	if len(repo.states) != 1 || repo.states[0] != activities.SyncStateRunning || trigger.runs != 1 {
		t.Errorf("got states %v and %d trigger runs, want a scheduled sync started", repo.states, trigger.runs)
	}
}
