package svc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteCSV_BOMAndHeader(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "test.csv")
	sections := []ReportSection{
		{
			Name:  SectionOverviewRoot,
			Title: "Overview — /",
			Entries: []ReportEntry{
				{Path: "/", IsDir: true, Depth: 0, Size: 100, Category: "directory"},
			},
		},
	}
	if err := WriteCSV(CSVOptions{Path: out, Sections: sections}); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// BOM check.
	if len(data) < 3 || data[0] != 0xEF || data[1] != 0xBB || data[2] != 0xBF {
		t.Errorf("expected BOM, got first bytes: % x", data[:3])
	}
	str := string(data[3:])
	if strings.Contains(str, "# overview-root") {
		t.Errorf("expected no section header, got: %s", str)
	}
	if !strings.Contains(str, "path,is_dir,depth,size,change_size") {
		t.Errorf("expected column header (v2 schema), got: %s", str)
	}
	if !strings.Contains(str, "/,true,0,100") {
		t.Errorf("expected row data with is_dir/depth, got: %s", str)
	}
}

func TestWriteCSV_SanitizesInvalidUTF8(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "test.csv")
	invalid := string([]byte{0xFF, 0xFE, 0xFD}) // invalid UTF-8 sequence
	sections := []ReportSection{
		{
			Name: SectionOverviewRoot,
			Entries: []ReportEntry{
				{Path: "/valid/path", Size: 100},
				{Path: invalid, Size: 200},
			},
		},
	}
	if err := WriteCSV(CSVOptions{Path: out, Sections: sections}); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	data, _ := os.ReadFile(out)
	if strings.Contains(string(data), string([]byte{0xFF})) {
		t.Error("expected invalid bytes to be sanitized, but they appear in output")
	}
}

func TestWriteCSV_TimeFormat(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "test.csv")
	now := time.Date(2026, 7, 11, 10, 30, 0, 0, time.UTC)
	sections := []ReportSection{
		{
			Name: SectionOverviewRoot,
			Entries: []ReportEntry{
				{Path: "/x", Size: 1, LastAccessed: now},
			},
		},
	}
	if err := WriteCSV(CSVOptions{Path: out, Sections: sections}); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), "2026-07-11T10:30:00Z") {
		t.Errorf("expected RFC3339 time, got: %s", string(data))
	}
}

func TestWriteCSV_EmptyPath(t *testing.T) {
	err := WriteCSV(CSVOptions{Path: ""})
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestSanitizeUTF8_Valid(t *testing.T) {
	in := "hello/world/中文"
	out := sanitizeUTF8(in)
	if out != in {
		t.Errorf("valid UTF-8 should pass through unchanged, got %q", out)
	}
}

func TestSanitizeUTF8_Invalid(t *testing.T) {
	in := string([]byte{0xFF, 0xFE}) + "ok"
	out := sanitizeUTF8(in)
	if !strings.Contains(out, "?") {
		t.Errorf("expected invalid bytes replaced with '?', got %q", out)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("expected 'ok' preserved, got %q", out)
	}
}

func TestReportBuilder_AddAndSections(t *testing.T) {
	b := NewReportBuilder()
	b.Add(SectionOverviewRoot, ReportEntry{Path: "/z", Size: 100})
	b.Add(SectionOverviewRoot, ReportEntry{Path: "/a", Size: 50})
	b.Add(SectionOverviewHome, ReportEntry{Path: "/Users/x", Size: 10})

	sections := b.Sections()
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if sections[0].Name != SectionOverviewRoot {
		t.Errorf("expected first section to be overview-root, got %s", sections[0].Name)
	}
}

func TestReportBuilder_SortEntriesByPath(t *testing.T) {
	b := NewReportBuilder()
	b.Add(SectionOverviewRoot, ReportEntry{Path: "/z", Size: 1})
	b.Add(SectionOverviewRoot, ReportEntry{Path: "/a", Size: 2})
	b.Add(SectionOverviewRoot, ReportEntry{Path: "/m", Size: 3})
	b.SortEntriesByPath()
	secs := b.Sections()
	if secs[0].Entries[0].Path != "/a" {
		t.Errorf("expected /a first, got %s", secs[0].Entries[0].Path)
	}
	if secs[0].Entries[2].Path != "/z" {
		t.Errorf("expected /z last, got %s", secs[0].Entries[2].Path)
	}
}

func TestDiffEntries(t *testing.T) {
	current := []Entry{
		{Path: "/a", Size: 200, LastAccessed: time.Now()},
		{Path: "/b", Size: 50, LastAccessed: time.Now()},
	}
	prev := []Entry{
		{Path: "/a", Size: 100},
	}
	diff := DiffEntries(current, prev)
	for _, e := range diff {
		if e.Path == "/a" && e.ChangeSize != 100 {
			t.Errorf("expected change_size=100 for /a, got %d", e.ChangeSize)
		}
		if e.Path == "/b" && e.ChangeSize != 50 {
			t.Errorf("expected change_size=50 for /b, got %d", e.ChangeSize)
		}
	}
}

func TestTopGrowth(t *testing.T) {
	entries := []ReportEntry{
		{Path: "/small-growth", ChangeSize: 10},
		{Path: "/big-growth", ChangeSize: 1000},
		{Path: "/medium-growth", ChangeSize: 100},
	}
	top := TopGrowth(entries, 2)
	if len(top) != 2 {
		t.Errorf("expected 2, got %d", len(top))
	}
	if top[0].Path != "/big-growth" {
		t.Errorf("expected /big-growth first, got %s", top[0].Path)
	}
}

func TestDuplicateGroup_WastedBytes2(t *testing.T) {
	g := DuplicateGroup{Size: 100, Paths: []string{"a", "b"}}
	if g.WastedBytes() != 100 {
		t.Errorf("expected 100 wasted for 2-path group, got %d", g.WastedBytes())
	}
}