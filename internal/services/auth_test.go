package services_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	authv1 "github.com/johanbrandhorst/trail-tools/internal/gen/auth/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/auth/v1/authv1connect"
	oauthv1 "github.com/johanbrandhorst/trail-tools/internal/gen/oauth/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/oauth/v1/oauthv1connect"
	usersv1 "github.com/johanbrandhorst/trail-tools/internal/gen/users/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/users/v1/usersv1connect"
)

// browser is a client that keeps the session cookie, like a browser.
type browser struct {
	auth  authv1connect.AuthServiceClient
	users usersv1connect.UserServiceClient
}

// cookieTransport sends the session cookie it was last given, like a
// browser. A cookie jar won't, as the cookie is secure and the test server
// isn't.
type cookieTransport struct {
	next   http.RoundTripper
	cookie string
}

func (t *cookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.cookie != "" {
		req = req.Clone(req.Context())
		req.Header.Set("Cookie", t.cookie)
	}
	resp, err := t.next.RoundTrip(req)
	if err == nil {
		for _, c := range resp.Cookies() {
			if c.Name == "session" {
				t.cookie = c.Name + "=" + c.Value
			}
		}
	}
	return resp, err
}

func (e *env) newBrowser() *browser {
	return e.browserWithCookie("")
}

// signedInBrowser is a browser signed in as the test user.
func (e *env) signedInBrowser() *browser {
	return e.browserWithCookie("session=" + e.session)
}

func (e *env) browserWithCookie(cookie string) *browser {
	httpClient := &http.Client{Transport: &cookieTransport{next: e.srv.Client().Transport, cookie: cookie}}
	client := connect.NewClient(connecthttp.NewTransport(httpClient, e.srv.URL))
	return &browser{
		auth:  authv1connect.NewAuthServiceClient(client),
		users: usersv1connect.NewUserServiceClient(client),
	}
}

// register registers a passkey with the authenticator and returns its
// credential ID.
func (b *browser) register(t *testing.T, a *authenticator, token, name string) ([]byte, error) {
	t.Helper()
	ctx := context.Background()
	begin, err := b.auth.BeginPasskeyRegistration(ctx, authv1.BeginPasskeyRegistrationRequest_builder{InvitationToken: token}.Build())
	if err != nil {
		return nil, err
	}
	if begin.GetRpId() != "localhost" {
		t.Errorf("unexpected relying party %q", begin.GetRpId())
	}
	id, clientData, attestation := a.create(begin.GetChallenge(), begin.GetUserHandle())
	_, err = b.auth.FinishPasskeyRegistration(ctx, authv1.FinishPasskeyRegistrationRequest_builder{
		ChallengeId:       begin.GetChallengeId(),
		UserName:          name,
		ClientDataJson:    clientData,
		AttestationObject: attestation,
		Transports:        []string{"internal"},
	}.Build())
	return id, err
}

// login signs in with a passkey.
func (b *browser) login(t *testing.T, a *authenticator, credentialID []byte) error {
	t.Helper()
	ctx := context.Background()
	begin, err := b.auth.BeginPasskeyLogin(ctx, authv1.BeginPasskeyLoginRequest_builder{}.Build())
	if err != nil {
		return err
	}
	clientData, authData, sig := a.get(credentialID, begin.GetChallenge())
	_, err = b.auth.FinishPasskeyLogin(ctx, authv1.FinishPasskeyLoginRequest_builder{
		ChallengeId:       begin.GetChallengeId(),
		CredentialId:      credentialID,
		ClientDataJson:    clientData,
		AuthenticatorData: authData,
		Signature:         sig,
		UserHandle:        a.keys[string(credentialID)].userHandle,
	}.Build())
	return err
}

func (b *browser) currentUser(t *testing.T) *usersv1.User {
	t.Helper()
	resp, err := b.users.GetCurrentUser(context.Background(), usersv1.GetCurrentUserRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("GetCurrentUser: %v", err)
	}
	return resp.GetUser()
}

