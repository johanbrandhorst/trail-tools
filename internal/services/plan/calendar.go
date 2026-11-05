package plan

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
	"github.com/johanbrandhorst/trail-tools/internal/plans"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

const (
	// CalendarPath is where calendar feeds are served: at
	// CalendarPath+"<token>.ics" for apps that can only subscribe to a URL,
	// or at calendarBasicAuthFile with the token as HTTP Basic auth's
	// password, which keeps it out of server logs.
	CalendarPath = "/calendar/"
	// calendarBasicAuthFile is the feed's file with HTTP Basic auth.
	calendarBasicAuthFile = "workouts.ics"
	// calendarBasicAuthUser is the user name of feed URLs with HTTP Basic
	// auth. Any user name is accepted.
	calendarBasicAuthUser = "feed"
	// calendarDaysAhead is how many days ahead feeds list workouts.
	calendarDaysAhead = 28
)

// CalendarRepository stores calendar feeds.
type CalendarRepository interface {
	GetCalendarToken(ctx context.Context, userID pgtype.UUID) (string, error)
	SetCalendarToken(ctx context.Context, userID pgtype.UUID, token string) error
	GetCalendarFeedUser(ctx context.Context, token string) (pgtype.UUID, string, error)
}

// SetCalendars lets the service serve calendar feeds, at URLs under the
// sharing base URL.
func (s *Service) SetCalendars(calendars CalendarRepository) {
	s.calendars = calendars
}

func (s *Service) calendarUser(ctx context.Context) (pgtype.UUID, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return pgtype.UUID{}, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if s.calendars == nil || s.baseURL == "" {
		return pgtype.UUID{}, connect.NewError(connect.CodeUnimplemented, "calendar feeds aren't set up on this server")
	}
	return userID, nil
}

// calendarFeed returns the URLs of the feed with a token.
func (s *Service) calendarFeed(token string) *plansv1.CalendarFeed {
	feed := plansv1.CalendarFeed_builder{Url: s.baseURL + CalendarPath + token + ".ics"}
	if u, err := url.Parse(s.baseURL + CalendarPath + calendarBasicAuthFile); err == nil {
		u.User = url.UserPassword(calendarBasicAuthUser, token)
		feed.BasicAuthUrl = u.String()
	}
	return feed.Build()
}

func (s *Service) GetCalendarFeed(ctx context.Context, _ *plansv1.GetCalendarFeedRequest) (*plansv1.GetCalendarFeedResponse, error) {
	userID, err := s.calendarUser(ctx)
	if err != nil {
		return nil, err
	}
	token, err := s.calendars.GetCalendarToken(ctx, userID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get calendar feed")
	}
	resp := &plansv1.GetCalendarFeedResponse{}
	if token != "" {
		resp.SetFeed(s.calendarFeed(token))
	}
	return resp, nil
}

func (s *Service) CreateCalendarFeed(ctx context.Context, _ *plansv1.CreateCalendarFeedRequest) (*plansv1.CreateCalendarFeedResponse, error) {
	userID, err := s.calendarUser(ctx)
	if err != nil {
		return nil, err
	}
	token := rand.Text()
	if err := s.calendars.SetCalendarToken(ctx, userID, token); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to create calendar feed")
	}
	return plansv1.CreateCalendarFeedResponse_builder{Feed: s.calendarFeed(token)}.Build(), nil
}

func (s *Service) DeleteCalendarFeed(ctx context.Context, _ *plansv1.DeleteCalendarFeedRequest) (*plansv1.DeleteCalendarFeedResponse, error) {
	userID, err := s.calendarUser(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.calendars.SetCalendarToken(ctx, userID, ""); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete calendar feed")
	}
	return &plansv1.DeleteCalendarFeedResponse{}, nil
}

// calendarToken returns the token of a feed request: from the path, or the
// password of HTTP Basic auth for calendarBasicAuthFile.
func calendarToken(r *http.Request) string {
	file := strings.TrimPrefix(r.URL.Path, CalendarPath)
	if file == calendarBasicAuthFile {
		_, password, _ := r.BasicAuth()
		return password
	}
	token, ok := strings.CutSuffix(file, ".ics")
	if !ok || strings.Contains(token, "/") {
		return ""
	}
	return token
}

