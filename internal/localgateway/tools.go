package localgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	vaultrun "github.com/nickvd7/vaultrun/sdk/go"
)

const (
	toolRunCommand  = "vaultrun_run_command"
	toolWriteFile   = "vaultrun_write_file"
	toolReadFile    = "vaultrun_read_file"
	toolListFiles   = "vaultrun_list_files"
	toolSessionInfo = "vaultrun_session_info"
)

// vaultRunToolDefs returns the fixed sandbox tool set injected into every request.
func vaultRunToolDefs() []ToolDef {
	return []ToolDef{
		{
			Type: "function",
			Function: ToolFunction{
				Name:        toolRunCommand,
				Description: "Execute a command inside the VaultRun sandbox (Docker exec; no shell). Prefer argv-style args over shell strings.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"command":{"type":"string","description":"Executable to run (e.g. python)"},
						"args":{"type":"array","items":{"type":"string"},"description":"Arguments (no shell expansion)"},
						"timeout_seconds":{"type":"integer","description":"Optional timeout in seconds"}
					},
					"required":["command"],
					"additionalProperties":false
				}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        toolWriteFile,
				Description: "Write a UTF-8 text file into the sandbox workspace.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"path":{"type":"string","description":"Relative workspace path"},
						"content":{"type":"string","description":"File contents"}
					},
					"required":["path","content"],
					"additionalProperties":false
				}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        toolReadFile,
				Description: "Read a text file from the sandbox workspace.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"path":{"type":"string","description":"Relative workspace path"}
					},
					"required":["path"],
					"additionalProperties":false
				}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        toolListFiles,
				Description: "List files currently tracked in the sandbox workspace.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{},
					"additionalProperties":false
				}`),
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        toolSessionInfo,
				Description: "Return the current VaultRun session id and status.",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{},
					"additionalProperties":false
				}`),
			},
		},
	}
}

func isVaultRunTool(name string) bool {
	switch name {
	case toolRunCommand, toolWriteFile, toolReadFile, toolListFiles, toolSessionInfo:
		return true
	default:
		return false
	}
}

