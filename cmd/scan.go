package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bizshuk/disk-analyzer/handler"
	"github.com/mattn/go-isatty"

	"github.com/spf13/cobra"
)

// Flag vars for `scan`. Declared at package scope so sub-commands can reuse
// them without coercion.
var (
	scanDate          string
	scanRoots         []string
	scanIncremental   bool
	scanWorkers       int
	scanExclude       string
	scanExcludeAppend string
	scanExcludeReset  bool
	scanTopN          int
	scanMinSize       string
	scanColdMode      string
	scanInteractive   string
	scanAllowMark     bool
	scanProgress      bool
	scanApparentSize  bool
)

var ScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Run a full (or incremental) disk analysis scan",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sig
			slog.Info("scan: interrupt received, cancelling")
			cancel()
		}()

		r, err := handler.Wire()
		if err != nil {
			return fmt.Errorf("scan: wire: %w", err)
		}
		defer r.Close()

		// Apply CLI --roots override (replaces settings.json roots entirely).
		if len(scanRoots) > 0 {
			r.SetRoots(scanRoots)
		}

		opts := handler.ScanOpts{
			Date:          scanDate,
			Incremental:   scanIncremental,
			Workers:       scanWorkers,
			Exclude:       scanExclude,
			ExcludeAppend: scanExcludeAppend,
			ExcludeReset:  scanExcludeReset,
			TopN:          scanTopN,
			MinSize:       scanMinSize,
			ColdMode:      scanColdMode,
			Interactive:   resolveInteractive(scanInteractive),
			AllowMark:     scanAllowMark,
			Progress:      scanProgress,
			ApparentSize:  scanApparentSize,
		}
		return r.RunScan(ctx, opts)
	},
}

func resolveInteractive(arg string) string {
	v := strings.ToLower(arg)
	switch v {
	case "true", "false":
		return v
	case "auto", "":
		// Auto-detect: TTY → true, otherwise false.
		if isatty.IsTerminal(os.Stdout.Fd()) && os.Getenv("DISK_ANALYZER_FORCE_TUI") != "false" {
			return "true"
		}
		return "false"
	default:
		slog.Warn("scan: unknown interactive mode, defaulting to auto", "got", arg)
		return "auto"
	}
}

func init() {
	RootCmd.AddCommand(ScanCmd)
	ScanCmd.Flags().StringVar(&scanDate, "date", "", "scan date (YYYY-MM-DD, default: today)")
	ScanCmd.Flags().StringSliceVar(&scanRoots, "roots", nil,
		"comma-separated paths to scan (overrides settings.json roots)")
	ScanCmd.Flags().BoolVar(&scanIncremental, "incremental", true, "use mtime-aware cache")
	ScanCmd.Flags().IntVar(&scanWorkers, "workers", 0, "max concurrent workers (default: NumCPU/2)")
	ScanCmd.Flags().StringVar(&scanExclude, "exclude", "",
		"override default exclude list with these patterns (comma-separated)")
	ScanCmd.Flags().StringVar(&scanExcludeAppend, "exclude-append", "",
		"append to the default exclude list (comma-separated)")
	ScanCmd.Flags().BoolVar(&scanExcludeReset, "exclude-reset", false,
		"start with empty exclude list before applying --exclude")
	ScanCmd.Flags().IntVar(&scanTopN, "top", 10, "top-N entries per section")
	ScanCmd.Flags().StringVar(&scanMinSize, "min-size", "200MB", "minimum size for breakdown")
	ScanCmd.Flags().StringVar(&scanColdMode, "cold-mode", "off",
		"cold-data handling: off | report | warn")
	ScanCmd.Flags().StringVar(&scanInteractive, "interactive", "auto",
		"TUI mode: auto | true | false")
	ScanCmd.Flags().BoolVar(&scanAllowMark, "allow-mark", false,
		"enable mark/unmark keys in TUI")
	ScanCmd.Flags().BoolVarP(&scanProgress, "progress", "p", false,
		"show live progress to stderr (counts files/dirs/bytes as they are scanned)")
	ScanCmd.Flags().BoolVar(&scanApparentSize, "apparent-size", false,
		"report logical file size (info.Size()) instead of on-disk allocation; matches `du -b` rather than `du`")

	if err := ScanCmd.MarkFlagFilename(""); err != nil {
		// No-op: just to demonstrate Cobra flag API parity.
	}
	_ = errors.New // keep imports explicit for future expansion
}