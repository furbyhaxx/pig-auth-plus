package fsx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockContentionHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.json")
	release, err := Lock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := Lock(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended lock: got %v, want deadline", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := Lock(context.Background(), path)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = again()
}

func TestWriteAtomicPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "f.toml")
	if err := WriteAtomic(path, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, []byte("b")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if string(data) != "b" {
		t.Errorf("content %q", data)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}
