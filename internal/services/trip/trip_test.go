package trip

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
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

func encodeTestImage(t *testing.T, w, h int, asPNG bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 100, A: 255})
		}
	}
	// A transparent corner, which must become white rather than black.
	img.Set(0, 0, color.NRGBA{})
	var buf bytes.Buffer
	var err error
	if asPNG {
		err = png.Encode(&buf, img)
	} else {
		err = jpeg.Encode(&buf, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestProcessImage(t *testing.T) {
	tests := []struct {
		name                string
		w, h                int
		png                 bool
		wantW, wantH        int
		wantThumbW, wantThW int
	}{
		{name: "large landscape JPEG", w: 4000, h: 3000, wantW: 2048, wantH: 1536, wantThumbW: 480, wantThW: 360},
		{name: "portrait PNG", w: 600, h: 1200, png: true, wantW: 600, wantH: 1200, wantThumbW: 240, wantThW: 480},
		{name: "small", w: 300, h: 200, wantW: 300, wantH: 200, wantThumbW: 300, wantThW: 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := processImage(t.Context(), encodeTestImage(t, tt.w, tt.h, tt.png))
			if err != nil {
				t.Fatal(err)
			}
			if got.width != tt.wantW || got.height != tt.wantH {
				t.Errorf("large is %dx%d, want %dx%d", got.width, got.height, tt.wantW, tt.wantH)
			}
			large, err := jpeg.Decode(bytes.NewReader(got.large))
			if err != nil {
				t.Fatalf("large is not a JPEG: %v", err)
			}
			if b := large.Bounds(); b.Dx() != tt.wantW || b.Dy() != tt.wantH {
				t.Errorf("decoded large is %v", b)
			}
			thumb, err := jpeg.Decode(bytes.NewReader(got.thumb))
			if err != nil {
				t.Fatalf("thumbnail is not a JPEG: %v", err)
			}
			if b := thumb.Bounds(); b.Dx() != tt.wantThumbW || b.Dy() != tt.wantThW {
				t.Errorf("thumbnail is %dx%d, want %dx%d", b.Dx(), b.Dy(), tt.wantThumbW, tt.wantThW)
			}
			if tt.png {
				if r, g, b, _ := large.At(0, 0).RGBA(); r>>8 < 200 || g>>8 < 200 || b>>8 < 200 {
					t.Errorf("transparent pixel is %v, want white", large.At(0, 0))
				}
			}
		})
	}
	for _, data := range [][]byte{[]byte("not an image"), []byte("GIF89a")} {
		if _, err := processImage(t.Context(), data); err == nil {
			t.Errorf("processImage(%q) succeeded", data)
		}
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