func TestPasskeysAndInvitations(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	laptop := newAuthenticator(t, "http://localhost:8080", "localhost")

	// Registration requires an invitation.
	anon := e.newBrowser()
	_, err := anon.register(t, laptop, "", "Stranger")
	wantCode(t, err, connect.CodeUnauthenticated)
	_, err = anon.register(t, laptop, "made-up-token", "Stranger")
	wantCode(t, err, connect.CodeFailedPrecondition)

	// The bootstrap token creates the first administrator.
	inv, err := anon.auth.GetInvitation(ctx, authv1.GetInvitationRequest_builder{Token: bootstrapToken}.Build())
	if err != nil || !inv.GetInvitation().GetBootstrap() {
		t.Fatalf("GetInvitation for bootstrap: %v %v", inv, err)
	}
	admin := e.newBrowser()
	adminKey, err := admin.register(t, laptop, bootstrapToken, "Johan")
	if err != nil {
		t.Fatalf("bootstrap registration: %v", err)
	}
	if u := admin.currentUser(t); u.GetName() != "Johan" || !u.GetAdmin() {
		t.Errorf("unexpected admin %v", u)
	}
	// The bootstrap token only works once.
	_, err = anon.auth.GetInvitation(ctx, authv1.GetInvitationRequest_builder{Token: bootstrapToken}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)

	// The admin invites a new user, who registers on their phone.
	created, err := admin.auth.CreateInvitation(ctx, authv1.CreateInvitationRequest_builder{
		Kind: authv1.InvitationKind_INVITATION_KIND_NEW_USER,
		Note: "For crew",
	}.Build())
	if err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}
	token, ok := strings.CutPrefix(created.GetUrl(), "http://localhost:8080/invite/")
	if !ok {
		t.Fatalf("unexpected invitation URL %q", created.GetUrl())
	}
	got, err := anon.auth.GetInvitation(ctx, authv1.GetInvitationRequest_builder{Token: token}.Build())
	if err != nil || got.GetInvitation().GetCreatedByName() != "Johan" || got.GetInvitation().GetNote() != "For crew" {
		t.Fatalf("GetInvitation: %v %v", got, err)
	}
	phone := newAuthenticator(t, "http://localhost:8080", "localhost")
	crew := e.newBrowser()
	crewKey, err := crew.register(t, phone, token, "Crew")
	if err != nil {
		t.Fatalf("registration with invitation: %v", err)
	}
	if u := crew.currentUser(t); u.GetName() != "Crew" || u.GetAdmin() {
		t.Errorf("unexpected crew user %v", u)
	}
	// The invitation can't be used again.
	_, err = e.newBrowser().register(t, newAuthenticator(t, "http://localhost:8080", "localhost"), token, "Someone else")
	wantCode(t, err, connect.CodeFailedPrecondition)

	// Only admins can invite new users, but anyone can add a passkey on
	// another device.
	_, err = crew.auth.CreateInvitation(ctx, authv1.CreateInvitationRequest_builder{
		Kind: authv1.InvitationKind_INVITATION_KIND_NEW_USER,
	}.Build())
	wantCode(t, err, connect.CodePermissionDenied)
	addDevice, err := crew.auth.CreateInvitation(ctx, authv1.CreateInvitationRequest_builder{
		Kind: authv1.InvitationKind_INVITATION_KIND_ADD_PASSKEY,
	}.Build())
	if err != nil {
		t.Fatalf("CreateInvitation for a device: %v", err)
	}
	tablet := newAuthenticator(t, "http://localhost:8080", "localhost")
	tabletBrowser := e.newBrowser()
	addToken := strings.TrimPrefix(addDevice.GetUrl(), "http://localhost:8080/invite/")
	if _, err := tabletBrowser.register(t, tablet, addToken, ""); err != nil {
		t.Fatalf("adding a device: %v", err)
	}
	if u := tabletBrowser.currentUser(t); u.GetName() != "Crew" {
		t.Errorf("the device was added to the wrong user: %v", u)
	}
	passkeys, err := crew.auth.ListPasskeys(ctx, authv1.ListPasskeysRequest_builder{}.Build())
	if err != nil || len(passkeys.GetPasskeys()) != 2 {
		t.Fatalf("ListPasskeys: %v %v", passkeys, err)
	}

	// Sign in on a new browser with a registered passkey.
	fresh := e.newBrowser()
	if err := fresh.login(t, phone, crewKey); err != nil {
		t.Fatalf("login: %v", err)
	}
	if u := fresh.currentUser(t); u.GetName() != "Crew" {
		t.Errorf("signed in as the wrong user: %v", u)
	}
	if err := e.newBrowser().login(t, laptop, adminKey); err != nil {
		t.Fatalf("admin login: %v", err)
	}

	// A replayed signature counter is rejected, as the passkey may have
	// been cloned.
	laptop.keys[string(adminKey)].counter = 0
	err = e.newBrowser().login(t, laptop, adminKey)
	wantCode(t, err, connect.CodeUnauthenticated)

	// Passkeys must verify the user.
	unverified := newAuthenticator(t, "http://localhost:8080", "localhost")
	unverified.userVerified = false
	_, err = crew.register(t, unverified, "", "")
	wantCode(t, err, connect.CodeInvalidArgument)

	// The last passkey of an account can't be deleted.
	if _, err := admin.auth.DeletePasskey(ctx, authv1.DeletePasskeyRequest_builder{
		Id: passkeysFor(t, admin)[0].GetId(),
	}.Build()); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("deleting the last passkey: got %v", err)
	}
}

