package user

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"image"
	"net/http"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/blob"
	usersv1 "github.com/johanbrandhorst/trail-tools/internal/gen/users/v1"
	"github.com/johanbrandhorst/trail-tools/internal/images"
	"github.com/johanbrandhorst/trail-tools/internal/users"
)

const (
	// AvatarPath is where avatar images are served, at
	// /avatars/<key>.jpg.
	AvatarPath = "/avatars/"
	// maxAvatarBytes limits the size of uploaded avatar pictures.
	maxAvatarBytes = 10 << 20
	// avatarSize is the width and height of the stored avatar images.
	avatarSize = 256
)

// avatarKeyRe matches avatar keys, as made by newAvatarKey.
var avatarKeyRe = regexp.MustCompile(`^[a-z2-7]{26}$`)

// AvatarURL returns the URL of the avatar image with the key, or "" if the
// key is empty.
func AvatarURL(key string) string {
	if key == "" {
		return ""
	}
	return AvatarPath + key + ".jpg"
}

// avatarObjectKey returns the blob store key of the avatar image with the
// key.
func avatarObjectKey(key string) string {
	return "avatars/" + key + ".jpg"
}

func (s *Service) UploadAvatar(ctx context.Context, req *usersv1.UploadAvatarRequest) (*usersv1.UploadAvatarResponse, error) {
	user, ok := authn.GetUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if len(req.GetData()) > maxAvatarBytes {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "the picture must be at most %d MB", maxAvatarBytes>>20)
	}
	var data []byte
	var encodeErr error
	if err := images.Decode(ctx, req.GetData(), func(img image.Image) error {
		data, encodeErr = images.EncodeJPEG(images.Square(img, avatarSize), 85)
		return nil
	}); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if encodeErr != nil {
		return nil, s.toConnectError(ctx, encodeErr, "failed to encode avatar")
	}
	// Each avatar gets a new key, so its image can be cached forever.
	key := strings.ToLower(rand.Text())
	if err := s.blobs.Put(ctx, avatarObjectKey(key), data, "image/jpeg"); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to store avatar")
	}
	old, err := s.avatars.SetAvatarKey(ctx, user.ID(), key)
	if err != nil {
		s.deleteAvatar(ctx, key)
		return nil, s.toConnectError(ctx, err, "failed to set avatar")
	}
	s.deleteAvatar(ctx, old)
	u, err := userWithAvatar(user, key)
	if err != nil {
		return nil, err
	}
	return usersv1.UploadAvatarResponse_builder{User: u}.Build(), nil
}

func (s *Service) DeleteAvatar(ctx context.Context, _ *usersv1.DeleteAvatarRequest) (*usersv1.DeleteAvatarResponse, error) {
	user, ok := authn.GetUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	old, err := s.avatars.SetAvatarKey(ctx, user.ID(), "")
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to remove avatar")
	}
	s.deleteAvatar(ctx, old)
	u, err := userWithAvatar(user, "")
	if err != nil {
		return nil, err
	}
	return usersv1.DeleteAvatarResponse_builder{User: u}.Build(), nil
}

// userWithAvatar returns the user with the avatar key, as the user in the
// context was loaded before the avatar changed.
func userWithAvatar(user users.User, key string) (*usersv1.User, error) {
	if u, ok := user.(*users.PasskeyUser); ok {
		withAvatar := *u
		withAvatar.AvatarKey = key
		user = &withAvatar
	}
	return userToProto(user)
}

// deleteAvatar deletes the image of a replaced or removed avatar. Failures
// are logged, as they only leave an unused image behind.
func (s *Service) deleteAvatar(ctx context.Context, key string) {
	if key == "" {
		return
	}
	if err := s.blobs.Delete(context.WithoutCancel(ctx), avatarObjectKey(key)); err != nil {
		s.report.Report(ctx, "Failed to delete avatar image", err)
	}
}

// AvatarHandler serves avatar images at /avatars/<key>.jpg. Avatars are
// public, like the profiles they're shown on, and their keys can't be
// guessed. An avatar's image never changes, so it can be cached forever.
func (s *Service) AvatarHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		key, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, AvatarPath), ".jpg")
		if !ok || !avatarKeyRe.MatchString(key) {
			http.NotFound(w, r)
			return
		}
		data, err := s.blobs.Get(r.Context(), avatarObjectKey(key))
		if err != nil {
			if errors.Is(err, blob.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			s.report.Report(r.Context(), "Failed to read avatar", err)
			http.Error(w, "failed to read avatar", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("ETag", `"`+key+`"`)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	})
}

func (s *Service) toConnectError(ctx context.Context, err error, msg string) error {
	s.report.Report(ctx, msg, err)
	return connect.NewError(connect.CodeInternal, msg).WithCause(err)
}
