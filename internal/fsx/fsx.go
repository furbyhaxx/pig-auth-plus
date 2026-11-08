// Package fsx provides cross-process file locks and atomic private writes.
package fsx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// lockPoll is how often a contended lock is retried.
const lockPoll = 10 * time.Millisecond

// Unlock releases a lock acquired with [Lock].
type Unlock func() error

// Lock takes an exclusive advisory lock on path+".lock", waiting until ctx ends.
// The lock file is created with mode 0600 and never follows a symlink.
func Lock(ctx context.Context, path string) (Unlock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	f, err := openLockFile(path + ".lock")
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	for {
		ok, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", filepath.Base(path), err)
		}
		if ok {
			return func() error { return errors.Join(unlock(f), f.Close()) }, nil
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", filepath.Base(path), ctx.Err())
		case <-time.After(lockPoll):
		}
	}
}

// WriteAtomic replaces path with data through a synced temporary file in the same directory.
// The result has mode 0600; the parent directory is created with mode 0700.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	name := f.Name()
	defer os.Remove(name)
	if err := writeSync(f, data); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return syncDir(dir)
}

func writeSync(f *os.File, data []byte) error {
	err := f.Chmod(0o600)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	return nil
}

// ReadOptional returns the file contents, or nil and no error when it does not exist.
func ReadOptional(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}
