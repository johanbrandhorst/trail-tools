package garmin_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/garmin"
)

// newClient returns a client of a fake Garmin API served by handler.
func newClient(t *testing.T, handler http.HandlerFunc) (*garmin.Client, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return garmin.NewClient(garmin.Config{
		ClientID: "id", ClientSecret: "secret", APIURL: srv.URL, TokenURL: srv.URL + "/token", HTTPClient: srv.Client(),
	}), srv.URL
}

func TestCallbacksMustBeGarmins(t *testing.T) {
	t.Parallel()
	called := false
	c, apiURL := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("got authorization %q", r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `[{"activityId":1,"activityName":"Run","activityType":"RUNNING","startTimeInSeconds":1700000000}]`)
	})
	// A forged notification can't make the client send the token
	// elsewhere.
	if _, err := c.FetchActivities(t.Context(), "token", "https://evil.example/activities"); err == nil || called {
		t.Fatal("fetched activities from another host")
	}
	if _, err := c.DownloadFile(t.Context(), "token", "https://evil.example/file"); err == nil || called {
		t.Fatal("downloaded a file from another host")
	}
	got, err := c.FetchActivities(t.Context(), "token", apiURL+"/wellness-api/rest/activities?token=x")
	if err != nil {
		t.Fatalf("FetchActivities: %v", err)
	}
	if len(got) != 1 || got[0].ActivityID != 1 || !got[0].StartTime().Equal(time.Unix(1700000000, 0)) || len(got[0].RawJSON) == 0 {
		t.Errorf("unexpected summaries %+v", got)
	}
}

func TestStatusErrors(t *testing.T) {
	t.Parallel()
	for status, want := range map[int]error{
		http.StatusUnauthorized:    garmin.ErrUnauthorized,
		http.StatusNotFound:        garmin.ErrNotFound,
		http.StatusGone:            garmin.ErrNotFound,
		http.StatusTooManyRequests: garmin.ErrRateLimited,
	} {
		c, apiURL := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		})
		if _, err := c.DownloadFile(t.Context(), "token", apiURL+"/file"); !errors.Is(err, want) {
			t.Errorf("status %d: got %v, want %v", status, err, want)
		}
	}
}

func TestWorkouts(t *testing.T) {
	t.Parallel()
	var created garmin.Workout
	var scheduled map[string]any
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/training-api/workout":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Error(err)
			}
			_, _ = io.WriteString(w, `{"workoutId":42}`)
		case r.Method == http.MethodPost && r.URL.Path == "/training-api/schedule":
			if err := json.NewDecoder(r.Body).Decode(&scheduled); err != nil {
				t.Error(err)
			}
			_, _ = io.WriteString(w, `7`)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/training-api/workout/"):
			// Already deleted on Garmin.
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
	})
	id, err := c.CreateWorkout(t.Context(), "token", &garmin.Workout{WorkoutName: "Legs", Sport: garmin.SportStrengthTraining})
	if err != nil || id != 42 || created.WorkoutName != "Legs" {
		t.Fatalf("CreateWorkout: got %d, %v, sent %+v", id, err, created)
	}
	scheduleID, err := c.ScheduleWorkout(t.Context(), "token", 42, time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC))
	if err != nil || scheduleID != 7 || scheduled["date"] != "2026-11-05" || scheduled["workoutId"] != float64(42) {
		t.Fatalf("ScheduleWorkout: got %d, %v, sent %v", scheduleID, err, scheduled)
	}
	// Deleting a workout that's gone isn't an error.
	if err := c.DeleteWorkout(t.Context(), "token", 42); err != nil {
		t.Errorf("DeleteWorkout: %v", err)
	}
}

func TestRefreshRevoked(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
	})
	if _, err := c.Refresh(t.Context(), "refresh"); !errors.Is(err, garmin.ErrUnauthorized) {
		t.Errorf("got %v, want ErrUnauthorized", err)
	}
}
