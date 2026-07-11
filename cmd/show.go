package cmd

import (
	"context"
	"fmt"

	"github.com/bizshuk/disk-analyzer/handler"

	"github.com/spf13/cobra"
)

var (
	showDate string
)

var ShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display the result of a past scan",
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := handler.Wire()
		if err != nil {
			return fmt.Errorf("show: wire: %w", err)
		}
		defer r.Close()
		return r.RunShow(context.Background(), showDate)
	},
}

func init() {
	RootCmd.AddCommand(ShowCmd)
	ShowCmd.Flags().StringVar(&showDate, "date", "",
		"scan date (YYYY-MM-DD, default: latest in DB)")
}