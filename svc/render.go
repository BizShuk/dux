package svc

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// RenderOptions controls PrintSection / PrintBarChart behavior.
type RenderOptions struct {
	Width       int  // terminal width for layout (default 100)
	MaxBarWidth int  // bar width in chars (default 40)
	ShowHeaders bool // print section titles
	// Prefix, when non-empty, is rendered as a "Showing: <prefix>" banner
	// before the first section — but only when its rune count is at or
	// below MaxPrefixBannerRunes. Longer prefixes are skipped because the
	// banner would dominate the output (and a truncated banner would be
	// less informative than the user already knowing what they typed).
	Prefix string
}

// MaxPrefixBannerRunes caps how long opts.Prefix may be for the "Showing:"
// banner to be emitted. 80 fits comfortably on a standard terminal without
// forcing the section table to wrap; prefixes longer than this are skipped
// to keep the output readable.
const MaxPrefixBannerRunes = 80

// DefaultRenderOptions returns a RenderOptions with sensible defaults.
func DefaultRenderOptions() RenderOptions {
	return RenderOptions{Width: 100, MaxBarWidth: 40, ShowHeaders: true}
}

// PrintSections writes all sections to w in order, each with a header.
// When opts.Prefix is set and short enough, a "Showing: <prefix>" banner
// is emitted first so the user can see at a glance which filter was applied.
func PrintSections(w io.Writer, sections []ReportSection, opts RenderOptions) {
	if opts.Prefix != "" && runeCount(opts.Prefix) <= MaxPrefixBannerRunes {
		fmt.Fprintf(w, "Showing: %s\n", opts.Prefix)
		fmt.Fprintln(w)
	}
	for i, s := range sections {
		if opts.ShowHeaders {
			fmt.Fprintln(w)
			fmt.Fprintf(w, "═══ %s ═══\n", s.Title)
			fmt.Fprintln(w)
		}
		if len(s.Entries) == 0 {
			fmt.Fprintln(w, "  (no entries)")
			continue
		}
		PrintTable(w, s.Entries, opts)
		_ = i
	}
}

// PrintTable writes a simple fixed-width path/size table.
func PrintTable(w io.Writer, entries []ReportEntry, opts RenderOptions) {
	if opts.Width <= 0 {
		opts.Width = 100
	}
	pathW := opts.Width - 30
	if pathW < 30 {
		pathW = 30
	}
	fmt.Fprintf(w, "  %-*s  %12s  %s\n", pathW, "PATH", "SIZE", "INDICATORS")
	for _, e := range entries {
		path := e.Path
		// Truncate long paths with leading ellipsis (mirror file_watcher style).
		if len(path) > pathW {
			path = "..." + path[len(path)-(pathW-3):]
		}
		var indicators []string
		if e.IsDuplicate {
			indicators = append(indicators, "DUP")
		}
		if e.ChangeSize > 0 {
			indicators = append(indicators, fmt.Sprintf("+%s", FormatBytes(e.ChangeSize)))
		}
		if !e.LastAccessed.IsZero() && time.Since(e.LastAccessed) > 180*24*time.Hour {
			indicators = append(indicators, "COLD")
		}
		ind := strings.Join(indicators, " ")
		fmt.Fprintf(w, "  %-*s  %12s  %s\n", pathW, path, FormatBytes(e.Size), ind)
	}
}

// PrintBarChart writes a horizontal bar chart of growth values.
// Mirrors file_watcher/svc/render.go style — used by the diff command.
func PrintBarChart(w io.Writer, entries []ReportEntry, opts RenderOptions) {
	if opts.MaxBarWidth <= 0 {
		opts.MaxBarWidth = 40
	}
	var maxChange int64
	for _, e := range entries {
		if e.ChangeSize > maxChange {
			maxChange = e.ChangeSize
		}
	}
	pathW := 50
	for _, e := range entries {
		fmt.Fprintf(w, "  %-*s  %s  %s\n", pathW, truncatePath(e.Path, pathW),
			barString(e.ChangeSize, maxChange, opts.MaxBarWidth),
			formatGrowth(e))
	}
}

func barString(value, max int64, width int) string {
	if max <= 0 || value <= 0 {
		return ""
	}
	barLen := int(float64(value) / float64(max) * float64(width))
	if barLen <= 0 {
		barLen = 1
	}
	return strings.Repeat("█", barLen)
}

func formatGrowth(e ReportEntry) string {
	if e.Size == 0 {
		return "-"
	}
	return fmt.Sprintf("%s (+%s)", FormatBytes(e.Size), FormatBytes(e.ChangeSize))
}

func truncatePath(p string, max int) string {
	if len(p) <= max {
		return p
	}
	return "..." + p[len(p)-(max-3):]
}

// runeCount returns the number of runes in s. Used by PrintSections to
// decide whether a Prefix fits in the banner width without truncation,
// independent of UTF-8 byte length.
func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// FormatBytes renders a byte count with adaptive unit (1024-based for binary).
// Mirrors file_watcher/svc/stats.go FormatBytes.
func FormatBytes(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case b < KB:
		return fmt.Sprintf("%dB", b)
	case b < MB:
		return fmt.Sprintf("%.1fKB", float64(b)/KB)
	case b < GB:
		return fmt.Sprintf("%.1fMB", float64(b)/MB)
	case b < TB:
		return fmt.Sprintf("%.2fGB", float64(b)/GB)
	default:
		return fmt.Sprintf("%.2fTB", float64(b)/TB)
	}
}

// TopBySize returns the top-N entries sorted by Size descending.
// Tie-break by Path ascending.
func TopBySize(entries []ReportEntry, n int) []ReportEntry {
	out := append([]ReportEntry(nil), entries...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Path < out[j].Path
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}