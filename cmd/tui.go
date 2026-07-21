package cmd

import (
	"errors"
	"fmt"
	"time"

	"github.com/bizshuk/dux/config"
	"github.com/bizshuk/dux/handler"
	"github.com/bizshuk/dux/svc"
	"github.com/bizshuk/dux/tui"

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

		// Build yesterday's path → size snapshot for the diff column.
		// If yesterday's CSV is missing we silently drop the diff column
		// rather than poisoning every row with "0 →" (which would mislead
		// the user into thinking every file was newly created).
		var prevSizes map[string]int64
		yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
		if yRows, yerr := r.ReadDate(yesterday); yerr == nil && len(yRows) > 0 {
			prevSizes = make(map[string]int64, len(yRows))
			for _, y := range yRows {
				prevSizes[y.Path] = y.SizeBytes
			}
		}

		m := tui.NewModel(rows, prevSizes, tuiAllowMark, cfg.PrivacyRedactPaths)
		p := tea.NewProgram(m, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("tui: program: %w", err)
		}
		return nil
	},
}

func init() {
	TuiCmd.Flags().StringVar(&tuiDate, "date", "",
		"scan date to load (default: latest scan CSV)")
	TuiCmd.Flags().BoolVar(&tuiAllowMark, "allow-mark", false,
		"enable mark/unmark keys")
}
