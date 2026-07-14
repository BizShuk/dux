// Package handler is the façade for dux sub-commands that need
// cross-cutting settings (show / diff / tui). After the SQLite removal,
// Runner no longer holds long-lived state — every method either reads a
// CSV file or transforms an in-memory slice.
//
// `scan` is driven directly from cmd/scan.go using the `du` library and
// does not flow through this package.
package handler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bizshuk/dux/config"
	"github.com/bizshuk/dux/svc"
)

// Runner is the façade for show / diff / tui. One Runner per command
// invocation; no resources to close (CSV reads are local and short-lived).
type Runner struct {
	cfg *config.Settings
}

// Wire builds a Runner with the active settings. Must be called after
// config.Default(). Returns an error when settings are missing or CSVDir
// is empty (which would prevent any subsequent CSV read from resolving).
func Wire() (*Runner, error) {
	cfg := config.GlobalSettings
	if cfg == nil {
		return nil, errors.New("handler: config not initialized — call config.Default() first")
	}
	if cfg.CSVDir == "" {
		return nil, errors.New("handler: cfg.CSVDir empty — config validate() may have failed")
	}
	return &Runner{cfg: cfg}, nil
}

// RunShow reads a scan CSV and prints the root overview section.
// Empty date → latest CSV by stem (filename date, ISO-8601).
func (r *Runner) RunShow(ctx context.Context, date string, prefix string) error {
	_ = ctx
	if date == "" {
		latest, err := svc.LatestDate(r.cfg.CSVDir)
		if err != nil {
			if errors.Is(err, svc.ErrNoScans) {
				return errors.New("show: no scan CSV yet — run 'scan' first")
			}
			return fmt.Errorf("show: latest date: %w", err)
		}
		date = latest
	}
	rows, err := svc.ReadCSV(filepath.Join(r.cfg.CSVDir, date+".csv"))
	if err != nil {
		return fmt.Errorf("show: read csv: %w", err)
	}
	overviewRoot := svc.BuildOverview(rows, "/", r.cfg.OverviewRootDepth, r.cfg.TopN)

	b := svc.NewReportBuilder()
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
	ropts := svc.DefaultRenderOptions()
	ropts.Prefix = prefix
	svc.PrintSections(os.Stdout, sections, ropts)
	return nil
}

// RunDiff compares two scan CSVs and prints the top growth paths.
// Empty date1 defaults to yesterday; empty date2 defaults to today
// (both in the user's local timezone).
func (r *Runner) RunDiff(ctx context.Context, date1, date2 string, prefix string) error {
	_ = ctx
	now := time.Now()
	if date2 == "" {
		date2 = now.Format("2006-01-02")
	}
	if date1 == "" {
		date1 = now.AddDate(0, 0, -1).Format("2006-01-02")
	}
	d1Rows, err := svc.ReadCSV(filepath.Join(r.cfg.CSVDir, date1+".csv"))
	if err != nil {
		return fmt.Errorf("diff: read %s: %w", date1, err)
	}
	d2Rows, err := svc.ReadCSV(filepath.Join(r.cfg.CSVDir, date2+".csv"))
	if err != nil {
		return fmt.Errorf("diff: read %s: %w", date2, err)
	}
	d1Entries := reportsToEntries(d1Rows)
	d2Entries := reportsToEntries(d2Rows)
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

// LatestDate returns the most recent scan date found in CSVDir.
func (r *Runner) LatestDate() (string, error) {
	return svc.LatestDate(r.cfg.CSVDir)
}

// ReadDate returns the snapshot rows for a given date, adapted to the
// slim EntryRow shape the TUI tree builder consumes.
func (r *Runner) ReadDate(date string) ([]svc.EntryRow, error) {
	rows, err := svc.ReadCSV(filepath.Join(r.cfg.CSVDir, date+".csv"))
	if err != nil {
		return nil, err
	}
	out := make([]svc.EntryRow, len(rows))
	for i, r := range rows {
		out[i] = svc.EntryRow{
			Path:      r.Path,
			IsDir:     r.IsDir,
			SizeBytes: r.Size,
			Depth:     r.Depth,
		}
	}
	return out, nil
}

// reportsToEntries converts ReportEntry rows (the CSV shape) into the
// narrower Entry shape consumed by svc.DiffEntries.
func reportsToEntries(rows []svc.ReportEntry) []svc.Entry {
	out := make([]svc.Entry, len(rows))
	for i, r := range rows {
		out[i] = svc.Entry{
			Path:         r.Path,
			Size:         r.Size,
			LastAccessed: r.LastAccessed,
			IsDir:        r.IsDir,
			Depth:        r.Depth,
		}
	}
	return out
}
