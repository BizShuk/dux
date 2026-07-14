package cmd

import (
	"context"
	"fmt"

	"github.com/bizshuk/dux/handler"

	"github.com/spf13/cobra"
)

var (
	diffFrom   string
	diffTo     string
	diffPrefix string
)

var DiffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Compare two scan dates and show top growth paths",
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := handler.Wire()
		if err != nil {
			return fmt.Errorf("diff: wire: %w", err)
		}
		return r.RunDiff(context.Background(), diffFrom, diffTo, diffPrefix)
	},
}

func init() {
	RootCmd.AddCommand(DiffCmd)
	DiffCmd.Flags().StringVar(&diffFrom, "from", "", "earlier scan date (YYYY-MM-DD, default: yesterday)")
	DiffCmd.Flags().StringVar(&diffTo, "to", "", "later scan date (YYYY-MM-DD, default: today)")
	DiffCmd.Flags().StringVar(&diffPrefix, "prefix", "", "filter entries by path prefix")
}
