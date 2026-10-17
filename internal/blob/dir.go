package blob

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Dir stores objects as files in a local directory, for running locally.
type Dir struct {
	root string
}

var _ Store = (*Dir)(nil)

// NewDir creates a store in the directory, creating it if needed.
func NewDir(root string) (*Dir, error) {
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create %s: %w", root, err)
	}
	return &Dir{root: root}, nil
}

func (d *Dir) path(key string) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	return filepath.Join(d.root, filepath.FromSlash(key)), nil
}

func (d *Dir) Put(_ context.Context, key string, data []byte, _ string) error {
	path, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	// Write to a temporary file first, so readers never see a partial file.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}
	return nil
}

func (d *Dir) Get(_ context.Context, key string) ([]byte, error) {
	path, err := d.path(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return data, nil
}

func (d *Dir) Delete(_ context.Context, key string) error {
	path, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	// Remove the directories left empty, up to the root. Removing a
	// directory that isn't empty fails, which ends the cleanup.
	for dir := filepath.Dir(path); dir != d.root && len(dir) > len(d.root); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break
		}
	}
	return nil
}
