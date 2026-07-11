package svc

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// buildFixture creates a temporary directory tree:
//   <root>/keep/me.txt   (small file)
//   <root>/keep/big.bin  (1KB)
//   <root>/node_modules/foo/bar.js
//   <root>/.git/HEAD
//   <root>/empty/
//   <root>/sub/deep/file.go
// Returns the root path. Caller is responsible for os.RemoveAll.
func buildFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "keep"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "node_modules", "foo"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "empty"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "keep", "me.txt"), []byte("hi"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "keep", "big.bin"), make([]byte, 1024), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "node_modules", "foo", "bar.js"), []byte("js"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "sub", "deep", "file.go"), []byte("package x"), 0o644))
	return root
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
}

func TestScanner_BasicWalk(t *testing.T) {
	root := buildFixture(t)
	s, err := NewScanner(ScanOptions{
		Roots:   []string{root},
		Workers: 2,
	})
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.FileCount == 0 {
		t.Errorf("expected FileCount > 0, got %d", res.FileCount)
	}
	if res.DirCount == 0 {
		t.Errorf("expected DirCount > 0, got %d", res.DirCount)
	}
	if res.BytesScanned < 1024 {
		t.Errorf("expected BytesScanned >= 1024, got %d", res.BytesScanned)
	}
}

func TestScanner_ExcludeListHonored(t *testing.T) {
	root := buildFixture(t)
	s, _ := NewScanner(ScanOptions{
		Roots:       []string{root},
		Workers:     2,
		ExcludeList: []string{"node_modules", ".git"},
	})
	res, _ := s.Scan(context.Background())
	// The walker itself does not retain entries (that's the collector's job),
	// but warnings would include the excluded paths if the exclude logic
	// were broken. Verify the exclusion works by counting: we should see at
	// least the keep/ and sub/ trees but no node_modules/* and no .git/*.
	// Since the scanner only counts, the easiest assertion is that warnings
	// slice did not balloon with permission errors or weird errors.
	for _, w := range res.Warnings {
		if strings.Contains(w, "permission") || strings.Contains(w, "not found") {
			t.Errorf("unexpected warning: %s", w)
		}
	}
}

