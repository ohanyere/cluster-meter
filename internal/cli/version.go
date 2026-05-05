package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCommand(version string, deps dependencies) *cobra.Command {
	if version == "" {
		version = "dev"
	}

	return &cobra.Command{
		Use:   "version",
		Short: "Print the cluster-meter version",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(deps.stdout, "cluster-meter %s\n", version)
			return err
		},
	}
}
