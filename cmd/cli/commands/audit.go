package commands

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newAuditCmd() *cobra.Command {
	var (
		sessionID string
		limit     int
		offset    int
	)

	cmd := &cobra.Command{
		Use:   "audit",
		Short: "List audit log entries",
		Long: `Display audit trail entries for the current actor's sessions.

Entries are ordered newest-first. Use --session to narrow results to a specific
session. Operators using the master API key see all actors' logs.

Examples:
  vaultrun audit
  vaultrun audit --session abc123
  vaultrun audit --limit 50 --offset 50`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/api/v1/audit"
			sep := "?"
			if sessionID != "" {
				path += sep + "session_id=" + sessionID
				sep = "&"
			}
			if limit > 0 {
				path += fmt.Sprintf("%slimit=%d", sep, limit)
				sep = "&"
			}
			if offset > 0 {
				path += fmt.Sprintf("%soffset=%d", sep, offset)
			}

			var resp struct {
				AuditLogs []map[string]interface{} `json:"audit_logs"`
				Pagination map[string]interface{}  `json:"pagination"`
			}
			if err := newClient().get(path, &resp); err != nil {
				return err
			}

			if len(resp.AuditLogs) == 0 {
				fmt.Println("No audit log entries found.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "TIMESTAMP\tACTOR\tACTION\tSESSION\tRUN")
			for _, entry := range resp.AuditLogs {
				ts := strVal(entry, "timestamp")
				actor := strVal(entry, "actor")
				action := strVal(entry, "action")
				session := strVal(entry, "session_id")
				run := strVal(entry, "run_id")
				if session == "" {
					session = "-"
				}
				if run == "" {
					run = "-"
				}
				// Shorten UUIDs for readability
				if len(session) > 8 {
					session = session[:8] + "…"
				}
				if len(run) > 8 {
					run = run[:8] + "…"
				}
				// Trim timestamp to date + time (drop sub-second precision + zone)
				if len(ts) > 19 {
					ts = ts[:19]
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", ts, actor, action, session, run)
			}
			w.Flush()

			if pg := resp.Pagination; pg != nil {
				total, _ := pg["total"].(float64)
				fmt.Printf("\n(%d/%d entries shown)\n", len(resp.AuditLogs), int(total))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&sessionID, "session", "", "Filter by session ID")
	cmd.Flags().IntVar(&limit, "limit", 25, "Number of entries to return")
	cmd.Flags().IntVar(&offset, "offset", 0, "Offset for pagination")

	return cmd
}

// strVal safely returns a string value from a loosely-typed JSON map.
func strVal(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
