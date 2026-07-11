// Package svc contains the domain types and operations for disk-analyzer.
//
// Files in this package:
//   - entry.go:        data types (Entry / ReportEntry / DuplicateGroup / ReportSection)
//   - scanner.go:      concurrent directory scan with exclude + privacy redact
//   - collector.go:    in-memory aggregate + mtime-aware cache + DB persistence
//   - db.go:           SQLite via gorm schema + DAO for entries table
//   - hasher.go:       3-stage xxHash duplicate detection
//   - report.go:       Overview / Breakdown / Cold / Duplicate section builders + categorize
//   - csv_writer.go:   defensive CSV writer (BOM + UTF-8 sanitize)
//   - metrics.go:      BufferedMetric with JSONL offline replay
//   - render.go:       terminal table + bar chart rendering
package svc

import "time"

// Entry is the atomic record produced by the scanner for a single filesystem
// entry (file or directory). Mirrors file_watcher's svc.Entry shape so the
// report layer can share comparison logic.
type Entry struct {
	Path          string    `json:"path"`           // absolute or normalized
	Size          int64     `json:"size"`           // bytes (file size for files, sum of children for dirs)
	LastModified  time.Time `json:"last_modified"`  // mtime
	LastAccessed  time.Time `json:"last_accessed"`  // atime (zero if unavailable — Linux noatime)
	IsDir         bool      `json:"is_dir"`
	IsSymlink     bool      `json:"is_symlink"`
	IsHardlink    bool      `json:"is_hardlink"`  // nlink > 1
	Inode         uint64    `json:"inode"`        // for hardlink grouping
	Depth         int       `json:"depth"`        // depth from scan root
}

// ReportEntry is the enriched row printed in a ReportSection.
// It includes the diff against the historical baseline and the categorization.
type ReportEntry struct {
	Path         string    `json:"path"`
	Size         int64     `json:"size"`
	ChangeSize   int64     `json:"change_size"`  // current.size - prev.size; 0 if no baseline
	IsDuplicate  bool      `json:"is_duplicate"`
	LastAccessed time.Time `json:"last_accessed"`
	Category     string    `json:"category"`     // media / archive / code / vm / other
	Depth        int       `json:"depth"`
}

// DuplicateGroup is a cluster of paths that share the same content hash
// (or the same size if the group never progressed past stage 1).
type DuplicateGroup struct {
	Hash  uint64   `json:"hash"`
	Size  int64    `json:"size"`
	Paths []string `json:"paths"`
	Stage int      `json:"stage"` // 1=size-only, 2=header match, 3=full match
}

// WastedBytes returns how many bytes would be reclaimed if all-but-one
// copy in the group were deleted. Used for the "potential savings" line
// in the duplicate section.
func (g DuplicateGroup) WastedBytes() int64 {
	if len(g.Paths) < 2 {
		return 0
	}
	return g.Size * int64(len(g.Paths)-1)
}

// ReportSection groups same-type rows for the terminal output and CSV export.
// Section names are stable strings so external tooling (CSV consumers, TUI
// filter logic) can refer to them without mapping tables.
type ReportSection struct {
	Name    string        `json:"name"`
	Title   string        `json:"title"`
	Entries []ReportEntry `json:"entries"`
}

// Section names — keep stable: they appear in CSV comment rows and
// are referenced by the diff command.
const (
	SectionOverviewRoot   = "overview-root"
	SectionOverviewHome   = "overview-home"
	SectionBreakdownRoot  = "breakdown-root"
	SectionBreakdownHome  = "breakdown-home"
	SectionDuplicate      = "duplicate"
	SectionCold           = "cold"
	SectionPermissionWarn = "permission-warnings"
)

// SectionTitles — terminal-visible titles, may be localized.
var SectionTitles = map[string]string{
	SectionOverviewRoot:   "Overview — /  (depth limited)",
	SectionOverviewHome:   "Overview — $HOME  (depth limited)",
	SectionBreakdownRoot:  "Breakdown — /  (>= threshold)",
	SectionBreakdownHome:  "Breakdown — $HOME  (>= threshold)",
	SectionDuplicate:      "Duplicate files",
	SectionCold:           "Cold data (>= 180d unaccessed, >= cold_size)",
	SectionPermissionWarn: "Permission warnings",
}