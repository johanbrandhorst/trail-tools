package services_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	oauthv1 "github.com/johanbrandhorst/trail-tools/internal/gen/oauth/v1"
	usersv1 "github.com/johanbrandhorst/trail-tools/internal/gen/users/v1"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// claudeRedirectURI is the redirect URI of claude.ai custom connectors.
const claudeRedirectURI = "https://claude.ai/api/mcp/auth_callback"

// tokenResult is a token endpoint response, either tokens or an error.
type tokenResult struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
}

// oauthClient is an OAuth client going through the authorization flow
// manually, to check every step.
type oauthClient struct {
	e        *env
	clientID string
}

// registerClient registers a client with the redirect URI.
func registerClient(t *testing.T, e *env, name, redirectURI string) *oauthClient {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"client_name":                name,
		"redirect_uris":              []string{redirectURI},
		"token_endpoint_auth_method": "client_secret_post",
	})
	resp, err := e.srv.Client().Post(e.srv.URL+"/oauth/register", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var reg oauthex.ClientRegistrationResponse
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated || reg.ClientID == "" {
		t.Fatalf("registration failed with status %d: %+v", resp.StatusCode, reg)
	}
	// Clients are public, whatever they asked for.
	if reg.TokenEndpointAuthMethod != "none" || reg.ClientSecret != "" {
		t.Errorf("got auth method %q and secret %q, want a public client", reg.TokenEndpointAuthMethod, reg.ClientSecret)
	}
	return &oauthClient{e: e, clientID: reg.ClientID}
}

// authorizeQuery returns the query of an authorization request with the
// PKCE verifier.
func (c *oauthClient) authorizeQuery(verifier, state string) string {
	hash := sha256.Sum256([]byte(verifier))
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {c.clientID},
		"redirect_uri":          {claudeRedirectURI},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(hash[:])},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"resource":              {c.e.srv.URL + "/mcp"},
		"scope":                 {"anything"},
	}.Encode()
}

// authorize goes through the authorization endpoint and approves the client
// as the user, returning the authorization code.
func (c *oauthClient) authorize(t *testing.T, verifier string) string {
	t.Helper()
	ctx := context.Background()
	query := c.authorizeQuery(verifier, "some-state")
	resp := getNoRedirect(t, c.e.srv.URL+"/oauth/authorize?"+query)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != consentPage {
		t.Fatalf("got status %d and body %q, want the consent page", resp.StatusCode, body)
	}
	approved, err := c.e.oauth.ApproveAuthorization(ctx, oauthv1.ApproveAuthorizationRequest_builder{Query: query}.Build())
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(approved.GetRedirectUri())
	if err != nil {
		t.Fatal(err)
	}
	q := redirect.Query()
	if got := redirect.Scheme + "://" + redirect.Host + redirect.Path; got != claudeRedirectURI {
		t.Errorf("got redirect to %q, want %q", got, claudeRedirectURI)
	}
	if q.Get("state") != "some-state" || q.Get("iss") != c.e.srv.URL || q.Get("code") == "" {
		t.Fatalf("got redirect query %v, want the state, issuer and a code", q)
	}
	return q.Get("code")
}

// token calls the token endpoint.
func (c *oauthClient) token(t *testing.T, params url.Values) tokenResult {
	t.Helper()
	resp, err := c.e.srv.Client().PostForm(c.e.srv.URL+"/oauth/token", params)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res tokenResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if (resp.StatusCode == http.StatusOK) != (res.Error == "") {
		t.Fatalf("got status %d with error %q", resp.StatusCode, res.Error)
	}
	return res
}

func (c *oauthClient) exchange(t *testing.T, code, verifier string) tokenResult {
	t.Helper()
	return c.token(t, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {c.clientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {claudeRedirectURI},
		"resource":      {c.e.srv.URL + "/mcp"},
	})
}

func (c *oauthClient) refresh(t *testing.T, refreshToken string) tokenResult {
	t.Helper()
	return c.token(t, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.clientID},
		"refresh_token": {refreshToken},
	})
}

func getNoRedirect(t *testing.T, u string) *http.Response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// mcpStatus calls the MCP server with the access token, returning the
// status code and the WWW-Authenticate header.
func mcpStatus(t *testing.T, e *env, accessToken string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("WWW-Authenticate")
}

