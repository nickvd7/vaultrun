package artifacts

import (
	"context"
	"io"
	"os"
	"path/filepath"
)

// LocalStore persists artifacts on the local filesystem under baseDir.
// Keys that are absolute paths are used as-is (backward compatibility with
// records created before this storage abstraction was introduced).
type LocalStore struct {
	baseDir string
}

func NewLocalStore(baseDir string) *LocalStore {
	return &LocalStore{baseDir: baseDir}
}

func (s *LocalStore) path(key string) string {
	if filepath.IsAbs(key) {
		return key
	}
	return filepath.Join(s.baseDir, key)
}

func (s *LocalStore) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	if err := os.MkdirAll(s.baseDir, 0o700); err != nil {
		return err
	}
	p := s.path(key)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = os.Remove(p)
		return err
	}
	return f.Close()
}

func (s *LocalStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	return os.Open(s.path(key))
}

func (s *LocalStore) Delete(_ context.Context, key string) error {
	err := os.Remove(s.path(key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
