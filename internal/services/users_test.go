package services_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	usersv1 "github.com/johanbrandhorst/trail-tools/internal/gen/users/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/users/v1/usersv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

func TestAPITokens(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// The test token authenticates as the test user.
	withToken := usersv1connect.NewUserServiceClient(connect.NewClient(connecthttp.NewTransport(e.srv.Client(), e.srv.URL), bearerInterceptor(e.token)))
	current, err := withToken.GetCurrentUser(ctx, usersv1.GetCurrentUserRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("GetCurrentUser: %v", err)
	}
	if current.GetUser().GetId() != store.UUIDToString(e.userID) {
		t.Errorf("signed in as %v, want %v", current.GetUser().GetId(), store.UUIDToString(e.userID))
	}

	_, err = e.users.CreateAPIToken(ctx, usersv1.CreateAPITokenRequest_builder{Name: " "}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
	created, err := e.users.CreateAPIToken(ctx, usersv1.CreateAPITokenRequest_builder{Name: "Laptop"}.Build())
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	secret := created.GetSecret()
	if !strings.HasPrefix(secret, "tt_") {
		t.Errorf("unexpected token secret %q", secret)
	}

	// The new token works, and is marked as used.
	laptop := usersv1connect.NewUserServiceClient(connect.NewClient(connecthttp.NewTransport(e.srv.Client(), e.srv.URL), bearerInterceptor(secret)))
	if _, err := laptop.GetCurrentUser(ctx, usersv1.GetCurrentUserRequest_builder{}.Build()); err != nil {
		t.Fatalf("GetCurrentUser with the new token: %v", err)
	}
	list, err := e.users.ListAPITokens(ctx, usersv1.ListAPITokensRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("ListAPITokens: %v", err)
	}
	if len(list.GetTokens()) != 2 {
		t.Fatalf("got %d tokens, want 2", len(list.GetTokens()))
	}
	for _, token := range list.GetTokens() {
		if !token.HasLastUsedTime() {
			t.Errorf("token %q has no last used time", token.GetName())
		}
	}

	// A deleted token no longer authenticates.
	if _, err := e.users.DeleteAPIToken(ctx, usersv1.DeleteAPITokenRequest_builder{Id: created.GetToken().GetId()}.Build()); err != nil {
		t.Fatalf("DeleteAPIToken: %v", err)
	}
	_, err = laptop.GetCurrentUser(ctx, usersv1.GetCurrentUserRequest_builder{}.Build())
	wantCode(t, err, connect.CodeUnauthenticated)
	_, err = e.users.DeleteAPIToken(ctx, usersv1.DeleteAPITokenRequest_builder{Id: created.GetToken().GetId()}.Build())
	wantCode(t, err, connect.CodeNotFound)

	// Invalid tokens are rejected with a bearer challenge.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.srv.URL+usersv1connect.UserServiceGetCurrentUserProcedure, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tt_made-up")
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("got status %d and WWW-Authenticate %q, want 401 with a bearer challenge", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
}
