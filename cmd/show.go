package cmd

import (
	"context"
	"fmt"

	"github.com/bizshuk/disk-analyzer/handler"

	"github.com/spf13/cobra"
)

var (
	showDate   string
	showPrefix string
)

var ShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display the result of a past scan",
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := handler.Wire()
		if err != nil {
			return fmt.Errorf("show: wire: %w", err)
		}
		return r.RunShow(context.Background(), showDate, showPrefix)
	},
}

func init() {
	RootCmd.AddCommand(ShowCmd)
	ShowCmd.Flags().StringVar(&showDate, "date", "",
		"scan date (YYYY-MM-DD, default: latest scan CSV)")
	ShowCmd.Flags().StringVar(&showPrefix, "prefix", "",
		"filter entries by path prefix")
}