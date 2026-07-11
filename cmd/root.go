// Package cmd wires cobra commands for disk-analyzer.
//
// Each file declares a var XxxCmd = &cobra.Command{...} and registers itself
// in init() with RootCmd.AddCommand(...). This mirrors the file_watcher
// convention exactly.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var RootCmd = &cobra.Command{
	Use:   "disk-analyzer",
	Short: "High-performance disk usage analyzer with TUI + observability",
	Long: `disk-analyzer — single-shot disk analysis with concurrent scanning,
incremental caching, duplicate detection, cold-data identification, and CSV export.

Use 'scan' for a full scan, 'show' to view a past scan, 'diff' to compare
two dates, and 'tui' to launch the interactive tree browser.`,
}

// Execute is the cobra entry point. Called from main().
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print disk-analyzer version",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("disk-analyzer %s\n", version)
		return nil
	},
}

func init() {
	RootCmd.AddCommand(versionCmd)
}