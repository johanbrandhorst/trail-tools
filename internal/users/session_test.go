package users_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/store/storetest"
	"github.com/johanbrandhorst/trail-tools/internal/users"
)

func TestSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storetest.New(t)
	repo, err := users.NewRepository(slog.New(slog.NewTextHandler(io.Discard, nil)), db)
	if err != nil {
		t.Fatal(err)
	}
	userID := storetest.NewUser(t, db)

	// A session from before tokens were hashed, whose token is its old ID,
	// hashed as the migration does.
	const oldToken = "6f1c2a4e-8d3b-4c5a-9e7f-0a1b2c3d4e5f"
	if _, err := db.Exec(ctx,
		"insert into sessions (user_id, token_sha256, expiry) values ($1, sha256(convert_to($2::uuid::text, 'UTF8')), now() + interval '1 day')",
		userID, oldToken,
	); err != nil {
		t.Fatal(err)
	}
	// An expired session, which creating a session deletes.
	if _, err := db.Exec(ctx,
		"insert into sessions (user_id, token_sha256, expiry) values ($1, 'expired', now() - interval '1 second')",
		userID,
	); err != nil {
		t.Fatal(err)
	}

	token, err := repo.CreateSession(ctx, userID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{token, oldToken} {
		user, err := repo.GetSession(ctx, tok)
		if err != nil {
			t.Fatalf("GetSession(%q): %v", tok, err)
		}
		if user.ID() != userID {
			t.Errorf("GetSession(%q) = user %v, want %v", tok, user.ID(), userID)
		}
	}

	var expired, plaintext int
	if err := db.QueryRow(ctx, "select count(*) filter (where expiry <= now()), count(*) filter (where id::text = $1 or token_sha256 = convert_to($1, 'UTF8')) from sessions", token).Scan(&expired, &plaintext); err != nil {
		t.Fatal(err)
	}
	if expired != 0 {
		t.Errorf("%d expired sessions are left after creating a session, want 0", expired)
	}
	if plaintext != 0 {
		t.Errorf("the session's token is stored in plain text")
	}

	if _, err := repo.GetSession(ctx, "not a token"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetSession with an unknown token: got %v, want %v", err, store.ErrNotFound)
	}
	if err := repo.DeleteSession(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetSession(ctx, token); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetSession after DeleteSession: got %v, want %v", err, store.ErrNotFound)
	}
}