func TestOAuthMetadata(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	// Unauthenticated MCP requests point to the protected resource metadata.
	status, challenge := mcpStatus(t, e, "")
	wantChallenge := `Bearer resource_metadata="` + e.srv.URL + `/.well-known/oauth-protected-resource/mcp"`
	if status != http.StatusUnauthorized || challenge != wantChallenge {
		t.Errorf("got status %d and challenge %q, want 401 and %q", status, challenge, wantChallenge)
	}
	status, challenge = mcpStatus(t, e, "tt_invalid")
	if status != http.StatusUnauthorized || !strings.Contains(challenge, `error="invalid_token"`) || !strings.Contains(challenge, "resource_metadata=") {
		t.Errorf("got status %d and challenge %q, want 401 with an invalid_token error and resource metadata", status, challenge)
	}

	var prm oauthex.ProtectedResourceMetadata
	getJSON(t, e.srv.URL+"/.well-known/oauth-protected-resource/mcp", &prm)
	if prm.Resource != e.srv.URL+"/mcp" || !slices.Equal(prm.AuthorizationServers, []string{e.srv.URL}) {
		t.Errorf("got protected resource metadata %+v", prm)
	}

	// The SDK checks the metadata the same way MCP clients do.
	asm, err := auth.GetAuthServerMetadata(context.Background(), e.srv.URL, e.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if asm.Issuer != e.srv.URL ||
		asm.AuthorizationEndpoint != e.srv.URL+"/oauth/authorize" ||
		asm.TokenEndpoint != e.srv.URL+"/oauth/token" ||
		asm.RegistrationEndpoint != e.srv.URL+"/oauth/register" ||
		!slices.Equal(asm.CodeChallengeMethodsSupported, []string{"S256"}) ||
		!slices.Contains(asm.GrantTypesSupported, "refresh_token") {
		t.Errorf("got authorization server metadata %+v", asm)
	}
}

func getJSON(t *testing.T, u string, v any) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", u, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthFlow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	c := registerClient(t, e, "Claude", claudeRedirectURI)
	const verifier = "a-verifier-that-is-long-enough-to-be-a-real-pkce-verifier"

	// The consent page shows the client.
	query := c.authorizeQuery(verifier, "state")
	got, err := e.oauth.GetAuthorization(ctx, oauthv1.GetAuthorizationRequest_builder{Query: query}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if got.GetClientName() != "Claude" || got.GetRedirectUri() != claudeRedirectURI {
		t.Errorf("got authorization %v", got)
	}
	// Only signed in users can approve clients.
	_, err = e.anonOAuth.ApproveAuthorization(ctx, oauthv1.ApproveAuthorizationRequest_builder{Query: query}.Build())
	wantCode(t, err, connect.CodeUnauthenticated)

	// A code can't be exchanged with the wrong verifier, and is gone after
	// a failed attempt.
	code := c.authorize(t, verifier)
	if res := c.exchange(t, code, "the-wrong-verifier"); res.Error != "invalid_grant" {
		t.Errorf("got %+v with the wrong verifier, want invalid_grant", res)
	}
	if res := c.exchange(t, code, verifier); res.Error != "invalid_grant" {
		t.Errorf("got %+v after a failed exchange, want invalid_grant", res)
	}
	// Codes are bound to the client.
	other := registerClient(t, e, "Other", claudeRedirectURI+"/other")
	code = c.authorize(t, verifier)
	if res := other.exchange(t, code, verifier); res.Error != "invalid_grant" {
		t.Errorf("got %+v for another client, want invalid_grant", res)
	}
	// Expired codes can't be exchanged.
	code = c.authorize(t, verifier)
	if _, err := e.db.Exec(ctx, "update oauth_authorization_codes set expire_time = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if res := c.exchange(t, code, verifier); res.Error != "invalid_grant" {
		t.Errorf("got %+v for an expired code, want invalid_grant", res)
	}

	// A code is exchanged for tokens. Exchanging it again is tested in
	// TestOAuthReplay.
	code = c.authorize(t, verifier)
	tokens := c.exchange(t, code, verifier)
	if tokens.Error != "" || tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.TokenType != "Bearer" || tokens.ExpiresIn <= 0 {
		t.Fatalf("got tokens %+v", tokens)
	}

	// The access token works with the MCP server.
	if status, _ := mcpStatus(t, e, tokens.AccessToken); status != http.StatusOK {
		t.Errorf("got status %d with the access token, want 200", status)
	}

	// Refreshing rotates the refresh token, and revokes the previous access
	// token. Refresh tokens are bound to the client. Reusing a refresh token
	// is tested in TestOAuthReplay.
	if res := other.refresh(t, tokens.RefreshToken); res.Error != "invalid_grant" {
		t.Errorf("got %+v refreshing as another client, want invalid_grant", res)
	}
	refreshed := c.refresh(t, tokens.RefreshToken)
	if refreshed.Error != "" || refreshed.AccessToken == tokens.AccessToken || refreshed.RefreshToken == tokens.RefreshToken {
		t.Fatalf("got refreshed tokens %+v", refreshed)
	}
	if status, _ := mcpStatus(t, e, refreshed.AccessToken); status != http.StatusOK {
		t.Errorf("got status %d with the refreshed access token, want 200", status)
	}
	if status, _ := mcpStatus(t, e, tokens.AccessToken); status != http.StatusUnauthorized {
		t.Errorf("got status %d with the access token before refreshing, want 401", status)
	}

	// Expired access tokens are rejected, and can be refreshed.
	if _, err := e.db.Exec(ctx, "update api_tokens set expire_time = now() - interval '1 second' where oauth_grant_id is not null"); err != nil {
		t.Fatal(err)
	}
	if status, challenge := mcpStatus(t, e, refreshed.AccessToken); status != http.StatusUnauthorized || !strings.Contains(challenge, "invalid_token") {
		t.Errorf("got status %d and challenge %q with an expired token, want 401 with invalid_token", status, challenge)
	}
	refreshed = c.refresh(t, refreshed.RefreshToken)
	if status, _ := mcpStatus(t, e, refreshed.AccessToken); status != http.StatusOK {
		t.Errorf("got status %d after refreshing an expired token, want 200", status)
	}

	// The client is listed as a connection, and not as an API token.
	connections, err := e.oauth.ListConnections(ctx, oauthv1.ListConnectionsRequest_builder{}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if len(connections.GetConnections()) != 1 {
		t.Fatalf("got connections %v, want 1", connections.GetConnections())
	}
	connection := connections.GetConnections()[0]
	if connection.GetClientName() != "Claude" || connection.GetRedirectUri() != claudeRedirectURI || !connection.HasLastUsedTime() {
		t.Errorf("got connection %v", connection)
	}
	apiTokens, err := e.users.ListAPITokens(ctx, usersv1.ListAPITokensRequest_builder{}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if len(apiTokens.GetTokens()) != 1 || apiTokens.GetTokens()[0].GetName() != "test" {
		t.Errorf("got API tokens %v, want only the test token", apiTokens.GetTokens())
	}

	// Other users can't see or delete the connection.
	_, err = e.anonOAuth.DeleteConnection(ctx, oauthv1.DeleteConnectionRequest_builder{Id: connection.GetId()}.Build())
	wantCode(t, err, connect.CodeUnauthenticated)

	// Deleting the connection revokes its tokens.
	_, err = e.oauth.DeleteConnection(ctx, oauthv1.DeleteConnectionRequest_builder{Id: connection.GetId()}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := mcpStatus(t, e, refreshed.AccessToken); status != http.StatusUnauthorized {
		t.Errorf("got status %d with a revoked access token, want 401", status)
	}
	if res := c.refresh(t, refreshed.RefreshToken); res.Error != "invalid_grant" {
		t.Errorf("got %+v with a revoked refresh token, want invalid_grant", res)
	}
	connections, err = e.oauth.ListConnections(ctx, oauthv1.ListConnectionsRequest_builder{}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if len(connections.GetConnections()) != 0 {
		t.Errorf("got connections %v after deleting, want none", connections.GetConnections())
	}
	_, err = e.oauth.DeleteConnection(ctx, oauthv1.DeleteConnectionRequest_builder{Id: connection.GetId()}.Build())
	wantCode(t, err, connect.CodeNotFound)
}

// TestOAuthReplay checks that using an authorization code or refresh token
// again, which means it was stolen, revokes the tokens issued for it: either
// the thief or the client has them, and the server can't tell which.
func TestOAuthReplay(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	c := registerClient(t, e, "Claude", claudeRedirectURI)
	const verifier = "a-verifier-that-is-long-enough-to-be-a-real-pkce-verifier"

	// Exchanging a code again fails, and revokes the tokens issued for it.
	code := c.authorize(t, verifier)
	tokens := c.exchange(t, code, verifier)
	if tokens.Error != "" {
		t.Fatalf("got %+v exchanging a code", tokens)
	}
	if res := c.exchange(t, code, verifier); res.Error != "invalid_grant" {
		t.Errorf("got %+v when reusing a code, want invalid_grant", res)
	}
	if status, _ := mcpStatus(t, e, tokens.AccessToken); status != http.StatusUnauthorized {
		t.Errorf("got status %d with the access token of a reused code, want 401", status)
	}
	if res := c.refresh(t, tokens.RefreshToken); res.Error != "invalid_grant" {
		t.Errorf("got %+v with the refresh token of a reused code, want invalid_grant", res)
	}
	// Using it a third time does no harm.
	if res := c.exchange(t, code, verifier); res.Error != "invalid_grant" {
		t.Errorf("got %+v when reusing a code twice, want invalid_grant", res)
	}

	// The client authorizes again, and refreshes twice.
	code = c.authorize(t, verifier)
	first := c.exchange(t, code, verifier)
	second := c.refresh(t, first.RefreshToken)
	third := c.refresh(t, second.RefreshToken)
	if first.Error != "" || second.Error != "" || third.Error != "" {
		t.Fatalf("got tokens %+v, %+v and %+v", first, second, third)
	}
	if status, _ := mcpStatus(t, e, third.AccessToken); status != http.StatusOK {
		t.Errorf("got status %d with the latest access token, want 200", status)
	}
	// Using any rotated-out refresh token, not only the last, revokes the
	// grant.
	if res := c.refresh(t, first.RefreshToken); res.Error != "invalid_grant" {
		t.Errorf("got %+v when reusing a refresh token, want invalid_grant", res)
	}
	if status, _ := mcpStatus(t, e, third.AccessToken); status != http.StatusUnauthorized {
		t.Errorf("got status %d with the latest access token after a refresh token was reused, want 401", status)
	}
	if res := c.refresh(t, third.RefreshToken); res.Error != "invalid_grant" {
		t.Errorf("got %+v with the latest refresh token after a refresh token was reused, want invalid_grant", res)
	}
	connections, err := e.oauth.ListConnections(ctx, oauthv1.ListConnectionsRequest_builder{}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if len(connections.GetConnections()) != 0 {
		t.Errorf("got connections %v after replays, want none", connections.GetConnections())
	}
}

func TestOAuthDeny(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	c := registerClient(t, e, "Claude", claudeRedirectURI)
	denied, err := e.oauth.DenyAuthorization(context.Background(), oauthv1.DenyAuthorizationRequest_builder{
		Query: c.authorizeQuery("verifier", "the-state"),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(denied.GetRedirectUri())
	if err != nil {
		t.Fatal(err)
	}
	if q := redirect.Query(); q.Get("error") != "access_denied" || q.Get("state") != "the-state" || q.Get("code") != "" {
		t.Errorf("got redirect %q, want an access_denied error", redirect)
	}
	// Invalid requests are rejected.
	_, err = e.oauth.GetAuthorization(context.Background(), oauthv1.GetAuthorizationRequest_builder{
		Query: "client_id=unknown",
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
}

// TestOAuthMCPClient connects with the MCP SDK's OAuth client, which
// discovers the authorization server, registers and authorizes like other MCP
// clients.
func TestOAuthMCPClient(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				ClientName:   "SDK client",
				RedirectURIs: []string{"http://localhost:12345/callback"},
			},
		},
		RedirectURL: "http://localhost:12345/callback",
		// Act as the browser: open the authorization URL, and approve the
		// client on the consent page as the signed in user.
		AuthorizationCodeFetcher: func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			authURL, err := url.Parse(args.URL)
			if err != nil {
				return nil, err
			}
			resp := getNoRedirect(t, args.URL)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("got status %d from the authorization endpoint", resp.StatusCode)
			}
			approved, err := e.oauth.ApproveAuthorization(ctx, oauthv1.ApproveAuthorizationRequest_builder{
				Query: authURL.RawQuery,
			}.Build())
			if err != nil {
				return nil, err
			}
			redirect, err := url.Parse(approved.GetRedirectUri())
			if err != nil {
				return nil, err
			}
			q := redirect.Query()
			return &auth.AuthorizationResult{Code: q.Get("code"), State: q.Get("state"), Iss: q.Get("iss")}, nil
		},
		RequestRefreshToken: true,
		Client:              e.srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:     e.srv.URL + "/mcp",
		HTTPClient:   e.srv.Client(),
		OAuthHandler: handler,
	}, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) == 0 {
		t.Error("got no tools")
	}
	connections, err := e.oauth.ListConnections(ctx, oauthv1.ListConnectionsRequest_builder{}.Build())
	if err != nil {
		t.Fatal(err)
	}
	if len(connections.GetConnections()) != 1 || connections.GetConnections()[0].GetClientName() != "SDK client" {
		t.Errorf("got connections %v, want the SDK client", connections.GetConnections())
	}
}
