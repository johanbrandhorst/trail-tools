package services_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/blob"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// activityKey returns the key of an object of one of the user's activities.
func (e *env) activityKey(activityID pgtype.UUID, name string) string {
	return "users/" + store.UUIDToString(e.userID) + "/activities/" + store.UUIDToString(activityID) + "/" + name
}

// responseKey returns the key of one of Strava's responses about one of the
// user's activities.
func (e *env) responseKey(stravaID, name string) string {
	return "users/" + store.UUIDToString(e.userID) + "/strava/activities/" + stravaID + "/" + name + ".json.gz"
}

// response returns one of Strava's responses from the blob store, decoded,
// or nil if there is none.
func (e *env) response(key string) any {
	e.t.Helper()
	data, err := e.activityBlobs.Get(context.Background(), key)
	if errors.Is(err, blob.ErrNotFound) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		e.t.Fatal(err)
	}
	decoded, err := io.ReadAll(zr)
	if err != nil {
		e.t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(decoded, &v); err != nil {
		e.t.Fatal(err)
	}
	return v
}

// exists returns whether an object exists in the activity blob store.
func (e *env) exists(key string) bool {
	e.t.Helper()
	_, err := e.activityBlobs.Get(context.Background(), key)
	if err != nil && !errors.Is(err, blob.ErrNotFound) {
		e.t.Fatal(err)
	}
	return err == nil
}

// apiStreams gets the streams of one of the user's activities through the
// API.
func (e *env) apiStreams(activityID pgtype.UUID) *activitiesv1.ActivityStreams {
	e.t.Helper()
	resp, err := e.activity.GetActivity(context.Background(), activitiesv1.GetActivityRequest_builder{
		Id:              store.UUIDToString(activityID),
		IncludeStreams:  true,
		MaxStreamPoints: 1000,
	}.Build())
	if err != nil {
		e.t.Fatal(err)
	}
	return resp.GetStreams()
}

func TestActivityBlobs(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// An activity's streams and Strava's responses are in the blob store.
	var track []geo.Point
	for i := range 100 {
		track = append(track, geo.Point{Lat: 46 + float64(i)*0.0001, Lng: 7.000123})
	}
	run := e.addTrackedActivity(7, track[:2], track)
	e.storeActivityDetail(run, &activities.Detail{
		Description: "Up and down",
		RawJSON:     []byte(`{"id":7,"name":"Run"}`),
		Photos: []activities.Photo{
			{StravaID: "p1", URL: "https://cdn.example/p1.jpg", RawJSON: []byte(`{"unique_id":"p1"}`)},
		},
	}, streamsOf(track), nil)
	if !e.exists(e.activityKey(run, "streams.pb")) {
		t.Error("the streams weren't stored in the blob store")
	}
	if got := e.response(e.responseKey("7", "summary")); got == nil {
		t.Error("the summary response wasn't stored")
	}
	if got, ok := e.response(e.responseKey("7", "detail")).(map[string]any); !ok || got["name"] != "Run" {
		t.Errorf("got detail response %v, want the stored one", got)
	}
	if got, ok := e.response(e.responseKey("7", "photos")).([]any); !ok || len(got) != 1 {
		t.Errorf("got photo responses %v, want one", got)
	}
	if got := e.apiStreams(run).GetLat(); len(got) != 100 || got[99] != track[99].Lat {
		t.Errorf("got %d latitudes, want the track's 100", len(got))
	}
	// Photos removed on Strava are removed from the responses.
	e.storeActivityDetail(run, &activities.Detail{RawJSON: []byte(`{"id":7}`)}, streamsOf(track), nil)
	if e.exists(e.responseKey("7", "photos")) {
		t.Error("the photo responses are still stored after the photos were removed")
	}

	// Deleting an activity deletes its data.
	if err := e.activities.DeleteActivityByStravaID(ctx, e.userID, 7); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{e.activityKey(run, "streams.pb"), e.responseKey("7", "summary"), e.responseKey("7", "detail"), e.responseKey("7", "photos")} {
		if e.exists(key) {
			t.Errorf("%s exists after deleting the activity", key)
		}
	}
}

// streamsOf returns streams with the positions of a track.
func streamsOf(track []geo.Point) *activitiesv1.ActivityStreams {
	streams := &activitiesv1.ActivityStreams{}
	for i, p := range track {
		streams.SetTimeSeconds(append(streams.GetTimeSeconds(), int32(i)))
		streams.SetLat(append(streams.GetLat(), p.Lat))
		streams.SetLng(append(streams.GetLng(), p.Lng))
	}
	return streams
}
