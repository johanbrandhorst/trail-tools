package trip

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/html"
	"github.com/johanbrandhorst/trail-tools/internal/photos"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"github.com/johanbrandhorst/trail-tools/internal/trips"
)

// PhotoHandler serves photos at /photos/<id>/<size>.jpg, where size is
// "large" or "thumb". It must be mounted at /photos/ behind the authn
// middleware. Photos of published reports are served to anyone, photos of
// drafts only to their author.
func (s *Service) PhotoHandler() http.Handler {
	return s.photoStore.Handler(s.report, PhotoPath, func(ctx context.Context, photoID pgtype.UUID) (*photos.Access, error) {
		a, err := s.repo.GetPhotoAccess(ctx, photoID)
		if err != nil {
			return nil, err
		}
		return &photos.Access{ObjectKey: a.ObjectKey, CreateTime: a.CreateTime, UserID: a.UserID, Published: a.Published}, nil
	})
}

// PageHandler serves the frontend for the public pages at /u/<handle> and
// /u/<handle>/<slug>, with the title, description and Open Graph tags of
// the profile or report in the HTML, for search engines and link previews.
// It must be mounted at /u/ behind the authn middleware, so authors can
// preview their drafts. watch adds the live reload script.
func (s *Service) PageHandler(watch bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, ProfilePath), "/"), "/")
		var (
			page html.Page
			err  error
		)
		switch {
		case len(parts) == 1 && parts[0] != "":
			page, err = s.profilePage(r, parts[0])
		case len(parts) == 2:
			page, err = s.reportPage(r, parts[0], parts[1])
		default:
			err = store.ErrNotFound
		}
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.report.Report(r.Context(), "Failed to get public page", err)
			}
			// The frontend shows its own error page.
			html.Render(w, watch, http.StatusNotFound, html.Page{Title: "Not found · trailrunn.ing", NoIndex: true})
			return
		}
		html.Render(w, watch, http.StatusOK, page)
	})
}

func (s *Service) profilePage(r *http.Request, handle string) (html.Page, error) {
	profile, err := s.repo.GetProfileByHandle(r.Context(), handle)
	if err != nil {
		return html.Page{}, err
	}
	reports, err := s.repo.ListReports(r.Context(), profile.UserID, trips.ListReportsFilter{PublishedOnly: true})
	if err != nil {
		return html.Page{}, err
	}
	page := html.Page{
		Title: "Trip reports by " + profile.DisplayName,
		URL:   s.profileURL(profile.Handle),
		Type:  "profile",
	}
	switch len(reports) {
	case 0:
		page.Description = fmt.Sprintf("Trip reports by %s on trailrunn.ing.", profile.DisplayName)
	case 1:
		page.Description = fmt.Sprintf("Trip reports by %s on trailrunn.ing, most recently %s.", profile.DisplayName, reports[0].Title)
	default:
		page.Description = fmt.Sprintf("%d trip reports by %s on trailrunn.ing, most recently %s.", len(reports), profile.DisplayName, reports[0].Title)
	}
	// Use the first photo of the most recent report with photos.
	if len(reports) > 0 {
		ids := make([]pgtype.UUID, len(reports))
		for i, report := range reports {
			ids[i] = report.ID
		}
		reportPhotos, err := s.repo.ListPhotos(r.Context(), ids...)
		if err != nil {
			return html.Page{}, err
		}
		for _, report := range reports {
			if cover := trips.CoverPhoto(report, reportPhotos[report.ID]); cover != nil {
				page.Image = s.photoURL(store.UUIDToString(cover.ID), photos.SizeLarge)
				break
			}
		}
	}
	return page, nil
}

func (s *Service) reportPage(r *http.Request, handle, slug string) (html.Page, error) {
	profile, report, err := s.publicReport(r.Context(), handle, slug)
	if err != nil {
		return html.Page{}, err
	}
	excerpts, err := s.repo.ListExcerpts(r.Context(), report.ID)
	if err != nil {
		return html.Page{}, err
	}
	page := html.Page{
		Title:         report.Title + " · " + profile.DisplayName,
		Description:   Excerpt(excerpts[report.ID], 200),
		URL:           s.reportURL(profile.Handle, report.Slug),
		Type:          "article",
		PublishedTime: report.PublishTime,
		// Authors can preview drafts, which shouldn't be indexed.
		NoIndex: !report.Published,
	}
	if page.Description == "" {
		page.Description = summarize(report, profile)
	}
	reportPhotos, err := s.repo.ListPhotos(r.Context(), report.ID)
	if err != nil {
		return html.Page{}, err
	}
	if cover := trips.CoverPhoto(report, reportPhotos[report.ID]); cover != nil {
		page.Image = s.photoURL(store.UUIDToString(cover.ID), photos.SizeLarge)
	}
	return page, nil
}

// summarize describes a report from its statistics, e.g. "A 21.1 km trail
// run with 1,234 m of climbing by Jane Doe, on 7 September 2026."
func summarize(r *trips.Report, profile *trips.Profile) string {
	var b strings.Builder
	sport := sportName(r.SportType)
	if sport == "" {
		sport = "trip"
	}
	if r.DistanceMeters > 0 {
		fmt.Fprintf(&b, "A %s km %s", strconv.FormatFloat(r.DistanceMeters/1000, 'f', 1, 64), sport)
	} else {
		fmt.Fprintf(&b, "A %s", sport)
	}
	if r.ElevationGainMeters >= 1 {
		fmt.Fprintf(&b, " with %s m of climbing", thousands(int(math.Round(r.ElevationGainMeters))))
	}
	fmt.Fprintf(&b, " by %s", profile.DisplayName)
	if !r.Date.IsZero() {
		fmt.Fprintf(&b, ", on %s", r.Date.Format("2 January 2006"))
	}
	b.WriteString(".")
	return b.String()
}

// sportName turns a Strava sport type into lowercase words, e.g. "TrailRun"
// into "trail run".
func sportName(sportType string) string {
	var b strings.Builder
	for i, r := range sportType {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// thousands formats a number with comma thousands separators.
func thousands(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + thousands(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
