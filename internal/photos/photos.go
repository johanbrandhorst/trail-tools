// Package photos turns uploaded photos into the images stored for trip and
// race reports, stores them in the blob store and serves them, and serves the
// links AI agents upload photos with.
package photos

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/blob"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/images"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

const (
	// MaxBytes limits the size of uploaded photos. Browsers scale photos down
	// before uploading them, so theirs are much smaller, but agents upload
	// photos as the camera took them.
	MaxBytes = 20 << 20
	// SizeLarge and SizeThumb are the sizes images are stored and served in.
	SizeLarge = "large"
	SizeThumb = "thumb"
	// largeSize and thumbSize are the maximum width and height of the stored
	// images.
	largeSize = 2048
	thumbSize = 480
)

// Processed is an uploaded photo turned into the images that are stored.
type Processed struct {
	// The large image, a JPEG of at most 2048 pixels, and its size.
	Large         []byte
	Width, Height int
	// The thumbnail, a JPEG of at most 480 pixels.
	Thumb []byte
	// What the upload's metadata says about where and when the photo was
	// taken. The stored images have no metadata.
	Metadata images.Metadata
}

// Process decodes an uploaded JPEG, PNG or WebP photo, turns it upright, and
// encodes it as a JPEG of at most 2048 pixels and a thumbnail of at most 480
// pixels. Re-encoding drops the upload's metadata, such as its GPS position,
// which it returns instead. It waits while other images are decoded, as
// images.Decode does, and returns ctx's error if it's done meanwhile. Its
// other errors say why the upload isn't a usable photo, for users.
func Process(ctx context.Context, data []byte) (*Processed, error) {
	processed := &Processed{Metadata: images.ReadMetadata(data)}
	err := images.Decode(ctx, data, func(img image.Image) error {
		// Scaling first turns fewer pixels, and the size bounds both sides.
		large := images.Orient(images.ScaleDown(img, largeSize), processed.Metadata.Orientation)
		var err error
		if processed.Large, err = images.EncodeJPEG(large, 85); err != nil {
			return err
		}
		if processed.Thumb, err = images.EncodeJPEG(images.ScaleDown(large, thumbSize), 80); err != nil {
			return err
		}
		processed.Width, processed.Height = large.Bounds().Dx(), large.Bounds().Dy()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return processed, nil
}

// Store stores the images of photos in a blob store, under
// <prefix>/<random key>/<size>.jpg.
type Store struct {
	blobs  blob.Store
	prefix string
}

// NewStore returns a Store keeping images in blobs, with keys starting with
// prefix, e.g. "trip-photos".
func NewStore(blobs blob.Store, prefix string) *Store {
	return &Store{blobs: blobs, prefix: prefix}
}

// imageKey returns the key of a photo's image of a size.
func imageKey(objectKey, size string) string {
	return objectKey + "/" + size + ".jpg"
}

// Put stores the images of a processed photo under a new object key, which
// it returns.
func (s *Store) Put(ctx context.Context, p *Processed) (string, error) {
	objectKey := s.prefix + "/" + strings.ToLower(rand.Text())
	if err := s.blobs.Put(ctx, imageKey(objectKey, SizeLarge), p.Large, "image/jpeg"); err != nil {
		return "", fmt.Errorf("failed to store the large image: %w", err)
	}
	if err := s.blobs.Put(ctx, imageKey(objectKey, SizeThumb), p.Thumb, "image/jpeg"); err != nil {
		// The photo isn't added, so nothing refers to the large image.
		return "", errors.Join(fmt.Errorf("failed to store the thumbnail: %w", err), s.blobs.Delete(ctx, imageKey(objectKey, SizeLarge)))
	}
	return objectKey, nil
}

// Get returns a photo's image of a size.
func (s *Store) Get(ctx context.Context, objectKey, size string) ([]byte, error) {
	return s.blobs.Get(ctx, imageKey(objectKey, size))
}

// Delete deletes the images of a photo.
func (s *Store) Delete(ctx context.Context, objectKey string) error {
	var errs []error
	for _, size := range []string{SizeLarge, SizeThumb} {
		if err := s.blobs.Delete(ctx, imageKey(objectKey, size)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Access is what's needed to serve a photo: where its images are, and who
// can see them.
type Access struct {
	ObjectKey  string
	CreateTime time.Time
	// The author of the report.
	UserID pgtype.UUID
	// Whether the report is published.
	Published bool
}

// Handler serves photos at <path><id>/<size>.jpg, where size is "large" or
// "thumb". access returns what's needed to serve a photo, or
// store.ErrNotFound if it doesn't exist. It must be mounted at path behind
// the authn middleware. Photos of published reports are served to anyone,
// photos of drafts only to their author. A photo's images never change, so
// they can be cached for a long time.
func (s *Store) Handler(report *errreport.Reporter, path string, access func(ctx context.Context, photoID pgtype.UUID) (*Access, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id, file, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, path), "/")
		size, isJPEG := strings.CutSuffix(file, ".jpg")
		if !ok || !isJPEG || (size != SizeLarge && size != SizeThumb) {
			http.NotFound(w, r)
			return
		}
		photoID, err := store.StringToUUID(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		a, err := access(r.Context(), photoID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			report.Report(r.Context(), "Failed to get photo", err)
			http.Error(w, "failed to get photo", http.StatusInternalServerError)
			return
		}
		cacheControl := "public, max-age=31536000, immutable"
		if !a.Published {
			// Drafts are only visible to their author, as if they didn't
			// exist for anyone else.
			if userID, ok := authn.GetUserID(r.Context()); !ok || userID != a.UserID {
				http.NotFound(w, r)
				return
			}
			cacheControl = "private, max-age=31536000, immutable"
		}
		data, err := s.Get(r.Context(), a.ObjectKey, size)
		if err != nil {
			if errors.Is(err, blob.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			report.Report(r.Context(), "Failed to read photo", err, slog.String("key", a.ObjectKey))
			http.Error(w, "failed to read photo", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", cacheControl)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("ETag", `"`+id+"-"+size+`"`)
		http.ServeContent(w, r, "", a.CreateTime, bytes.NewReader(data))
	})
}

// NewUploadToken returns the secret token of a new upload link, and its
// hash, which is stored instead so that the database can't be used to
// upload.
func NewUploadToken() (token string, hash []byte) {
	token = rand.Text()
	return token, HashUploadToken(token)
}

// HashUploadToken returns the hash of an upload link's token.
func HashUploadToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
