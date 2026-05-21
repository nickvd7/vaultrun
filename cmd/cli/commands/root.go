package commands

import (
	"os"

	"github.com/spf13/cobra"
)

var (
	apiURL string
	apiKey string
)

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "vaultrun",
		Short: "VaultRun CLI — manage sandboxed AI agent sessions",
		Long: `VaultRun is a self-hosted secure runtime for AI agents.
Use this CLI to create sessions, upload files, execute commands, and inspect results.`,
	}

	root.PersistentFlags().StringVar(&apiURL, "api-url", envOrDefault("VAULTRUN_API_URL", "http://localhost:8080"), "API base URL")
	root.PersistentFlags().StringVar(&apiKey, "api-key", envOrDefault("VAULTRUN_API_KEY", ""), "API key")

	root.AddCommand(
		newSessionCmd(),
		newFileCmd(),
		newRunCmd(),
		newRunAsyncCmd(),
		newStreamCmd(),
		newLogsCmd(),
		newUpCmd(),
		newKeyCmd(),
	)

	return root
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
