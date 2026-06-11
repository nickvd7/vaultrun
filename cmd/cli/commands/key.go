package commands

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func newKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Manage API keys",
	}
	cmd.AddCommand(keyCreateCmd(), keyListCmd(), keyRevokeCmd())
	return cmd
}

func keyCreateCmd() *cobra.Command {
	var expires string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new API key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]interface{}{"name": args[0]}
			if expires != "" {
				t, err := parseDuration(expires)
				if err != nil {
					return fmt.Errorf("invalid --expires value %q: %w", expires, err)
				}
				body["expires_at"] = t.UTC().Format(time.RFC3339)
			}
			var result map[string]interface{}
			if err := newClient().post("/api/v1/keys", body, &result); err != nil {
				return err
			}
			fmt.Printf("Key created. Save the value below — it will not be shown again.\n\n")
			fmt.Printf("  Name:   %v\n", result["name"])
			fmt.Printf("  Prefix: %v\n", result["prefix"])
			fmt.Printf("  Key:    %v\n", result["key"])
			if exp, ok := result["expires_at"]; ok && exp != nil {
				fmt.Printf("  Expires: %v\n", exp)
			}
			fmt.Println()
			return nil
		},
	}
	cmd.Flags().StringVar(&expires, "expires", "", "expiry duration: 7d, 30d, 90d, 1y, or RFC3339 timestamp")
	return cmd
}

// parseDuration converts shorthand like "7d", "30d", "90d", "1y" into an absolute time.
// Also accepts a full RFC3339 timestamp for precise control.
func parseDuration(s string) (time.Time, error) {
	switch s {
	case "7d":
		return time.Now().Add(7 * 24 * time.Hour), nil
	case "30d":
		return time.Now().Add(30 * 24 * time.Hour), nil
	case "90d":
		return time.Now().Add(90 * 24 * time.Hour), nil
	case "1y":
		return time.Now().Add(365 * 24 * time.Hour), nil
	}
	// Try RFC3339
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("use 7d, 30d, 90d, 1y or RFC3339 timestamp")
	}
	return t, nil
}

func keyListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List API keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			var result struct {
				APIKeys []map[string]interface{} `json:"api_keys"`
			}
			if err := newClient().get("/api/v1/keys", &result); err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tPREFIX\tACTIVE\tEXPIRES\tCREATED\tLAST USED")
			for _, k := range result.APIKeys {
				lastUsed := "never"
				if v := k["last_used_at"]; v != nil {
					lastUsed = fmt.Sprintf("%v", v)
				}
				exp := "never"
				if v := k["expires_at"]; v != nil {
					exp = fmt.Sprintf("%v", v)
				}
				fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\t%v\t%v\n",
					k["id"], k["name"], k["prefix"], k["active"], exp, k["created_at"], lastUsed)
			}
			return w.Flush()
		},
	}
}

func keyRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <key-id>",
		Short: "Revoke an API key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newClient().delete("/api/v1/keys/" + args[0]); err != nil {
				return err
			}
			fmt.Printf("Key %s revoked\n", args[0])
			return nil
		},
	}
}
