// Package oauth implements an OAuth 2.1 authorization server for the MCP
// server, so MCP clients that require OAuth, like claude.ai custom
// connectors, can act on behalf of users.
//
// It follows the MCP authorization specification:
//
//   - Protected resource metadata (RFC 9728) points clients from /mcp to
//     this authorization server.
//   - Authorization server metadata (RFC 8414) describes the endpoints.
//   - Clients register with dynamic client registration (RFC 7591). The
//     registration is encoded in the client ID instead of being stored, so
//     unauthenticated registrations don't write to the database. Only
//     redirect URIs of known MCP clients are allowed.
//   - Clients get tokens with the authorization code grant with PKCE (S256
//     only), after the user approves the client on a consent page, and
//     refresh them with rotating refresh tokens.
//
// Access tokens are opaque API tokens, so the authn middleware accepts them
// like any other API token.
package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/users"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// Paths of the OAuth endpoints.
const (
	AuthorizationServerMetadataPath = "/.well-known/oauth-authorization-server"
	ProtectedResourceMetadataPath   = "/.well-known/oauth-protected-resource"
	AuthorizePath                   = "/oauth/authorize"
	TokenPath                       = "/oauth/token"
	RegisterPath                    = "/oauth/register"
)

const (
	// codeTTL is how long users have between approving a client and the
	// client exchanging the authorization code.
	codeTTL = 10 * time.Minute
	// accessTokenTTL is how long access tokens are valid.
	accessTokenTTL = 30 * 24 * time.Hour
	// refreshTokenTTL is how long refresh tokens are valid. Refreshing
	// issues a new refresh token, so clients stay connected as long as they
	// are used.
	refreshTokenTTL = 90 * 24 * time.Hour
	// clientIDPrefix identifies client IDs, which encode the registration.
	clientIDPrefix = "ttcl_"
	// maxClientNameLength is the maximum length of a client name, in bytes.
	maxClientNameLength = 100
	// maxRedirectURIs is the maximum number of redirect URIs a client can
	// register.
	maxRedirectURIs = 10
	// unknownClientName is the name of clients that don't register one.
	unknownClientName = "Unknown client"
)

// Repository stores authorization codes and grants.
type Repository interface {
	CreateOAuthAuthorizationCode(ctx context.Context, authz *users.OAuthAuthorization, expiry time.Time) (string, error)
	ConsumeOAuthAuthorizationCode(ctx context.Context, code string) (*users.OAuthAuthorization, error)
	CreateOAuthGrant(ctx context.Context, authz *users.OAuthAuthorization, accessExpiry, refreshExpiry time.Time) (*users.OAuthTokens, error)
	RefreshOAuthGrant(ctx context.Context, refreshToken, clientID string, accessExpiry, refreshExpiry time.Time) (*users.OAuthTokens, error)
}

// Server is an OAuth authorization server for a protected resource.
type Server struct {
	log      *slog.Logger
	report   *errreport.Reporter
	repo     Repository
	issuer   string
	resource string
	now      func() time.Time
}

// NewServer creates an authorization server. The publicURL is the public URL
// of the application, which is the issuer, and resourcePath is the path of
// the protected resource, e.g. "/mcp".
func NewServer(log *slog.Logger, report *errreport.Reporter, repo Repository, publicURL, resourcePath string) *Server {
	issuer := strings.TrimSuffix(publicURL, "/")
	return &Server{
		log:      log,
		report:   report,
		repo:     repo,
		issuer:   issuer,
		resource: issuer + resourcePath,
		now:      time.Now,
	}
}

// ResourceMetadataURL returns the URL of the protected resource metadata,
// which clients are pointed to by the WWW-Authenticate header of 401
// responses from the protected resource.
func (s *Server) ResourceMetadataURL() string {
	u, err := url.Parse(s.resource)
	if err != nil {
		return s.issuer + ProtectedResourceMetadataPath
	}
	// RFC 9728 section 3.1: insert the well-known path between the host and
	// the path of the resource.
	return s.issuer + ProtectedResourceMetadataPath + u.Path
}

