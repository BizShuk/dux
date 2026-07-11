package svc

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReadCSV_RoundtripV2(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "rt.csv")
	now := time.Date(2026, 7, 11, 10, 30, 0, 0, time.UTC)
	in := []ReportSection{
		{
			Name: SectionOverviewRoot,
			Entries: []ReportEntry{
				{Path: "/", IsDir: true, Size: 1000, Depth: 0, Category: "directory"},
				{Path: "/Users", IsDir: true, Size: 500, Depth: 1, Category: "directory"},
				{Path: "/Users/x.zip", IsDir: false, Size: 200, Depth: 2, Category: "archive", LastAccessed: now},
			},
		},
	}
	if err := WriteCSV(CSVOptions{Path: out, Sections: in}); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	got, err := ReadCSV(out)
	if err != nil {
		t.Fatalf("ReadCSV: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(got))
	}
	if got[0].Path != "/" || !got[0].IsDir || got[0].Depth != 0 {
		t.Errorf("row 0 mismatch: %+v", got[0])
	}
	if got[1].Path != "/Users" || !got[1].IsDir || got[1].Depth != 1 {
		t.Errorf("row 1 mismatch: %+v", got[1])
	}
	if got[2].Path != "/Users/x.zip" || got[2].IsDir || got[2].Depth != 2 {
		t.Errorf("row 2 mismatch: %+v", got[2])
	}
	if !got[2].LastAccessed.Equal(now) {
		t.Errorf("row 2 LastAccessed mismatch: got %v want %v", got[2].LastAccessed, now)
	}
	if got[2].Category != "archive" {
		t.Errorf("row 2 Category mismatch: got %q want %q", got[2].Category, "archive")
	}
}

func TestReadCSV_AcceptsV1Schema(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "legacy.csv")
	// Simulate the pre-refactor 6-column schema.
	v1 := "\xEF\xBB\xBF" +
		"path,size,change_size,is_duplicate,last_accessed,category\n" +
		"/a,100,0,false,,media\n" +
		"/b,200,50,true,2026-07-11T10:30:00Z,archive\n"
	if err := os.WriteFile(out, []byte(v1), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := ReadCSV(out)
	if err != nil {
		t.Fatalf("ReadCSV: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(got))
	}
	// v1 reader should default IsDir=false, Depth=0.
	if got[0].IsDir || got[0].Depth != 0 {
		t.Errorf("v1 row 0 should default IsDir=false Depth=0, got %+v", got[0])
	}
	if got[1].Size != 200 || got[1].ChangeSize != 50 || !got[1].IsDuplicate {
		t.Errorf("v1 row 1 size/change/dup mismatch: %+v", got[1])
	}
	if got[1].Category != "archive" {
		t.Errorf("v1 row 1 category mismatch: got %q", got[1].Category)
	}
}

func TestReadCSV_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "empty.csv")
	if err := os.WriteFile(out, []byte("\xEF\xBB\xBF"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := ReadCSV(out)
	if err != nil {
		t.Fatalf("ReadCSV: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 rows, got %d", len(got))
	}
}

func TestReadCSV_RejectsUnknownHeader(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "bad.csv")
	if err := os.WriteFile(out, []byte("foo,bar\n1,2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := ReadCSV(out); err == nil {
		t.Fatal("expected error for unknown header")
	}
}

func TestLatestDate_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	d, err := LatestDate(dir)
	if d != "" || !errors.Is(err, ErrNoScans) {
		t.Errorf("expected ErrNoScans on empty dir, got d=%q err=%v", d, err)
	}
}

func TestLatestDate_MissingDir(t *testing.T) {
	d, err := LatestDate("/tmp/does-not-exist-disk-analyzer-csv-test")
	if d != "" {
		t.Errorf("missing dir should return empty date, got %q", d)
	}
	if !errors.Is(err, ErrNoScans) {
		t.Errorf("missing dir should return ErrNoScans, got %v", err)
	}
}

func TestAvailableDates_MissingDir(t *testing.T) {
	got, err := AvailableDates("/tmp/does-not-exist-disk-analyzer-csv-test")
	if err != nil {
		t.Errorf("AvailableDates missing dir should return nil error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing dir should return empty slice, got %v", got)
	}
}

func TestLatestDate_PicksMax(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"2026-07-10.csv", "2026-07-12.csv", "2026-07-11.csv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("\xEF\xBB\xBF\n"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	// Add a non-CSV file that must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	d, err := LatestDate(dir)
	if err != nil {
		t.Fatalf("LatestDate: %v", err)
	}
	if d != "2026-07-12" {
		t.Errorf("expected 2026-07-12, got %q", d)
	}
}

func TestAvailableDates_SkipsMalformed(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"2026-07-10.csv", "garbage.csv", "2026-07-11.csv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("\xEF\xBB\xBF\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	got, err := AvailableDates(dir)
	if err != nil {
		t.Fatalf("AvailableDates: %v", err)
	}
	want := []string{"2026-07-10", "2026-07-11"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AvailableDates mismatch: got %v want %v", got, want)
	}
}

func TestReadCSV_PreservesOrderingAndSkipsBadRows(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "mixed.csv")
	body := "\xEF\xBB\xBF" +
		"path,is_dir,depth,size,change_size,is_duplicate,last_accessed,category\n" +
		"/a,true,0,10,0,false,,other\n" +
		",true,0,0,0,false,,other\n" + // blank path → skipped
		"/c,false,1,30,0,false,,other\n"
	if err := os.WriteFile(out, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := ReadCSV(out)
	if err != nil {
		t.Fatalf("ReadCSV: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows after skipping bad, got %d (%+v)", len(got), got)
	}
	if got[0].Path != "/a" || got[1].Path != "/c" {
		t.Errorf("ordering broken: got %q %q", got[0].Path, got[1].Path)
	}
	// Sanity: bad row did not pollute anything.
	if strings.Contains(got[0].Path, ",") {
		t.Errorf("unexpected comma in path: %q", got[0].Path)
	}
}