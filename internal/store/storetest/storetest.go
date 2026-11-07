// Package storetest creates databases for tests.
package storetest

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// EnvVar is the environment variable with the URL of the postgres server to
// run tests against. Tests that need a database are skipped if it's unset.
const EnvVar = "TEST_DATABASE_URL"

// New creates a new, migrated database for the test and drops it when the
// test finishes.
func New(t testing.TB) *store.DB {
	t.Helper()
	rawURL := os.Getenv(EnvVar)
	if rawURL == "" {
		t.Skipf("%s is not set, skipping test that requires a database; run with `make test`", EnvVar)
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, rawURL)
	if err != nil {
		t.Fatalf("Failed to connect to %s: %v", EnvVar, err)
	}
	t.Cleanup(func() { admin.Close(context.Background()) })
	name := "test_" + strings.ToLower(rand.Text())
	if _, err := admin.Exec(ctx, "create database "+name); err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "drop database "+name+" with (force)"); err != nil {
			t.Errorf("Failed to drop test database: %v", err)
		}
	})
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", EnvVar, err)
	}
	u.Path = "/" + name
	db, err := store.New(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), u)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

// NewUser creates a user and returns its ID.
func NewUser(t testing.TB, db *store.DB) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := db.QueryRow(context.Background(),
		"insert into passkey_users (user_handle, name) values ($1, $2) returning id",
		[]byte(rand.Text()), "Test user",
	).Scan(&id); err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	return id
}