// RegisterHandlers registers the OAuth endpoints on the mux. Valid
// authorization requests are served by consentPage, which asks the user to
// approve the client with the OAuth service.
func (s *Server) RegisterHandlers(mux *http.ServeMux, consentPage http.Handler) {
	resourcePath := strings.TrimPrefix(s.resource, s.issuer)
	mux.Handle("GET "+ProtectedResourceMetadataPath, cors(http.HandlerFunc(s.handleProtectedResourceMetadata)))
	mux.Handle("GET "+ProtectedResourceMetadataPath+resourcePath, cors(http.HandlerFunc(s.handleProtectedResourceMetadata)))
	mux.Handle("GET "+AuthorizationServerMetadataPath, cors(http.HandlerFunc(s.handleAuthorizationServerMetadata)))
	mux.Handle("POST "+RegisterPath, cors(http.HandlerFunc(s.handleRegister)))
	mux.Handle("POST "+TokenPath, cors(http.HandlerFunc(s.handleToken)))
	for _, path := range []string{ProtectedResourceMetadataPath, ProtectedResourceMetadataPath + resourcePath, AuthorizationServerMetadataPath, RegisterPath, TokenPath} {
		mux.Handle("OPTIONS "+path, cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})))
	}
	mux.Handle("GET "+AuthorizePath, s.authorizeHandler(consentPage))
}

// cors allows browser based clients to discover the endpoints, register and
// get tokens. These endpoints don't use cookies, so any origin is allowed.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, &oauthex.ProtectedResourceMetadata{
		Resource:               s.resource,
		AuthorizationServers:   []string{s.issuer},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "trailrunn.ing",
	})
}

// authorizationServerMetadata is the authorization server metadata of RFC
// 8414. oauthex.AuthServerMeta always includes jwks_uri, which doesn't apply
// to opaque tokens.
type authorizationServerMetadata struct {
	Issuer                                     string   `json:"issuer"`
	AuthorizationEndpoint                      string   `json:"authorization_endpoint"`
	TokenEndpoint                              string   `json:"token_endpoint"`
	RegistrationEndpoint                       string   `json:"registration_endpoint"`
	ResponseTypesSupported                     []string `json:"response_types_supported"`
	ResponseModesSupported                     []string `json:"response_modes_supported"`
	GrantTypesSupported                        []string `json:"grant_types_supported"`
	TokenEndpointAuthMethodsSupported          []string `json:"token_endpoint_auth_methods_supported"`
	CodeChallengeMethodsSupported              []string `json:"code_challenge_methods_supported"`
	AuthorizationResponseIssParameterSupported bool     `json:"authorization_response_iss_parameter_supported"`
}

func (s *Server) handleAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, &authorizationServerMetadata{
		Issuer:                                     s.issuer,
		AuthorizationEndpoint:                      s.issuer + AuthorizePath,
		TokenEndpoint:                              s.issuer + TokenPath,
		RegistrationEndpoint:                       s.issuer + RegisterPath,
		ResponseTypesSupported:                     []string{"code"},
		ResponseModesSupported:                     []string{"query"},
		GrantTypesSupported:                        []string{"authorization_code", "refresh_token"},
		TokenEndpointAuthMethodsSupported:          []string{"none"},
		CodeChallengeMethodsSupported:              []string{"S256"},
		AuthorizationResponseIssParameterSupported: true,
	})
}

// client is a registered client. It is encoded in the client ID.
type client struct {
	Name         string   `json:"n"`
	RedirectURIs []string `json:"r"`
}

