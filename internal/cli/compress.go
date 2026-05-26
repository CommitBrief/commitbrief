package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

func newCompressCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "compress",
		Short: "Shrink COMMITBRIEF.md losslessly (Phase 8)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("compress: not yet implemented (Phase 8 / v0.4.0)")
		},
	}
}