// mergeTools injects VaultRun tools, replacing any client-supplied tools with
// the same name so callers cannot shadow sandbox tools with lookalikes.
func mergeTools(clientTools []ToolDef) []ToolDef {
	ours := vaultRunToolDefs()
	oursNames := make(map[string]bool, len(ours))
	for _, t := range ours {
		oursNames[t.Function.Name] = true
	}
	out := make([]ToolDef, 0, len(ours)+len(clientTools))
	out = append(out, ours...)
	for _, t := range clientTools {
		if t.Type != "" && t.Type != "function" {
			continue
		}
		name := t.Function.Name
		if name == "" || oursNames[name] || !isSafeClientToolName(name) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func isSafeClientToolName(name string) bool {
	if len(name) > 64 || len(name) == 0 {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	// Never allow client tools that look like VaultRun / privileged names.
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "vaultrun_") || strings.HasPrefix(lower, "mcp_") {
		return false
	}
	return true
}

type runCommandArgs struct {
	Command        string   `json:"command"`
	Args           []string `json:"args"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type readFileArgs struct {
	Path string `json:"path"`
}

func (g *Gateway) executeTool(ctx context.Context, sessionID, name, argumentsJSON string) (string, error) {
	if !isVaultRunTool(name) {
		return "", fmt.Errorf("unknown or disallowed tool %q", name)
	}
	if len(argumentsJSON) > int(g.cfg.MaxBodyBytes) {
		return "", fmt.Errorf("tool arguments too large")
	}
	if strings.ContainsRune(argumentsJSON, 0) {
		return "", fmt.Errorf("tool arguments contain null byte")
	}

	switch name {
	case toolRunCommand:
		var args runCommandArgs
		if err := strictJSON([]byte(argumentsJSON), &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		if err := validateCommand(args.Command, args.Args); err != nil {
			return "", err
		}
		timeout := args.TimeoutSeconds
		if timeout <= 0 {
			timeout = g.cfg.RunTimeoutSeconds
		}
		if timeout > g.cfg.MaxRunTimeoutSeconds {
			timeout = g.cfg.MaxRunTimeoutSeconds
		}
		run, err := g.vr.Run(ctx, sessionID, vaultrun.RunOptions{
			Command:        args.Command,
			Args:           args.Args,
			TimeoutSeconds: timeout,
		})
		if err != nil {
			return "", err
		}
		return formatRunResult(run, g.cfg.MaxToolResultBytes), nil

	case toolWriteFile:
		var args writeFileArgs
		if err := strictJSON([]byte(argumentsJSON), &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		p, err := sanitizeWorkspacePath(args.Path)
		if err != nil {
			return "", err
		}
		if len(args.Content) > g.cfg.MaxFileBytes {
			return "", fmt.Errorf("file content exceeds limit of %d bytes", g.cfg.MaxFileBytes)
		}
		if !utf8Safe(args.Content) {
			return "", fmt.Errorf("file content must be valid UTF-8")
		}
		f, err := g.vr.UploadFile(ctx, sessionID, p, bytes.NewReader([]byte(args.Content)))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`{"ok":true,"path":%q,"size_bytes":%d}`, f.Path, f.SizeBytes), nil

	case toolReadFile:
		var args readFileArgs
		if err := strictJSON([]byte(argumentsJSON), &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		p, err := sanitizeWorkspacePath(args.Path)
		if err != nil {
			return "", err
		}
		rc, err := g.vr.DownloadFile(ctx, sessionID, p)
		if err != nil {
			return "", err
		}
		defer rc.Close()
		raw, err := io.ReadAll(io.LimitReader(rc, int64(g.cfg.MaxFileBytes)+1))
		if err != nil {
			return "", err
		}
		if len(raw) > g.cfg.MaxFileBytes {
			return "", fmt.Errorf("file exceeds read limit of %d bytes", g.cfg.MaxFileBytes)
		}
		if !utf8Safe(string(raw)) {
			return "", fmt.Errorf("file is not valid UTF-8")
		}
		return string(raw), nil

	case toolListFiles:
		files, err := g.vr.ListFiles(ctx, sessionID)
		if err != nil {
			return "", err
		}
		type entry struct {
			Path      string `json:"path"`
			SizeBytes int64  `json:"size_bytes"`
		}
		out := make([]entry, 0, len(files))
		for _, f := range files {
			out = append(out, entry{Path: f.Path, SizeBytes: f.SizeBytes})
		}
		b, _ := json.Marshal(out)
		return string(b), nil

	case toolSessionInfo:
		sess, err := g.vr.GetSession(ctx, sessionID)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{
			"id":              sess.ID,
			"status":          sess.Status,
			"image":           sess.Image,
			"network_enabled": sess.NetworkEnabled,
		})
		return string(b), nil
	}
	return "", fmt.Errorf("unhandled tool %q", name)
}

func validateCommand(command string, args []string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("command is required")
	}
	if strings.ContainsRune(command, 0) {
		return fmt.Errorf("command contains null byte")
	}
	// Absolute paths are OK (e.g. /usr/bin/python); reject traversal segments.
	if strings.Contains(command, "..") {
		return fmt.Errorf("command must not contain '..'")
	}
	if len(command) > 256 {
		return fmt.Errorf("command too long")
	}
	if len(args) > 64 {
		return fmt.Errorf("too many args (max 64)")
	}
	for i, a := range args {
		if strings.ContainsRune(a, 0) {
			return fmt.Errorf("args[%d] contains null byte", i)
		}
		if len(a) > 4096 {
			return fmt.Errorf("args[%d] too long", i)
		}
	}
	return nil
}

func formatRunResult(run *vaultrun.Run, maxBytes int) string {
	stdout, stderr := "", ""
	if run.Stdout != nil {
		stdout = *run.Stdout
	}
	if run.Stderr != nil {
		stderr = *run.Stderr
	}
	stdout, stdoutTrunc := truncateBytes(stdout, maxBytes)
	stderr, stderrTrunc := truncateBytes(stderr, maxBytes/2)
	exit := -1
	if run.ExitCode != nil {
		exit = *run.ExitCode
	}
	payload := map[string]any{
		"run_id":          run.ID,
		"status":          run.Status,
		"exit_code":       exit,
		"stdout":          stdout,
		"stderr":          stderr,
		"stdout_truncated": stdoutTrunc || run.OutputTruncated,
		"stderr_truncated": stderrTrunc,
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

func truncateBytes(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	return s[:max] + "…", true
}

func utf8Safe(s string) bool {
	return strings.ToValidUTF8(s, "") == s && !strings.ContainsRune(s, 0)
}

// strictJSON rejects unknown fields to reduce confuse/smuggle surface.
func strictJSON(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	// Ensure no trailing junk.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing data in JSON")
	}
	return nil
}
