package store

import (
	"regexp"
	"strings"
	"testing"
)

func TestQuerySpanName(t *testing.T) {
	t.Parallel()
	long := "select " + strings.Repeat("a, ", 30) + "b from t"
	for stmt, want := range map[string]string{
		"-- name: GetRoute :one\nselect * from routes where id = $1": "GetRoute",
		"  -- name: ListRoutes :many\nselect 1":                      "ListRoutes",
		"select count(*) from routes\nwhere user_id = $1":            "select count(*) from routes",
		long: long[:maxSpanNameLength] + "…",
	} {
		if got := querySpanName(stmt); got != want {
			t.Errorf("querySpanName(%q) = %q, want %q", stmt, got, want)
		}
	}
}

// TestMigrationsAreAtomic checks that each migration runs in a single
// transaction, so a failed one leaves nothing behind and can be retried, as
// migrateSchema does. A file runs as a single query, in one transaction
// unless it ends one before its end, or runs statements that can't run in a
// transaction.
func TestMigrationsAreAtomic(t *testing.T) {
	t.Parallel()
	entries, err := fs.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	commit := regexp.MustCompile(`(?im)^\s*(commit|rollback)\s*;`)
	comment := regexp.MustCompile(`(?m)--.*$`)
	for _, e := range entries {
		data, err := fs.ReadFile("migrations/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		sql := strings.ToLower(comment.ReplaceAllString(string(data), ""))
		for _, word := range []string{"concurrently", "vacuum", "alter system"} {
			if strings.Contains(sql, word) {
				t.Errorf("%s: %q can't run in the migration's transaction", e.Name(), word)
			}
		}
		for _, loc := range commit.FindAllStringIndex(sql, -1) {
			if strings.TrimSpace(sql[loc[1]:]) != "" {
				t.Errorf("%s: ends its transaction before its end", e.Name())
			}
		}
	}
}
