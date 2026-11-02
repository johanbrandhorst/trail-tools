// Package html serves the frontend's index.html, which loads the single page
// application.
package html

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"time"

	"github.com/johanbrandhorst/reload"
)

const templateName = "index.html"

// defaultTitle is the title of pages without their own.
const defaultTitle = "trailrunn.ing"

// defaultDescription describes pages without their own description.
const defaultDescription = "Training plans, race day plans and trip reports for trail runners."

// defaultImagePath is the path of the link preview image of pages without
// their own: the app's mark and name.
const defaultImagePath = "/img/og.png"

// indexTemplate is the page every route serves. Besides the root the app
// renders into, it has a splash screen with the app's mark, styled by
// .splash in web/base.css, which covers the page while the bundle downloads
// and the app loads the signed in user. Being in the HTML, it shows as soon
// as the stylesheet has loaded, rather than after the much larger script,
// and the app fades it out once it has something to show (see
// web/helpers/splash.ts).
var indexTemplate = template.Must(template.New(templateName).Parse(
	fmt.Sprintf(
		`<!doctype html>
<html lang="en" class="h-full bg-gray-100">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <meta http-equiv="X-UA-Compatible" content="ie=edge" />
    <title>{{ .Page.Title }}</title>
    {{- with .Page }}
    {{- if .NoIndex }}
    <meta name="robots" content="noindex" />
    {{- end }}
    {{- if .Description }}
    <meta name="description" content="{{ .Description }}" />
    {{- end }}
    {{- if .URL }}
    <link rel="canonical" href="{{ .URL }}" />
    <meta property="og:site_name" content="trailrunn.ing" />
    <meta property="og:type" content="{{ or .Type "website" }}" />
    <meta property="og:title" content="{{ .Title }}" />
    <meta property="og:url" content="{{ .URL }}" />
    {{- if .Description }}
    <meta property="og:description" content="{{ .Description }}" />
    {{- end }}
    {{- if .Image }}
    <meta property="og:image" content="{{ .Image }}" />
    <meta name="twitter:card" content="summary_large_image" />
    {{- else }}
    <meta name="twitter:card" content="summary" />
    {{- end }}
    {{- if not .PublishedTime.IsZero }}
    <meta property="article:published_time" content="{{ .PublishedTime.UTC.Format "2006-01-02T15:04:05Z07:00" }}" />
    {{- end }}
    {{- end }}
    {{- end }}
    <link rel="stylesheet" href="{{ .CSS }}" />
    <link rel="icon" href="/favicon.svg" />
    <link rel="manifest" href="/manifest.webmanifest" />
    <link rel="apple-touch-icon" href="/img/icon-192.png" />
    <meta name="theme-color" content="#111111" />
    <meta name="mobile-web-app-capable" content="yes" />
    <meta name="apple-mobile-web-app-title" content="trailrunn.ing" />
    <meta name="apple-mobile-web-app-status-bar-style" content="black" />
    <script src="{{ .JS }}" defer></script>
  </head>
  <body class="h-full">
    <div id="root" class="h-full"></div>
    <div id="splash" class="splash" role="status" aria-label="Loading trailrunn.ing">
      <div class="splash-content">
        <svg class="splash-mark" aria-hidden="true" viewBox="0 0 40 26" fill="none">
          <path class="splash-ridge" pathLength="1" d="M2 22 L9 15 L13 18 L21 6 L26 12 L30 9 L38 20" stroke="#d2601a" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" />
          <circle class="splash-summit" cx="21" cy="6" r="2.6" fill="#ffffff" />
          <path d="M2 24.5 L38 24.5" stroke="#404040" stroke-width="1.5" stroke-linecap="round" />
        </svg>
        <div class="splash-wordmark">trailrunn<span>.ing</span></div>
        <p class="splash-status">Lacing up…</p>
      </div>
      <svg class="splash-ridges" aria-hidden="true" viewBox="0 0 600 120" preserveAspectRatio="none">
        <path d="M0 100 L60 80 L100 90 L170 50 L210 65 L280 20 L330 55 L370 40 L430 75 L480 60 L540 90 L600 80" fill="none" stroke="#d2601a" stroke-width="2" stroke-opacity="0.55" stroke-linejoin="round" />
        <path d="M0 112 L80 98 L140 104 L220 82 L300 92 L380 74 L460 96 L540 88 L600 100" fill="none" stroke="#404040" stroke-width="1.5" stroke-linejoin="round" />
      </svg>
    </div>
  </body>
  {{- if .Watch }}
  %s
  {{- end }}
</html>
`,
		reload.Script,
	),
))

// Page describes a page for search engines and link previews, which don't
// run the single page application.
type Page struct {
	// The title of the page. Defaults to "trailrunn.ing".
	Title string
	// A short description of the page. Defaults to a description of the
	// app.
	Description string
	// The canonical URL of the page. The Open Graph tags are only added
	// for pages with a URL.
	URL string
	// The URL of an image representing the page. Pages with a URL default
	// to the app's preview image.
	Image string
	// The Open Graph type, e.g. "article" or "profile". Defaults to
	// "website".
	Type string
	// When an article was published.
	PublishedTime time.Time
	// Ask search engines not to index the page.
	NoIndex bool
}

// contentSecurityPolicy allows the app to load only its own scripts, and to
// connect only to its own server, so that markup injected into a page, such
// as through a bug rendering a user's text, can't run scripts or send data
// elsewhere. Images come from anywhere over HTTPS: the map tiles from the
// tile servers the user picks, and activity photos from Strava. Styles may
// be inline, as Leaflet and the charts set them on elements. Other sites
// can't frame the pages, so they can't trick users into clicking in them,
// like on the OAuth consent page.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob: https:; font-src 'self'; connect-src 'self'; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// watchContentSecurityPolicy is contentSecurityPolicy while watching for
// changes, which allows the inline live reload script and its websocket.
const watchContentSecurityPolicy = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob: https:; font-src 'self'; connect-src 'self' ws: wss:; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// Render writes index.html with the status code, with the page's metadata
// in its head. watch adds the live reload script.
func Render(w http.ResponseWriter, watch bool, status int, page Page) {
	if page.Title == "" {
		page.Title = defaultTitle
	}
	if page.Description == "" {
		page.Description = defaultDescription
	}
	if page.Image == "" && page.URL != "" {
		if u, err := url.Parse(page.URL); err == nil {
			page.Image = (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: defaultImagePath}).String()
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", contentSecurityPolicy)
	if watch {
		h.Set("Content-Security-Policy", watchContentSecurityPolicy)
	}
	// For browsers without CSP's frame-ancestors.
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	// The bundle being watched is served from its directory, without hashes.
	css, js := "/index.css", "/index.js"
	if !watch {
		css, js = bundleURL("index.css"), bundleURL("index.js")
	}
	_ = indexTemplate.ExecuteTemplate(w, templateName, map[string]any{
		"Watch": watch,
		"Page":  page,
		"CSS":   css,
		"JS":    js,
	})
}

// ServeIndexHTML serves index.html for any request. watch adds the live
// reload script.
func ServeIndexHTML(watch bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Render(w, watch, http.StatusOK, Page{})
	})
}

// TokenURL returns the path of page with a link's secret token in its
// fragment, e.g. "/invite#<token>". Browsers don't send fragments to the
// server, so the token stays out of request logs, like Cloud Run's, which
// record the URL of every request, and out of the Referer of the requests
// the page makes.
func TokenURL(page, token string) string {
	return (&url.URL{Path: page, Fragment: token}).String()
}
