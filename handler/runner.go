// Package handler is the DI container for the disk-analyzer sub-commands.
// After the scan-rewrite, the only remaining responsibilities are:
//   - open the SQLite DB once per invocation (show / diff / tui read from it)
//   - dispatch each sub-command to a single Run* method on Runner
//
// `scan` is now driven directly from cmd/scan.go using the `du` library and
// no longer touches the DB, the metric buffer, or the excludes/privacy layers.
// `show` / `diff` / `tui` are kept as-is so historical CSV/DB consumers
// continue to work until a future PR rewires them to read the CSV.
package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/bizshuk/disk-analyzer/config"
	"github.com/bizshuk/disk-analyzer/svc"
)

// Runner is the DI container for disk-analyzer commands that need the DB.
// One Runner per command invocation, no long-lived state.
type Runner struct {
	cfg *config.Settings
	db  *svc.DB
}

// Wire builds a Runner with the DB initialized.
// Must be called after config.Default().
func Wire() (*Runner, error) {
	cfg := config.GlobalSettings
	if cfg == nil {
		return nil, errors.New("handler: config not initialized — call config.Default() first")
	}
	if cfg.SQLitePath == "" {
		return nil, errors.New("handler: cfg.SQLitePath empty — config validate() may have failed")
	}
	db, err := svc.OpenDB(cfg.SQLitePath)
	if err != nil {
		return nil, fmt.Errorf("handler: open db: %w", err)
	}
	return &Runner{cfg: cfg, db: db}, nil
}

// Close releases owned resources. Best-effort: errors are logged but not
// propagated (the process is exiting).
func (r *Runner) Close() {
	if r == nil {
		return
	}
	if err := r.db.Close(); err != nil {
		slog.Warn("runner: db close failed", "err", err)
	}
}

// RunShow reads historical rows for a given date and prints them.
// Empty date → latest in DB.
func (r *Runner) RunShow(ctx context.Context, date string, prefix string) error {
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
	streamer := func(cb func(svc.EntryRow) error) error {
		return r.db.StreamEntries(date, cb)
	}
	b := svc.NewReportBuilder()
	overviewRoot := svc.BuildOverview(streamer, "/", r.cfg.OverviewRootDepth, r.cfg.TopN)
	b.Set(svc.SectionOverviewRoot, overviewRoot)
	b.SortEntriesByPath()

	sections := b.Sections()
	if prefix != "" {
		for i := range sections {
			filtered := make([]svc.ReportEntry, 0)
			for _, entry := range sections[i].Entries {
				if strings.HasPrefix(entry.Path, prefix) {
					filtered = append(filtered, entry)
				}
			}
			sections[i].Entries = filtered
		}
	}
	// Pass prefix through to the renderer so it can emit a "Showing:"
	// banner when the prefix is short enough to render in full.
	ropts := svc.DefaultRenderOptions()
	ropts.Prefix = prefix
	svc.PrintSections(os.Stdout, sections, ropts)
	return nil
}

// RunDiff compares two dates and prints top growth entries.
func (r *Runner) RunDiff(ctx context.Context, date1, date2 string, prefix string) error {
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

	if prefix != "" {
		filtered := make([]svc.ReportEntry, 0)
		for _, e := range diff {
			if strings.HasPrefix(e.Path, prefix) {
				filtered = append(filtered, e)
			}
		}
		diff = filtered
	}

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

// retainDateShape ensures the date string is YYYY-MM-DD; returns "" if invalid.
func retainDateShape(s string) string {
	_, err := time.Parse("2006-01-02", s)
	if err != nil {
		return ""
	}
	return s
}