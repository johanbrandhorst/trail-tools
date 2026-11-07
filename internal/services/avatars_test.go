package services_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"testing"

	"connectrpc.com/connect/v2"
	usersv1 "github.com/johanbrandhorst/trail-tools/internal/gen/users/v1"
)

// pngImage returns a PNG of a solid colour.
func pngImage(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		for y := range h {
			img.Set(x, y, color.RGBA{R: 200, G: 80, B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAvatars(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	get := func(url string) (int, []byte) {
		t.Helper()
		resp, err := e.srv.Client().Get(e.srv.URL + url)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(resp.Body); err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, buf.Bytes()
	}

	_, err := e.users.UploadAvatar(ctx, usersv1.UploadAvatarRequest_builder{Data: []byte("not an image")}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)

	uploaded, err := e.users.UploadAvatar(ctx, usersv1.UploadAvatarRequest_builder{Data: pngImage(t, 600, 400)}.Build())
	if err != nil {
		t.Fatalf("UploadAvatar: %v", err)
	}
	first := uploaded.GetUser().GetAvatarUrl()
	if first == "" {
		t.Fatal("the uploaded avatar has no URL")
	}
	current, err := e.users.GetCurrentUser(ctx, usersv1.GetCurrentUserRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("GetCurrentUser: %v", err)
	}
	if current.GetUser().GetAvatarUrl() != first {
		t.Errorf("current user's avatar is %q, want %q", current.GetUser().GetAvatarUrl(), first)
	}

	// Avatars are public: they're fetched without credentials, as a square.
	status, data := get(first)
	if status != http.StatusOK {
		t.Fatalf("GET %s: %d", first, status)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the avatar isn't a JPEG: %v", err)
	}
	if cfg.Width != 256 || cfg.Height != 256 {
		t.Errorf("the avatar is %dx%d, want 256x256", cfg.Width, cfg.Height)
	}

	// A new avatar replaces the old one, whose image is deleted.
	replaced, err := e.users.UploadAvatar(ctx, usersv1.UploadAvatarRequest_builder{Data: pngImage(t, 100, 100)}.Build())
	if err != nil {
		t.Fatalf("UploadAvatar: %v", err)
	}
	second := replaced.GetUser().GetAvatarUrl()
	if second == first {
		t.Error("a new avatar has the same URL")
	}
	if status, _ := get(first); status != http.StatusNotFound {
		t.Errorf("GET the replaced avatar: %d, want 404", status)
	}

	deleted, err := e.users.DeleteAvatar(ctx, usersv1.DeleteAvatarRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("DeleteAvatar: %v", err)
	}
	if deleted.GetUser().GetAvatarUrl() != "" {
		t.Errorf("the user still has avatar %q", deleted.GetUser().GetAvatarUrl())
	}
	if status, _ := get(second); status != http.StatusNotFound {
		t.Errorf("GET the deleted avatar: %d, want 404", status)
	}
	for _, path := range []string{"/avatars/../trip-photos/x.jpg", "/avatars/ABC.jpg", "/avatars/"} {
		if status, _ := get(path); status != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, status)
		}
	}
}
