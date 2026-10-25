package race

import (
	"fmt"
	"net/url"
	"strings"
)

// maxWebsiteURLLength is the maximum length of a race or event website link
// in bytes, generous for real links while keeping stored strings small.
const maxWebsiteURLLength = 2048

// parseWebsiteURL validates a race or event website link, which is shown as a
// link on the user's pages and on shared race plans. Only http and https
// links are allowed, as others, like javascript: links, could run code in the
// viewer's browser. It returns the trimmed link, or "" if none is set.
func parseWebsiteURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > maxWebsiteURLLength {
		return "", fmt.Errorf("website_url must be at most %d bytes", maxWebsiteURLLength)
	}
	if !isWebLink(raw) {
		return "", fmt.Errorf("website_url must be an http or https link, e.g. https://example.com")
	}
	return raw, nil
}

// websiteURL returns a stored website link for display, or "" if it isn't
// an http or https link. Links stored before they were validated may be
// anything.
func websiteURL(stored string) string {
	if !isWebLink(stored) {
		return ""
	}
	return stored
}

// isWebLink reports whether s is an absolute http or https link.
func isWebLink(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
