package trip

import (
	"strings"
	"testing"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/trips"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		title, want string
	}{
		{"Morning Run", "morning-run"},
		{"  Säntis via the Lisengrat!  ", "santis-via-the-lisengrat"},
		{"Grand Tour of Mont-Blanc -- day 2/7", "grand-tour-of-mont-blanc-day-2-7"},
		{"Großglockner", "grossglockner"},
		{"Ørsta & Æsir", "rsta-sir"},
		{"🏔️⛰️", ""},
		{strings.Repeat("long ", 30), strings.TrimSuffix(strings.Repeat("long-", 16), "-")},
	}
	for _, tt := range tests {
		got := Slugify(tt.title)
		if got != tt.want {
			t.Errorf("Slugify(%q) = %q, want %q", tt.title, got, tt.want)
		}
		if got != "" {
			if err := ValidateSlug(got); err != nil {
				t.Errorf("Slugify(%q) = %q, which is invalid: %v", tt.title, got, err)
			}
		}
	}
}

func TestValidateSlug(t *testing.T) {
	for _, slug := range []string{"a", "my-first-ultra", "2026-utmb"} {
		if err := ValidateSlug(slug); err != nil {
			t.Errorf("ValidateSlug(%q) = %v, want nil", slug, err)
		}
	}
	for _, slug := range []string{"", "-a", "a-", "a--b", "A", "a b", "a_b", "ä", strings.Repeat("a", 81)} {
		if err := ValidateSlug(slug); err == nil {
			t.Errorf("ValidateSlug(%q) = nil, want an error", slug)
		}
	}
}

func TestValidateHandle(t *testing.T) {
	for _, handle := range []string{"abc", "jane-doe", "runner42", "a-b", strings.Repeat("a", 30)} {
		if err := validateHandle(handle); err != nil {
			t.Errorf("validateHandle(%q) = %v, want nil", handle, err)
		}
	}
	for _, handle := range []string{"", "ab", "-abc", "abc-", "Jane", "jane doe", "jane_doe", "jané", strings.Repeat("a", 31)} {
		if err := validateHandle(handle); err == nil {
			t.Errorf("validateHandle(%q) = nil, want an error", handle)
		}
	}
}

func TestExcerpt(t *testing.T) {
	tests := []struct {
		text      string
		maxLength int
		want      string
	}{
		{"", 10, ""},
		{"Short story.", 20, "Short story."},
		{"First paragraph.\n\nSecond   paragraph.", 100, "First paragraph. Second paragraph."},
		{"The trail climbed steeply through the forest", 25, "The trail climbed…"},
		{"Supercalifragilisticexpialidocious", 10, "Supercali…"},
	}
	for _, tt := range tests {
		if got := Excerpt(tt.text, tt.maxLength); got != tt.want {
			t.Errorf("Excerpt(%q, %d) = %q, want %q", tt.text, tt.maxLength, got, tt.want)
		}
		if got := Excerpt(tt.text, tt.maxLength); len([]rune(got)) > tt.maxLength {
			t.Errorf("Excerpt(%q, %d) = %q, which is too long", tt.text, tt.maxLength, got)
		}
	}
}

func TestParseLocalTime(t *testing.T) {
	got, err := parseLocalTime("2026-09-07T10:15:30", "Europe/Zurich")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 7, 8, 15, 30, 0, time.UTC); !got.Equal(want) {
		t.Errorf("parseLocalTime = %v, want %v", got, want)
	}
	got, err = parseLocalTime("2026-09-07T10:15:30", "Not/AZone")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 7, 10, 15, 30, 0, time.UTC); !got.Equal(want) {
		t.Errorf("parseLocalTime with an unknown zone = %v, want %v", got, want)
	}
	if _, err := parseLocalTime("2026:09:07 10:15:30", "UTC"); err == nil {
		t.Error("parseLocalTime of an EXIF formatted time succeeded")
	}
}

func TestSummarize(t *testing.T) {
	r := &trips.Report{
		SportType:           "TrailRun",
		DistanceMeters:      21097,
		ElevationGainMeters: 1234.4,
		Date:                time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC),
	}
	profile := &trips.Profile{DisplayName: "Jane Doe"}
	want := "A 21.1 km trail run with 1,234 m of climbing by Jane Doe, on 7 September 2026."
	if got := summarize(r, profile); got != want {
		t.Errorf("summarize = %q, want %q", got, want)
	}
	if got, want := summarize(&trips.Report{}, profile), "A trip by Jane Doe."; got != want {
		t.Errorf("summarize = %q, want %q", got, want)
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -1234: "-1,234"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}
