package oauth

import (
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/errreport"
)

func TestValidateRedirectURI(t *testing.T) {
	t.Parallel()
	for _, uri := range []string{
		"http://localhost:33418/callback",
		"http://127.0.0.1/callback",
		"http://[::1]:8080/callback",
		"https://claude.ai/api/mcp/auth_callback",
		"https://claude.com/api/mcp/auth_callback",
		"https://chatgpt.com/connector_platform_oauth_redirect",
		"https://vscode.dev/redirect",
		"cursor://anysphere.cursor-mcp/oauth/callback",
		"https://oauth-redirect.googleusercontent.com/r/user_bound_custom-mcp-12345-trailrunn.ing",
		"https://oauth-redirect-sandbox.googleusercontent.com/a/user_bound_custom-mcp-12345-trailrunn_ing",
		"https://oauth-redirect-test.googleusercontent.com/r/user_bound_custom-mcp-12345-trailrunn_ing",
	} {
		if err := validateRedirectURI(uri); err != nil {
			t.Errorf("validateRedirectURI(%q) = %v, want nil", uri, err)
		}
	}
	for _, uri := range []string{
		"",
		"https://evil.example/callback",
		"https://claude.ai.evil.example/callback",
		"https://evil.example/https://claude.ai/",
		"http://evil.example/callback",
		"http://claude.ai/api/mcp/auth_callback",
		"https://claude.ai/callback#fragment",
		"https://user@claude.ai/callback",
		"javascript:alert(1)",
		"data:text/html,hi",
		"ftp://localhost/callback",
		"/relative/callback",
		// Google's relay forwards to any Cloud project, so only Gemini's
		// connectors are allowed.
		"https://oauth-redirect.googleusercontent.com/r/evil-project",
		"https://oauth-redirect-sandbox.googleusercontent.com/r/evil-project",
		"https://oauth-redirect.googleusercontent.com/b/user_bound_custom-mcp-1",
		"https://oauth-redirect-evil.googleusercontent.com/r/user_bound_custom-mcp-1",
		"https://oauth-redirect.googleusercontent.com/",
		"https://oauth-redirect.googleusercontent.com/r/user_bound_custom-mcp-1/../../r/evil-project",
		"https://oauth-redirect.googleusercontent.com/r/user_bound_custom-mcp-1%2F..%2Fevil-project",
		"http://oauth-redirect.googleusercontent.com/r/user_bound_custom-mcp-1",
	} {
		if err := validateRedirectURI(uri); err == nil {
			t.Errorf("validateRedirectURI(%q) = nil, want an error", uri)
		}
	}
}

func TestMatchRedirectURI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		registered, requested string
		want                  bool
	}{
		{"https://claude.ai/api/mcp/auth_callback", "https://claude.ai/api/mcp/auth_callback", true},
		{"https://claude.ai/api/mcp/auth_callback", "https://claude.ai/api/mcp/other", false},
		// Native clients listen on an ephemeral port.
		{"http://127.0.0.1/callback", "http://127.0.0.1:51234/callback", true},
		{"http://localhost:1234/callback", "http://localhost:4321/callback", true},
		{"http://localhost/callback", "http://127.0.0.1/callback", false},
		{"http://localhost/callback", "http://localhost/other", false},
		{"https://localhost/callback", "https://localhost:1234/callback", false},
	} {
		if got := matchRedirectURI(tc.registered, tc.requested); got != tc.want {
			t.Errorf("matchRedirectURI(%q, %q) = %v, want %v", tc.registered, tc.requested, got, tc.want)
		}
	}
}

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(log, errreport.NewLocal(log), nil, "http://"+ts.Listener.Addr().String(), "/mcp")
	mux := http.NewServeMux()
	s.RegisterHandlers(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "consent")
	}))
	ts.Config.Handler = mux
	ts.Start()
	t.Cleanup(ts.Close)
	return s, ts
}

