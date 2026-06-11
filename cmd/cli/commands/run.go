package commands

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

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

func newStreamCmd() *cobra.Command {
	var (
		timeout int
		env     []string
		workDir string
	)

	cmd := &cobra.Command{
		Use:   "stream <session-id> -- <command> [args...]",
		Short: "Execute a command with live output (SSE streaming)",
		Args:  cobra.MinimumNArgs(2),
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

			b, _ := json.Marshal(body)
			client := newClient()
			req, err := http.NewRequest("POST",
				client.baseURL+"/api/v1/sessions/"+sessionID+"/run/stream",
				bytes.NewReader(b))
			if err != nil {
				return err
			}
			req.Header.Set("X-API-Key", client.key)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "text/event-stream")

			resp, err := client.http.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 400 {
				body2, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("stream error (%d): %s", resp.StatusCode, string(body2))
			}

			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				line := scanner.Text()
				if len(line) < 6 || line[:6] != "data: " {
					continue
				}
				var ev struct {
					Type     string `json:"type"`
					Data     string `json:"data"`
					Status   string `json:"status"`
					ExitCode *int   `json:"exit_code"`
					Error    string `json:"error"`
				}
				if err := json.Unmarshal([]byte(line[6:]), &ev); err != nil {
					continue
				}
				switch ev.Type {
				case "stdout":
					fmt.Fprint(os.Stdout, ev.Data)
				case "stderr":
					fmt.Fprint(os.Stderr, ev.Data)
				case "done":
					if ev.Error != "" {
						return fmt.Errorf("run failed: %s", ev.Error)
					}
					if ev.ExitCode != nil && *ev.ExitCode != 0 {
						os.Exit(*ev.ExitCode)
					}
					return nil
				}
			}
			return scanner.Err()
		},
	}

	cmd.Flags().IntVar(&timeout, "timeout", 30, "Execution timeout in seconds")
	cmd.Flags().StringArrayVar(&env, "env", nil, "Environment variables (KEY=VALUE)")
	cmd.Flags().StringVar(&workDir, "workdir", "", "Working directory inside container")
	return cmd
}

func newRunAsyncCmd() *cobra.Command {
	var (
		timeout     int
		env         []string
		workDir     string
		callbackURL string
		poll        bool
	)

	cmd := &cobra.Command{
		Use:   "run-async <session-id> -- <command> [args...]",
		Short: "Submit a command for non-blocking (async) execution",
		Long: `Submit a command for execution without waiting for it to finish.
Returns immediately with the run_id. Use 'vaultrun logs <run-id>' to check output.
Optionally poll until completion with --poll, or receive a webhook via --callback.

Examples:
  vaultrun run-async abc123 -- python train.py
  vaultrun run-async abc123 --callback https://my.app/webhook -- python train.py
  vaultrun run-async abc123 --poll -- python train.py`,
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
			if callbackURL != "" {
				body["callback_url"] = callbackURL
			}

			var result map[string]interface{}
			if err := newClient().post("/api/v1/sessions/"+sessionID+"/run/async", body, &result); err != nil {
				return err
			}

			prettyJSON(result)

			if poll {
				runID, _ := result["run_id"].(string)
				if runID == "" {
					return fmt.Errorf("no run_id in response")
				}
				return pollRun(runID)
			}
			return nil
		},
	}

	cmd.Flags().IntVar(&timeout, "timeout", 30, "Execution timeout in seconds")
	cmd.Flags().StringArrayVar(&env, "env", nil, "Environment variables (KEY=VALUE)")
	cmd.Flags().StringVar(&workDir, "workdir", "", "Working directory inside container")
	cmd.Flags().StringVar(&callbackURL, "callback", "", "Webhook URL to POST result to when done")
	cmd.Flags().BoolVar(&poll, "poll", false, "Poll until the run completes and print output")
	return cmd
}

// pollRun polls GET /runs/:id every second until the run reaches a terminal state.
func pollRun(runID string) error {
	client := newClient()
	for {
		var result map[string]interface{}
		if err := client.get("/api/v1/runs/"+runID, &result); err != nil {
			return err
		}
		status, _ := result["status"].(string)
		switch status {
		case "completed", "failed", "timeout":
			if stdout, ok := result["stdout"].(string); ok && stdout != "" {
				fmt.Println("\n--- stdout ---")
				fmt.Print(stdout)
			}
			if stderr, ok := result["stderr"].(string); ok && stderr != "" {
				fmt.Println("\n--- stderr ---")
				fmt.Print(stderr)
			}
			fmt.Printf("\nRun %s: status=%s\n", runID, status)
			return nil
		default:
			// still pending/running — wait and retry
			time.Sleep(time.Second)
		}
	}
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
