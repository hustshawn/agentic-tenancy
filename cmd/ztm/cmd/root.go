package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var (
	version   string
	commit    string
	buildDate string

	// Global flags
	namespace       string
	context         string
	orchestratorURL string
	routerURL       string
	outputFormat    string
	noColor         bool
)

var rootCmd = &cobra.Command{
	Use:   "ztm",
	Short: "Agentic Tenancy CLI",
	Long: `ztm is a CLI for managing multi-tenant AI agent instances.

It provides commands to create, list, update, and delete tenants,
as well as register Telegram webhooks.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&namespace, "namespace", getEnvOrDefault("ZTM_NAMESPACE", "tenants"), "Kubernetes namespace")
	rootCmd.PersistentFlags().StringVar(&context, "context", os.Getenv("ZTM_KUBE_CONTEXT"), "kubectl context")
	rootCmd.PersistentFlags().StringVar(&orchestratorURL, "orchestrator-url", os.Getenv("ZTM_ORCHESTRATOR_URL"), "Orchestrator HTTP URL (bypasses kubectl)")
	rootCmd.PersistentFlags().StringVar(&routerURL, "router-url", os.Getenv("ZTM_ROUTER_URL"), "Router HTTP URL")
	rootCmd.PersistentFlags().StringVar(&outputFormat, "output", "table", "Output format: json|table")
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "Disable colored output")
}

func Execute() error {
	return rootCmd.Execute()
}

func SetVersion(v, c, d string) {
	version = v
	commit = c
	buildDate = d
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
