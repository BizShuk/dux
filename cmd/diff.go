package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/bizshuk/disk-analyzer/handler"

	"github.com/spf13/cobra"
)

var (
	diffFrom string
	diffTo   string
)

var DiffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Compare two scan dates and show top growth paths",
	RunE: func(cmd *cobra.Command, args []string) error {
		if diffFrom == "" || diffTo == "" {
			return errors.New("diff: both --from and --to dates are required (YYYY-MM-DD)")
		}
		r, err := handler.Wire()
		if err != nil {
			return fmt.Errorf("diff: wire: %w", err)
		}
		defer r.Close()
		return r.RunDiff(context.Background(), diffFrom, diffTo)
	},
}

func init() {
	RootCmd.AddCommand(DiffCmd)
	DiffCmd.Flags().StringVar(&diffFrom, "from", "", "earlier scan date (YYYY-MM-DD)")
	DiffCmd.Flags().StringVar(&diffTo, "to", "", "later scan date (YYYY-MM-DD)")
}