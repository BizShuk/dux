package svc

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// openTestDB returns a fresh on-disk SQLite-backed DB for the test to use.
// Each call gets an independent DB so tests do not leak state into each other.
// Uses a per-test temp dir so the t.Cleanup removes the file when done.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "test.db")
	d, err := OpenDB(tmp)
	if err != nil {
		t.Fatalf("OpenDB(%s): %v", tmp, err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestOpenDB_InvalidPath(t *testing.T) {
	_, err := OpenDB("")
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestDB_LastEntryEmpty(t *testing.T) {
	d := openTestDB(t)
	row, err := d.LastEntry("/nonexistent")
	if err != nil {
		t.Fatalf("LastEntry: %v", err)
	}
	if row != nil {
		t.Errorf("expected nil row, got %+v", row)
	}
}

func TestDB_LatestDateEmpty(t *testing.T) {
	d := openTestDB(t)
	date, err := d.LatestDate()
	if err != nil {
		t.Fatalf("LatestDate: %v", err)
	}
	if date != "" {
		t.Errorf("expected empty string for empty DB, got %q", date)
	}
}

func TestDB_UpsertEntriesChunked(t *testing.T) {
	d := openTestDB(t)
	now := time.Now()
	rows := make([]EntryRow, 1200) // forces 3 chunks at chunk=500
	for i := range rows {
		// Each row has a UNIQUE path so the (path, scan_date) primary key is satisfied.
		rows[i] = EntryRow{
			Path:      filepath.Join("/tmp", "chunked", "row-"+strconv.Itoa(i)),
			ScanDate:  "2026-07-11",
			ScanTS:    now.Unix(),
			SizeBytes: int64(i),
		}
	}
	written, err := d.UpsertEntries(rows)
	if err != nil {
		t.Fatalf("UpsertEntries: %v", err)
	}
	if written != 1200 {
		t.Errorf("expected 1200 written, got %d", written)
	}
}

func TestDB_MarkDuplicates(t *testing.T) {
	d := openTestDB(t)
	now := time.Now()
	rows := []EntryRow{
		{Path: "/a", ScanDate: "2026-07-11", ScanTS: now.Unix(), SizeBytes: 100},
		{Path: "/b", ScanDate: "2026-07-11", ScanTS: now.Unix(), SizeBytes: 100},
	}
	d.UpsertEntries(rows)
	if err := d.MarkDuplicates("2026-07-11", []string{"/a", "/b"}, true); err != nil {
		t.Fatalf("MarkDuplicates: %v", err)
	}
	got, _ := d.EntriesForDate("2026-07-11")
	for _, r := range got {
		if !r.IsDuplicate {
			t.Errorf("expected IsDuplicate=true for %s", r.Path)
		}
	}
}

func TestDB_EntriesBetween(t *testing.T) {
	d := openTestDB(t)
	now := time.Now()
	d.UpsertEntries([]EntryRow{
		{Path: "/a", ScanDate: "2026-07-10", ScanTS: now.Unix(), SizeBytes: 10},
		{Path: "/a", ScanDate: "2026-07-11", ScanTS: now.Unix(), SizeBytes: 20},
	})
	d1, d2, err := d.EntriesBetween("2026-07-10", "2026-07-11")
	if err != nil {
		t.Fatalf("EntriesBetween: %v", err)
	}
	if len(d1) != 1 || d1[0].SizeBytes != 10 {
		t.Errorf("expected d1=[10], got %+v", d1)
	}
	if len(d2) != 1 || d2[0].SizeBytes != 20 {
		t.Errorf("expected d2=[20], got %+v", d2)
	}
}

func TestRowFromEntry(t *testing.T) {
	e := Entry{
		Path:         "/tmp/x",
		Size:         1024,
		LastModified: time.Unix(1700000000, 0),
		LastAccessed: time.Unix(1700000100, 0),
		IsDir:        false,
		Depth:        2,
	}
	r := RowFromEntry(e, "2026-07-11", CatCode, 2)
	if r.Path != "/tmp/x" {
		t.Errorf("expected path /tmp/x, got %s", r.Path)
	}
	if r.MTimeUnix != 1700000000 {
		t.Errorf("expected MTimeUnix=1700000000, got %d", r.MTimeUnix)
	}
	if r.ATimeUnix != 1700000100 {
		t.Errorf("expected ATimeUnix=1700000100, got %d", r.ATimeUnix)
	}
	if r.Category != CatCode {
		t.Errorf("expected category=code, got %s", r.Category)
	}
	_ = context.Background()
}