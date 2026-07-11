package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/bizshuk/disk-analyzer/config"
	"github.com/bizshuk/disk-analyzer/svc"

	sdkcfg "github.com/bizshuk/gosdk/config"
	"github.com/bizshuk/gosdk/metric"
)

// Runner is the DI container for disk-analyzer commands.
// It mirrors the file_watcher `runtime` struct pattern but is flattened:
// one Runner per command invocation, no long-lived state.
type Runner struct {
	cfg    *config.Settings
	db     *svc.DB
	metric *svc.BufferedMetric
}

// Wire builds a Runner with all dependencies initialized.
// Must be called after config.Default().
// Each sub-command calls Wire() then defers Close().
func Wire() (*Runner, error) {
	cfg := config.GlobalSettings
	if cfg == nil {
		return nil, errors.New("handler: config not initialized — call config.Default() first")
	}
	// Ensure derived paths are present (config.Default sets them).
	if cfg.SQLitePath == "" {
		return nil, errors.New("handler: cfg.SQLitePath empty — config validate() may have failed")
	}
	db, err := svc.OpenDB(cfg.SQLitePath)
	if err != nil {
		return nil, fmt.Errorf("handler: open db: %w", err)
	}
	svcMetric := metric.NewMetricService("")
	bm, err := svc.NewBufferedMetric(svcMetric, filepath.Join(cfg.DataDir, "metric-buffer.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("handler: buffered metric: %w", err)
	}
	return &Runner{cfg: cfg, db: db, metric: bm}, nil
}

// Close releases owned resources.
// Best-effort: errors are logged but not propagated (the process is exiting).
func (r *Runner) Close() {
	if r == nil {
		return
	}
	if err := r.metric.Flush(); err != nil {
		slog.Warn("runner: metric flush failed", "err", err)
	}
	if err := r.db.Close(); err != nil {
		slog.Warn("runner: db close failed", "err", err)
	}
}

// SetRoots overrides cfg.Roots with the given paths (post-`~` expansion).
// Used by `scan --roots` to override settings.json at runtime.
func (r *Runner) SetRoots(paths []string) {
	if r == nil || r.cfg == nil {
		return
	}
	expanded := make([]string, 0, len(paths))
	for _, p := range paths {
		expanded = append(expanded, sdkcfg.ExpandHome(p))
	}
	r.cfg.Roots = expanded
}

// ScanOpts configures RunScan.
type ScanOpts struct {
	Date         string // override scan_date (default: today)
	Incremental  bool
	Workers      int
	Exclude      string // --exclude (overrides default)
	ExcludeAppend string // appended to defaults
	ExcludeReset bool   // empties default
	TopN         int
	MinSize      string
	ColdMode     string // off | report | warn
	Interactive  string // auto | true | false
	AllowMark    bool
	Progress     bool   // show live progress to stderr
}

// RunScan is the main entry for the `scan` sub-command.
// It performs the full pipeline: walk → collect → dedup → categorize → CSV → metric.
func (r *Runner) RunScan(ctx context.Context, opts ScanOpts) error {
	date := opts.Date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	minSize, err := r.cfg.BreakdownThresholdBytes()
	if err != nil {
		return fmt.Errorf("scan: parse breakdown_threshold: %w", err)
	}
	coldSize, err := r.cfg.ColdSizeThresholdBytes()
	if err != nil {
		return fmt.Errorf("scan: parse cold_size_threshold: %w", err)
	}

	// Apply exclude flags.
	exclude := r.cfg.ExcludeList
	if opts.ExcludeReset {
		exclude = nil
	}
	if opts.Exclude != "" {
		exclude = splitCSV(opts.Exclude)
	}
	if opts.ExcludeAppend != "" {
		exclude = append(exclude, splitCSV(opts.ExcludeAppend)...)
	}

	// Step 1: scan.
	scanOpts := svc.ScanOptions{
		Roots:              r.cfg.Roots,
		ExcludeList:        exclude,
		Workers:            opts.Workers,
		FollowSymlinks:     r.cfg.FollowSymlinks,
		PrivacyRedact:      r.cfg.PrivacyRedact,
		PrivacyRedactPaths: r.cfg.PrivacyRedactPaths,
	}
	if opts.Progress {
		scanOpts.Progress = renderProgressToStderr
	}
	scanner, err := svc.NewScanner(scanOpts)
	if err != nil {
		return fmt.Errorf("scan: new scanner: %w", err)
	}
	scanResult, err := scanner.Scan(ctx)
	if err != nil {
		return fmt.Errorf("scan: walk: %w", err)
	}
	slog.Info("scan complete",
		"files", scanResult.FileCount,
		"dirs", scanResult.DirCount,
		"bytes", scanResult.BytesScanned,
		"warnings", len(scanResult.Warnings),
		"duration", scanResult.Duration,
	)
	// If progress mode was on, terminate the carriage-return line so the
	// final summary and subsequent sections render on their own lines.
	if opts.Progress {
		fmt.Fprintln(os.Stderr)
	}

	// Step 2: collector + cache write.
	// Persist each scanner entry to the DB so report builders can read them back.
	rows := make([]svc.EntryRow, 0, len(scanResult.Entries))
	for _, e := range scanResult.Entries {
		cat := svc.CategorizeByExt(e.Path)
		rows = append(rows, svc.RowFromEntry(e, date, cat, e.Depth))
	}
	if _, err := r.db.UpsertEntries(rows); err != nil {
		return fmt.Errorf("scan: persist rows: %w", err)
	}
	for _, w := range scanResult.Warnings {
		slog.Warn("scan warning", "msg", w)
	}

	// Step 3: read back the just-persisted rows to populate report sections.
	allRows, err := r.db.EntriesForDate(date)
	if err != nil {
		return fmt.Errorf("scan: read rows: %w", err)
	}
	allEntries := rowsToEntries(allRows)

	// Step 4: report sections.
	b := svc.NewReportBuilder()

	// Overview — root + home at configured depths.
	overviewRoot := svc.BuildOverview(allEntries, "/", r.cfg.OverviewRootDepth, opts.TopN)
	overviewHome := svc.BuildOverview(allEntries, home(), r.cfg.OverviewHomeDepth, opts.TopN)
	if overviewRoot != nil {
		b.Set(svc.SectionOverviewRoot, overviewRoot)
	}
	if overviewHome != nil {
		b.Set(svc.SectionOverviewHome, overviewHome)
	}

	// Breakdown — root + home, > minSize.
	breakdownRoot := svc.BuildBreakdown(allEntries, "/", minSize)
	breakdownHome := svc.BuildBreakdown(allEntries, home(), minSize)
	if breakdownRoot != nil {
		b.Set(svc.SectionBreakdownRoot, breakdownRoot)
	}
	if breakdownHome != nil {
		b.Set(svc.SectionBreakdownHome, breakdownHome)
	}

	// Step 5: dedup.
	hasher := svc.NewHasher(svc.HashOptions{Workers: opts.Workers})
	dupGroups, err := hasher.DetectDuplicates(ctx, allEntries)
	if err != nil {
		slog.Warn("scan: dedup failed, continuing", "err", err)
	}
	if dupGroups != nil {
		b.Set(svc.SectionDuplicate, svc.DuplicateSectionEntries(dupGroups))
		// Mark in DB.
		for _, g := range dupGroups {
			_ = r.db.MarkDuplicates(date, g.Paths, true)
		}
	}

	// Step 6: cold.
	cutoff := time.Now().Add(-time.Duration(r.cfg.ColdAgeDays) * 24 * time.Hour)
	cold := svc.BuildCold(allEntries, cutoff, coldSize)
	if cold != nil {
		b.Set(svc.SectionCold, cold)
	}

	// Step 7: warnings section.
	if len(scanResult.Warnings) > 0 {
		entries := make([]svc.ReportEntry, 0, len(scanResult.Warnings))
		for _, w := range scanResult.Warnings {
			entries = append(entries, svc.ReportEntry{Path: w, Category: "warning"})
		}
		b.Set(svc.SectionPermissionWarn, entries)
	}

	// Step 8: sort by path and emit.
	b.SortEntriesByPath()
	sections := b.Sections()

	// Step 9: print to stdout.
	svc.PrintSections(os.Stdout, sections, svc.DefaultRenderOptions())

	// Step 10: write CSV.
	csvPath := filepath.Join(r.cfg.CSVDir, date+".csv")
	if err := svc.WriteCSV(svc.CSVOptions{Path: csvPath, Sections: sections}); err != nil {
		return fmt.Errorf("scan: write csv: %w", err)
	}
	slog.Info("csv written", "path", csvPath)

	// Step 11: emit metric.
	summary := svc.ScanSummaryMetric{
		TotalBytes: scanResult.BytesScanned,
		TotalFiles: scanResult.FileCount,
		TotalDirs:  scanResult.DirCount,
		DurationMS: scanResult.Duration.Milliseconds(),
		Warnings:   int64(len(scanResult.Warnings)),
	}
	if err := r.metric.EmitScanSummary(ctx, summary); err != nil {
		slog.Warn("metric emit failed", "err", err)
	}

	_ = opts.Incremental
	_ = opts.ColdMode
	_ = opts.Interactive
	_ = opts.AllowMark
	_ = opts.MinSize
	return nil
}

// RunShow reads historical rows for a given date and prints them.
// Empty date → latest in DB.
func (r *Runner) RunShow(ctx context.Context, date string) error {
	_ = ctx
	if date == "" {
		var err error
		date, err = r.db.LatestDate()
		if err != nil {
			return fmt.Errorf("show: latest date: %w", err)
		}
		if date == "" {
			return errors.New("show: no scans in DB yet")
		}
	}
	rows, err := r.db.EntriesForDate(date)
	if err != nil {
		return fmt.Errorf("show: read rows: %w", err)
	}
	allEntries := rowsToEntries(rows)
	b := svc.NewReportBuilder()
	overviewRoot := svc.BuildOverview(allEntries, "/", r.cfg.OverviewRootDepth, r.cfg.TopN)
	b.Set(svc.SectionOverviewRoot, overviewRoot)
	b.SortEntriesByPath()
	svc.PrintSections(os.Stdout, b.Sections(), svc.DefaultRenderOptions())
	return nil
}

// RunDiff compares two dates and prints top growth entries.
func (r *Runner) RunDiff(ctx context.Context, date1, date2 string) error {
	_ = ctx
	if date1 == "" || date2 == "" {
		return errors.New("diff: both --from and --to dates required")
	}
	d1Rows, d2Rows, err := r.db.EntriesBetween(date1, date2)
	if err != nil {
		return fmt.Errorf("diff: read: %w", err)
	}
	d1Entries := rowsToEntries(d1Rows)
	d2Entries := rowsToEntries(d2Rows)
	diff := svc.DiffEntries(d2Entries, d1Entries)
	top := svc.TopGrowth(diff, r.cfg.TopN)
	fmt.Fprintf(os.Stdout, "diff %s → %s — top %d growth paths:\n", date1, date2, r.cfg.TopN)
	for _, e := range top {
		fmt.Fprintf(os.Stdout, "  %-50s  %s  +%s\n",
			e.Path, svc.FormatBytes(e.Size), svc.FormatBytes(e.ChangeSize))
	}
	return nil
}

// LatestDate returns the most recent scan_date present in the DB.
// Exposed so cmd/tui.go can pick the latest scan to load.
func (r *Runner) LatestDate() (string, error) {
	return r.db.LatestDate()
}

// ReadDate returns the EntryRows for a given date.
// Exposed so cmd/tui.go can load the raw rows for tree rendering.
func (r *Runner) ReadDate(date string) ([]svc.EntryRow, error) {
	return r.db.EntriesForDate(date)
}

// renderProgressToStderr writes a one-line progress snapshot to stderr with a
// carriage return so subsequent updates overwrite the same line. ANSI escapes
// are used minimally — non-TTY stderr will just show stale-line churn, which
// is acceptable for a CLI tool.
//
// Format:
//
//   [disk-analyzer pid=12345]  1234 files · 234 dirs · 12.3 MB scanned · 5 warnings · 8 goroutines · 1.2s elapsed
func renderProgressToStderr(snap svc.ProgressSnapshot) {
	const clear = "\x1b[K"
	line := fmt.Sprintf("\r[disk-analyzer pid=%d]  %d files · %d dirs · %s scanned · %d warnings · %d goroutines · %s elapsed%s",
		snap.PID, snap.Files, snap.Dirs, svc.FormatBytes(snap.Bytes),
		snap.Warnings, snap.Goroutines, snap.Elapsed.Round(time.Millisecond), clear)
	fmt.Fprint(os.Stderr, line)
}

// rowsToEntries converts DB rows back into the Entry type used in collectors.
func rowsToEntries(rows []svc.EntryRow) []svc.Entry {
	out := make([]svc.Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, svc.Entry{
			Path: r.Path,
			Size: r.SizeBytes,
			LastModified: time.Unix(r.MTimeUnix, 0),
			LastAccessed: time.Unix(r.ATimeUnix, 0),
			IsDir: r.IsDir,
			Depth: r.Depth,
		})
	}
	return out
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return h
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	curr := ""
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			if curr != "" {
				out = append(out, curr)
				curr = ""
			}
			continue
		}
		curr += string(s[i])
	}
	if curr != "" {
		out = append(out, curr)
	}
	return out
}

// retainDateShape ensures the date string is YYYY-MM-DD; returns "" if invalid.
func retainDateShape(s string) string {
	_, err := time.Parse("2006-01-02", s)
	if err != nil {
		return ""
	}
	return s
}