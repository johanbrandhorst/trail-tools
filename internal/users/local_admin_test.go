package users_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/store/storetest"
	"github.com/johanbrandhorst/trail-tools/internal/users"
)

func TestLocalAdmin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storetest.New(t)
	repo, err := users.NewRepository(slog.New(slog.NewTextHandler(io.Discard, nil)), db)
	if err != nil {
		t.Fatal(err)
	}

	// Without an administrator, one is created.
	first, err := repo.LocalAdmin(ctx)
	if err != nil {
		t.Fatalf("LocalAdmin: %v", err)
	}
	user, err := repo.GetUser(ctx, first)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !user.IsAdmin() {
		t.Errorf("got a user who isn't an administrator, want one who is")
	}

	// After that, the same administrator is used.
	again, err := repo.LocalAdmin(ctx)
	if err != nil {
		t.Fatalf("LocalAdmin: %v", err)
	}
	if again != first {
		t.Errorf("got administrator %v, want the first one, %v", again, first)
	}
}
