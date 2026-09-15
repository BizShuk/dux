package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/bizshuk/dux/config"
	"github.com/bizshuk/dux/handler"
	"github.com/bizshuk/dux/svc"

	"github.com/spf13/cobra"
)

var (
	analyzeDate       string
	analyzeTop        int
	analyzeReviewTop  int
	analyzeMinSize    string
	analyzeRules      string
	analyzeNoCollapse bool
	analyzeMembers    bool
	analyzeShowRules  bool
)

var AnalyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Rank a scan into deletion candidates, counting every byte once",
	Long: `Turn a scan CSV into a ranked list of things that can actually be deleted.

A directory whose entire subtree is regenerable (cache, build artifact,
re-downloadable dependency) is listed as a single row and its children are
suppressed, so nothing is counted twice. A directory that is not wholly
deletable is never proposed; analyze drills into it looking for deletable
children instead, and reports it under "manual review" only when it hides
nothing reclaimable.

Verdicts come from a rule table embedded in the binary — see --show-rules —
which can be replaced with --rules. Paths matching no rule default to manual
review, so anything unrecognized is shown rather than proposed for deletion.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		rules, err := svc.DefaultRules()
		if analyzeRules != "" {
			rules, err = svc.LoadRules(analyzeRules)
		}
		if err != nil {
			return fmt.Errorf("analyze: rules: %w", err)
		}
		if analyzeShowRules {
			svc.PrintRules(os.Stdout, rules)
			return nil
		}

		var minSize int64
		if analyzeMinSize != "" {
			v, perr := config.ParseSize(analyzeMinSize)
			if perr != nil {
				return fmt.Errorf("analyze: parse --min-size %q: %w", analyzeMinSize, perr)
			}
			minSize = v
		}

		r, err := handler.Wire()
		if err != nil {
			return fmt.Errorf("analyze: wire: %w", err)
		}
		return r.RunAnalyze(context.Background(), analyzeDate, handler.AnalyzeOptions{
			Rollup: svc.RollupOptions{
				Top:        analyzeTop,
				ReviewTop:  analyzeReviewTop,
				MinSize:    minSize,
				NoCollapse: analyzeNoCollapse,
			},
			RulesPath:   analyzeRules,
			ShowMembers: analyzeMembers,
		})
	},
}

func init() {
	AnalyzeCmd.Flags().StringVar(&analyzeDate, "date", "",
		"scan date (YYYY-MM-DD, default: latest scan CSV)")
	AnalyzeCmd.Flags().IntVar(&analyzeTop, "top", 20, "number of deletion candidates")
	AnalyzeCmd.Flags().IntVar(&analyzeReviewTop, "review", 10,
		"number of manual-review rows (0 hides the section, -1 for all)")
	AnalyzeCmd.Flags().StringVar(&analyzeMinSize, "min-size", "",
		"ignore entries smaller than this (e.g. 500MB, 1GB)")
	AnalyzeCmd.Flags().StringVar(&analyzeRules, "rules", "",
		"path to a rules.json replacing the embedded rule table")
	AnalyzeCmd.Flags().BoolVar(&analyzeNoCollapse, "no-collapse", false,
		"list every sibling match instead of grouping repeats into one row")
	AnalyzeCmd.Flags().BoolVar(&analyzeMembers, "members", false,
		"list the individual paths behind each collapsed row")
	AnalyzeCmd.Flags().BoolVar(&analyzeShowRules, "show-rules", false,
		"print the deletability rule table and exit")
}
