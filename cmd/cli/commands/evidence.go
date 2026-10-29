package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nickvd7/vaultrun/internal/evidence"
)

func newEvidenceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "evidence",
		Short: "Export and verify signed session evidence packs",
		Long: `An evidence pack is a signed record of everything a session did: its
sandbox configuration, every command with its output, the workspace files and
the audit entries. It is signed with Ed25519, so anyone with the public key can
check it offline, without access to the VaultRun server.`,
	}
	cmd.AddCommand(newEvidenceExportCmd(), newEvidenceVerifyCmd(), newEvidenceTimestampCmd(), newEvidencePublicKeyCmd(), newEvidenceKeygenCmd())
	return cmd
}

func newEvidenceExportCmd() *cobra.Command {
	var (
		out, dir     string
		all          bool
		since, until string
	)
	cmd := &cobra.Command{
		Use:   "export [session-id]",
		Short: "Download signed evidence packs",
		Long: `Download one session's evidence pack, or with --all every session's pack
(optionally created in [--since, --until)) into a directory, for example to
build a compliance report from.`,
		Example: `  vaultrun evidence export 3f2a... -o evidence.json
  vaultrun evidence export --all --since 2026-10-01 --until 2026-11-01 -d packs/
  vaultrun evidence verify evidence.json --key vaultrun-evidence.pub`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			if all {
				if len(args) > 0 {
					return errors.New("give a session id or --all, not both")
				}
				return exportAll(cmd, c, dir, since, until)
			}
			if len(args) != 1 {
				return errors.New("give a session id, or --all")
			}
			body, err := c.doRaw("GET", "/api/v1/sessions/"+args[0]+"/evidence")
			if err != nil {
				return err
			}
			if out == "" {
				out = "vaultrun-evidence-" + args[0] + ".json"
			}
			if err := os.WriteFile(out, body, 0o600); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s (%d bytes)\n", out, len(body))
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "file to write (default vaultrun-evidence-<session-id>.json)")
	cmd.Flags().BoolVar(&all, "all", false, "export every session the key can see")
	cmd.Flags().StringVarP(&dir, "dir", "d", "evidence", "directory for --all (one <session-id>.json per session)")
	cmd.Flags().StringVar(&since, "since", "", "with --all: sessions created on or after this date (YYYY-MM-DD, UTC)")
	cmd.Flags().StringVar(&until, "until", "", "with --all: sessions created before this date (YYYY-MM-DD, UTC)")
	return cmd
}

// exportAll writes the evidence pack of every listed session created in
// [since, until) to dir. A failing session does not stop the others; the
// command fails at the end if any did.
func exportAll(cmd *cobra.Command, c *apiClient, dir, since, until string) error {
	from, err := parseDay(since)
	if err != nil {
		return fmt.Errorf("--since: %w", err)
	}
	to, err := parseDay(until)
	if err != nil {
		return fmt.Errorf("--until: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	const limit = 200
	var ids []string
	for page := 1; ; page++ {
		var resp struct {
			Sessions []struct {
				ID        string    `json:"id"`
				CreatedAt time.Time `json:"created_at"`
			} `json:"sessions"`
			Pagination struct {
				Total int `json:"total"`
			} `json:"pagination"`
		}
		if err := c.get(fmt.Sprintf("/api/v1/sessions?page=%d&limit=%d", page, limit), &resp); err != nil {
			return err
		}
		for _, s := range resp.Sessions {
			if (!from.IsZero() && s.CreatedAt.Before(from)) || (!to.IsZero() && !s.CreatedAt.Before(to)) {
				continue
			}
			ids = append(ids, s.ID)
		}
		if len(resp.Sessions) < limit || page*limit >= resp.Pagination.Total {
			break
		}
	}
	failed := 0
	for _, id := range ids {
		body, err := c.doRaw("GET", "/api/v1/sessions/"+id+"/evidence")
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, id+".json"), body, 0o600)
		}
		if err != nil {
			failed++
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", id, err)
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Exported %d of %d sessions to %s\n", len(ids)-failed, len(ids), dir)
	if failed > 0 {
		return fmt.Errorf("%d sessions could not be exported", failed)
	}
	return nil
}

func parseDay(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse("2006-01-02", s)
}

func newEvidencePublicKeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "public-key",
		Short: "Print the server's evidence verification key (PEM)",
		Long: `Print the public key evidence packs are signed with. Give it to whoever
verifies packs; it is not secret. Save it, e.g.:

  vaultrun evidence public-key > vaultrun-evidence.pub`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var resp struct {
				KeyID string `json:"key_id"`
				PEM   string `json:"public_key_pem"`
			}
			if err := newClient().get("/api/v1/evidence/public-key", &resp); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "key id %s\n", resp.KeyID)
			fmt.Fprint(cmd.OutOrStdout(), resp.PEM)
			return nil
		},
	}
}