func register(t *testing.T, ts *httptest.Server, body string) (int, map[string]any) {
	t.Helper()
	resp, err := ts.Client().Post(ts.URL+RegisterPath, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, res
}

func TestRegister(t *testing.T) {
	t.Parallel()
	_, ts := newTestServer(t)

	status, res := register(t, ts, `{"client_name":"Claude","redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"token_endpoint_auth_method":"client_secret_basic"}`)
	if status != http.StatusCreated {
		t.Fatalf("got status %d: %v", status, res)
	}
	c, err := decodeClientID(res["client_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Claude" || len(c.RedirectURIs) != 1 {
		t.Errorf("got client %+v", c)
	}
	if res["token_endpoint_auth_method"] != "none" || res["client_secret"] != nil {
		t.Errorf("got registration %v, want a public client", res)
	}

	// Gemini registers its redirects on all of Google's relay hosts.
	var gemini []string
	for _, host := range []string{"oauth-redirect-sandbox", "oauth-redirect-test", "oauth-redirect"} {
		for _, path := range []string{"r", "a"} {
			gemini = append(gemini, `"https://`+host+`.googleusercontent.com/`+path+`/user_bound_custom-mcp-116425298551065020144-trailrunn_ing"`)
		}
	}
	status, res = register(t, ts, `{"client_name":"Google","redirect_uris":[`+strings.Join(gemini, ",")+`],"grant_types":["authorization_code","refresh_token"],"response_types":["code"]}`)
	if status != http.StatusCreated {
		t.Fatalf("registering Gemini: got status %d: %v", status, res)
	}

	// Clients asking for more than is supported get what is.
	status, res = register(t, ts, `{"client_name":"Gemini","redirect_uris":["https://oauth-redirect.googleusercontent.com/r/user_bound_custom-mcp-1-trailrunn.ing"],"grant_types":["authorization_code","refresh_token","client_credentials"],"response_types":["code","token"],"scope":"offline_access"}`)
	if status != http.StatusCreated {
		t.Fatalf("got status %d: %v", status, res)
	}
	if grants, _ := json.Marshal(res["grant_types"]); string(grants) != `["authorization_code","refresh_token"]` {
		t.Errorf("got grant types %s", grants)
	}
	if responses, _ := json.Marshal(res["response_types"]); string(responses) != `["code"]` {
		t.Errorf("got response types %s", responses)
	}

	for _, tc := range []struct {
		body string
		want string
	}{
		{`{"client_name":"Evil","redirect_uris":["https://evil.example/callback"]}`, "invalid_redirect_uri"},
		{`{"client_name":"Evil","redirect_uris":["https://claude.ai/ok","http://evil.example/callback"]}`, "invalid_redirect_uri"},
		{`{"client_name":"Evil","redirect_uris":["javascript:alert(1)"]}`, "invalid_redirect_uri"},
		{`{"client_name":"None","redirect_uris":[]}`, "invalid_client_metadata"},
		{`{"client_name":"Implicit","redirect_uris":["http://localhost/cb"],"response_types":["token"]}`, "invalid_client_metadata"},
		{`{"client_name":"Credentials","redirect_uris":["http://localhost/cb"],"grant_types":["client_credentials"]}`, "invalid_client_metadata"},
		{`{"client_name":"` + strings.Repeat("a", 101) + `","redirect_uris":["http://localhost/cb"]}`, "invalid_client_metadata"},
		{`not json`, "invalid_client_metadata"},
	} {
		status, res := register(t, ts, tc.body)
		if status != http.StatusBadRequest || res["error"] != tc.want {
			t.Errorf("register(%s) = %d %v, want 400 %s", tc.body, status, res, tc.want)
		}
	}
}

func TestAuthorizeErrors(t *testing.T) {
	t.Parallel()
	s, ts := newTestServer(t)
	clientID, err := encodeClientID(&client{Name: "Claude", RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"}})
	if err != nil {
		t.Fatal(err)
	}
	valid := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"code_challenge":        {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
		"code_challenge_method": {"S256"},
		"state":                 {"xyz"},
		"resource":              {ts.URL + "/mcp"},
	}
	with := func(key, value string) url.Values {
		q := maps.Clone(valid)
		if value == "" {
			q.Del(key)
		} else {
			q.Set(key, value)
		}
		return q
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	get := func(q url.Values) *http.Response {
		t.Helper()
		resp, err := noRedirect.Get(ts.URL + AuthorizePath + "?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	if resp := get(valid); resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d for a valid request, want the consent page", resp.StatusCode)
	}
	// A single registered redirect URI may be omitted.
	if resp := get(with("redirect_uri", "")); resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d without a redirect URI, want the consent page", resp.StatusCode)
	}

	// Errors with the client or redirect URI are shown to the user, as the
	// redirect URI can't be trusted.
	for _, q := range []url.Values{
		with("client_id", ""),
		with("client_id", "ttcl_garbage"),
		with("client_id", "someone-elses-client"),
		with("redirect_uri", "https://claude.ai/api/mcp/other"),
		with("redirect_uri", "https://evil.example/callback"),
	} {
		if resp := get(q); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("got status %d for %v, want 400", resp.StatusCode, q)
		}
	}
	// A client ID can't be forged with a redirect URI that isn't allowed.
	forged, err := encodeClientID(&client{Name: "Evil", RedirectURIs: []string{"https://evil.example/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp := get(with("client_id", forged)); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("got status %d for a forged client ID, want 400", resp.StatusCode)
	}

	// Other errors are returned to the client.
	for _, tc := range []struct {
		query url.Values
		want  string
	}{
		{with("response_type", "token"), "unsupported_response_type"},
		{with("code_challenge", ""), "invalid_request"},
		{with("code_challenge", "too-short"), "invalid_request"},
		{with("code_challenge_method", "plain"), "invalid_request"},
		{with("code_challenge_method", ""), "invalid_request"},
		{with("resource", "https://other.example/mcp"), "invalid_target"},
	} {
		resp := get(tc.query)
		location, err := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode != http.StatusFound || err != nil {
			t.Errorf("got status %d for %v, want a redirect", resp.StatusCode, tc.query)
			continue
		}
		q := location.Query()
		if !strings.HasPrefix(location.String(), "https://claude.ai/api/mcp/auth_callback?") ||
			q.Get("error") != tc.want || q.Get("state") != "xyz" || q.Get("iss") != s.issuer {
			t.Errorf("got redirect %q for %v, want error %s", location, tc.query, tc.want)
		}
	}
}

func TestResourceMetadataURL(t *testing.T) {
	t.Parallel()
	s := NewServer(nil, nil, nil, "https://trail.example/", "/mcp")
	if got, want := s.ResourceMetadataURL(), "https://trail.example/.well-known/oauth-protected-resource/mcp"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
