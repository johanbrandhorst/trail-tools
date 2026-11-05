package services_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
)

func TestCalendarFeed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// Without a feed there's no URL, and only signed in users have one.
	got, err := e.shares.GetCalendarFeed(ctx, &plansv1.GetCalendarFeedRequest{})
	if err != nil || got.HasFeed() {
		t.Fatalf("got feed %v (%v) before creating one", got.GetFeed(), err)
	}
	_, err = e.anonShares.CreateCalendarFeed(ctx, &plansv1.CreateCalendarFeedRequest{})
	wantCode(t, err, connect.CodeUnauthenticated)

	// The user has no activities, so times are in UTC.
	today := time.Now().UTC().Truncate(24 * time.Hour)
	tomorrow := today.AddDate(0, 0, 1).Format(time.DateOnly)
	plan, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{
			Name: "Build", StartDate: today.AddDate(0, 0, -1).Format(time.DateOnly), EndDate: today.AddDate(0, 0, 60).Format(time.DateOnly),
		}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	run := plansv1.RunWorkout_builder{Intensity: plansv1.RunIntensity_RUN_INTENSITY_BASE, TargetDurationSeconds: 3600}.Build()
	if _, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
		PlanId: plan.GetPlan().GetId(),
		Workouts: []*plansv1.Workout{
			plansv1.Workout_builder{Date: tomorrow, Title: "Hills, easy", StartTime: "17:00", StartWindowMinutes: 30, Run: run}.Build(),
			plansv1.Workout_builder{Date: tomorrow, Title: "Core", StartTime: "07:00", Strength: plansv1.StrengthWorkout_builder{TargetDurationSeconds: 1800}.Build()}.Build(),
			// Not in the feed: no start time, a rest day, and too far ahead.
			plansv1.Workout_builder{Date: tomorrow, Title: "Whenever", Run: run}.Build(),
			plansv1.Workout_builder{Date: tomorrow, Rest: &plansv1.RestDay{}}.Build(),
			plansv1.Workout_builder{Date: today.AddDate(0, 0, 40).Format(time.DateOnly), Title: "Later", StartTime: "09:00", Run: run}.Build(),
		},
	}.Build()); err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}

	created, err := e.shares.CreateCalendarFeed(ctx, &plansv1.CreateCalendarFeedRequest{})
	if err != nil {
		t.Fatalf("CreateCalendarFeed: %v", err)
	}
	feed := created.GetFeed()
	if !strings.HasPrefix(feed.GetUrl(), "https://trail.example/calendar/") || !strings.HasPrefix(feed.GetBasicAuthUrl(), "https://feed:") {
		t.Fatalf("got feed URLs %q and %q", feed.GetUrl(), feed.GetBasicAuthUrl())
	}
	if got, err := e.shares.GetCalendarFeed(ctx, &plansv1.GetCalendarFeedRequest{}); err != nil || got.GetFeed().GetUrl() != feed.GetUrl() {
		t.Errorf("got feed %v (%v), want the created one", got.GetFeed(), err)
	}

	resp, body := e.get(feed.GetUrl(), "")
	cal := string(body)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/calendar") {
		t.Fatalf("getting the feed: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	day := strings.ReplaceAll(tomorrow, "-", "")
	for _, want := range []string{
		"BEGIN:VCALENDAR\r\n",
		"DTSTART:" + day + "T170000Z\r\n",
		"DTEND:" + day + "T180000Z\r\n",
		"SUMMARY:Base run: Hills\\, easy\r\n",
		`DESCRIPTION:latest_end: 18:30\n`,
		"DTSTART:" + day + "T070000Z\r\n",
		"DTEND:" + day + "T073000Z\r\n",
		`DESCRIPTION:latest_end: 08:30\n`,
	} {
		if !strings.Contains(cal, want) {
			t.Errorf("the feed doesn't have %q:\n%s", want, cal)
		}
	}
	if n := strings.Count(cal, "BEGIN:VEVENT"); n != 2 {
		t.Errorf("got %d events, want the 2 with a start time and duration within four weeks:\n%s", n, cal)
	}

	// The token works as HTTP Basic auth's password too.
	token := strings.TrimSuffix(strings.TrimPrefix(feed.GetUrl(), "https://trail.example/calendar/"), ".ics")
	basic := func(password string) int {
		req, err := http.NewRequest(http.MethodGet, e.srv.URL+"/calendar/workouts.ics", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth("feed", password)
		resp, err := e.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := basic(token); code != http.StatusOK {
		t.Errorf("getting the feed with Basic auth: %d", code)
	}
	if code := basic("wrong"); code != http.StatusUnauthorized {
		t.Errorf("getting the feed with the wrong password: %d, want 401", code)
	}

	// Replacing the feed stops the old URL working, and deleting it the new.
	replaced, err := e.shares.CreateCalendarFeed(ctx, &plansv1.CreateCalendarFeedRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp, _ := e.get(feed.GetUrl(), ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the replaced feed: %d, want 404", resp.StatusCode)
	}
	if resp, _ := e.get(replaced.GetFeed().GetUrl(), ""); resp.StatusCode != http.StatusOK {
		t.Errorf("the new feed: %d", resp.StatusCode)
	}
	if _, err := e.shares.DeleteCalendarFeed(ctx, &plansv1.DeleteCalendarFeedRequest{}); err != nil {
		t.Fatal(err)
	}
	if resp, _ := e.get(replaced.GetFeed().GetUrl(), ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the deleted feed: %d, want 404", resp.StatusCode)
	}
}
