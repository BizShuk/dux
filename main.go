// dux — high-performance disk usage analyzer.
package main

import (
	"fmt"
	"os"

	appcmd "github.com/bizshuk/dux/cmd"
	cmddelete "github.com/bizshuk/dux/cmd/delete"
	appconfig "github.com/bizshuk/dux/config"
	"github.com/spf13/cobra"
)

// RootCmd is the executable's cobra composition root. Reusable commands live
// in cmd/, while the host application decides which commands to expose.
var RootCmd = &cobra.Command{
	Use:   "dux",
	Short: "High-performance disk usage analyzer with TUI + observability",
	Long: `dux — single-shot disk analysis with concurrent scanning and CSV export.

Use 'scan' for a full scan, 'analyze' to rank what can be deleted,
'show' to view a past scan, 'diff' to compare
two dates, 'monitor' (alias 'm') to launch the interactive tree browser, and 'config' to
inspect or update application configuration.`,
}

func init() {
	RootCmd.AddCommand(
		appcmd.AnalyzeCmd,
		cmddelete.Cmd,
		appcmd.DiffCmd,
		appcmd.DuCmd,
		appcmd.ScanCmd,
		appcmd.ShowCmd,
		appcmd.MonitorCmd,
	)
}

func main() {
	if err := appconfig.Default(); err != nil {
		// Continue so cobra can still print help and expose the config command.
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
	}
	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