// CalendarHandler serves calendar feeds, without signing in: the token
// identifies the user. Calendar apps refresh often, so it takes two
// round trips to the database.
func (s *Service) CalendarHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		token := calendarToken(r)
		if s.calendars == nil || token == "" {
			s.calendarUnauthorized(w, r)
			return
		}
		userID, timezone, err := s.calendars.GetCalendarFeedUser(ctx, token)
		if errors.Is(err, store.ErrNotFound) {
			s.calendarUnauthorized(w, r)
			return
		}
		if err != nil {
			s.report.Report(ctx, "Failed to get calendar feed", err)
			http.Error(w, "failed to get the calendar", http.StatusInternalServerError)
			return
		}
		loc, err := time.LoadLocation(timezone)
		if err != nil || timezone == "" {
			loc = time.UTC
		}
		now := time.Now().In(loc)
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		workouts, err := s.plans.ListUserWorkouts(ctx, userID, today.AddDate(0, 0, -1), today.AddDate(0, 0, calendarDaysAhead))
		if err != nil {
			s.report.Report(ctx, "Failed to list workouts for a calendar feed", err)
			http.Error(w, "failed to get the calendar", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.Header().Set("Cache-Control", "private, no-store")
		_, _ = w.Write([]byte(workoutCalendar(workouts, loc, now, s.baseURL)))
	})
}

func (s *Service) calendarUnauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.TrimPrefix(r.URL.Path, CalendarPath) == calendarBasicAuthFile {
		w.Header().Set("WWW-Authenticate", `Basic realm="trailrunn.ing calendar"`)
		http.Error(w, "this calendar feed doesn't exist or was replaced; copy its URL again from Settings", http.StatusUnauthorized)
		return
	}
	http.Error(w, "this calendar feed doesn't exist or was replaced; copy its URL again from Settings", http.StatusNotFound)
}

// workoutTypeNames name the types of workouts in calendars.
var workoutTypeNames = map[plans.WorkoutType]string{
	plans.WorkoutTypeBase:          "Base run",
	plans.WorkoutTypeIntensity:     "Intensity",
	plans.WorkoutTypeCrossTraining: "Cross training",
	plans.WorkoutTypeStrength:      "Strength",
	plans.WorkoutTypeRace:          "Race",
}

// workoutCalendar returns an iCalendar of workouts: an event for each that
// isn't a rest day or done, with a start time and a target duration, in
// UTC from their local times in loc.
func workoutCalendar(workouts []*plans.Workout, loc *time.Location, now time.Time, baseURL string) string {
	var b strings.Builder
	line := func(s string) {
		// Lines are folded at 75 octets, continuing with a space.
		for len(s) > 75 {
			cut := 75
			for cut > 1 && s[cut]&0xC0 == 0x80 {
				// Don't split a UTF-8 sequence.
				cut--
			}
			b.WriteString(s[:cut] + "\r\n")
			s = " " + s[cut:]
		}
		b.WriteString(s + "\r\n")
	}
	const stamp = "20060102T150405Z"
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//trailrunn.ing//Workouts//EN")
	line("CALSCALE:GREGORIAN")
	line("X-WR-CALNAME:Workouts")
	for _, w := range workouts {
		name, ok := workoutTypeNames[w.Type]
		if !ok || w.CompletedActivityID.Valid || w.StartTime == "" || w.TargetDurationSeconds <= 0 {
			continue
		}
		clock, err := time.Parse("15:04", w.StartTime)
		if err != nil {
			continue
		}
		start := time.Date(w.Date.Year(), w.Date.Month(), w.Date.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
		duration := time.Duration(w.TargetDurationSeconds) * time.Second
		window := time.Duration(w.StartWindowMinutes) * time.Minute
		if window <= 0 {
			window = defaultStartWindowMinutes * time.Minute
		}
		summary := name
		if title := strings.TrimSpace(w.Title); title != "" {
			summary += ": " + title
		}
		description := fmt.Sprintf("latest_end: %s\n%s/workouts/%s", start.Add(window+duration).Format("15:04"), baseURL, store.UUIDToString(w.ID))
		line("BEGIN:VEVENT")
		line("UID:" + store.UUIDToString(w.ID) + "@trailrunn.ing")
		line("DTSTAMP:" + now.UTC().Format(stamp))
		line("DTSTART:" + start.UTC().Format(stamp))
		line("DTEND:" + start.Add(duration).UTC().Format(stamp))
		line("SUMMARY:" + icalText(summary))
		line("DESCRIPTION:" + icalText(description))
		if w.LocationName != "" {
			line("LOCATION:" + icalText(w.LocationName))
		}
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return b.String()
}

// icalText escapes text for an iCalendar property.
func icalText(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`).Replace(s)
}
