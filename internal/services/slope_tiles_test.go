package services_test

import (
	"bytes"
	"image/png"
	"net/http"
	"testing"
)

func TestSlopeTiles(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	// The fake elevations rise 1000 m per degree north, about half a
	// degree of slope, so the tile is clear.
	resp, data := e.get("/tiles/slope/14/8192/8000.png", e.token)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" ||
		resp.Header.Get("Cache-Control") != "private, max-age=2592000" {
		t.Fatalf("got status %d and headers %v", resp.StatusCode, resp.Header)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if _, _, _, alpha := img.At(128, 128).RGBA(); alpha != 0 || img.Bounds().Dx() != 256 {
		t.Errorf("got a %v image with alpha %d in the middle, want a clear 256 px tile", img.Bounds(), alpha)
	}

	// Signing in is required, and tiles are only rendered at zooms 12 to
	// 15, on the map.
	if resp, _ := e.get("/tiles/slope/14/8192/8000.png", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous: status %d, want 401", resp.StatusCode)
	}
	for _, path := range []string{"/tiles/slope/11/0/0.png", "/tiles/slope/16/0/0.png", "/tiles/slope/12/4096/0.png", "/tiles/slope/12/1/a.png", "/tiles/slope/12/1/1.jpg"} {
		if resp, _ := e.get(path, e.token); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
		}
	}
}
