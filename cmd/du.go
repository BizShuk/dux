package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bizshuk/dux/du"
	"github.com/spf13/cobra"
)

var (
	duAllFiles     bool
	duApparent     bool
	duMaxDepth     int
	duThreshold    int64
	duSeparateDirs bool
	duOneFS        bool
	duCountLinks   bool
	duExcludes     []string
	duExcludeFrom  string
	duTotal        bool
	duBytes        bool
	duHuman        bool
	duSummarize    bool
	duFollowLinks  string
)

var DuCmd = &cobra.Command{
	Use:   "du [paths...]",
	Short: "Summarize disk usage (coreutils du port)",
	Long: `A Go port of GNU coreutils du. Summarizes device usage of the given
paths, recursively for directories. Defaults to current directory if
no path is given.

Faithfully implements coreutils du semantics: depth-first accumulation
via dulevel stack, hardlink dedup via dev/ino pairs, apparent-size vs
disk-blocks sizing, and all major filtering options.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sig
			slog.Info("du: interrupt received, cancelling")
			cancel()
		}()

		opts := du.Options{
			Paths:         args,
			AllFiles:      duAllFiles,
			ApparentSize:  duApparent || duBytes,
			MaxDepth:      duMaxDepth,
			Threshold:     duThreshold,
			SeparateDirs:  duSeparateDirs,
			OneFileSystem: duOneFS,
			CountLinks:    duCountLinks,
			Excludes:      duExcludes,
			ExcludeFrom:   duExcludeFrom,
		}

		// -b = --apparent-size --block-size=1 (bytes mode).
		if duBytes {
			opts.ApparentSize = true
		}

		// -s = --summarize = --max-depth=0.
		if duSummarize {
			opts.MaxDepth = 0
		}

		// Symlink mode.
		switch duFollowLinks {
		case "L":
			opts.Symlinks = du.SymlinkLogical
		case "H", "D":
			opts.Symlinks = du.SymlinkCommandLine
		default:
			opts.Symlinks = du.SymlinkPhysical
		}

		result, err := du.Usage(ctx, opts)
		if err != nil {
			return fmt.Errorf("du: %w", err)
		}

		// Print entries.
		for _, e := range result.Entries {
			printDuEntry(e)
		}

		// Print grand total if -c/--total.
		if duTotal {
			printDuSize(result.GrandTotal.Size)
			fmt.Print("\t")
			fmt.Println("total")
		}

		return nil
	},
}

// printDuEntry formats a single entry matching coreutils du output.
func printDuEntry(e du.EntryInfo) {
	printDuSize(e.Info.Size)
	fmt.Print("\t")
	fmt.Println(e.Path)
}

// printDuSize prints the size value in the selected format.
func printDuSize(size uint64) {
	if duHuman {
		fmt.Print(humanSize(size))
	} else if duBytes || duApparent {
		fmt.Print(size)
	} else {
		// Default: 1K blocks (like du without flags).
		blocks := (size + 1023) / 1024
		fmt.Print(blocks)
	}
}

// humanSize formats bytes into human-readable units (1K = 1024).
func humanSize(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"K", "M", "G", "T", "P", "E"}
	return fmt.Sprintf("%.1f%s", float64(b)/float64(div), units[exp])
}

func init() {
	RootCmd.AddCommand(DuCmd)

	DuCmd.Flags().BoolVarP(&duAllFiles, "all", "a", false,
		"write counts for all files, not just directories")
	DuCmd.Flags().BoolVarP(&duApparent, "apparent-size", "A", false,
		"print apparent sizes rather than device usage")
	DuCmd.Flags().BoolVarP(&duBytes, "bytes", "b", false,
		"equivalent to --apparent-size --block-size=1")
	DuCmd.Flags().BoolVarP(&duTotal, "total", "c", false,
		"produce a grand total")
	DuCmd.Flags().IntVarP(&duMaxDepth, "max-depth", "d", -1,
		"max depth to display (-1 = unlimited)")
	DuCmd.Flags().BoolVar(&duHuman, "human-readable", false,
		"print sizes in human readable format (e.g., 1K 234M 2G)")
	DuCmd.Flags().BoolVarP(&duCountLinks, "count-links", "l", false,
		"count sizes many times if hard linked")
	DuCmd.Flags().BoolVarP(&duSummarize, "summarize", "s", false,
		"display only a total for each argument")
	DuCmd.Flags().BoolVarP(&duSeparateDirs, "separate-dirs", "S", false,
		"do not include size of subdirectories")
	DuCmd.Flags().BoolVarP(&duOneFS, "one-file-system", "x", false,
		"skip directories on different file systems")
	DuCmd.Flags().Int64VarP(&duThreshold, "threshold", "t", 0,
		"exclude entries smaller (positive) or larger (negative) than SIZE")
	DuCmd.Flags().StringSliceVar(&duExcludes, "exclude", nil,
		"exclude files matching PATTERN")
	DuCmd.Flags().StringVarP(&duExcludeFrom, "exclude-from", "X", "",
		"exclude patterns from FILE")
	DuCmd.Flags().StringVar(&duFollowLinks, "dereference", "P",
		"symlink mode: P (physical/default), L (logical), H/D (command-line only)")
}
