package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
)

// adminRepo is a Repository that only knows whether there is an
// administrator.
type adminRepo struct {
	Repository
	hasAdmin bool
}

func (r *adminRepo) HasAdmin(context.Context) (bool, error) {
	return r.hasAdmin, nil
}

func TestBootstrapHandler(t *testing.T) {
	index := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "index")
	})
	tests := []struct {
		name       string
		token      string
		hasAdmin   bool
		path       string
		host       string
		remoteAddr string
		redirect   bool
	}{
		{name: "local first visit", token: "local-admin", path: "/", host: "localhost:8080", remoteAddr: "127.0.0.1:5000", redirect: true},
		{name: "local IPv6", token: "local-admin", path: "/", host: "[::1]:8080", remoteAddr: "[::1]:5000", redirect: true},
		{name: "local IP", token: "local-admin", path: "/", host: "127.0.0.1:8080", remoteAddr: "127.0.0.1:5000", redirect: true},
		{name: "with an administrator", token: "local-admin", hasAdmin: true, path: "/", host: "localhost:8080", remoteAddr: "127.0.0.1:5000"},
		{name: "without a token", path: "/", host: "localhost:8080", remoteAddr: "127.0.0.1:5000"},
		{name: "another page", token: "local-admin", path: "/routes", host: "localhost:8080", remoteAddr: "127.0.0.1:5000"},
		{name: "remote", token: "secret", path: "/", host: "trail.example", remoteAddr: "203.0.113.7:5000"},
		{name: "proxied", token: "secret", path: "/", host: "trail.example", remoteAddr: "127.0.0.1:5000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			s, err := NewService(log, errreport.NewLocal(log), &adminRepo{hasAdmin: tt.hasAdmin}, "http://localhost:8080", tt.token)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Host = tt.host
			req.RemoteAddr = tt.remoteAddr
			rec := httptest.NewRecorder()
			s.BootstrapHandler(index).ServeHTTP(rec, req)
			if tt.redirect {
				if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/invite#"+tt.token {
					t.Errorf("got status %d to %q, want a redirect to the invitation", rec.Code, rec.Header().Get("Location"))
				}
			} else if rec.Code != http.StatusOK || rec.Body.String() != "index" {
				t.Errorf("got status %d with %q, want the index", rec.Code, rec.Body.String())
			}
		})
	}
}

// localRepo is a Repository that signs in one administrator.
type localRepo struct {
	Repository
	admin    pgtype.UUID
	sessions []pgtype.UUID
}

func (r *localRepo) LocalAdmin(context.Context) (pgtype.UUID, error) {
	return r.admin, nil
}

func (r *localRepo) CreateSession(_ context.Context, userID pgtype.UUID, _ []byte, _ time.Time) (string, error) {
	r.sessions = append(r.sessions, userID)
	return "token", nil
}

func TestLocalSignInHandler(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		host       string
		remoteAddr string
		// The redirect, or empty for a 404.
		want string
	}{
		{name: "local", method: http.MethodGet, target: "/local-sign-in", host: "localhost:8080", remoteAddr: "127.0.0.1:5000", want: "/"},
		{name: "to a page", method: http.MethodGet, target: "/local-sign-in?next=/trip-reports/abc?x=1", host: "localhost:8080", remoteAddr: "[::1]:5000", want: "/trip-reports/abc?x=1"},
		{name: "to another site", method: http.MethodGet, target: "/local-sign-in?next=https://evil.example/", host: "localhost:8080", remoteAddr: "127.0.0.1:5000", want: "/"},
		{name: "to a protocol-relative URL", method: http.MethodGet, target: "/local-sign-in?next=//evil.example/", host: "localhost:8080", remoteAddr: "127.0.0.1:5000", want: "/"},
		{name: "remote", method: http.MethodGet, target: "/local-sign-in", host: "trail.example", remoteAddr: "203.0.113.7:5000"},
		{name: "proxied", method: http.MethodGet, target: "/local-sign-in", host: "trail.example", remoteAddr: "127.0.0.1:5000"},
		{name: "post", method: http.MethodPost, target: "/local-sign-in", host: "localhost:8080", remoteAddr: "127.0.0.1:5000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			repo := &localRepo{admin: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}}
			s, err := NewService(log, errreport.NewLocal(log), repo, "http://localhost:8080", "")
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(tt.method, tt.target, nil)
			req.Host = tt.host
			req.RemoteAddr = tt.remoteAddr
			rec := httptest.NewRecorder()
			s.LocalSignInHandler().ServeHTTP(rec, req)
			if tt.want == "" {
				if rec.Code != http.StatusNotFound || len(repo.sessions) != 0 {
					t.Errorf("got status %d with %d sessions, want a 404 without one", rec.Code, len(repo.sessions))
				}
				return
			}
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != tt.want {
				t.Errorf("got status %d to %q, want a redirect to %q", rec.Code, rec.Header().Get("Location"), tt.want)
			}
			if len(repo.sessions) != 1 || repo.sessions[0] != repo.admin {
				t.Errorf("got sessions for %v, want one for the administrator", repo.sessions)
			}
			if cookie := rec.Header().Get("Set-Cookie"); !strings.Contains(cookie, "token") {
				t.Errorf("got cookie %q, want the session's", cookie)
			}
		})
	}
}

func TestIsLocalURL(t *testing.T) {
	for url, want := range map[string]bool{
		"http://localhost:8080":     true,
		"http://app.localhost:8080": true,
		"http://127.0.0.1:8080":     true,
		"http://[::1]:8080":         true,
		"https://trailrunn.ing":     false,
		"http://0.0.0.0:8080":       false,
		"http://192.168.1.2:8080":   false,
	} {
		if got := IsLocalURL(url); got != want {
			t.Errorf("IsLocalURL(%q) = %v, want %v", url, got, want)
		}
	}
}
