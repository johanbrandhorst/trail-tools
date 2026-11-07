package services_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/bs-tools/trailrunn.ing/internal/activities"
	"github.com/bs-tools/trailrunn.ing/internal/garmin"
	activitiesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/activities/v1"
	garminv1 "github.com/bs-tools/trailrunn.ing/internal/gen/garmin/v1"
	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
	routesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/routes/v1"
	stravav1 "github.com/bs-tools/trailrunn.ing/internal/gen/strava/v1"
	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/bs-tools/trailrunn.ing/internal/store"
	"github.com/bs-tools/trailrunn.ing/internal/strava"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// garminUserID is Garmin's ID of the test user.
const garminUserID = "garmin-user-1"

// fakeGarmin is Garmin's API. Ping notifications' callback URLs return the
// summaries set with addSummaries, and activity files' callback URLs the
// files set with addFile. Workouts are kept as created, and scheduled on a
// date. It counts the files downloaded, so tests can check the data used.
type fakeGarmin struct {
	mu          sync.Mutex
	permissions []string
	summaries   map[string][]*garmin.ActivitySummary
	files       map[string][]byte
	// The callback URLs of the files downloaded.
	downloads []string
	backfills int
	nextID    int64
	// The workouts on the calendar, by ID, and their dates.
	workouts  map[int64]*garmin.Workout
	scheduled map[int64]time.Time
	created   int
	deleted   []int64
	// The courses on the account, by ID.
	courses map[int64]*garmin.Course
	// Whether the app was deregistered, after which tokens are rejected.
	deregistered bool
}

func newFakeGarmin() *fakeGarmin {
	return &fakeGarmin{
		permissions: []string{garmin.PermissionActivityExport, garmin.PermissionWorkoutImport},
		summaries:   map[string][]*garmin.ActivitySummary{},
		files:       map[string][]byte{},
		workouts:    map[int64]*garmin.Workout{},
		scheduled:   map[int64]time.Time{},
		courses:     map[int64]*garmin.Course{},
		nextID:      100,
	}
}

func (f *fakeGarmin) check() error {
	if f.deregistered {
		return garmin.ErrUnauthorized
	}
	return nil
}

func (f *fakeGarmin) AuthCodeURL(state, verifier string) string {
	return "https://connect.garmin.example/oauth2Confirm?state=" + state
}

func (f *fakeGarmin) Exchange(context.Context, string, string) (*garmin.Token, error) {
	return &garmin.Token{AccessToken: "access", RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour)}, nil
}

func (f *fakeGarmin) Refresh(context.Context, string) (*garmin.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.check(); err != nil {
		return nil, err
	}
	return &garmin.Token{AccessToken: "refreshed", RefreshToken: "refresh-2", Expiry: time.Now().Add(time.Hour)}, nil
}

func (f *fakeGarmin) UserID(context.Context, string) (string, error) {
	return garminUserID, nil
}

func (f *fakeGarmin) Permissions(context.Context, string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.permissions), nil
}

func (f *fakeGarmin) Deregister(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deregistered = true
	return nil
}

func (f *fakeGarmin) BackfillActivities(context.Context, string, time.Time, time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.backfills++
	return f.check()
}

func (f *fakeGarmin) FetchActivities(_ context.Context, _, callbackURL string) ([]*garmin.ActivitySummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.check(); err != nil {
		return nil, err
	}
	return f.summaries[callbackURL], nil
}

func (f *fakeGarmin) DownloadFile(_ context.Context, _, callbackURL string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.check(); err != nil {
		return nil, err
	}
	file, ok := f.files[callbackURL]
	if !ok {
		return nil, garmin.ErrNotFound
	}
	f.downloads = append(f.downloads, callbackURL)
	return file, nil
}

func (f *fakeGarmin) CreateWorkout(_ context.Context, _ string, w *garmin.Workout) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.check(); err != nil {
		return 0, err
	}
	f.nextID++
	f.workouts[f.nextID] = w
	f.created++
	return f.nextID, nil
}

func (f *fakeGarmin) DeleteWorkout(_ context.Context, _ string, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.check(); err != nil {
		return err
	}
	if _, ok := f.workouts[id]; !ok {
		return garmin.ErrNotFound
	}
	delete(f.workouts, id)
	delete(f.scheduled, id)
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeGarmin) ScheduleWorkout(_ context.Context, _ string, workoutID int64, date time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.check(); err != nil {
		return 0, err
	}
	f.scheduled[workoutID] = date
	return workoutID + 1000, nil
}

func (f *fakeGarmin) CreateCourse(_ context.Context, _ string, c *garmin.Course) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.check(); err != nil {
		return 0, err
	}
	f.nextID++
	f.courses[f.nextID] = c
	return f.nextID, nil
}

func (f *fakeGarmin) DeleteCourse(_ context.Context, _ string, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.check(); err != nil {
		return err
	}
	delete(f.courses, id)
	return nil
}

// courseNames returns the names of the courses on the account, sorted.
func (f *fakeGarmin) courseNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for _, c := range f.courses {
		names = append(names, c.CourseName)
	}
	slices.Sort(names)
	return names
}

