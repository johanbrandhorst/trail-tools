package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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
