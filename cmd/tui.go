package cmd

import (
	"errors"
	"fmt"

	"github.com/bizshuk/disk-analyzer/config"
	"github.com/bizshuk/disk-analyzer/handler"
	"github.com/bizshuk/disk-analyzer/svc"
	"github.com/bizshuk/disk-analyzer/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var (
	tuiDate      string
	tuiAllowMark bool
)

var TuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the interactive tree browser (loads latest scan CSV)",
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := handler.Wire()
		if err != nil {
			return fmt.Errorf("tui: wire: %w", err)
		}

		date := tuiDate
		if date == "" {
			var lerr error
			date, lerr = r.LatestDate()
			if lerr != nil {
				if errors.Is(lerr, svc.ErrNoScans) {
					return errors.New("tui: no scan CSV yet — run 'scan' first")
				}
				return fmt.Errorf("tui: pick date: %w", lerr)
			}
		}
		if date == "" {
			return errors.New("tui: no scans yet — run 'scan' first")
		}
		rows, err := r.ReadDate(date)
		if err != nil {
			return fmt.Errorf("tui: read date: %w", err)
		}
		if len(rows) == 0 {
			return errors.New("tui: no entries for that date — try another or run 'scan'")
		}
		cfg := config.GlobalSettings
		if cfg == nil {
			return errors.New("tui: config not initialized")
		}

		m := tui.NewModel(rows, tuiAllowMark, cfg.PrivacyRedactPaths)
		p := tea.NewProgram(m, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("tui: program: %w", err)
		}
		return nil
	},
}

func init() {
	RootCmd.AddCommand(TuiCmd)
	TuiCmd.Flags().StringVar(&tuiDate, "date", "",
		"scan date to load (default: latest scan CSV)")
	TuiCmd.Flags().BoolVar(&tuiAllowMark, "allow-mark", false,
		"enable mark/unmark keys")
}