func newEvidenceKeygenCmd() *cobra.Command {
	var pubOut string
	cmd := &cobra.Command{
		Use:   "keygen",
		Short: "Generate an evidence signing key (offline)",
		Long: `Generate an Ed25519 key pair. The private key is printed as the
EVIDENCE_SIGNING_KEY value for the API's .env; the public key is written to a
PEM file for verifiers. ` + "`make up`" + ` generates a key when none is set, so this is
for installations that manage the key themselves.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			seed, pub, err := evidence.GenerateKey()
			if err != nil {
				return err
			}
			if err := os.WriteFile(pubOut, pub, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "EVIDENCE_SIGNING_KEY=%s\n", seed)
			fmt.Fprintf(cmd.ErrOrStderr(), "Public key written to %s. Keep EVIDENCE_SIGNING_KEY secret.\n", pubOut)
			return nil
		},
	}
	cmd.Flags().StringVar(&pubOut, "public-key-out", "vaultrun-evidence.pub", "where to write the public key")
	return cmd
}

func newEvidenceVerifyCmd() *cobra.Command {
	var (
		keyFile string
		tsaCA   string
		asJSON  bool
		strict  bool
	)
	cmd := &cobra.Command{
		Use:   "verify <pack.json>",
		Short: "Verify an evidence pack offline",
		Long: `Check an evidence pack's signature with the public key, then report
what it contains and anything it cannot vouch for (audit entries the server
could not verify at export, limits reached). Needs no server and no API key.

Exit status: 0 when the signature is valid, 1 when it is not or the file is not
a pack, and also 1 for warnings when --strict is set.`,
		Example: `  vaultrun evidence verify evidence.json --key vaultrun-evidence.pub
  vaultrun evidence verify evidence.json --key vaultrun-evidence.pub --json | jq .runs`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if keyFile == "" {
				return errors.New("--key is required: the public key file (vaultrun evidence public-key)")
			}
			keyPEM, err := os.ReadFile(keyFile)
			if err != nil {
				return err
			}
			pub, err := evidence.ParsePublicKeyPEM(keyPEM)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			env, err := evidence.Open(data)
			if err != nil {
				return err
			}
			pack, err := env.Verify(pub)
			if err != nil {
				return fmt.Errorf("NOT VALID: %w", err)
			}
			findings := evidence.Check(pack)
			stamp, stampFindings, err := checkPackTimestamp(env, tsaCA)
			if err != nil {
				return fmt.Errorf("NOT VALID: %w", err)
			}
			findings = append(findings, stampFindings...)
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(pack); err != nil {
					return err
				}
			} else {
				printPackSummary(cmd.OutOrStdout(), pack, evidence.KeyID(pub), stamp, findings)
			}
			if strict {
				for _, f := range findings {
					if f.Severity == "warning" {
						return errors.New("pack has warnings (--strict)")
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&keyFile, "key", "", "public key PEM file")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the verified pack as JSON instead of a summary")
	cmd.Flags().BoolVar(&strict, "strict", false, "fail when the pack has warnings")
	cmd.Flags().StringVar(&tsaCA, "tsa-ca", "", "PEM roots the pack's timestamp authority must chain to (default: system roots)")
	return cmd
}

func printPackSummary(w io.Writer, p *evidence.Pack, keyID string, stamp *evidence.TimestampInfo, findings []evidence.Finding) {
	fmt.Fprintf(w, "VALID signature (ed25519, key %s)\n", keyID)
	if stamp != nil {
		fmt.Fprintf(w, "Timestamp %s by %s (RFC 3161, serial %s)\n", stamp.Time.Format("2006-01-02 15:04:05 MST"), stamp.TSA, stamp.Serial)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Pack      %s, exported %s by %s (%s %s)\n",
		p.ID, p.CreatedAt.Format("2006-01-02 15:04:05 MST"), p.ExportedBy, p.Issuer.Product, p.Issuer.Version)

	s := p.Session
	name := ""
	if s.Name != "" {
		name = fmt.Sprintf(" %q", s.Name)
	}
	runtime := s.Runtime
	if runtime == "" {
		runtime = "not recorded"
	}
	fmt.Fprintf(w, "Session   %s%s, %s\n", s.ID, name, s.Status)
	fmt.Fprintf(w, "          image %s, runtime %s, %.2g CPU, %d MB\n", s.Image, runtime, s.CPULimit, s.MemoryLimitMB)
	switch {
	case !s.NetworkEnabled:
		fmt.Fprintln(w, "          network off")
	case len(s.AllowedHosts) > 0:
		fmt.Fprintf(w, "          network limited to %s\n", strings.Join(s.AllowedHosts, ", "))
	default:
		fmt.Fprintln(w, "          network unrestricted")
	}
	created := s.CreatedBy
	if s.CreatedByName != "" && s.CreatedByName != s.CreatedBy {
		created = fmt.Sprintf("%s (%s)", s.CreatedByName, s.CreatedBy)
	}
	fmt.Fprintf(w, "          created %s by %s\n", s.CreatedAt.Format("2006-01-02 15:04:05 MST"), created)

	ok, failed := 0, 0
	for _, r := range p.Runs {
		if r.ExitCode != nil && *r.ExitCode == 0 {
			ok++
		} else {
			failed++
		}
	}
	fmt.Fprintf(w, "Runs      %d (%d exit 0, %d other)\n", len(p.Runs), ok, failed)
	for _, r := range p.Runs {
		exit := "-"
		if r.ExitCode != nil {
			exit = fmt.Sprint(*r.ExitCode)
		}
		fmt.Fprintf(w, "          %s  exit %-3s %s\n", r.CreatedAt.Format("2006-01-02 15:04:05"), exit,
			strings.TrimSpace(r.Command+" "+strings.Join(r.Args, " ")))
	}
	fmt.Fprintf(w, "Files     %d\n", len(p.Files))

	verified := 0
	for _, e := range p.Audit.Entries {
		if e.SigStatus == "valid" {
			verified++
		}
	}
	fmt.Fprintf(w, "Audit     %d entries (scope %s), %d verified by the server at export\n",
		len(p.Audit.Entries), p.Audit.Scope, verified)

	var warnings, notes []string
	for _, f := range findings {
		if f.Severity == "warning" {
			warnings = append(warnings, f.Message)
		} else {
			notes = append(notes, f.Message)
		}
	}
	if len(warnings) > 0 {
		fmt.Fprintf(w, "\nWarnings\n")
		for _, m := range warnings {
			fmt.Fprintf(w, "  - %s\n", m)
		}
	}
	if len(notes) > 0 {
		fmt.Fprintf(w, "\nNotes\n")
		for _, m := range notes {
			fmt.Fprintf(w, "  - %s\n", m)
		}
	}
}

// checkPackTimestamp verifies the pack's RFC 3161 timestamp, if any. A token
// that does not match the pack is an error; one from a TSA that does not chain
// to the roots is a warning, and so is having none.
func checkPackTimestamp(env *evidence.Envelope, caFile string) (*evidence.TimestampInfo, []evidence.Finding, error) {
	if len(env.Timestamp) == 0 {
		return nil, []evidence.Finding{{Severity: "info",
			Message: "no external timestamp: the pack's time is what the signing server says (see vaultrun evidence timestamp)"}}, nil
	}
	roots, err := evidence.LoadCertPool(caFile)
	if err != nil {
		return nil, nil, err
	}
	info, err := evidence.VerifyTimestamp(env, roots)
	switch {
	case err == nil:
		return info, nil, nil
	case errors.Is(err, evidence.ErrTimestampUntrusted):
		return nil, []evidence.Finding{{Severity: "warning", Message: fmt.Sprintf(
			"timestamp of %s is intact but its authority is not trusted here (%v); pass its root with --tsa-ca",
			info.Time.Format("2006-01-02 15:04:05 MST"), err)}}, nil
	default:
		return nil, nil, err
	}
}

func newEvidenceTimestampCmd() *cobra.Command {
	var (
		tsaURL string
		tsaCA  string
		out    string
	)
	cmd := &cobra.Command{
		Use:   "timestamp <pack.json>",
		Short: "Add an RFC 3161 timestamp to an evidence pack",
		Long: `Have a Time Stamping Authority (TSA) timestamp the pack's signature, proving
the pack existed no later than the TSA's time. Use it for packs from servers
that cannot reach a TSA themselves (with EVIDENCE_TSA_URL set, the server
stamps every pack). The pack's own signature is not changed.`,
		Example: `  vaultrun evidence timestamp evidence.json --tsa http://timestamp.digicert.com`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tsaURL == "" {
				return errors.New("--tsa is required: the URL of an RFC 3161 time stamping authority")
			}
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			env, err := evidence.Open(data)
			if err != nil {
				return err
			}
			if len(env.Timestamp) > 0 {
				return errors.New("pack already has a timestamp")
			}
			if err := evidence.RequestTimestamp(cmd.Context(), http.DefaultClient, tsaURL, env); err != nil {
				return err
			}
			roots, err := evidence.LoadCertPool(tsaCA)
			if err != nil {
				return err
			}
			info, err := evidence.VerifyTimestamp(env, roots)
			if err != nil {
				return err
			}
			b, err := json.Marshal(env)
			if err != nil {
				return err
			}
			if out == "" {
				out = args[0]
			}
			if err := os.WriteFile(out, b, 0o600); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Timestamped %s at %s by %s\n", out, info.Time.Format(time.RFC3339), info.TSA)
			return nil
		},
	}
	cmd.Flags().StringVar(&tsaURL, "tsa", "", "RFC 3161 time stamping authority URL")
	cmd.Flags().StringVar(&tsaCA, "tsa-ca", "", "PEM roots the authority must chain to (default: system roots)")
	cmd.Flags().StringVarP(&out, "output", "o", "", "file to write (default: overwrite the input)")
	return cmd
}