func encodeClientID(c *client) (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return clientIDPrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

// decodeClientID decodes and validates a client ID.
func decodeClientID(clientID string) (*client, error) {
	encoded, ok := strings.CutPrefix(clientID, clientIDPrefix)
	if !ok {
		return nil, errors.New("unknown client_id")
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("malformed client_id")
	}
	var c client
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, errors.New("malformed client_id")
	}
	if err := validateClient(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

func validateClient(c *client) error {
	if len(c.RedirectURIs) == 0 {
		return errors.New("redirect_uris is required")
	}
	if len(c.RedirectURIs) > maxRedirectURIs {
		return fmt.Errorf("at most %d redirect_uris are allowed", maxRedirectURIs)
	}
	if len(c.Name) > maxClientNameLength {
		return fmt.Errorf("client_name must be at most %d bytes", maxClientNameLength)
	}
	for _, uri := range c.RedirectURIs {
		if err := validateRedirectURI(uri); err != nil {
			return fmt.Errorf("redirect URI %q: %w", uri, err)
		}
	}
	return nil
}

// allowedRedirectHosts are the hosts of HTTPS redirect URIs of MCP clients
// that run in the browser or redirect through their website, like claude.ai
// custom connectors and Claude Desktop. Allowing any host would let anyone
// register a client that sends authorization codes to their website.
var allowedRedirectHosts = []string{
	// https://claude.com/docs/connectors/building/authentication
	"claude.ai",
	"claude.com",
	// https://developers.openai.com/apps-sdk/build/auth
	"chatgpt.com",
	// https://code.visualstudio.com/api/extension-guides/ai/mcp
	"vscode.dev",
	"insiders.vscode.dev",
}

// allowedRedirectPrefixes are prefixes of HTTPS redirect URIs of MCP clients
// on shared hosts, where only some paths belong to the client.
var allowedRedirectPrefixes = []string{
	// Gemini's custom MCP connectors redirect through Google's OAuth relay,
	// which forwards /r/<id> to the Google Cloud project with that ID, so the
	// hosts can't be allowed as a whole. Gemini's IDs start with
	// user_bound_custom-mcp-, which no Cloud project ID can, as those can't
	// contain underscores. Gemini registers its redirects on the relay's
	// production, sandbox and test hosts, under both /r/ and /a/.
	"https://oauth-redirect.googleusercontent.com/r/user_bound_custom-mcp-",
	"https://oauth-redirect.googleusercontent.com/a/user_bound_custom-mcp-",
	"https://oauth-redirect-sandbox.googleusercontent.com/r/user_bound_custom-mcp-",
	"https://oauth-redirect-sandbox.googleusercontent.com/a/user_bound_custom-mcp-",
	"https://oauth-redirect-test.googleusercontent.com/r/user_bound_custom-mcp-",
	"https://oauth-redirect-test.googleusercontent.com/a/user_bound_custom-mcp-",
}

// allowedRedirectSchemes are the private use URI schemes of desktop MCP
// clients.
var allowedRedirectSchemes = []string{
	"cursor",
	"vscode",
	"vscode-insiders",
}

// validateRedirectURI checks that a redirect URI belongs to a known MCP
// client: a loopback address for native clients like Claude Code, an HTTPS
// URL on an allowed host, or an allowed private use scheme.
func validateRedirectURI(uri string) error {
	u, err := url.Parse(uri)
	if err != nil {
		return errors.New("invalid URL")
	}
	if u.Fragment != "" || strings.Contains(uri, "#") {
		return errors.New("must not have a fragment")
	}
	if u.User != nil {
		return errors.New("must not have user info")
	}
	switch u.Scheme {
	case "http":
		if !isLoopback(u.Hostname()) {
			return errors.New("http is only allowed for loopback addresses")
		}
		return nil
	case "https":
		if isLoopback(u.Hostname()) || slices.Contains(allowedRedirectHosts, u.Hostname()) || hasAllowedPrefix(u) {
			return nil
		}
		return fmt.Errorf("host %q is not a known MCP client", u.Hostname())
	default:
		if slices.Contains(allowedRedirectSchemes, u.Scheme) {
			return nil
		}
		return fmt.Errorf("scheme %q is not allowed", u.Scheme)
	}
}

// hasAllowedPrefix returns whether a redirect URI has one of the allowed
// prefixes. Paths with dot segments or escaped slashes don't, as they could
// lead elsewhere once resolved.
func hasAllowedPrefix(u *url.URL) bool {
	if path.Clean(u.Path) != u.Path || u.RawPath != "" {
		return false
	}
	target := u.Scheme + "://" + u.Host + u.Path
	return slices.ContainsFunc(allowedRedirectPrefixes, func(prefix string) bool {
		return strings.HasPrefix(target, prefix)
	})
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// matchRedirectURI returns whether the redirect URI of an authorization
// request matches a registered one. Loopback redirect URIs match on any
// port, as native clients listen on an ephemeral port (RFC 8252 section 7.3).
func matchRedirectURI(registered, requested string) bool {
	if registered == requested {
		return true
	}
	r, err := url.Parse(registered)
	if err != nil {
		return false
	}
	q, err := url.Parse(requested)
	if err != nil {
		return false
	}
	return r.Scheme == "http" && q.Scheme == "http" &&
		isLoopback(r.Hostname()) && r.Hostname() == q.Hostname() &&
		r.Path == q.Path && r.RawQuery == q.RawQuery
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var meta oauthex.ClientRegistrationMetadata
	// Rejected registrations are logged, as clients rarely show why.
	reject := func(code, description string) {
		s.log.WarnContext(r.Context(), "Rejected OAuth client registration",
			slog.String("error", code),
			slog.String("description", description),
			slog.String("client_name", meta.ClientName),
			slog.Any("redirect_uris", meta.RedirectURIs),
			slog.Any("grant_types", meta.GrantTypes),
			slog.Any("response_types", meta.ResponseTypes),
			slog.String("user_agent", r.UserAgent()),
		)
		writeError(w, http.StatusBadRequest, code, description)
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&meta); err != nil {
		reject("invalid_client_metadata", "invalid JSON: "+err.Error())
		return
	}
	// Clients may ask for more than is supported; they get what is, as long
	// as that includes the authorization code flow (RFC 7591 section 3.2.1).
	if len(meta.GrantTypes) > 0 && !slices.Contains(meta.GrantTypes, "authorization_code") {
		reject("invalid_client_metadata", "the authorization_code grant type is required")
		return
	}
	if len(meta.ResponseTypes) > 0 && !slices.Contains(meta.ResponseTypes, "code") {
		reject("invalid_client_metadata", `the "code" response type is required`)
		return
	}
	c := &client{
		Name:         strings.TrimSpace(meta.ClientName),
		RedirectURIs: meta.RedirectURIs,
	}
	if err := validateClient(c); err != nil {
		code := "invalid_client_metadata"
		if len(c.RedirectURIs) > 0 && len(c.RedirectURIs) <= maxRedirectURIs && len(c.Name) <= maxClientNameLength {
			code = "invalid_redirect_uri"
		}
		reject(code, err.Error())
		return
	}
	clientID, err := encodeClientID(c)
	if err != nil {
		s.report.Report(r.Context(), "Failed to encode client ID", err)
		writeError(w, http.StatusInternalServerError, "server_error", "failed to register client")
		return
	}
	// Clients are public: they authenticate with PKCE, not a secret.
	meta.TokenEndpointAuthMethod = "none"
	meta.GrantTypes = []string{"authorization_code", "refresh_token"}
	meta.ResponseTypes = []string{"code"}
	meta.ClientName = c.Name
	writeJSON(w, http.StatusCreated, &oauthex.ClientRegistrationResponse{
		ClientRegistrationMetadata: meta,
		ClientID:                   clientID,
		ClientIDIssuedAt:           s.now(),
	})
}

// AuthorizationRequest is a validated authorization request.
type AuthorizationRequest struct {
	// ClientID is the ID of the client.
	ClientID string
	// ClientName is the name the client registered with.
	ClientName string
	// RedirectURI is where to redirect the user to.
	RedirectURI string
	state       string
	challenge   string
	issuer      string
}

// AuthorizationError is an error of an authorization request. If Redirect
// is true, the error is returned to the client by redirecting the user.
// Otherwise the redirect URI can't be trusted, and the error is shown to the
// user.
type AuthorizationError struct {
	Code        string
	Description string
	Redirect    bool
}

func (e *AuthorizationError) Error() string {
	return e.Code + ": " + e.Description
}

// ParseAuthorizationRequest validates the query of an authorization request.
// If the request is invalid, it returns an *AuthorizationError, and if the
// error can be returned to the client, the request.
func (s *Server) ParseAuthorizationRequest(query string) (*AuthorizationRequest, error) {
	q, err := url.ParseQuery(query)
	if err != nil {
		return nil, &AuthorizationError{Code: "invalid_request", Description: "invalid query"}
	}
	for key, values := range q {
		// RFC 6749 section 3.1: parameters must not be repeated.
		if len(values) > 1 {
			return nil, &AuthorizationError{Code: "invalid_request", Description: fmt.Sprintf("repeated parameter %q", key)}
		}
	}
	c, err := decodeClientID(q.Get("client_id"))
	if err != nil {
		return nil, &AuthorizationError{Code: "invalid_client", Description: err.Error()}
	}
	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" {
		if len(c.RedirectURIs) != 1 {
			return nil, &AuthorizationError{Code: "invalid_request", Description: "redirect_uri is required"}
		}
		redirectURI = c.RedirectURIs[0]
	}
	if !slices.ContainsFunc(c.RedirectURIs, func(registered string) bool {
		return matchRedirectURI(registered, redirectURI)
	}) {
		return nil, &AuthorizationError{Code: "invalid_request", Description: "redirect_uri is not registered for the client"}
	}
	name := c.Name
	if name == "" {
		name = unknownClientName
	}
	req := &AuthorizationRequest{
		ClientID:    q.Get("client_id"),
		ClientName:  name,
		RedirectURI: redirectURI,
		state:       q.Get("state"),
		challenge:   q.Get("code_challenge"),
		issuer:      s.issuer,
	}
	// From here on, errors are returned to the client.
	redirectErr := func(code, description string) (*AuthorizationRequest, error) {
		return req, &AuthorizationError{Code: code, Description: description, Redirect: true}
	}
	if rt := q.Get("response_type"); rt != "code" {
		return redirectErr("unsupported_response_type", "response_type must be code")
	}
	if req.challenge == "" {
		return redirectErr("invalid_request", "code_challenge is required")
	}
	if q.Get("code_challenge_method") != "S256" {
		return redirectErr("invalid_request", "code_challenge_method must be S256")
	}
	// A S256 challenge is the base64url encoded SHA-256 hash of the verifier.
	if decoded, err := base64.RawURLEncoding.DecodeString(req.challenge); err != nil || len(decoded) != sha256.Size {
		return redirectErr("invalid_request", "invalid code_challenge")
	}
	if resource := q.Get("resource"); resource != "" && !s.isResource(resource) {
		return redirectErr("invalid_target", "unknown resource")
	}
	if mode := q.Get("response_mode"); mode != "" && mode != "query" {
		return redirectErr("invalid_request", "response_mode must be query")
	}
	// Requested scopes are ignored: clients get access to the full API, like
	// API tokens.
	return req, nil
}

// isResource returns whether the resource indicator (RFC 8707) refers to
// the protected resource.
func (s *Server) isResource(resource string) bool {
	return strings.TrimSuffix(resource, "/") == s.resource
}

// authorizeHandler validates authorization requests before serving the
// consent page.
func (s *Server) authorizeHandler(consentPage http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := s.ParseAuthorizationRequest(r.URL.RawQuery)
		if err != nil {
			var authzErr *AuthorizationError
			if errors.As(err, &authzErr) && authzErr.Redirect && req != nil {
				http.Redirect(w, r, req.ErrorRedirect(authzErr.Code, authzErr.Description), http.StatusFound)
				return
			}
			http.Error(w, "Invalid authorization request: "+err.Error(), http.StatusBadRequest)
			return
		}
		consentPage.ServeHTTP(w, r)
	})
}

