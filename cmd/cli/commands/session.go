package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage sandbox sessions",
	}

	cmd.AddCommand(
		sessionCreateCmd(),
		sessionListCmd(),
		sessionGetCmd(),
		sessionStopCmd(),
		sessionDeleteCmd(),
		sessionLabelsCmd(),
	)

	return cmd
}

func sessionCreateCmd() *cobra.Command {
	var (
		name       string
		image      string
		network    bool
		cpu        float64
		memMB      int
		timeoutSec int
		labels     []string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new sandbox session",
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]interface{}{
				"image":           image,
				"network_enabled": network,
				"cpu_limit":       cpu,
				"memory_limit_mb": memMB,
				"timeout_seconds": timeoutSec,
			}
			if name != "" {
				body["name"] = name
			}
			if len(labels) > 0 {
				lm := map[string]string{}
				for _, l := range labels {
					if idx := strings.IndexByte(l, '='); idx > 0 {
						lm[l[:idx]] = l[idx+1:]
					}
				}
				body["labels"] = lm
			}

			var result map[string]interface{}
			if err := newClient().post("/api/v1/sessions", body, &result); err != nil {
				return err
			}

			prettyJSON(result)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Optional session name")
	cmd.Flags().StringVar(&image, "image", "python:3.12-slim", "Container image")
	cmd.Flags().BoolVar(&network, "network", false, "Enable network access")
	cmd.Flags().Float64Var(&cpu, "cpu", 1.0, "CPU limit (fractional cores)")
	cmd.Flags().IntVar(&memMB, "mem", 512, "Memory limit (MB)")
	cmd.Flags().IntVar(&timeoutSec, "timeout", 300, "Session idle timeout (seconds)")
	cmd.Flags().StringArrayVar(&labels, "label", nil, "Labels as key=value (repeatable)")

	return cmd
}

func sessionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List active sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			var result struct {
				Sessions []map[string]interface{} `json:"sessions"`
			}
			if err := newClient().get("/api/v1/sessions", &result); err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tIMAGE\tSTATUS\tCREATED")
			for _, s := range result.Sessions {
				fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\n",
					s["id"], s["name"], s["image"], s["status"], s["created_at"])
			}
			return w.Flush()
		},
	}
}

func sessionGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <session-id>",
		Short: "Get session details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var result map[string]interface{}
			if err := newClient().get("/api/v1/sessions/"+args[0], &result); err != nil {
				return err
			}
			prettyJSON(result)
			return nil
		},
	}
}

// sessionStopCmd is an alias for delete with friendlier naming.
func sessionStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <session-id>",
		Short: "Stop a session and remove its container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newClient().delete("/api/v1/sessions/" + args[0]); err != nil {
				return err
			}
			fmt.Printf("Session %s stopped\n", args[0])
			return nil
		},
	}
}

func sessionDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <session-id>",
		Short: "Delete a session and its container (alias: stop)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newClient().delete("/api/v1/sessions/" + args[0]); err != nil {
				return err
			}
			fmt.Printf("Session %s deleted\n", args[0])
			return nil
		},
	}
}

// sessionLabelsCmd manages session labels.
func sessionLabelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "labels",
		Short: "Get or set session labels",
	}
	cmd.AddCommand(sessionLabelsGetCmd(), sessionLabelsSetCmd())
	return cmd
}

func sessionLabelsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <session-id>",
		Short: "Show all labels for a session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var result map[string]interface{}
			if err := newClient().get("/api/v1/sessions/"+args[0], &result); err != nil {
				return err
			}
			labels, _ := result["labels"]
			b, _ := json.MarshalIndent(labels, "", "  ")
			fmt.Println(string(b))
			return nil
		},
	}
}

func sessionLabelsSetCmd() *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "set <session-id> [key=value...]",
		Short: "Replace session labels (key=value pairs; --clear to remove all)",
		Long: `Replace all labels on a session. Each label is specified as key=value.
Use --clear with no key=value pairs to remove all labels.

Examples:
  vaultrun session labels set abc123 env=prod team=ml
  vaultrun session labels set abc123 --clear`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			pairs := args[1:]

			labels := map[string]string{}
			if !clear {
				for _, p := range pairs {
					if idx := strings.IndexByte(p, '='); idx > 0 {
						labels[p[:idx]] = p[idx+1:]
					} else {
						return fmt.Errorf("invalid label %q: expected key=value", p)
					}
				}
			}

			var result map[string]interface{}
			if err := newClient().patch("/api/v1/sessions/"+sessionID+"/labels",
				map[string]interface{}{"labels": labels}, &result); err != nil {
				return err
			}
			prettyJSON(result)
			return nil
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "Remove all labels")
	return cmd
}

func prettyJSON(v interface{}) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
