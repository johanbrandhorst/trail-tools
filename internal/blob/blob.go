// Package blob stores files, such as photos, in a local directory or a
// Google Cloud Storage bucket.
package blob

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

// ErrNotFound is returned when an object doesn't exist.
var ErrNotFound = errors.New("object not found")

// Store stores objects by key.
type Store interface {
	// Put stores an object, replacing any object with the same key.
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Get returns the contents of an object, or ErrNotFound.
	Get(ctx context.Context, key string) ([]byte, error)
	// Delete deletes an object. Deleting an object that doesn't exist is not
	// an error.
	Delete(ctx context.Context, key string) error
}

// keyRe matches valid keys: slash separated segments of letters, digits,
// dots, dashes and underscores, that don't start with a dot.
var keyRe = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*(/[A-Za-z0-9_-][A-Za-z0-9._-]*)*$`)

// ValidateKey checks that a key is safe to use as a file path and an object
// name.
func ValidateKey(key string) error {
	if len(key) > 512 || !keyRe.MatchString(key) {
		return fmt.Errorf("invalid object key %q", key)
	}
	return nil
}