// addSummaries makes a ping's callback URL return the summaries.
func (f *fakeGarmin) addSummaries(callbackURL string, summaries ...*garmin.ActivitySummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.summaries[callbackURL] = summaries
}

// addFile makes an activity file's callback URL return the file.
func (f *fakeGarmin) addFile(callbackURL string, file []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[callbackURL] = file
}

// calendar returns the titles of the workouts on the calendar, by date.
func (f *fakeGarmin) calendar() map[string][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	cal := map[string][]string{}
	for id, date := range f.scheduled {
		key := date.Format(time.DateOnly)
		cal[key] = append(cal[key], f.workouts[id].WorkoutName)
		slices.Sort(cal[key])
	}
	return cal
}

// stepExercises returns the catalog exercises of the steps of the workout
// on Garmin with a name.
func (f *fakeGarmin) stepExercises(name string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var exercises []string
	var walk func(steps []garmin.WorkoutStep)
	walk = func(steps []garmin.WorkoutStep) {
		for _, s := range steps {
			if s.ExerciseCategory != "" {
				exercises = append(exercises, s.ExerciseCategory+"/"+s.ExerciseName)
			}
			walk(s.Steps)
		}
	}
	for _, w := range f.workouts {
		if w.WorkoutName == name {
			for _, segment := range w.Segments {
				walk(segment.Steps)
			}
		}
	}
	return exercises
}

// connectGarmin stores a Garmin connection for the user, as the OAuth
// callback does, with an expired token.
func (e *env) connectGarmin(permissions ...string) {
	e.t.Helper()
	if _, err := e.activities.UpsertGarminConnection(context.Background(), &activities.GarminConnection{
		UserID:       e.userID,
		GarminUserID: garminUserID,
		AccessToken:  "expired",
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(-time.Hour),
		Permissions:  permissions,
	}); err != nil {
		e.t.Fatal(err)
	}
}

// notifyGarmin sends a notification to the webhook, as Garmin does, then
// handles the queue, as the scheduled sync the webhook triggers does, and
// returns the webhook's status code.
func (e *env) notifyGarmin(url string, notification any) int {
	e.t.Helper()
	code := e.postGarmin(url, garminClientID, notification)
	if err := e.garminSyncer.HandleNotifications(context.Background()); err != nil {
		e.t.Fatalf("HandleNotifications: %v", err)
	}
	return code
}

