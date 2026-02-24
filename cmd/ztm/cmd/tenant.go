package cmd

import (
	"github.com/shawn/agentic-tenancy/internal/cli/api"
	"github.com/spf13/cobra"
)

func newTenantCmd(client api.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tenant",
		Short: "Manage tenants",
		Long:  `Create, list, get, update, and delete tenants.`,
	}

	// Add subcommands
	cmd.AddCommand(newTenantCreateCmd(client))
	cmd.AddCommand(newTenantListCmd(client))
	cmd.AddCommand(newTenantGetCmd(client))
	cmd.AddCommand(newTenantUpdateCmd(client))
	cmd.AddCommand(newTenantDeleteCmd(client))

	return cmd
}

func init() {
	// Client will be initialized in root.go based on flags
	rootCmd.AddCommand(newTenantCmd(nil))
}
