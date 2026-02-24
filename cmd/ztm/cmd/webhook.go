package cmd

import (
	stdcontext "context"
	"fmt"
	"time"

	"github.com/shawn/agentic-tenancy/internal/cli/api"
	"github.com/shawn/agentic-tenancy/internal/cli/output"
	"github.com/spf13/cobra"
)

func newWebhookRegisterCmd(client api.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "register <tenant-id>",
		Short: "Register Telegram webhook for a tenant",
		Long: `Register the Telegram webhook for a tenant.

This is normally done automatically when creating a tenant if ROUTER_PUBLIC_URL
is configured on the orchestrator. Use this command to manually register or
re-register a webhook.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tenantID := args[0]
			styler := output.NewStyler(noColor)
			styler.PrintInfo(fmt.Sprintf("Registering webhook for tenant '%s'...", tenantID))

			ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 30*time.Second)
			defer cancel()

			resp, err := client.RegisterWebhook(ctx, tenantID)
			if err != nil {
				styler.PrintError(fmt.Sprintf("Failed to register webhook: %v", err))
				return err
			}

			styler.PrintSuccess("Webhook registered")

			if outputFormat == "json" {
				jsonStr, err := output.FormatJSON(resp)
				if err != nil {
					return fmt.Errorf("failed to format output: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), jsonStr)
			} else {
				if resp.URL != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "\nWebhook URL: %s\n", resp.URL)
				}
				if resp.Message != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Message:     %s\n", resp.Message)
				}
			}

			return nil
		},
	}
}

func newWebhookCmd(client api.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhook",
		Short: "Manage Telegram webhooks",
		Long:  `Register Telegram webhooks for tenants.`,
	}

	cmd.AddCommand(newWebhookRegisterCmd(client))

	return cmd
}

func init() {
	rootCmd.AddCommand(newWebhookCmd(nil))
}