// postGarmin sends a notification to the webhook with a client ID, and
// returns the status code.
func (e *env) postGarmin(url, clientID string, notification any) int {
	e.t.Helper()
	body, err := json.Marshal(notification)
	if err != nil {
		e.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("garmin-client-id", clientID)
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// garminActivity gets the user's activity by its Garmin ID through the API.
func (e *env) garminActivity(garminID int64) *activitiesv1.Activity {
	e.t.Helper()
	stored, err := e.activities.GetGarminActivity(context.Background(), e.userID, garminID)
	if err != nil {
		e.t.Fatalf("GetGarminActivity(%d): %v", garminID, err)
	}
	resp, err := e.activity.GetActivity(context.Background(), activitiesv1.GetActivityRequest_builder{
		Id: store.UUIDToString(stored.ID), IncludeStreams: true,
	}.Build())
	if err != nil {
		e.t.Fatalf("GetActivity: %v", err)
	}
	return resp.GetActivity()
}

func readTestFIT(t *testing.T, name string) []byte {
	t.Helper()
	file, err := os.ReadFile("../fit/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func TestGarminAndStravaInHarmony(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	e.connectGarmin(garmin.PermissionActivityExport)

	// A run recorded by the watch reached Strava first, with its streams.
	start := time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC)
	fromWatch := &strava.Activity{
		ID: 2000, Name: "Morning trail", SportType: "TrailRun", StartDate: start, StartDateLocal: start.Format(time.RFC3339),
		ExternalID: "garmin_ping_5001", Distance: 1500,
	}
	fromWatch.Raw, _ = json.Marshal(fromWatch)
	id, _, err := e.activities.UpsertActivity(ctx, e.userID, &activities.ActivitySummary{
		StravaID: 2000, ExternalID: fromWatch.ExternalID, Name: fromWatch.Name, SportType: "TrailRun",
		StartTime: start, StartDateLocal: fromWatch.LocalDate(), DistanceMeters: 1500, RawJSON: fromWatch.Raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.storeActivityDetail(id, &activities.Detail{Description: "Lovely", RawJSON: []byte("{}")},
		activitiesv1.ActivityStreams_builder{TimeSeconds: []int32{0, 60}, DistanceMeters: []float64{0, 150}}.Build(), nil)

	// Garmin then pushes the same activity, with its name from the watch.
	// It's one activity with both IDs, keeping Strava's name.
	webhook := e.garminWebhookURL
	if code := e.notifyGarmin(webhook, map[string]any{"activities": []map[string]any{{
		"userId": garminUserID, "summaryId": "5001-detail", "activityId": 5001, "activityName": "Squamish Trail Running",
		"activityType": "TRAIL_RUNNING", "startTimeInSeconds": start.Unix() + 3, "durationInSeconds": 600, "distanceInMeters": 1510,
	}}}); code != http.StatusOK {
		t.Fatalf("got status %d for an activity notification, want 200", code)
	}
	both := e.garminActivity(5001)
	if both.GetId() != store.UUIDToString(id) || both.GetStravaId() != "2000" || both.GetGarminActivityId() != "5001" ||
		both.GetName() != "Morning trail" || both.GetDataSource() != activitiesv1.ActivitySource_ACTIVITY_SOURCE_STRAVA {
		t.Errorf("unexpected activity on both %v", both)
	}
	if both.GetGarminUrl() != "https://connect.garmin.com/modern/activity/5001" {
		t.Errorf("got Garmin URL %q", both.GetGarminUrl())
	}
	// Its file isn't downloaded, as Strava already has the samples.
	e.garmin.addFile("https://apis.garmin.example/file/5001", readTestFIT(t, "run.fit"))
	if code := e.notifyGarmin(webhook, map[string]any{"activityFiles": []map[string]any{{
		"userId": garminUserID, "activityId": 5001, "fileType": "FIT", "callbackURL": "https://apis.garmin.example/file/5001",
	}}}); code != http.StatusOK {
		t.Fatalf("got status %d for a file notification, want 200", code)
	}
	if len(e.garmin.downloads) != 0 {
		t.Errorf("downloaded %v, want nothing for an activity Strava has the samples of", e.garmin.downloads)
	}

	// Another run only reaches Garmin, first by a ping. Its file arrives
	// before the summary, and waits for it.
	e.garmin.addFile("https://apis.garmin.example/file/5002", readTestFIT(t, "run.fit"))
	if code := e.notifyGarmin(webhook, map[string]any{"activityFiles": []map[string]any{{
		"userId": garminUserID, "activityId": 5002, "fileType": "FIT", "callbackURL": "https://apis.garmin.example/file/5002",
	}}}); code != http.StatusOK {
		t.Fatalf("got status %d, want 200", code)
	}
	later := start.Add(26 * time.Hour)
	e.garmin.addSummaries("https://apis.garmin.example/ping/1", &garmin.ActivitySummary{
		ActivityID: 5002, ActivityName: "Valley run", ActivityType: "RUNNING", StartTimeInSeconds: later.Unix(),
		StartTimeOffsetInSeconds: -8 * 3600, DurationInSeconds: 600, DistanceInMeters: 1500, RawJSON: []byte(`{"activityId":5002}`),
	})
	if code := e.notifyGarmin(webhook, map[string]any{"activities": []map[string]any{{
		"userId": garminUserID, "callbackURL": "https://apis.garmin.example/ping/1",
	}}}); code != http.StatusOK {
		t.Fatalf("got status %d for a ping, want 200", code)
	}
	if _, err := e.garminAPI.SyncGarmin(ctx, garminv1.SyncGarminRequest_builder{}.Build()); err != nil {
		t.Fatalf("SyncGarmin: %v", err)
	}
	garminOnly := e.garminActivity(5002)
	if garminOnly.GetStravaId() != "" || garminOnly.GetStravaUrl() != "" || garminOnly.GetName() != "Valley run" ||
		garminOnly.GetDataSource() != activitiesv1.ActivitySource_ACTIVITY_SOURCE_GARMIN || !garminOnly.GetHasStreams() ||
		garminOnly.GetStartDateLocal() != "2026-11-06" {
		t.Errorf("unexpected Garmin activity %v", garminOnly)
	}
	if got := len(e.garmin.downloads); got != 1 {
		t.Errorf("downloaded %d files, want 1", got)
	}
	// Its file and summary are kept in blob storage, and its streams come
	// from the file.
	if !e.exists(e.activityKey(mustUUID(t, garminOnly.GetId()), "streams.pb")) {
		t.Error("the Garmin activity's streams weren't stored")
	}
	if got := e.apiStreams(mustUUID(t, garminOnly.GetId())); len(got.GetTimeSeconds()) != 11 {
		t.Errorf("got %d samples, want the file's 11", len(got.GetTimeSeconds()))
	}

	// Strava gets the run later, uploaded from Garmin. It's the same
	// activity: Strava's name wins, but Garmin's samples stay and Strava's
	// streams aren't fetched.
	e.strava.upsertActivity(&strava.Activity{
		ID: 2001, Name: "Valley loop", SportType: "Run", StartDate: later, StartDateLocal: later.Add(-8 * time.Hour).Format(time.RFC3339),
		ExternalID: "garmin_push_5002", Distance: 1490,
	})
	e.connectStrava()
	if _, err := e.stravaAPI.SyncStrava(ctx, stravav1.SyncStravaRequest_builder{}.Build()); err != nil {
		t.Fatalf("SyncStrava: %v", err)
	}
	e.waitForSync()
	merged := e.garminActivity(5002)
	if merged.GetId() != garminOnly.GetId() || merged.GetStravaId() != "2001" || merged.GetName() != "Valley loop" ||
		merged.GetDataSource() != activitiesv1.ActivitySource_ACTIVITY_SOURCE_GARMIN || merged.GetDistanceMeters() != 1500 {
		t.Errorf("unexpected merged activity %v", merged)
	}
	if slices.Contains(e.streamsFetched(), 2001) {
		t.Error("Strava's streams were fetched for an activity with Garmin's samples")
	}
	if got := len(e.apiStreams(mustUUID(t, merged.GetId())).GetTimeSeconds()); got != 11 {
		t.Errorf("got %d samples after Strava's sync, want Garmin's 11", got)
	}

	// Deleting the run on Garmin keeps it, as it's on Strava too.
	if code := e.notifyGarmin(webhook, map[string]any{"deletions": []map[string]any{{"userId": garminUserID, "activityId": 5002}}}); code != http.StatusOK {
		t.Fatalf("got status %d for a deletion, want 200", code)
	}
	kept, err := e.activity.GetActivity(ctx, activitiesv1.GetActivityRequest_builder{Id: merged.GetId()}.Build())
	if err != nil {
		t.Fatalf("GetActivity after deleting on Garmin: %v", err)
	}
	if kept.GetActivity().GetGarminActivityId() != "" || kept.GetActivity().GetStravaId() != "2001" {
		t.Errorf("unexpected activity after deleting on Garmin %v", kept.GetActivity())
	}

	// Notifications for users who aren't connected are ignored, and only
	// the secret path is served.
	if code := e.notifyGarmin(webhook, map[string]any{"activities": []map[string]any{{
		"userId": "someone-else", "activityId": 9999, "activityType": "RUNNING", "startTimeInSeconds": later.Unix(),
	}}}); code != http.StatusOK {
		t.Errorf("got status %d for another user's activity, want 200", code)
	}
	if _, err := e.activities.GetGarminActivity(ctx, e.userID, 9999); err == nil {
		t.Error("stored another Garmin user's activity")
	}
	if code := e.notifyGarmin(e.srv.URL+"/garmin/webhook/wrong-secret-of-enough-length", map[string]any{}); code != http.StatusNotFound {
		t.Errorf("got status %d for the wrong path, want 404", code)
	}
	if code := e.notifyGarmin(webhook, "not a notification"); code != http.StatusBadRequest {
		t.Errorf("got status %d for a malformed notification, want 400", code)
	}
}

func TestGarminStrengthSetsLogWorkout(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	e.connectGarmin(garmin.PermissionActivityExport)

	// Plan a strength workout of split squats at 25 lb.
	created, err := e.exercises.CreateExercises(ctx, plansv1.CreateExercisesRequest_builder{
		Exercises: []*plansv1.Exercise{
			plansv1.Exercise_builder{Name: "Bulgarian split squat"}.Build(),
			plansv1.Exercise_builder{Name: "Goblet squat"}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateExercises: %v", err)
	}
	plan, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{Name: "Strength", StartDate: "2026-11-02", EndDate: "2026-11-30"}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	workouts, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
		PlanId: plan.GetPlan().GetId(),
		Workouts: []*plansv1.Workout{plansv1.Workout_builder{
			Date: "2026-11-05", Title: "Legs", Strength: plansv1.StrengthWorkout_builder{Exercises: []*plansv1.PlannedExercise{
				plansv1.PlannedExercise_builder{ExerciseId: created.GetExercises()[0].GetId(), Sets: 2, Reps: 8, Weight: pounds(25), RestSeconds: 90}.Build(),
				plansv1.PlannedExercise_builder{ExerciseId: created.GetExercises()[1].GetId(), Sets: 1, Reps: 10, Weight: kilograms(10)}.Build(),
			}}.Build(),
		}.Build()},
	}.Build())
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	workoutID := workouts.GetWorkouts()[0].GetId()

	// The watch records it, and Garmin pushes the activity on the same day,
	// which completes the workout, and then its file with the sets.
	start := time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)
	e.notifyGarmin(e.garminWebhookURL, map[string]any{"activities": []map[string]any{{
		"userId": garminUserID, "activityId": 6001, "activityType": "STRENGTH_TRAINING", "startTimeInSeconds": start.Unix(), "durationInSeconds": 1800,
	}}})
	e.garmin.addFile("https://apis.garmin.example/file/6001", readTestFIT(t, "strength.fit"))
	e.notifyGarmin(e.garminWebhookURL, map[string]any{"activityFiles": []map[string]any{{
		"userId": garminUserID, "activityId": 6001, "fileType": "FIT", "callbackURL": "https://apis.garmin.example/file/6001",
	}}})
	// The file is downloaded even though it has no route, for its sets.
	if got := len(e.garmin.downloads); got != 1 {
		t.Fatalf("downloaded %d files, want the strength workout's", got)
	}
	got, err := e.plans.GetWorkout(ctx, plansv1.GetWorkoutRequest_builder{Id: workoutID}.Build())
	if err != nil {
		t.Fatalf("GetWorkout: %v", err)
	}
	log := got.GetWorkout().GetStrength().GetLog()
	if got.GetWorkout().GetCompletedActivityId() == "" || log == nil || log.GetNotes() != "Sets recorded by the watch." {
		t.Fatalf("the watch's sets didn't log the workout: %v", got.GetWorkout())
	}
	first := log.GetExercises()[0]
	if first.GetExerciseId() != created.GetExercises()[0].GetId() || first.GetSets()[0].GetReps() != 8 ||
		first.GetSets()[0].GetWeight().GetUnit() != plansv1.WeightUnit_WEIGHT_UNIT_POUNDS || first.GetSets()[0].GetWeight().GetValue() != 25 {
		t.Errorf("unexpected logged exercise %v", first)
	}
}

func TestGarminCalendar(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// Garmin can't be synced before it's connected.
	_, err := e.garminAPI.SyncGarmin(ctx, garminv1.SyncGarminRequest_builder{}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)
	status, err := e.garminAPI.GetGarminConnection(ctx, garminv1.GetGarminConnectionRequest_builder{}.Build())
	if err != nil || status.GetConnection().GetConnected() || !status.GetConnection().GetConfigured() {
		t.Fatalf("unexpected connection before connecting %v (%v)", status, err)
	}

	// Plan workouts over the coming weeks, before connecting.
	today := time.Now().UTC().Truncate(24 * time.Hour)
	day := func(days int) string { return today.AddDate(0, 0, days).Format(time.DateOnly) }
	plan, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{Name: "Build", StartDate: day(0), EndDate: day(40)}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	planID := plan.GetPlan().GetId()
	exercise, err := e.exercises.CreateExercises(ctx, plansv1.CreateExercisesRequest_builder{
		Exercises: []*plansv1.Exercise{plansv1.Exercise_builder{Name: "Goblet squat"}.Build()},
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	created, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
		PlanId: planID,
		Workouts: []*plansv1.Workout{
			plansv1.Workout_builder{Date: day(1), Title: "Easy", Run: baseRun()}.Build(),
			plansv1.Workout_builder{Date: day(2), Title: "Legs", Strength: plansv1.StrengthWorkout_builder{Exercises: []*plansv1.PlannedExercise{
				plansv1.PlannedExercise_builder{ExerciseId: exercise.GetExercises()[0].GetId(), Sets: 3, Reps: 10}.Build(),
			}}.Build()}.Build(),
			plansv1.Workout_builder{Date: day(3), Title: "Off", Rest: plansv1.RestDay_builder{}.Build()}.Build(),
			// Too far ahead for the calendar.
			plansv1.Workout_builder{Date: day(30), Title: "Later", Run: baseRun()}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}
	easy, legs := created.GetWorkouts()[0], created.GetWorkouts()[1]
	if e.garmin.created != 0 {
		t.Fatalf("sent %d workouts to Garmin before connecting", e.garmin.created)
	}

	// Connecting puts the runs and strength workouts of the coming two
	// weeks on the calendar, but not rest days or later workouts.
	e.connectGarmin(garmin.PermissionActivityExport, garmin.PermissionWorkoutImport)
	resp, err := e.garminAPI.SyncGarmin(ctx, garminv1.SyncGarminRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("SyncGarmin: %v", err)
	}
	want := map[string][]string{day(1): {"Easy"}, day(2): {"Legs"}}
	if got := e.garmin.calendar(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got calendar %v, want %v", got, want)
	}
	if conn := resp.GetConnection(); !conn.GetConnected() || !conn.GetWorkoutsAllowed() || conn.GetWorkoutCount() != 2 || !conn.HasWorkoutsSyncTime() {
		t.Errorf("unexpected connection %v", conn)
	}

	// The strength workout's exercise is Garmin's, matched by its name.
	if got := e.garmin.stepExercises("Legs"); fmt.Sprint(got) != "[SQUAT/GOBLET_SQUAT]" {
		t.Errorf("got exercises %v on Garmin, want the catalog's goblet squat", got)
	}

	// Syncing again sends nothing, as nothing changed.
	if _, err := e.garminAPI.SyncGarmin(ctx, garminv1.SyncGarminRequest_builder{}.Build()); err != nil {
		t.Fatalf("SyncGarmin: %v", err)
	}
	if e.garmin.created != 2 {
		t.Errorf("created %d workouts, want 2 with nothing changed", e.garmin.created)
	}

	// Moving a workout replaces it on the calendar right away.
	moved := plansv1.Workout_builder{Id: easy.GetId(), Date: day(4)}.Build()
	if _, err := e.plans.UpdateWorkout(ctx, plansv1.UpdateWorkoutRequest_builder{
		Workout: moved, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"date"}},
	}.Build()); err != nil {
		t.Fatalf("UpdateWorkout: %v", err)
	}
	want = map[string][]string{day(2): {"Legs"}, day(4): {"Easy"}}
	if got := e.garmin.calendar(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got calendar %v after moving a workout, want %v", got, want)
	}
	// Deleting one removes it right away.
	if _, err := e.plans.DeleteWorkouts(ctx, plansv1.DeleteWorkoutsRequest_builder{PlanId: planID, Ids: []string{legs.GetId()}}.Build()); err != nil {
		t.Fatalf("DeleteWorkouts: %v", err)
	}
	want = map[string][]string{day(4): {"Easy"}}
	if got := e.garmin.calendar(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got calendar %v after deleting a workout, want %v", got, want)
	}

	// Agents can't disconnect Garmin, only the web app's session.
	_, err = e.garminAPI.DisconnectGarmin(ctx, garminv1.DisconnectGarminRequest_builder{}.Build())
	wantCode(t, err, connect.CodePermissionDenied)
	if _, err := e.garminSession.DisconnectGarmin(ctx, garminv1.DisconnectGarminRequest_builder{}.Build()); err != nil {
		t.Fatalf("DisconnectGarmin: %v", err)
	}
	// Disconnecting removes the upcoming workouts and deregisters the app.
	if got := e.garmin.calendar(); len(got) != 0 || !e.garmin.deregistered {
		t.Errorf("got calendar %v and deregistered %t after disconnecting", got, e.garmin.deregistered)
	}
	status, err = e.garminAPI.GetGarminConnection(ctx, garminv1.GetGarminConnectionRequest_builder{}.Build())
	if err != nil || status.GetConnection().GetConnected() {
		t.Errorf("unexpected connection after disconnecting %v (%v)", status, err)
	}
}

func TestGarminDeregistrationAndPermissions(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()
	e.connectGarmin(garmin.PermissionActivityExport, garmin.PermissionWorkoutImport)

	// The user takes away the calendar permission on Garmin.
	e.notifyGarmin(e.garminWebhookURL, map[string]any{"userPermissionsChange": []map[string]any{{
		"userId": garminUserID, "permissions": []string{garmin.PermissionActivityExport},
	}}})
	status, err := e.garminAPI.GetGarminConnection(ctx, garminv1.GetGarminConnectionRequest_builder{}.Build())
	if err != nil || !status.GetConnection().GetActivitiesAllowed() || status.GetConnection().GetWorkoutsAllowed() {
		t.Errorf("unexpected connection after a permission change %v (%v)", status, err)
	}

	// Notifications are answered first and handled after: queued ones wait
	// for the scheduled sync, which a notification triggers once.
	runs := e.garminTrigger.count()
	for range 2 {
		if code := e.postGarmin(e.garminWebhookURL, garminClientID, map[string]any{"userPermissionsChange": []map[string]any{{
			"userId": garminUserID, "permissions": []string{garmin.PermissionActivityExport, garmin.PermissionWorkoutImport},
		}}}); code != http.StatusOK {
			t.Fatalf("got status %d, want 200", code)
		}
	}
	if got := e.garminTrigger.count() - runs; got != 1 {
		t.Errorf("triggered the sync %d times for two notifications, want once", got)
	}
	status, err = e.garminAPI.GetGarminConnection(ctx, garminv1.GetGarminConnectionRequest_builder{}.Build())
	if err != nil || status.GetConnection().GetWorkoutsAllowed() {
		t.Errorf("handled a notification before the sync: %v (%v)", status, err)
	}
	if err := e.garminSyncer.HandleNotifications(ctx); err != nil {
		t.Fatalf("HandleNotifications: %v", err)
	}
	status, err = e.garminAPI.GetGarminConnection(ctx, garminv1.GetGarminConnectionRequest_builder{}.Build())
	if err != nil || !status.GetConnection().GetWorkoutsAllowed() {
		t.Errorf("unexpected connection after the sync %v (%v)", status, err)
	}
	// Notifications for another app are rejected.
	if code := e.postGarmin(e.garminWebhookURL, "another-client", map[string]any{"deregistrations": []map[string]any{{"userId": garminUserID}}}); code != http.StatusForbidden {
		t.Errorf("got status %d for another client ID, want 403", code)
	}

	// Then disconnects the app on Garmin, which forgets the connection.
	e.notifyGarmin(e.garminWebhookURL, map[string]any{"deregistrations": []map[string]any{{"userId": garminUserID}}})
	status, err = e.garminAPI.GetGarminConnection(ctx, garminv1.GetGarminConnectionRequest_builder{}.Build())
	if err != nil || status.GetConnection().GetConnected() {
		t.Errorf("unexpected connection after deregistering %v (%v)", status, err)
	}

	// A Garmin account can only be connected to one user.
	_, err = e.activities.UpsertGarminConnection(ctx, &activities.GarminConnection{
		UserID: e.otherUserID, GarminUserID: "shared", AccessToken: "a", RefreshToken: "r", Expiry: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.activities.UpsertGarminConnection(ctx, &activities.GarminConnection{
		UserID: e.userID, GarminUserID: "shared", AccessToken: "a", RefreshToken: "r", Expiry: time.Now(),
	})
	if err != activities.ErrGarminAccountInUse {
		t.Errorf("got %v connecting another user's Garmin account, want ErrGarminAccountInUse", err)
	}
}

// count returns how often the trigger ran.
func (f *fakeTrigger) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs
}

// mustUUID parses an ID.
func mustUUID(t *testing.T, id string) pgtype.UUID {
	t.Helper()
	u, err := store.StringToUUID(id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestGarminExercises(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// Agents find the exercise of Garmin's catalog to set.
	found, err := e.exercises.SearchGarminExercises(ctx, plansv1.SearchGarminExercisesRequest_builder{Query: "bulgarian split squat", PageSize: 5}.Build())
	if err != nil {
		t.Fatalf("SearchGarminExercises: %v", err)
	}
	if got := found.GetExercises(); len(got) == 0 || got[0].GetCategory() != "LUNGE" || got[0].GetId() != got[0].GetCategory()+"/"+got[0].GetName() {
		t.Fatalf("unexpected exercises %v", got)
	}
	// Only exercises of the catalog can be set, in any case.
	_, err = e.exercises.CreateExercises(ctx, plansv1.CreateExercisesRequest_builder{
		Exercises: []*plansv1.Exercise{plansv1.Exercise_builder{Name: "Copenhagen plank", GarminExercise: "PLANK/COPENHAGEN"}.Build()},
	}.Build())
	wantCode(t, err, connect.CodeInvalidArgument)
	created, err := e.exercises.CreateExercises(ctx, plansv1.CreateExercisesRequest_builder{
		Exercises: []*plansv1.Exercise{plansv1.Exercise_builder{Name: "Split squat", GarminExercise: "lunge/dumbbell_bulgarian_split_squat"}.Build()},
	}.Build())
	if err != nil {
		t.Fatalf("CreateExercises: %v", err)
	}
	if got := created.GetExercises()[0].GetGarminExercise(); got != "LUNGE/DUMBBELL_BULGARIAN_SPLIT_SQUAT" {
		t.Errorf("got Garmin exercise %q", got)
	}
}

func TestGarminCourses(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// Two routes, one for a run this week and one for a race next month.
	newRoute := func(name string, waypoints ...float64) string {
		created, err := e.routes.CreateRoute(ctx, routesv1.CreateRouteRequest_builder{
			Name:      name,
			Waypoints: routesv1.Waypoints_builder{Waypoints: latLngs(waypoints...)}.Build(),
		}.Build())
		if err != nil {
			t.Fatalf("CreateRoute: %v", err)
		}
		return created.GetRoute().GetId()
	}
	hill := newRoute("Hill loop", 0, 0, 0.01, 0, 0.02, 0)
	race := newRoute("Race course", 0, 0, 0.03, 0)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	day := func(days int) string { return today.AddDate(0, 0, days).Format(time.DateOnly) }
	plan, err := e.plans.CreateTrainingPlan(ctx, plansv1.CreateTrainingPlanRequest_builder{
		Plan: plansv1.TrainingPlan_builder{Name: "Build", StartDate: day(0), EndDate: day(60)}.Build(),
	}.Build())
	if err != nil {
		t.Fatal(err)
	}
	run := baseRun()
	run.SetRouteId(hill)
	later := baseRun()
	later.SetRouteId(race)
	created, err := e.plans.CreateWorkouts(ctx, plansv1.CreateWorkoutsRequest_builder{
		PlanId: plan.GetPlan().GetId(),
		Workouts: []*plansv1.Workout{
			plansv1.Workout_builder{Date: day(2), Title: "Hills", Run: run}.Build(),
			plansv1.Workout_builder{Date: day(40), Title: "Course recce", Run: later}.Build(),
		},
	}.Build())
	if err != nil {
		t.Fatalf("CreateWorkouts: %v", err)
	}

	// Only the routes of the coming two weeks become courses, with points at
	// most 100 m apart, once the user allows it.
	e.connectGarmin(garmin.PermissionWorkoutImport)
	if _, err := e.garminAPI.SyncGarmin(ctx, garminv1.SyncGarminRequest_builder{}.Build()); err != nil {
		t.Fatalf("SyncGarmin: %v", err)
	}
	if got := e.garmin.courseNames(); len(got) != 0 {
		t.Fatalf("put courses %v on Garmin without the permission", got)
	}
	e.connectGarmin(garmin.PermissionWorkoutImport, garmin.PermissionCourseImport)
	resp, err := e.garminAPI.SyncGarmin(ctx, garminv1.SyncGarminRequest_builder{}.Build())
	if err != nil {
		t.Fatalf("SyncGarmin: %v", err)
	}
	if got := e.garmin.courseNames(); fmt.Sprint(got) != "[Hill loop]" {
		t.Fatalf("got courses %v, want the hill loop", got)
	}
	if conn := resp.GetConnection(); !conn.GetCoursesAllowed() || conn.GetCourseCount() != 1 {
		t.Errorf("unexpected connection %v", conn)
	}
	e.garmin.mu.Lock()
	for _, c := range e.garmin.courses {
		if len(c.GeoPoints) < 23 || c.ActivityType != garmin.CourseTrailRunning || !approx(c.DistanceMeters, 2224, 5) || c.CoordinateSystem != "WGS84" {
			t.Errorf("unexpected course %+v", c)
		}
		for i := 1; i < len(c.GeoPoints); i++ {
			a, b := c.GeoPoints[i-1], c.GeoPoints[i]
			if d := geo.Distance(geo.Point{Lat: a.Latitude, Lng: a.Longitude}, geo.Point{Lat: b.Latitude, Lng: b.Longitude}); d > garmin.MaxCoursePointSpacingMeters+0.5 {
				t.Errorf("points %d and %d are %.0f m apart", i-1, i, d)
			}
		}
	}
	e.garmin.mu.Unlock()

	// Syncing again sends nothing; moving the race's workout closer sends
	// its course, and moving the run away removes its course.
	if _, err := e.garminAPI.SyncGarmin(ctx, garminv1.SyncGarminRequest_builder{}.Build()); err != nil {
		t.Fatalf("SyncGarmin: %v", err)
	}
	if got := len(e.garmin.courses); got != 1 {
		t.Errorf("got %d courses after syncing again, want 1", got)
	}
	mask := &fieldmaskpb.FieldMask{Paths: []string{"date"}}
	for id, date := range map[string]string{created.GetWorkouts()[1].GetId(): day(3), created.GetWorkouts()[0].GetId(): day(30)} {
		if _, err := e.plans.UpdateWorkout(ctx, plansv1.UpdateWorkoutRequest_builder{
			Workout: plansv1.Workout_builder{Id: id, Date: date}.Build(), UpdateMask: mask,
		}.Build()); err != nil {
			t.Fatalf("UpdateWorkout: %v", err)
		}
	}
	if got := e.garmin.courseNames(); fmt.Sprint(got) != "[Race course]" {
		t.Errorf("got courses %v after moving the workouts, want the race course", got)
	}

	// A route sent to Garmin stays there without a workout coming up, and
	// syncing keeps it.
	sent, err := e.garminAPI.SendRouteToGarmin(ctx, garminv1.SendRouteToGarminRequest_builder{RouteId: hill}.Build())
	if err != nil {
		t.Fatalf("SendRouteToGarmin: %v", err)
	}
	if st := sent.GetStatus(); !st.GetOnGarmin() || !st.GetSent() {
		t.Errorf("got status %v after sending, want it sent", st)
	}
	if _, err := e.garminAPI.SyncGarmin(ctx, garminv1.SyncGarminRequest_builder{}.Build()); err != nil {
		t.Fatalf("SyncGarmin: %v", err)
	}
	if got := e.garmin.courseNames(); fmt.Sprint(got) != "[Hill loop Race course]" {
		t.Errorf("got courses %v after sending the hill loop and syncing, want both", got)
	}
	// Sending the route of an upcoming workout marks it sent without
	// sending it again, and removing it keeps it for the workout.
	calls := len(e.garmin.courses)
	if _, err := e.garminAPI.SendRouteToGarmin(ctx, garminv1.SendRouteToGarminRequest_builder{RouteId: race}.Build()); err != nil {
		t.Fatalf("SendRouteToGarmin: %v", err)
	}
	if got := len(e.garmin.courses); got != calls {
		t.Errorf("got %d courses after sending an unchanged route, want %d", got, calls)
	}
	removed, err := e.garminAPI.RemoveRouteFromGarmin(ctx, garminv1.RemoveRouteFromGarminRequest_builder{RouteId: race}.Build())
	if err != nil {
		t.Fatalf("RemoveRouteFromGarmin: %v", err)
	}
	if st := removed.GetStatus(); !st.GetOnGarmin() || st.GetSent() {
		t.Errorf("got status %v after removing an upcoming route, want it kept for the workout", st)
	}
	// Removing the hill loop takes it off Garmin.
	if _, err := e.garminAPI.RemoveRouteFromGarmin(ctx, garminv1.RemoveRouteFromGarminRequest_builder{RouteId: hill}.Build()); err != nil {
		t.Fatalf("RemoveRouteFromGarmin: %v", err)
	}
	if got := e.garmin.courseNames(); fmt.Sprint(got) != "[Race course]" {
		t.Errorf("got courses %v after removing the hill loop, want the race course", got)
	}
	status, err := e.garminAPI.GetRouteGarminStatus(ctx, garminv1.GetRouteGarminStatusRequest_builder{RouteId: hill}.Build())
	if err != nil || status.GetStatus().GetOnGarmin() {
		t.Errorf("GetRouteGarminStatus after removing: %v, %v", status, err)
	}
	// Another user's route isn't found.
	_, err = e.garminAPI.SendRouteToGarmin(ctx, garminv1.SendRouteToGarminRequest_builder{RouteId: "00000000-0000-0000-0000-000000000000"}.Build())
	wantCode(t, err, connect.CodeNotFound)

	// Disconnecting removes the courses, and routes can't be sent without a
	// connection.
	if _, err := e.garminSession.DisconnectGarmin(ctx, garminv1.DisconnectGarminRequest_builder{}.Build()); err != nil {
		t.Fatalf("DisconnectGarmin: %v", err)
	}
	if got := e.garmin.courseNames(); len(got) != 0 {
		t.Errorf("got courses %v after disconnecting", got)
	}
	_, err = e.garminAPI.SendRouteToGarmin(ctx, garminv1.SendRouteToGarminRequest_builder{RouteId: hill}.Build())
	wantCode(t, err, connect.CodeFailedPrecondition)
}
