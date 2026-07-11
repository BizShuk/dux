package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/bizshuk/dux/config"
	"github.com/bizshuk/dux/du"
	"github.com/bizshuk/dux/svc"

	"github.com/spf13/cobra"
)

// Scan flag vars. Kept minimal: only date / threshold / top / progress.
var (
	scanDate        string
	scanMinSize     string
	scanTop         int
	scanProgress    bool
	scanExcludes    []string
	scanExcludeFrom string
)

var ScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Walk $HOME with du and write entries ≥ threshold to CSV",
	Long: `Run a disk-usage scan rooted at the user's home directory and persist
the entries that meet the size threshold (default 200MB) to a CSV file under
the configured CSV directory. The CSV uses the v2 schema:

  path,is_dir,depth,size,change_size,is_duplicate,last_accessed,category

so it can be ingested by spreadsheets, scripts, or the dux
` + "`show`" + `, ` + "`diff`" + `, and ` + "`tui`" + ` sub-commands.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sig
			slog.Info("scan: interrupt received, cancelling")
			cancel()
		}()

		cfg := config.GlobalSettings
		if cfg == nil {
			return errors.New("scan: config not initialized — call config.Default() first")
		}
		if cfg.CSVDir == "" {
			return errors.New("scan: cfg.CSVDir empty — config validate() may have failed")
		}

		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("scan: home dir: %w", err)
		}

		date := scanDate
		if date == "" {
			date = time.Now().Format("2006-01-02")
		}

		// Threshold defaults to 200MB; --min-size overrides (KB/MB/GB/TB SI units).
		const defaultThresholdBytes = int64(200 * 1024 * 1024)
		threshold := defaultThresholdBytes
		if scanMinSize != "" {
			v, perr := config.ParseSize(scanMinSize)
			if perr != nil {
				return fmt.Errorf("scan: parse --min-size %q: %w", scanMinSize, perr)
			}
			if v <= 0 {
				return fmt.Errorf("scan: --min-size must be positive (got %d)", v)
			}
			threshold = v
		}
		_ = scanTop      // reserved for a future breakdown top-N display; ignored for now
		_ = scanProgress // reserved; future PR will wire a live counter

		// Merge excludes: config provides persistent baseline, CLI appends.
		// ExcludeFrom is a single absolute path, so CLI overrides config when set.
		mergedExcludes := make([]string, 0, len(cfg.Excludes)+len(scanExcludes))
		mergedExcludes = append(mergedExcludes, cfg.Excludes...)
		mergedExcludes = append(mergedExcludes, scanExcludes...)
		excludeFrom := scanExcludeFrom
		if excludeFrom == "" {
			excludeFrom = cfg.ExcludeFrom
		}

		duOpts := du.Options{
			Paths:       []string{home},
			AllFiles:    true,        // emit both files and directories
			Threshold:   threshold,   // positive value → exclude entries smaller than this
			MaxDepth:    math.MaxInt, // -1 in du.Options = unlimited; 0 would mean "summarize only"
			Excludes:    mergedExcludes,
			ExcludeFrom: excludeFrom,
		}

		slog.Info("scan: starting du walk",
			"root", home,
			"threshold_bytes", threshold,
			"threshold_human", svc.FormatBytes(threshold),
		)

		result, err := du.Usage(ctx, duOpts)
		if err != nil {
			return fmt.Errorf("scan: du: %w", err)
		}

		// Map du.EntryInfo → svc.ReportEntry. LastAccessed carries MaxTime (mtime)
		// because du does not expose atime; CSV consumers can interpret it as
		// "most recent activity observed in the subtree" for directories.
		//
		// All entries (including depth-0 = $HOME root) are written so the TUI
		// has a tree root to anchor. `du`'s shouldEmit applies the size threshold
		// uniformly, so entries below threshold are already absent.
		rows := make([]svc.ReportEntry, 0, len(result.Entries))
		for _, e := range result.Entries {
			rows = append(rows, svc.ReportEntry{
				Path:         e.Path,
				IsDir:        e.IsDir,
				Size:         int64(e.Info.Size),
				Category:     svc.CategorizeByExt(e.Path),
				LastAccessed: e.Info.MaxTime,
				Depth:        e.Depth,
			})
		}
		// Sort by size desc so the largest offenders surface first in the CSV.
		sort.Slice(rows, func(i, j int) bool { return rows[i].Size > rows[j].Size })

		// Wrap into a single ReportSection. We use SectionBreakdownHome because
		// (a) we are scanning $HOME and (b) the section name is stable across
		// CSV consumers. No overview/duplicate/cold sections are produced — that
		// is intentional given the simplified scope.
		b := svc.NewReportBuilder()
		b.Set(svc.SectionBreakdownHome, rows)
		sections := b.Sections()

		csvPath := filepath.Join(cfg.CSVDir, date+".csv")
		if err := svc.WriteCSV(svc.CSVOptions{Path: csvPath, Sections: sections}); err != nil {
			return fmt.Errorf("scan: write csv: %w", err)
		}

		fmt.Fprintf(os.Stderr, "scan: %d entries ≥ %s → %s\n",
			len(rows), svc.FormatBytes(threshold), csvPath)
		slog.Info("scan complete",
			"entries", len(rows),
			"threshold_bytes", threshold,
			"csv", csvPath,
		)
		return nil
	},
}

func init() {
	RootCmd.AddCommand(ScanCmd)
	ScanCmd.Flags().StringVar(&scanDate, "date", "", "scan date (YYYY-MM-DD, default: today)")
	ScanCmd.Flags().StringVar(&scanMinSize, "min-size", "200MB",
		"minimum size for inclusion in the CSV (e.g. 200MB, 1GB)")
	ScanCmd.Flags().IntVar(&scanTop, "top", 10, "top-N entries per section (reserved)")
	ScanCmd.Flags().BoolVarP(&scanProgress, "progress", "p", false,
		"show live progress (reserved; not wired in this version)")
	ScanCmd.Flags().StringSliceVar(&scanExcludes, "exclude", nil,
		"glob pattern to exclude (repeatable); merged with config excludes "+
			"(basename match: \"*.tmp\"; path match: \"Library/Caches\")")
	ScanCmd.Flags().StringVar(&scanExcludeFrom, "exclude-from", "",
		"path to file of exclude patterns (one per line, # = comment); "+
			"overrides config exclude_from when set")
}