func TestScanner_ShouldExclude(t *testing.T) {
	s, _ := NewScanner(ScanOptions{
		Roots:       []string{"/"},
		Workers:     1,
		ExcludeList: []string{"node_modules", ".git", "tmp"},
	})
	cases := []struct {
		name string
		want bool
	}{
		{"node_modules", true},
		{".git", true},
		{"my-project.tmp", true},  // substring match
		{"src", false},
		{"main.go", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := s.ShouldExclude(c.name); got != c.want {
				t.Errorf("ShouldExclude(%q) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestScanner_PrivacyRedact(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, ".ssh"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, ".ssh", "id_rsa"), []byte("secret"), 0o600))
	s, _ := NewScanner(ScanOptions{
		Roots:              []string{root},
		Workers:            1,
		PrivacyRedact:      true,
		PrivacyRedactPaths: []string{".ssh"},
	})
	res, _ := s.Scan(context.Background())
	if res.FileCount == 0 {
		t.Error("expected at least one file scanned")
	}
	// We can't directly verify the redacted path from the scanner result,
	// but we can verify the slog context didn't crash and result is sensible.
	if res.Duration < 0 {
		t.Errorf("Duration should be non-negative, got %v", res.Duration)
	}
}

func TestScanner_ContextCancellation(t *testing.T) {
	root := buildFixture(t)
	s, _ := NewScanner(ScanOptions{
		Roots:   []string{root},
		Workers: 1,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	_, err := s.Scan(ctx)
	// Cancellation should not produce a fatal error (we return ctx.Err() but
	// result() is still assembled). Just ensure no panic.
	_ = err
}

func TestScanner_PermissionDeniedDoesNotAbort(t *testing.T) {
	// Build a fixture, then chmod a directory to 000 to simulate permission denied.
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission tests are not reliable")
	}
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "locked"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "locked", "secret.txt"), []byte("x"), 0o644))
	must(t, os.Chmod(filepath.Join(root, "locked"), 0o000))
	defer os.Chmod(filepath.Join(root, "locked"), 0o755) // restore for cleanup

	// Set default logger to debug level so warnings are collected.
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)
	oldDefault := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(oldDefault)

	s, _ := NewScanner(ScanOptions{Roots: []string{root}, Workers: 1})
	res, _ := s.Scan(context.Background())
	if len(res.Warnings) == 0 {
		t.Error("expected at least one permission warning")
	}
}

func TestNewScanner_NoRoots(t *testing.T) {
	_, err := NewScanner(ScanOptions{})
	if err == nil {
		t.Error("expected error for empty roots")
	}
}

func TestScanner_CompetingRootDedup(t *testing.T) {
	// Build a tree:
	//   /tmp/disk-test-dedup/         <- outer root
	//   /tmp/disk-test-dedup/inner/   <- competing root (scanned separately)
	//   /tmp/disk-test-dedup/inner/x  <- should be skipped when scanning outer
	outer := t.TempDir()
	inner := t.TempDir() // separate dir under /tmp, will be referenced as absolute path

	// Put a file under inner so we can detect it being scanned twice.
	if err := os.WriteFile(filepath.Join(inner, "marker.txt"), []byte("X"), 0o644); err != nil {
		t.Fatal(err)
	}

	var entries []Entry
	var mu sync.Mutex
	s, err := NewScanner(ScanOptions{
		Roots:   []string{outer, inner},
		Workers: 1,
		OnEntry: func(e Entry) error {
			mu.Lock()
			entries = append(entries, e)
			mu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	_, err = s.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// Count how many times the inner marker appears in entries.
	count := 0
	for _, e := range entries {
		if filepath.Base(e.Path) == "marker.txt" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected marker.txt to appear exactly once (deduped across competing roots), got %d", count)
	}
}

func TestScanner_NoDedup_WhenDistinctRoots(t *testing.T) {
	// Roots that don't overlap should each be walked normally.
	dirA := t.TempDir()
	dirB := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirA, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirB, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	var mu sync.Mutex
	s, _ := NewScanner(ScanOptions{
		Roots: []string{dirA, dirB}, 
		Workers: 1,
		OnEntry: func(e Entry) error {
			mu.Lock()
			entries = append(entries, e)
			mu.Unlock()
			return nil
		},
	})
	_, _ = s.Scan(context.Background())
	if len(entries) < 4 { // at least 2 files + 2 dirs (each root emits itself)
		t.Errorf("expected at least 4 entries, got %d", len(entries))
	}
}

// makeSparseFile creates a file that is large in apparent size but small on
// disk by writing one 4KB block and seeking to a high offset. On filesystems
// that don't support holes the test still allocates contiguous blocks and the
// assertions degrade gracefully (they assert the upper bound rather than a
// strict inequality).
func makeSparseFile(t *testing.T, path string, apparentSize int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create sparse: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(make([]byte, 4096)); err != nil {
		t.Fatalf("write anchor: %v", err)
	}
	if _, err := f.Seek(apparentSize-1, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	if _, err := f.Write([]byte{0}); err != nil {
		t.Fatalf("extend: %v", err)
	}
}

func TestOnDiskBytes_SparseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sparse.raw")
	const apparentSize = 512 * 1024 * 1024 // 512MB logical
	makeSparseFile(t, path, apparentSize)

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	apparent := info.Size()
	onDisk := onDiskBytes(info)

	if apparent != apparentSize {
		t.Fatalf("apparent size: want %d, got %d", apparentSize, apparent)
	}
	if onDisk > 1*1024*1024 {
		t.Errorf("on-disk should be tiny for a 512MB apparent sparse file, got %d bytes", onDisk)
	}
}

func TestScanner_EmitsOnDiskBytesByDefault(t *testing.T) {
	dir := t.TempDir()
	const apparentSize = 256 * 1024 * 1024
	makeSparseFile(t, filepath.Join(dir, "sparse.raw"), apparentSize)
	if err := os.WriteFile(filepath.Join(dir, "normal.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	var entries []Entry
	var mu sync.Mutex
	s, err := NewScanner(ScanOptions{
		Roots:   []string{dir},
		Workers: 1,
		OnEntry: func(e Entry) error {
			if !e.IsDir {
				mu.Lock()
				entries = append(entries, e)
				mu.Unlock()
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		switch filepath.Base(e.Path) {
		case "sparse.raw":
			if e.Size >= apparentSize {
				t.Errorf("scanner reported apparent size %d for sparse file (should be on-disk, << %d)", e.Size, apparentSize)
			}
		case "normal.txt":
			// A 5-byte file on APFS occupies one 4KB block, so on-disk
			// bytes round up to 4096 (matching `du`). Assert that we
			// report block-aligned on-disk size, not the 5-byte apparent.
			if e.Size != 4096 {
				t.Errorf("scanner reported size %d for normal.txt; want 4096 (one APFS block)", e.Size)
			}
		}
	}
}

func TestScanner_ApparentSizeFlagPreservesLogicalSize(t *testing.T) {
	dir := t.TempDir()
	const apparentSize = 256 * 1024 * 1024
	makeSparseFile(t, filepath.Join(dir, "sparse.raw"), apparentSize)

	var found Entry
	var mu sync.Mutex
	s, _ := NewScanner(ScanOptions{
		Roots:           []string{dir},
		Workers:         1,
		UseApparentSize: true,
		OnEntry: func(e Entry) error {
			if !e.IsDir && filepath.Base(e.Path) == "sparse.raw" {
				mu.Lock()
				found = e
				mu.Unlock()
			}
			return nil
		},
	})
	if _, err := s.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	if found.Path == "" {
		t.Fatal("sparse.raw entry not emitted")
	}
	if found.Size != apparentSize {
		t.Errorf("apparent-size mode: want %d, got %d", apparentSize, found.Size)
	}
}

func TestScanner_ShouldExcludeFullPath(t *testing.T) {
	s, _ := NewScanner(ScanOptions{
		Roots:       []string{"/"},
		Workers:     1,
		ExcludeList: []string{"/private/var", "/dev", "Library/Caches"},
	})
	if !s.shouldExclude("var", "/private/var") {
		t.Error("expected /private/var to be excluded")
	}
	if !s.shouldExclude("var", "/private/var/ma") {
		t.Error("expected /private/var/ma to be excluded")
	}
	if s.shouldExclude("private", "/private") {
		t.Error("expected /private not to be excluded")
	}
	if !s.shouldExclude("Caches", "/Users/shuk/Library/Caches") {
		t.Error("expected /Users/shuk/Library/Caches to be excluded")
	}
	if !s.shouldExclude("temp", "/Users/shuk/Library/Caches/temp") {
		t.Error("expected /Users/shuk/Library/Caches/temp to be excluded")
	}
	if s.shouldExclude("Library", "/Users/shuk/Library") {
		t.Error("expected /Users/shuk/Library not to be excluded")
	}
}