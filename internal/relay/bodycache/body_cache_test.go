package bodycache

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBodyCacheReplaysInMemoryBody(t *testing.T) {
	payload := []byte(`{"model":"gpt-image-2","prompt":"draw a lighthouse"}`)
	source := &trackingReadCloser{Reader: bytes.NewReader(payload)}

	cache, err := New(source)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	if !source.closed {
		t.Fatal("New() must close the source body")
	}
	if cache.IsFile() {
		t.Fatal("small body unexpectedly spilled to disk")
	}
	if got := cache.Size(); got != int64(len(payload)) {
		t.Fatalf("Size() = %d, want %d", got, len(payload))
	}

	assertCacheReaderBody(t, cache, payload)
	assertCacheReaderBody(t, cache, payload)

	if err := cache.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := cache.NewReader(); err == nil {
		t.Fatal("NewReader() after Close() unexpectedly succeeded")
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestBodyCacheSpillsInsideConfiguredDirectoryAndRemovesOwnedFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envBodyMaxMB, "2")
	t.Setenv(envMemoryThresholdMB, "1")
	t.Setenv(envTmpDir, dir)
	payload := bytes.Repeat([]byte("i"), int(bytesPerMB)+257)

	cache, err := New(io.NopCloser(bytes.NewReader(payload)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	path := cache.TmpPath()
	t.Cleanup(func() {
		_ = cache.Close()
		if path != "" {
			_ = os.Remove(path)
		}
	})

	if !cache.IsFile() {
		t.Fatal("body larger than the threshold did not spill to disk")
	}
	assertOwnedCachePath(t, dir, path)
	assertCacheReaderBody(t, cache, payload)
	assertCacheReaderBody(t, cache, payload)

	if err := cache.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned cache file still exists after Close(): %v", err)
	}
}

func TestBodyCacheTooLargeRemovesPartialSpillFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envBodyMaxMB, "1")
	t.Setenv(envMemoryThresholdMB, "1")
	t.Setenv(envTmpDir, dir)
	payload := bytes.Repeat([]byte("x"), int(bytesPerMB)+1)

	cache, err := New(io.NopCloser(bytes.NewReader(payload)))
	if cache != nil {
		_ = cache.Close()
		t.Fatal("New() returned a cache for an oversized body")
	}
	var tooLarge *BodyTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("New() error = %v, want *BodyTooLargeError", err)
	}
	if tooLarge.MaxBytes != bytesPerMB || tooLarge.ActualBytes != bytesPerMB+1 {
		t.Fatalf("BodyTooLargeError = %+v, want max=%d actual=%d", tooLarge, bytesPerMB, bytesPerMB+1)
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("ReadDir() error = %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), TmpFilePrefix) {
			t.Errorf("oversized request leaked partial cache file %q", entry.Name())
		}
	}
}

func TestCleanupOldTmpFilesOnlyRemovesOldOwnedFiles(t *testing.T) {
	dir := t.TempDir()
	oldOwned := filepath.Join(dir, TmpFilePrefix+"old")
	recentOwned := filepath.Join(dir, TmpFilePrefix+"recent")
	oldForeign := filepath.Join(dir, "other-service-old")

	for _, path := range []string{oldOwned, recentOwned, oldForeign} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	for _, path := range []string{oldOwned, oldForeign} {
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatalf("Chtimes(%q) error = %v", path, err)
		}
	}

	if err := CleanupOldTmpFiles(dir, TmpFilePrefix, time.Hour); err != nil {
		t.Fatalf("CleanupOldTmpFiles() error = %v", err)
	}
	if _, err := os.Stat(oldOwned); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old owned file was not removed: %v", err)
	}
	for _, path := range []string{recentOwned, oldForeign} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("file outside cleanup scope %q was removed: %v", path, err)
		}
	}
}

func assertCacheReaderBody(t *testing.T, cache *BodyCache, want []byte) {
	t.Helper()
	reader, err := cache.NewReader()
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("replayed body length = %d, want %d", len(got), len(want))
	}
}

func assertOwnedCachePath(t *testing.T, dir string, path string) {
	t.Helper()
	if path == "" {
		t.Fatal("TmpPath() is empty for disk-backed cache")
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		t.Fatalf("filepath.Rel() error = %v", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("cache path %q escaped configured directory %q", path, dir)
	}
	if !strings.HasPrefix(filepath.Base(path), TmpFilePrefix) {
		t.Fatalf("cache file %q does not use owned prefix %q", path, TmpFilePrefix)
	}
}

type trackingReadCloser struct {
	*bytes.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}
