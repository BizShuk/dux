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

	"github.com/bizshuk/disk-analyzer/config"
	"github.com/bizshuk/disk-analyzer/du"
	"github.com/bizshuk/disk-analyzer/svc"

	"github.com/spf13/cobra"
)

// Scan flag vars. Kept minimal: only date / threshold / top / progress.
var (
	scanDate     string
	scanMinSize  string
	scanTop      int
	scanProgress bool
)

var ScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Walk $HOME with du and write entries ≥ threshold to CSV",
	Long: `Run a disk-usage scan rooted at the user's home directory and persist
the entries that meet the size threshold (default 200MB) to a CSV file under
the configured CSV directory. The CSV uses the same schema as other sections
(path, size, change_size, is_duplicate, last_accessed, category) so it can be
ingested by spreadsheets or scripts.

By design this command is single-shot and write-only — it does NOT populate
SQLite, so ` + "`show`" + `, ` + "`diff`" + `, and ` + "`tui`" + ` will not find new
data until they are rewired to read the CSV in a future change.`,
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
		_ = scanTop // reserved for a future breakdown top-N display; ignored for now
		_ = scanProgress // reserved; future PR will wire a live counter

		duOpts := du.Options{
			Paths:     []string{home},
			AllFiles:  true,             // emit both files and directories
			Threshold: threshold,        // positive value → exclude entries smaller than this
			MaxDepth:  math.MaxInt,      // -1 in du.Options = unlimited; 0 would mean "summarize only"
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
		// Depth-0 (the scan root, $HOME) is dropped: du's `shouldEmit` always
		// emits the root regardless of threshold, but the user's intent is
		// "files and folders ≥ threshold", not the home-dir total itself.
		rows := make([]svc.ReportEntry, 0, len(result.Entries))
		for _, e := range result.Entries {
			if e.Depth == 0 {
				continue
			}
			rows = append(rows, svc.ReportEntry{
				Path:         e.Path,
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
}