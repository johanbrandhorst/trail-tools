package activities

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"strconv"
	"sync"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/blob"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// Activities' streams and Strava's raw responses about them are stored in
// the blob store rather than the database, as they're written once, read
// whole by key and never queried. Their keys start with the user's ID, so
// all of a user's data can be found by its prefix.

// Strava's responses about an activity, stored as gzipped JSON.
const (
	summaryResponse = "summary"
	detailResponse  = "detail"
	// The photos, as an array.
	photosResponse = "photos"
)

// responseNames are the names of all of Strava's responses about an
// activity.
var responseNames = []string{summaryResponse, detailResponse, photosResponse}

// blobConcurrency is how many objects are stored or deleted at once.
const blobConcurrency = 8

// streamsKey returns the key of an activity's streams, encoded by
// encodeStreams.
func streamsKey(userID, activityID pgtype.UUID) string {
	return "users/" + store.UUIDToString(userID) + "/activities/" + store.UUIDToString(activityID) + "/streams.pb"
}

// responseKey returns the key of one of Strava's responses about an
// activity. It uses Strava's ID of the activity, which is known before the
// activity is stored, so the response can be stored first.
func responseKey(userID pgtype.UUID, stravaID int64, name string) string {
	return "users/" + store.UUIDToString(userID) + "/strava/activities/" + strconv.FormatInt(stravaID, 10) + "/" + name + ".json.gz"
}

// object is an object to store.
type object struct {
	key         string
	data        []byte
	contentType string
}

// streamsObject returns the object of an activity's encoded streams.
func streamsObject(userID, activityID pgtype.UUID, encoded []byte) object {
	return object{key: streamsKey(userID, activityID), data: encoded, contentType: "application/x-protobuf"}
}

// responseObject returns the object of one of Strava's responses, gzipped,
// as JSON compresses several times over.
func responseObject(userID pgtype.UUID, stravaID int64, name string, response []byte) object {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	// Writes to a bytes.Buffer don't fail.
	_, _ = zw.Write(response)
	_ = zw.Close()
	return object{key: responseKey(userID, stravaID, name), data: buf.Bytes(), contentType: "application/gzip"}
}

// putObjects stores objects, a few at a time. With create, objects that
// exist are left as they are, as when moving data that a sync may meanwhile
// have stored anew.
func (r *Repository) putObjects(ctx context.Context, objects []object, create bool) error {
	return forEach(len(objects), func(i int) error {
		o := objects[i]
		if !create {
			return r.blobs.Put(ctx, o.key, o.data, o.contentType)
		}
		if err := r.blobs.Create(ctx, o.key, o.data, o.contentType); err != nil && !errors.Is(err, blob.ErrExists) {
			return err
		}
		return nil
	})
}

// deleteObjects deletes objects, a few at a time.
func (r *Repository) deleteObjects(ctx context.Context, keys []string) error {
	return forEach(len(keys), func(i int) error {
		return r.blobs.Delete(ctx, keys[i])
	})
}

// forEach calls fn for each index up to n, blobConcurrency at a time, and
// returns their errors.
func forEach(n int, fn func(i int) error) error {
	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup
	sem := make(chan struct{}, blobConcurrency)
	for i := range n {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			if err := fn(i); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}
