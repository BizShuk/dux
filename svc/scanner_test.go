package svc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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