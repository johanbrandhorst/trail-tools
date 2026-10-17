package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// testStore checks the behavior shared by all stores.
func testStore(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Get(ctx, "photos/a/large.jpg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get of a missing object returned %v, want ErrNotFound", err)
	}
	if err := s.Put(ctx, "photos/a/large.jpg", []byte("large"), "image/jpeg"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put(ctx, "photos/a/thumb.jpg", []byte("thumb"), "image/jpeg"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, "photos/a/large.jpg")
	if err != nil || string(got) != "large" {
		t.Fatalf("Get = %q, %v, want large", got, err)
	}
	if err := s.Put(ctx, "photos/a/large.jpg", []byte("replaced"), "image/jpeg"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if got, err := s.Get(ctx, "photos/a/large.jpg"); err != nil || string(got) != "replaced" {
		t.Fatalf("Get = %q, %v, want replaced", got, err)
	}
	if err := s.Delete(ctx, "photos/a/large.jpg"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "photos/a/large.jpg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get of a deleted object returned %v, want ErrNotFound", err)
	}
	if got, err := s.Get(ctx, "photos/a/thumb.jpg"); err != nil || string(got) != "thumb" {
		t.Fatalf("Get = %q, %v, want thumb", got, err)
	}
	if err := s.Delete(ctx, "photos/a/large.jpg"); err != nil {
		t.Fatalf("Delete of a missing object: %v", err)
	}
	for _, key := range []string{"", "../etc/passwd", "a/../b", "/abs", "a//b", ".hidden", "a/.."} {
		if err := s.Put(ctx, key, []byte("x"), "text/plain"); err == nil {
			t.Errorf("Put(%q) succeeded, want an invalid key error", key)
		}
		if _, err := s.Get(ctx, key); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) returned %v, want an invalid key error", key, err)
		}
	}
}

func TestDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "photos")
	s, err := NewDir(root)
	if err != nil {
		t.Fatal(err)
	}
	testStore(t, s)
	if err := s.Delete(context.Background(), "photos/a/thumb.jpg"); err != nil {
		t.Fatal(err)
	}
	// Empty directories are removed, but not the root.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("root has %d entries after deleting everything, want 0", len(entries))
	}
}

// fakeGCS implements the parts of the Cloud Storage JSON API the store
// uses.
type fakeGCS struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
}

func (f *fakeGCS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	const bucketPath = "/storage/v1/b/bucket/o/"
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/upload/storage/v1/b/bucket/o":
		if r.URL.Query().Get("uploadType") != "media" {
			http.Error(w, "bad upload type", http.StatusBadRequest)
			return
		}
		data, _ := io.ReadAll(r.Body)
		name := r.URL.Query().Get("name")
		f.objects[name] = data
		f.types[name] = r.Header.Get("Content-Type")
		_, _ = io.WriteString(w, `{"name":"`+name+`"}`)
	case strings.HasPrefix(r.URL.EscapedPath(), bucketPath):
		// Object names are a single, escaped path segment.
		escaped := strings.TrimPrefix(r.URL.EscapedPath(), bucketPath)
		if strings.Contains(escaped, "/") {
			http.Error(w, "unescaped object name", http.StatusBadRequest)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, bucketPath)
		data, ok := f.objects[name]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("alt") != "media" {
				http.Error(w, "metadata not supported", http.StatusBadRequest)
				return
			}
			_, _ = w.Write(data)
		case http.MethodDelete:
			delete(f.objects, name)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
		}
	default:
		http.Error(w, "unexpected request "+r.Method+" "+r.URL.String(), http.StatusBadRequest)
	}
}

func TestGCS(t *testing.T) {
	fake := &fakeGCS{objects: map[string][]byte{}, types: map[string]string{}}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	s := NewGCSWithClient("bucket", srv.URL, srv.Client())
	testStore(t, s)
	if got := fake.types["photos/a/thumb.jpg"]; got != "image/jpeg" {
		t.Errorf("content type = %q, want image/jpeg", got)
	}
	if !bytes.Equal(fake.objects["photos/a/thumb.jpg"], []byte("thumb")) {
		t.Errorf("stored object = %q, want thumb", fake.objects["photos/a/thumb.jpg"])
	}
}
