package blob

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2/google"
)

// gcsURL is the base URL of the Cloud Storage JSON API.
const gcsURL = "https://storage.googleapis.com"

// maxObjectBytes limits the size of objects read from Cloud Storage.
const maxObjectBytes = 64 << 20

// GCS stores objects in a Google Cloud Storage bucket, using the JSON API.
type GCS struct {
	bucket  string
	baseURL string
	client  *http.Client
}

var _ Store = (*GCS)(nil)

// NewGCS creates a store in the bucket. It authenticates with the
// application default credentials, such as the service account of a Cloud
// Run service.
func NewGCS(ctx context.Context, bucket string) (*GCS, error) {
	client, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/devstorage.read_write")
	if err != nil {
		return nil, fmt.Errorf("failed to get Google credentials: %w", err)
	}
	return NewGCSWithClient(bucket, gcsURL, client), nil
}

// NewGCSWithClient creates a store in the bucket of the Cloud Storage JSON
// API at baseURL, making requests with client, e.g. for tests.
func NewGCSWithClient(bucket, baseURL string, client *http.Client) *GCS {
	return &GCS{bucket: bucket, baseURL: baseURL, client: client}
}

// objectURL returns the URL of an object's metadata. Object names are a
// single path segment, with slashes escaped.
func (g *GCS) objectURL(key string) string {
	return g.baseURL + "/storage/v1/b/" + url.PathEscape(g.bucket) + "/o/" + strings.ReplaceAll(url.PathEscape(key), "/", "%2F")
}

func (g *GCS) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	query := url.Values{"uploadType": {"media"}, "name": {key}}
	u := g.baseURL + "/upload/storage/v1/b/" + url.PathEscape(g.bucket) + "/o?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload %s: %w", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to upload %s: %w", key, statusError(resp))
	}
	return nil
}

func (g *GCS) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.objectURL(key)+"?alt=media", nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", key, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNotFound
	default:
		return nil, fmt.Errorf("failed to download %s: %w", key, statusError(resp))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxObjectBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", key, err)
	}
	if len(data) > maxObjectBytes {
		return nil, fmt.Errorf("object %s is larger than %d bytes", key, maxObjectBytes)
	}
	return data, nil
}

func (g *GCS) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, g.objectURL(key), nil)
	if err != nil {
		return err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete %s: %w", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("failed to delete %s: %w", key, statusError(resp))
	}
	return nil
}

// statusError describes an unexpected response.
func statusError(resp *http.Response) error {
	message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("status %d: %s", resp.StatusCode, bytes.TrimSpace(message))
}
