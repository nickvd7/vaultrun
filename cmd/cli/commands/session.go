package commands

import (
	"encoding/json"
	"fmt"
	"os"
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
		sessionDeleteCmd(),
		sessionGetCmd(),
	)

	return cmd
}

func sessionCreateCmd() *cobra.Command {
	var (
		name      string
		image     string
		network   bool
		cpu       float64
		memMB     int
		timeoutSec int
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

func sessionDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <session-id>",
		Short: "Delete a session and its container",
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

func prettyJSON(v interface{}) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
