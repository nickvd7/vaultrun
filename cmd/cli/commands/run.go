package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	var (
		timeout int
		env     []string
		workDir string
	)

	cmd := &cobra.Command{
		Use:   "run <session-id> -- <command> [args...]",
		Short: "Execute a command inside a session",
		Long: `Execute a command inside a sandbox session.
Use -- to separate session-id from the command and its arguments.

Examples:
  vaultrun run abc123 -- python script.py
  vaultrun run abc123 -- ls -la /workspace`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			command := args[1]
			cmdArgs := args[2:]

			envMap := map[string]string{}
			for _, e := range env {
				for i, c := range e {
					if c == '=' {
						envMap[e[:i]] = e[i+1:]
						break
					}
				}
			}

			body := map[string]interface{}{
				"command":         command,
				"args":            cmdArgs,
				"timeout_seconds": timeout,
				"env":             envMap,
			}
			if workDir != "" {
				body["working_dir"] = workDir
			}

			var result map[string]interface{}
			if err := newClient().post("/api/v1/sessions/"+sessionID+"/run", body, &result); err != nil {
				return err
			}

			prettyJSON(result)

			// Also print stdout/stderr directly for convenience
			if stdout, ok := result["stdout"].(string); ok && stdout != "" {
				fmt.Println("\n--- stdout ---")
				fmt.Print(stdout)
			}
			if stderr, ok := result["stderr"].(string); ok && stderr != "" {
				fmt.Println("\n--- stderr ---")
				fmt.Print(stderr)
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&timeout, "timeout", 30, "Execution timeout in seconds")
	cmd.Flags().StringArrayVar(&env, "env", nil, "Environment variables (KEY=VALUE)")
	cmd.Flags().StringVar(&workDir, "workdir", "", "Working directory inside container")

	return cmd
}

func newLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs <run-id>",
		Short: "Get logs for a run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var result map[string]interface{}
			if err := newClient().get("/api/v1/runs/"+args[0], &result); err != nil {
				return err
			}

			if stdout, ok := result["stdout"].(string); ok {
				fmt.Print(stdout)
			}
			if stderr, ok := result["stderr"].(string); ok && stderr != "" {
				fmt.Print(stderr)
			}
			return nil
		},
	}
}

func newUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Check API connectivity",
		RunE: func(cmd *cobra.Command, args []string) error {
			var result map[string]interface{}
			if err := newClient().get("/health", &result); err != nil {
				return fmt.Errorf("API not reachable at %s: %w", apiURL, err)
			}
			prettyJSON(result)
			return nil
		},
	}
}
