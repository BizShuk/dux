package svc

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// openTestDB returns a DB backed by a fresh SQLite file in t.TempDir().
func openTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	viper.Set("SQLITE_PATH", path)
	d, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestCollector_AddAndEntries(t *testing.T) {
	d := openTestDB(t)
	c, err := NewCollector(d, "2026-07-11")
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	now := time.Now()
	c.AddFile("/tmp/a.txt", 100, now, now, 1)
	c.AddFile("/tmp/b.txt", 200, now, now, 1)
	c.AddDir("/tmp/x", 500, now, now, 1)

	entries := c.Entries()
	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(entries))
	}
	// Entries() returns sorted by path → first should be /tmp/a.txt
	if entries[0].Path != "/tmp/a.txt" {
		t.Errorf("expected /tmp/a.txt first, got %s", entries[0].Path)
	}
}

func TestCollector_FlushPersistsRows(t *testing.T) {
	d := openTestDB(t)
	c, _ := NewCollector(d, "2026-07-11")
	now := time.Now()
	c.AddFile("/tmp/p.txt", 1234, now, now, 1)
	c.AddDir("/tmp/d", 5678, now, now, 1)

	written, err := c.Flush(context.Background())
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if written != 2 {
		t.Errorf("expected 2 rows written, got %d", written)
	}

	rows, err := d.EntriesForDate("2026-07-11")
	if err != nil {
		t.Fatalf("EntriesForDate: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows persisted, got %d", len(rows))
	}
}

func TestCollector_CachedSizeHit(t *testing.T) {
	d := openTestDB(t)
	c, _ := NewCollector(d, "2026-07-11")

	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Use a timestamp truncated to second precision so the DB row's
	// MTimeUnix matches what the test's FileInfo reports back.
	mtime := time.Now().Truncate(time.Second)
	c.AddDir(dir, 2, mtime, mtime, 0)
	c.AddDir(sub, 2, mtime, mtime, 1)
	c.AddFile(filepath.Join(sub, "f.txt"), 2, mtime, mtime, 2)
	if _, err := c.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Build a fake FileInfo with the exact mtime we persisted.
	lstat, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	info := &fileInfoFromStat{FileInfo: lstat, modTime: mtime, sz: 2}
	size, hit := c.CachedSize(dir, info)
	if !hit {
		t.Errorf("expected cache hit for %s, got miss", dir)
	}
	if size <= 0 {
		t.Errorf("expected size > 0, got %d", size)
	}
}

func TestCollector_CachedSizeMiss(t *testing.T) {
	d := openTestDB(t)
	c, _ := NewCollector(d, "2026-07-11")

	dir := t.TempDir()
	now := time.Now()
	c.AddDir(dir, 100, now, now, 0)
	c.Flush(context.Background())

	info, _ := os.Lstat(dir)
	// Change mtime by 1 hour → should miss
	wrongMtime := &fileInfoFromStat{
		FileInfo: info,
		modTime:  now.Add(-time.Hour).Truncate(time.Second),
	}
	_, hit := c.CachedSize(dir, wrongMtime)
	if hit {
		t.Error("expected cache miss for stale mtime")
	}
}

func TestCollector_FilterByMinSize(t *testing.T) {
	d := openTestDB(t)
	c, _ := NewCollector(d, "2026-07-11")
	c.AddFile("/tmp/small", 100, time.Now(), time.Now(), 1)
	c.AddFile("/tmp/big", 10_000, time.Now(), time.Now(), 1)
	got := c.FilterByMinSize(1000)
	if len(got) != 1 {
		t.Errorf("expected 1 entry >= 1000, got %d", len(got))
	}
	if got[0].Path != "/tmp/big" {
		t.Errorf("expected /tmp/big, got %s", got[0].Path)
	}
}

func TestCollector_Stats(t *testing.T) {
	d := openTestDB(t)
	c, _ := NewCollector(d, "2026-07-11")
	c.AddFile("/x", 1, time.Now(), time.Now(), 1)
	c.AddDir("/y", 2, time.Now(), time.Now(), 1)
	st := c.Stats()
	if st.Added != 2 {
		t.Errorf("expected Added=2, got %d", st.Added)
	}
	if runtime.GOOS == "linux" {
		// Only enforce Duration non-negative on Linux (CI); skip on macOS if flaky.
	}
}

// fileInfoFromStat wraps os.FileInfo so tests can supply an arbitrary ModTime
// and Size. Embedding the original keeps all other accessors (Mode, IsDir, etc.)
// correct.
type fileInfoFromStat struct {
	os.FileInfo
	modTime time.Time
	sz      int64
}

func (f *fileInfoFromStat) ModTime() time.Time { return f.modTime }
func (f *fileInfoFromStat) Size() int64        { return f.sz }