// redirect returns the redirect URI with the response parameters, the state
// and the issuer (RFC 9207) added to its query.
func (req *AuthorizationRequest) redirect(params url.Values) string {
	u, err := url.Parse(req.RedirectURI)
	if err != nil {
		// The redirect URI was validated when parsing the request.
		panic(err)
	}
	q := u.Query()
	maps.Copy(q, params)
	if req.state != "" {
		q.Set("state", req.state)
	}
	q.Set("iss", req.issuer)
	u.RawQuery = q.Encode()
	return u.String()
}

// ErrorRedirect returns the URL redirecting the user to the client with an
// error.
func (req *AuthorizationRequest) ErrorRedirect(code, description string) string {
	return req.redirect(url.Values{"error": {code}, "error_description": {description}})
}

// Approve approves the authorization request for the user, and returns the
// URL redirecting the user to the client with an authorization code.
func (s *Server) Approve(ctx context.Context, userID pgtype.UUID, req *AuthorizationRequest) (string, error) {
	code, err := s.repo.CreateOAuthAuthorizationCode(ctx, &users.OAuthAuthorization{
		UserID:        userID,
		ClientID:      req.ClientID,
		ClientName:    req.ClientName,
		RedirectURI:   req.RedirectURI,
		CodeChallenge: req.challenge,
	}, s.now().Add(codeTTL))
	if err != nil {
		return "", fmt.Errorf("failed to create authorization code: %w", err)
	}
	return req.redirect(url.Values{"code": {code}}), nil
}

