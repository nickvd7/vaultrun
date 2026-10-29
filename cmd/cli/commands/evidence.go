package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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
	cmd.AddCommand(newEvidenceExportCmd(), newEvidenceVerifyCmd(), newEvidencePublicKeyCmd(), newEvidenceKeygenCmd())
	return cmd
}

func newEvidenceExportCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "export <session-id>",
		Short: "Download a session's signed evidence pack",
		Example: `  vaultrun evidence export 3f2a... -o evidence.json
  vaultrun evidence verify evidence.json --key vaultrun-evidence.pub`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := newClient().doRaw("GET", "/api/v1/sessions/"+args[0]+"/evidence")
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
	return cmd
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
			pack, err := evidence.Verify(data, pub)
			if err != nil {
				return fmt.Errorf("NOT VALID: %w", err)
			}
			findings := evidence.Check(pack)
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(pack); err != nil {
					return err
				}
			} else {
				printPackSummary(cmd.OutOrStdout(), pack, evidence.KeyID(pub), findings)
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
	return cmd
}

func printPackSummary(w io.Writer, p *evidence.Pack, keyID string, findings []evidence.Finding) {
	fmt.Fprintf(w, "VALID signature (ed25519, key %s)\n\n", keyID)
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
