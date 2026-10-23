package html

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRender(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, false, http.StatusOK, Page{
		Title:         `Up & over "the" <ridge>`,
		Description:   "A long day.",
		URL:           "https://trail.example/u/jane/up-and-over",
		Image:         "https://trail.example/photos/1/large.jpg",
		Type:          "article",
		PublishedTime: time.Date(2026, 9, 7, 10, 0, 0, 0, time.FixedZone("CEST", 2*3600)),
	})
	body := rec.Body.String()
	for _, want := range []string{
		`<title>Up &amp; over &#34;the&#34; &lt;ridge&gt;</title>`,
		`<meta name="description" content="A long day." />`,
		`<link rel="canonical" href="https://trail.example/u/jane/up-and-over" />`,
		`<meta property="og:type" content="article" />`,
		`<meta property="og:title" content="Up &amp; over &#34;the&#34; &lt;ridge&gt;" />`,
		`<meta property="og:image" content="https://trail.example/photos/1/large.jpg" />`,
		`<meta name="twitter:card" content="summary_large_image" />`,
		`<meta property="article:published_time" content="2026-09-07T08:00:00Z" />`,
		`<script src="/index.js?v=`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page does not contain %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "noindex") {
		t.Error("indexable page has noindex")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("content type = %q", got)
	}
}

func TestRenderDefaultImage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, false, http.StatusOK, Page{
		Title: "Trip reports by Jane",
		URL:   "https://trail.example/u/jane",
	})
	body := rec.Body.String()
	for _, want := range []string{
		`<meta property="og:image" content="https://trail.example/img/og.png" />`,
		`<meta name="twitter:card" content="summary_large_image" />`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page does not contain %q:\n%s", want, body)
		}
	}
}

func TestRenderDefault(t *testing.T) {
	rec := httptest.NewRecorder()
	ServeIndexHTML(false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "<title>trailrunn.ing</title>") {
		t.Errorf("page has no default title:\n%s", body)
	}
	if !strings.Contains(body, `<meta name="description" content="Training plans, race day plans and trip reports for trail runners." />`) {
		t.Errorf("page has no default description:\n%s", body)
	}
	if strings.Contains(body, "og:") {
		t.Errorf("page without a URL has Open Graph tags:\n%s", body)
	}

	rec = httptest.NewRecorder()
	Render(rec, false, http.StatusNotFound, Page{Title: "Not found", NoIndex: true})
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `<meta name="robots" content="noindex" />`) {
		t.Errorf("not found page: %d\n%s", rec.Code, rec.Body.String())
	}
}

func TestRenderSecurityHeaders(t *testing.T) {
	t.Parallel()
	for _, watch := range []bool{false, true} {
		rec := httptest.NewRecorder()
		Render(rec, watch, http.StatusOK, Page{})
		h := rec.Header()
		csp := h.Get("Content-Security-Policy")
		for _, want := range []string{"frame-ancestors 'none'", "object-src 'none'", "img-src 'self' data: blob: https:"} {
			if !strings.Contains(csp, want) {
				t.Errorf("watch %v: Content-Security-Policy %q doesn't contain %q", watch, csp, want)
			}
		}
		// The live reload script is inline, so it's only allowed while
		// watching.
		if got := strings.Contains(csp, "script-src 'self' 'unsafe-inline'"); got != watch {
			t.Errorf("watch %v: inline scripts allowed: %v", watch, got)
		}
		if got := h.Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("watch %v: X-Frame-Options = %q, want DENY", watch, got)
		}
		if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("watch %v: X-Content-Type-Options = %q, want nosniff", watch, got)
		}
	}
}

func TestServeBundle(t *testing.T) {
	t.Parallel()
	// The page links the script with the hash of its contents.
	rec := httptest.NewRecorder()
	Render(rec, false, http.StatusOK, Page{})
	jsURL := bundleURL("index.js")
	if !strings.Contains(jsURL, "?v=") || !strings.Contains(rec.Body.String(), `<script src="`+jsURL+`" defer>`) {
		t.Fatalf("page doesn't link %q:\n%s", jsURL, rec.Body.String())
	}

	h := ServeBundle()
	for url, wantCache := range map[string]string{
		jsURL:                  "public, max-age=31536000, immutable",
		"/index.js":            "no-cache",
		"/index.js?v=outdated": "no-cache",
		"/favicon.svg":         "no-cache",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		etag := rec.Header().Get("ETag")
		if rec.Code != http.StatusOK || etag == "" {
			t.Errorf("GET %s: got status %d and ETag %q, want 200 with an ETag", url, rec.Code, etag)
			continue
		}
		if got := rec.Header().Get("Cache-Control"); got != wantCache {
			t.Errorf("GET %s: Cache-Control = %q, want %q", url, got, wantCache)
		}
		// Browsers with the current file get no body.
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("If-None-Match", etag)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
			t.Errorf("GET %s with its ETag: got status %d and %d bytes, want 304 and none", url, rec.Code, rec.Body.Len())
		}
	}
}