// Deny returns the URL redirecting the user to the client with an error
// saying the user denied access.
func (req *AuthorizationRequest) Deny() string {
	return req.ErrorRedirect("access_denied", "the user denied access")
}

// tokenResponse is a successful token response (RFC 6749 section 5.1).
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid form")
		return
	}
	// Clients are public, so they identify themselves with a client_id
	// parameter. Accept HTTP basic authentication too, ignoring the secret,
	// for clients that send one regardless.
	clientID := r.PostForm.Get("client_id")
	if user, _, ok := r.BasicAuth(); ok {
		// RFC 6749 section 2.3.1: the credentials are form encoded.
		user, err := url.QueryUnescape(user)
		if err != nil || (clientID != "" && clientID != user) {
			writeError(w, http.StatusUnauthorized, "invalid_client", "invalid client authentication")
			return
		}
		clientID = user
	}
	if clientID == "" {
		writeError(w, http.StatusUnauthorized, "invalid_client", "client_id is required")
		return
	}
	if _, err := decodeClientID(clientID); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		return
	}
	if resource := r.PostForm.Get("resource"); resource != "" && !s.isResource(resource) {
		writeError(w, http.StatusBadRequest, "invalid_target", "unknown resource")
		return
	}
	var tokens *users.OAuthTokens
	var tokenErr *tokenError
	switch grantType := r.PostForm.Get("grant_type"); grantType {
	case "authorization_code":
		tokens, tokenErr = s.exchangeCode(r, clientID)
	case "refresh_token":
		tokens, tokenErr = s.refresh(r, clientID)
	default:
		tokenErr = &tokenError{"unsupported_grant_type", fmt.Sprintf("unsupported grant_type %q", grantType)}
	}
	if tokenErr != nil {
		status := http.StatusBadRequest
		if tokenErr.code == "server_error" {
			status = http.StatusInternalServerError
		}
		writeError(w, status, tokenErr.code, tokenErr.description)
		return
	}
	writeJSON(w, http.StatusOK, &tokenResponse{
		AccessToken:  tokens.AccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(tokens.AccessExpireTime.Sub(s.now()).Seconds()),
		RefreshToken: tokens.RefreshToken,
	})
}

