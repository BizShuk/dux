package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

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
	Short: "Launch the interactive tree browser (loads latest scan from DB)",
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := handler.Wire()
		if err != nil {
			return fmt.Errorf("tui: wire: %w", err)
		}
		defer r.Close()

		date := tuiDate
		if date == "" {
			var lerr error
			date, lerr = r.LatestDate()
			if lerr != nil {
				return fmt.Errorf("tui: pick date: %w", lerr)
			}
		}
		if date == "" {
			return errors.New("tui: no scans in DB yet — run 'scan' first")
		}
		rows, err := r.ReadDate(date)
		if err != nil {
			return fmt.Errorf("tui: read date: %w", err)
		}
		entries := rowsToEntries(rows)
		cfg := config.GlobalSettings

		// Build sections similar to scan.go for a unified display.
		b := svc.NewReportBuilder()
		b.Set(svc.SectionOverviewRoot, svc.BuildOverview(entries, "/", cfg.OverviewRootDepth, cfg.TopN))
		h := os.Getenv("HOME")
		if h == "" {
			h = "/"
		}
		b.Set(svc.SectionOverviewHome, svc.BuildOverview(entries, h, cfg.OverviewHomeDepth, cfg.TopN))
		b.Set(svc.SectionBreakdownRoot, svc.BuildBreakdown(entries, "/", 200*1024*1024))
		b.Set(svc.SectionBreakdownHome, svc.BuildBreakdown(entries, h, 200*1024*1024))
		b.SortEntriesByPath()

		m := tui.NewModel(b.Sections(), tuiAllowMark)
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
		"scan date to load (default: latest in DB)")
	TuiCmd.Flags().BoolVar(&tuiAllowMark, "allow-mark", false,
		"enable mark/unmark keys")
}

// rowsToEntries converts DB rows to in-memory Entry for report builders.
func rowsToEntries(rows []svc.EntryRow) []svc.Entry {
	out := make([]svc.Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, svc.Entry{
			Path:         r.Path,
			Size:         r.SizeBytes,
			LastModified: time.Unix(r.MTimeUnix, 0),
			LastAccessed: time.Unix(r.ATimeUnix, 0),
			IsDir:        r.IsDir,
			Depth:        r.Depth,
		})
	}
	return out
}