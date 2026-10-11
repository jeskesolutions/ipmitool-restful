package cli

import "github.com/spf13/cobra"

func NewCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:           "ipmitool-microservice",
		Short:         "Authenticated REST API for ipmitool",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.PersistentFlags().StringVar(&configPath, "config", "", "Configuration file (default: ./config.yaml)")
	cmd.AddCommand(newServeCommand(&configPath))
	return cmd
}
