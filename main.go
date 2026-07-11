// dux — high-performance disk usage analyzer.
//
// Single-shot CLI driven by cobra sub-commands:
//
//	scan    perform a full scan + write CSV/DB + emit metrics
//	show    display a past scan
//	diff    compare two scans
//	tui     launch the interactive tree browser
//	version print version
//
// Architecture follows the file_watcher convention:
//   - config.Default() bootstraps gosdk settings
//   - cmd/*.go declares one cobra command per file with init() self-registration
//   - handler.Wire() builds a Runner with all dependencies
//   - svc/ holds domain types and operations
//   - tui/ holds the bubbletea-based interactive shell
package main

import (
	"github.com/bizshuk/dux/cmd"
	"github.com/bizshuk/dux/config"
)

func main() {
	if err := config.Default(); err != nil {
		// Fall back to a minimal root so cobra still prints help.
		// This preserves the convention from file_watcher/cmd/monitor.go.
		cmd.Execute()
		return
	}
	cmd.Execute()
}
