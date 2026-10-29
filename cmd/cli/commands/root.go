package commands

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/nickvd7/vaultrun/internal/version"
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
		SilenceUsage: true,
		Version:      version.String(),
	}

	root.PersistentFlags().StringVar(&apiURL, "api-url", envOrDefault("VAULTRUN_API_URL", "http://localhost:8080"), "API base URL")
	apiKeyFlag := root.PersistentFlags().VarPF(newMaskedStringValue(&apiKey, envOrDefault("VAULTRUN_API_KEY", "")), "api-key", "", "API key (set via VAULTRUN_API_KEY or --api-key)")
	apiKeyFlag.DefValue = "***"

	root.AddCommand(
		newSessionCmd(),
		newFileCmd(),
		newRunCmd(),
		newRunAsyncCmd(),
		newStreamCmd(),
		newLogsCmd(),
		newUpCmd(),
		newKeyCmd(),
		newAuditCmd(),
		NewSnapshotCmd(),
		NewArtifactCmd(),
		newEvidenceCmd(),
	)

	return root
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type maskedStringValue struct {
	value  *string
	actual string
}

func newMaskedStringValue(p *string, val string) *maskedStringValue {
	*p = val
	return &maskedStringValue{value: p, actual: val}
}

func (s *maskedStringValue) Set(val string) error {
	*s.value = val
	s.actual = val
	return nil
}

func (s *maskedStringValue) Type() string {
	return "string"
}

func (s *maskedStringValue) String() string {
	if s.actual != "" {
		return "***"
	}
	return ""
}