func passkeysFor(t *testing.T, b *browser) []*authv1.Passkey {
	t.Helper()
	resp, err := b.auth.ListPasskeys(context.Background(), authv1.ListPasskeysRequest_builder{}.Build())
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetPasskeys()
}

// TestCredentialsRequireSession checks that API tokens and OAuth access
// tokens, which API clients and AI agents hold, can't manage the account's
// credentials. Otherwise a leaked token could add a passkey and sign in as
// the user, or create more tokens.
func TestCredentialsRequireSession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// An OAuth client connects, and the user invites another device.
	c := registerClient(t, e, "Claude", claudeRedirectURI)
	const verifier = "a-verifier-that-is-long-enough-to-be-a-real-pkce-verifier"
	oauthTokens := c.exchange(t, c.authorize(t, verifier), verifier)
	if oauthTokens.Error != "" {
		t.Fatalf("got tokens %+v", oauthTokens)
	}
	signedIn := e.signedInBrowser()
	invitation, err := signedIn.auth.CreateInvitation(ctx, authv1.CreateInvitationRequest_builder{
		Kind: authv1.InvitationKind_INVITATION_KIND_ADD_PASSKEY,
	}.Build())
	if err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}
	apiTokens, err := e.users.ListAPITokens(ctx, usersv1.ListAPITokensRequest_builder{}.Build())
	if err != nil || len(apiTokens.GetTokens()) != 1 {
		t.Fatalf("ListAPITokens: %v %v", apiTokens, err)
	}
	connections, err := e.oauth.ListConnections(ctx, oauthv1.ListConnectionsRequest_builder{}.Build())
	if err != nil || len(connections.GetConnections()) != 1 {
		t.Fatalf("ListConnections: %v %v", connections, err)
	}

	for name, token := range map[string]string{"an API token": e.token, "an OAuth access token": oauthTokens.AccessToken} {
		client := connect.NewClient(connecthttp.NewTransport(e.srv.Client(), e.srv.URL), bearerInterceptor(token))
		authClient := authv1connect.NewAuthServiceClient(client)
		usersClient := usersv1connect.NewUserServiceClient(client)
		oauthClient := oauthv1connect.NewOAuthServiceClient(client)

		// The token works for the rest of the API.
		if _, err := usersClient.GetCurrentUser(ctx, usersv1.GetCurrentUserRequest_builder{}.Build()); err != nil {
			t.Fatalf("GetCurrentUser with %s: %v", name, err)
		}
		// It can't manage passkeys, invitations, API tokens or OAuth
		// connections.
		calls := map[string]func() error{
			"BeginPasskeyRegistration": func() error {
				_, err := authClient.BeginPasskeyRegistration(ctx, authv1.BeginPasskeyRegistrationRequest_builder{}.Build())
				return err
			},
			"ListPasskeys": func() error {
				_, err := authClient.ListPasskeys(ctx, authv1.ListPasskeysRequest_builder{}.Build())
				return err
			},
			"DeletePasskey": func() error {
				_, err := authClient.DeletePasskey(ctx, authv1.DeletePasskeyRequest_builder{Id: "AAAA"}.Build())
				return err
			},
			"CreateInvitation": func() error {
				_, err := authClient.CreateInvitation(ctx, authv1.CreateInvitationRequest_builder{
					Kind: authv1.InvitationKind_INVITATION_KIND_ADD_PASSKEY,
				}.Build())
				return err
			},
			"ListInvitations": func() error {
				_, err := authClient.ListInvitations(ctx, authv1.ListInvitationsRequest_builder{}.Build())
				return err
			},
			"DeleteInvitation": func() error {
				_, err := authClient.DeleteInvitation(ctx, authv1.DeleteInvitationRequest_builder{Id: invitation.GetInvitation().GetId()}.Build())
				return err
			},
			"CreateAPIToken": func() error {
				_, err := usersClient.CreateAPIToken(ctx, usersv1.CreateAPITokenRequest_builder{Name: "More"}.Build())
				return err
			},
			"ListAPITokens": func() error {
				_, err := usersClient.ListAPITokens(ctx, usersv1.ListAPITokensRequest_builder{}.Build())
				return err
			},
			"DeleteAPIToken": func() error {
				_, err := usersClient.DeleteAPIToken(ctx, usersv1.DeleteAPITokenRequest_builder{Id: apiTokens.GetTokens()[0].GetId()}.Build())
				return err
			},
			"ApproveAuthorization": func() error {
				_, err := oauthClient.ApproveAuthorization(ctx, oauthv1.ApproveAuthorizationRequest_builder{
					Query: c.authorizeQuery(verifier, "state"),
				}.Build())
				return err
			},
			"ListConnections": func() error {
				_, err := oauthClient.ListConnections(ctx, oauthv1.ListConnectionsRequest_builder{}.Build())
				return err
			},
			"DeleteConnection": func() error {
				_, err := oauthClient.DeleteConnection(ctx, oauthv1.DeleteConnectionRequest_builder{Id: connections.GetConnections()[0].GetId()}.Build())
				return err
			},
		}
		for rpc, call := range calls {
			if err := call(); connect.CodeOf(err) != connect.CodePermissionDenied || !strings.Contains(err.Error(), "web app") {
				t.Errorf("%s with %s: got %v, want PermissionDenied pointing to the web app", rpc, name, err)
			}
		}
	}

	// Nothing was deleted, and the web app, signed in with the session, can
	// still manage everything.
	invitations, err := signedIn.auth.ListInvitations(ctx, authv1.ListInvitationsRequest_builder{}.Build())
	if err != nil || len(invitations.GetInvitations()) != 1 {
		t.Errorf("ListInvitations: %v %v", invitations, err)
	}
	if _, err := signedIn.auth.DeleteInvitation(ctx, authv1.DeleteInvitationRequest_builder{Id: invitation.GetInvitation().GetId()}.Build()); err != nil {
		t.Errorf("DeleteInvitation: %v", err)
	}
	laptop := newAuthenticator(t, "http://localhost:8080", "localhost")
	if _, err := signedIn.register(t, laptop, "", ""); err != nil {
		t.Fatalf("registering a passkey: %v", err)
	}
	if passkeys := passkeysFor(t, signedIn); len(passkeys) != 1 {
		t.Errorf("got passkeys %v, want the new one", passkeys)
	}
	created, err := signedIn.users.CreateAPIToken(ctx, usersv1.CreateAPITokenRequest_builder{Name: "More"}.Build())
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	if _, err := signedIn.users.DeleteAPIToken(ctx, usersv1.DeleteAPITokenRequest_builder{Id: created.GetToken().GetId()}.Build()); err != nil {
		t.Errorf("DeleteAPIToken: %v", err)
	}
	if _, err := e.oauth.DeleteConnection(ctx, oauthv1.DeleteConnectionRequest_builder{Id: connections.GetConnections()[0].GetId()}.Build()); err != nil {
		t.Errorf("DeleteConnection: %v", err)
	}
}
