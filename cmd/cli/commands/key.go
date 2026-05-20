package commands

import (
	"fmt"
	"os"
	"text/tabwriter"

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
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new API key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var result map[string]interface{}
			if err := newClient().post("/api/v1/keys", map[string]string{"name": args[0]}, &result); err != nil {
				return err
			}
			fmt.Printf("Key created. Save the value below — it will not be shown again.\n\n")
			fmt.Printf("  Name:   %v\n", result["name"])
			fmt.Printf("  Prefix: %v\n", result["prefix"])
			fmt.Printf("  Key:    %v\n\n", result["key"])
			return nil
		},
	}
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
			fmt.Fprintln(w, "ID\tNAME\tPREFIX\tACTIVE\tCREATED\tLAST USED")
			for _, k := range result.APIKeys {
				lastUsed := "never"
				if v := k["last_used_at"]; v != nil {
					lastUsed = fmt.Sprintf("%v", v)
				}
				fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\t%v\n",
					k["id"], k["name"], k["prefix"], k["active"], k["created_at"], lastUsed)
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
