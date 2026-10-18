package authn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/users"
	"gitlab.com/greyxor/slogor"
)

const (
	sessionCookieName = "session"
	logoutPath        = "/logout"
)

type SessionRepository interface {
	GetSession(ctx context.Context, token string) (users.User, error)
	DeleteSession(ctx context.Context, token string) error
	GetAPITokenUser(ctx context.Context, secret string) (users.User, error)
}

type authCtx struct{}

// authInfo is the authenticated user of a request, and how they
// authenticated.
type authInfo struct {
	user users.User
	// session is whether the user authenticated with a session cookie,
	// rather than a bearer token.
	session bool
}

// Option configures the authn middleware.
type Option func(*options)

type options struct {
	resourceMetadataURL string
}

// WithResourceMetadata points OAuth clients that send an invalid token to
// the protected resource metadata (RFC 9728) at the URL, so they can get a
// new token.
func WithResourceMetadata(url string) Option {
	return func(o *options) {
		o.resourceMetadataURL = url
	}
}

func NewAuthnMiddleware(ctx context.Context, log *slog.Logger, report *errreport.Reporter, repo SessionRepository, next http.Handler, opts ...Option) (http.Handler, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API clients authenticate with a bearer token instead of a cookie.
		if secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			user, err := repo.GetAPITokenUser(r.Context(), secret)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					log.DebugContext(r.Context(), "Invalid API token", slog.String("path", r.URL.Path))
					w.Header().Set("WWW-Authenticate", bearerChallenge(o.resourceMetadataURL, "invalid_token"))
					http.Error(w, "invalid or expired token", http.StatusUnauthorized)
					return
				}
				report.Report(r.Context(), "Failed to get API token user", err)
				http.Error(w, "failed to authenticate", http.StatusInternalServerError)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authCtx{}, authInfo{user: user})))
			return
		}
		sessionCookie, err := r.Cookie(sessionCookieName)
		if errors.Is(err, http.ErrNoCookie) {
			log.DebugContext(r.Context(), "User had no session cookie, continue unauthenticated", slog.String("path", r.URL.Path))
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			log.DebugContext(r.Context(), "Failed to get session cookie, continue unauthenticated", slogor.Err(err), slog.String("path", r.URL.Path))
			next.ServeHTTP(w, r)
			return
		}
		user, err := repo.GetSession(r.Context(), sessionCookie.Value)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				log.DebugContext(r.Context(), "session could not be found, continue unauthenticated")
				expireCookie(w, sessionCookie)
				next.ServeHTTP(w, r)
				return
			}
			report.Report(r.Context(), "failed to get session", err)
			http.Error(w, "failed to get session", http.StatusInternalServerError)
			return
		}
		newCtx := context.WithValue(r.Context(), authCtx{}, authInfo{user: user, session: true})
		next.ServeHTTP(w, r.WithContext(newCtx))
	}), nil
}

// GetUser gets the authenticated user, if there is one. It returns
// (nil, false) if the current request is unauthenticated.
func GetUser(ctx context.Context) (users.User, bool) {
	info, ok := ctx.Value(authCtx{}).(authInfo)
	return info.user, ok
}

// ViaSession returns whether the request was authenticated with a session
// cookie, which only the web app has, rather than a bearer token.
func ViaSession(ctx context.Context) bool {
	info, ok := ctx.Value(authCtx{}).(authInfo)
	return ok && info.session
}

// GetSessionUser gets the user authenticated with a session cookie, for
// managing the account's credentials: passkeys, invitations, API tokens and
// OAuth connections. Bearer tokens are rejected, as they are held by API
// clients and AI agents, which must not be able to sign in as the user,
// create more tokens or connect more clients. It returns a Connect error with
// code Unauthenticated if the request is unauthenticated, and
// PermissionDenied if it was authenticated with a bearer token.
func GetSessionUser(ctx context.Context) (users.User, error) {
	info, ok := ctx.Value(authCtx{}).(authInfo)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if !info.session {
		return nil, connect.NewError(connect.CodePermissionDenied, "API tokens and OAuth access tokens can't manage passkeys, invitations, API tokens or connections; sign in to the trailrunn.ing web app to do this in the settings")
	}
	return info.user, nil
}

// GetUserID gets the ID of the authenticated user, if there is one. It
// returns false if the current request is unauthenticated.
func GetUserID(ctx context.Context) (pgtype.UUID, bool) {
	user, ok := GetUser(ctx)
	if !ok {
		return pgtype.UUID{}, false
	}
	return user.ID(), true
}

// RequireUser rejects unauthenticated requests with a 401 Unauthorized. It
// must be wrapped by the authn middleware. If resourceMetadataURL is set, the
// WWW-Authenticate header points OAuth clients to the protected resource
// metadata (RFC 9728), so they can discover how to sign in.
func RequireUser(resourceMetadataURL string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := GetUser(r.Context()); !ok {
			w.Header().Set("WWW-Authenticate", bearerChallenge(resourceMetadataURL, ""))
			http.Error(w, "authentication required: connect with OAuth, or create an API token in the trailrunn.ing settings and send it in an \"Authorization: Bearer <token>\" header", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerChallenge returns a Bearer WWW-Authenticate challenge (RFC 6750
// section 3), with the optional resource metadata URL and error code.
func bearerChallenge(resourceMetadataURL, errorCode string) string {
	var params []string
	if resourceMetadataURL != "" {
		params = append(params, fmt.Sprintf("resource_metadata=%q", resourceMetadataURL))
	}
	if errorCode != "" {
		params = append(params, fmt.Sprintf("error=%q", errorCode))
	}
	if len(params) == 0 {
		return "Bearer"
	}
	return "Bearer " + strings.Join(params, ", ")
}

// SessionCookie returns the session cookie for a session's token.
func SessionCookie(token string, expiry time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		MaxAge:   int(time.Until(expiry).Seconds()),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
	}
}

// RegisterLogoutHandler registers a handler that logs out the user by
// expiring the session cookie and redirecting to the root path.
func RegisterLogoutHandler(report *errreport.Reporter, mux *http.ServeMux, sessionRepo SessionRepository) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookieName); err == nil {
			if err := sessionRepo.DeleteSession(r.Context(), c.Value); err != nil && !errors.Is(err, store.ErrNotFound) {
				report.Report(r.Context(), "failed to delete session", err)
			}
			expireCookie(w, c)
		}
		http.Redirect(w, r, "/", http.StatusFound)
	})
	mux.Handle(logoutPath, h)
}

func expireCookie(w http.ResponseWriter, oldCookie *http.Cookie) {
	oldCookie.Expires = time.Now().Add(-time.Hour)
	oldCookie.Path = "/"
	oldCookie.Value = ""
	http.SetCookie(w, oldCookie)
}