// tokenError is an error response of the token endpoint.
type tokenError struct {
	code        string
	description string
}

var errTokenServer = &tokenError{"server_error", "failed to issue tokens"}

// exchangeCode exchanges an authorization code for tokens.
func (s *Server) exchangeCode(r *http.Request, clientID string) (*users.OAuthTokens, *tokenError) {
	code := r.PostForm.Get("code")
	verifier := r.PostForm.Get("code_verifier")
	if code == "" {
		return nil, &tokenError{"invalid_request", "code is required"}
	}
	if verifier == "" {
		return nil, &tokenError{"invalid_request", "code_verifier is required"}
	}
	// The code is used up even if the exchange fails below, so a stolen
	// code can't be retried. Using it again revokes the tokens issued for it.
	authz, err := s.repo.ConsumeOAuthAuthorizationCode(r.Context(), code)
	if err != nil {
		if errors.Is(err, users.ErrOAuthReplay) {
			s.log.WarnContext(r.Context(), "Revoked an OAuth grant after its authorization code was used again", slog.String("client_id", clientID))
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil, &tokenError{"invalid_grant", "invalid authorization code"}
		}
		s.report.Report(r.Context(), "Failed to consume OAuth authorization code", err)
		return nil, errTokenServer
	}
	if authz.ClientID != clientID {
		return nil, &tokenError{"invalid_grant", "the authorization code was issued to another client"}
	}
	if redirectURI := r.PostForm.Get("redirect_uri"); redirectURI != "" && redirectURI != authz.RedirectURI {
		return nil, &tokenError{"invalid_grant", "redirect_uri mismatch"}
	}
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	if subtle.ConstantTimeCompare([]byte(challenge), []byte(authz.CodeChallenge)) != 1 {
		return nil, &tokenError{"invalid_grant", "code_verifier does not match the code_challenge"}
	}
	tokens, err := s.repo.CreateOAuthGrant(r.Context(), authz, s.now().Add(accessTokenTTL), s.now().Add(refreshTokenTTL))
	if err != nil {
		if errors.Is(err, users.ErrOAuthReplay) {
			s.log.WarnContext(r.Context(), "Didn't create an OAuth grant, as its authorization code was used again", slog.String("client_id", clientID))
			return nil, &tokenError{"invalid_grant", "invalid authorization code"}
		}
		s.report.Report(r.Context(), "Failed to create OAuth grant", err)
		return nil, errTokenServer
	}
	return tokens, nil
}

// refresh issues new tokens with a refresh token.
func (s *Server) refresh(r *http.Request, clientID string) (*users.OAuthTokens, *tokenError) {
	refreshToken := r.PostForm.Get("refresh_token")
	if refreshToken == "" {
		return nil, &tokenError{"invalid_request", "refresh_token is required"}
	}
	tokens, err := s.repo.RefreshOAuthGrant(r.Context(), refreshToken, clientID, s.now().Add(accessTokenTTL), s.now().Add(refreshTokenTTL))
	if err != nil {
		if errors.Is(err, users.ErrOAuthReplay) {
			s.log.WarnContext(r.Context(), "Revoked an OAuth grant after a rotated refresh token was used again", slog.String("client_id", clientID))
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil, &tokenError{"invalid_grant", "invalid refresh token"}
		}
		s.report.Report(r.Context(), "Failed to refresh OAuth grant", err)
		return nil, errTokenServer
	}
	return tokens, nil
}

// writeError writes an OAuth error response (RFC 6749 section 5.2).
func writeError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]string{
		"error":             code,
		"error_description": description,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
