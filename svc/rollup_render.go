package svc

import (
	"fmt"
	"io"
	"strings"
)

// rollup_render.go renders a RollupResult as a terminal report. Kept apart from
// render.go because that file renders ReportSection tables, not deletion plans.

// RollupRenderOptions controls the report layout.
type RollupRenderOptions struct {
	Width      int  // total rule width
	ShowMember bool // list the individual paths behind a collapsed row
}

// DefaultRollupRenderOptions returns a layout that fits a standard terminal.
func DefaultRollupRenderOptions() RollupRenderOptions {
	return RollupRenderOptions{Width: 100}
}

// PrintRollup writes the candidate table, the reclaimable total, and the
// manual-review list.
func PrintRollup(w io.Writer, source string, res RollupResult, opts RollupRenderOptions) {
	if opts.Width <= 0 {
		opts.Width = DefaultRollupRenderOptions().Width
	}
	rule := strings.Repeat("-", opts.Width)

	fmt.Fprintf(w, "scan: %s  (%d entries)\n\n", source, res.Scanned)
	if len(res.Candidates) == 0 {
		fmt.Fprintln(w, "no deletion candidates — lower --min-size or rescan")
	} else {
		fmt.Fprintf(w, "%3s  %8s  %-10s  %s\n", "#", "SIZE", "MTIME", "PATH")
		fmt.Fprintln(w, rule)
		for i, c := range res.Candidates {
			date := ""
			if !c.LastAccessed.IsZero() {
				date = c.LastAccessed.Format("2006-01-02")
			}
			fmt.Fprintf(w, "%3d  %8s  %-10s  %s\n", i+1, FormatBytes(c.Size), date, displayPath(c))
			fmt.Fprintf(w, "%3s  %8s  %-10s  └─ %s\n", "", "", "", c.Why)
			if c.Collapsed() && opts.ShowMember {
				for _, m := range c.Members {
					fmt.Fprintf(w, "%3s  %8s  %-10s     · %s\n", "", "", "", m)
				}
			}
		}
		fmt.Fprintln(w, rule)
	}
	fmt.Fprintf(w, "reclaimable (no double counting): %s\n", FormatBytes(res.Total))

	if len(res.Review) > 0 {
		fmt.Fprintln(w, "\nmanual review — big, not automatically safe to delete:")
		for _, c := range res.Review {
			fmt.Fprintf(w, "  %8s  %s\n", FormatBytes(c.Size), c.Path)
			fmt.Fprintf(w, "  %8s  └─ %s\n", "", c.Why)
		}
	}
}

// displayPath marks a collapsed row so nobody mistakes the anchor directory for
// something they can delete outright.
func displayPath(c Candidate) string {
	if !c.Collapsed() {
		return c.Path
	}
	return fmt.Sprintf("%s  [%d × %s]", c.Path, c.Count, pathBase(c.Glob))
}

// PrintRules lists the rule table in precedence order.
func PrintRules(w io.Writer, rs *RuleSet) {
	fmt.Fprintf(w, "%-8s  %-55s  %s\n", "VERDICT", "GLOB", "WHY")
	fmt.Fprintln(w, strings.Repeat("-", 110))
	for _, r := range rs.Rules() {
		glob := r.Glob
		if r.Collapse {
			glob += "  (collapse)"
		}
		fmt.Fprintf(w, "%-8s  %-55s  %s\n", r.Verdict, glob, r.Why)
	}
}
