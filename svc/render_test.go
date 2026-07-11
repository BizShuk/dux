package svc

import (
	"bytes"
	"strings"
	"testing"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0B"},
		{100, "100B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1048576, "1.0MB"},
		{1073741824, "1.00GB"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := FormatBytes(tt.input)
			if got != tt.expected {
				t.Errorf("FormatBytes(%d) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestPrintSections(t *testing.T) {
	var buf bytes.Buffer
	sections := []ReportSection{
		{
			Name:  SectionOverviewRoot,
			Title: SectionTitles[SectionOverviewRoot],
			Entries: []ReportEntry{
				{Path: "/", Size: 100, Category: "directory"},
				{Path: "/Users", Size: 50, Category: "directory"},
			},
		},
	}
	PrintSections(&buf, sections, DefaultRenderOptions())
	if !strings.Contains(buf.String(), "PATH") {
		t.Errorf("expected PATH column header, got: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "100B") {
		t.Errorf("expected size formatting, got: %q", buf.String())
	}
}

func TestPrintSections_PrefixBanner(t *testing.T) {
	sections := []ReportSection{
		{
			Name:    SectionOverviewRoot,
			Title:   SectionTitles[SectionOverviewRoot],
			Entries: []ReportEntry{{Path: "/Users/shuk", Size: 100}},
		},
	}

	t.Run("empty prefix emits no banner", func(t *testing.T) {
		var buf bytes.Buffer
		PrintSections(&buf, sections, RenderOptions{Prefix: ""})
		if strings.Contains(buf.String(), "Showing:") {
			t.Errorf("expected no banner for empty prefix, got: %q", buf.String())
		}
	})

	t.Run("short prefix emits banner", func(t *testing.T) {
		var buf bytes.Buffer
		PrintSections(&buf, sections, RenderOptions{Prefix: "/Users/shuk"})
		if !strings.Contains(buf.String(), "Showing: /Users/shuk") {
			t.Errorf("expected banner with short prefix, got: %q", buf.String())
		}
	})

	t.Run("long prefix is skipped", func(t *testing.T) {
		var buf bytes.Buffer
		longPrefix := strings.Repeat("a", MaxPrefixBannerRunes+1)
		PrintSections(&buf, sections, RenderOptions{Prefix: longPrefix})
		if strings.Contains(buf.String(), "Showing:") {
			t.Errorf("expected no banner for over-length prefix, got prefix in: %q", buf.String())
		}
	})

	t.Run("prefix at the limit emits banner", func(t *testing.T) {
		var buf bytes.Buffer
		atLimit := strings.Repeat("a", MaxPrefixBannerRunes)
		PrintSections(&buf, sections, RenderOptions{Prefix: atLimit})
		if !strings.Contains(buf.String(), "Showing: "+atLimit) {
			t.Errorf("expected banner for prefix at exact limit, got: %q", buf.String())
		}
	})

	t.Run("multi-byte prefix uses rune count, not byte length", func(t *testing.T) {
		// "中文目錄" is 12 bytes in UTF-8 but only 4 runes. Must be allowed.
		var buf bytes.Buffer
		PrintSections(&buf, sections, RenderOptions{Prefix: "中文目錄"})
		if !strings.Contains(buf.String(), "Showing: 中文目錄") {
			t.Errorf("expected banner for short multi-byte prefix, got: %q", buf.String())
		}
	})
}

func TestPrintBarChart(t *testing.T) {
	var buf bytes.Buffer
	entries := []ReportEntry{
		{Path: "/a", Size: 1000, ChangeSize: 500},
		{Path: "/b", Size: 500, ChangeSize: 250},
	}
	PrintBarChart(&buf, entries, RenderOptions{MaxBarWidth: 10})
	if !strings.Contains(buf.String(), "█") {
		t.Errorf("expected bar chars, got: %q", buf.String())
	}
}

func TestTopBySize(t *testing.T) {
	entries := []ReportEntry{
		{Path: "/small", Size: 10},
		{Path: "/big", Size: 100},
		{Path: "/medium", Size: 50},
	}
	got := TopBySize(entries, 2)
	if len(got) != 2 {
		t.Errorf("expected 2 entries, got %d", len(got))
	}
	if got[0].Path != "/big" {
		t.Errorf("expected /big first, got %s", got[0].Path)
	}
}

func TestTruncatePath(t *testing.T) {
	long := "/very/long/path/that/exceeds/the/maximum/width"
	got := truncatePath(long, 20)
	if !strings.HasPrefix(got, "...") {
		t.Errorf("expected prefix '...', got %q", got)
	}
	if len(got) > 20 {
		t.Errorf("expected len <= 20, got %d", len(got))
	}
}

func TestCategorizeByExt(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/x.mp4", CatMedia},
		{"/x.zip", CatArchive},
		{"/x.go", CatCode},
		{"/x.vmdk", CatVM},
		{"/x.pdf", CatDoc},
		{"/x.tar.gz", CatArchive},
		{"/x.unknown", CatOther},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := CategorizeByExt(tt.path)
			if got != tt.want {
				t.Errorf("CategorizeByExt(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}