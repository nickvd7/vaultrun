// MCP tool definitions and dispatch.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// toolDefinitions returns the full list of MCP tools this server exposes.
func toolDefinitions() []mcpTool {
	return []mcpTool{
		{
			Name: "create_session",
			Description: "Create a new isolated sandbox session with a Docker container. " +
				"Returns the session ID needed for subsequent operations. " +
				"Always delete the session when done to free resources.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"image": {
						Type:        "string",
						Description: "Docker image to use (e.g. 'python:3.12-slim', 'node:20-slim'). Defaults to python:3.12-slim.",
					},
					"name": {
						Type:        "string",
						Description: "Optional human-readable name for the session.",
					},
					"network_enabled": {
						Type:        "string",
						Enum:        []string{"true", "false"},
						Description: "Whether to enable network access in the container. Default false.",
					},
					"cpu_limit": {
						Type:        "string",
						Description: "CPU limit in cores (e.g. '0.5', '2.0'). Default 1.0.",
					},
					"memory_limit_mb": {
						Type:        "string",
						Description: "Memory limit in megabytes (e.g. '512', '1024'). Default 512.",
					},
					"timeout_seconds": {
						Type:        "string",
						Description: "Session timeout in seconds. Default 300.",
					},
				},
			},
		},
		{
			Name:        "list_sessions",
			Description: "List all active sandbox sessions. Returns session IDs, images, status, and creation times.",
			InputSchema: inputSchema{Type: "object", Properties: map[string]schemaProp{}},
		},
		{
			Name:        "get_session",
			Description: "Get details about a specific session including its status.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "The session ID to look up."},
				},
				Required: []string{"session_id"},
			},
		},
		{
			Name:        "delete_session",
			Description: "Stop and delete a sandbox session, freeing all resources. Call this when you are done with a session.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "The session ID to delete."},
				},
				Required: []string{"session_id"},
			},
		},
		{
			Name: "run_command",
			Description: "Execute a command inside a sandbox session. Returns stdout, stderr, exit code, and duration. " +
				"The command runs as an unprivileged user inside an isolated container. " +
				"For multi-step workflows, prefer uploading a script file and running it rather than chaining shell commands.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "The session ID to run the command in."},
					"command": {
						Type:        "string",
						Description: "The executable to run (e.g. 'python', 'node', 'bash', 'pip'). No shell expansion.",
					},
					"args": {
						Type:        "string",
						Description: "JSON array of arguments (e.g. '[\"script.py\", \"--verbose\"]'). Optional.",
					},
					"working_dir": {
						Type:        "string",
						Description: "Working directory inside the container. Default: /workspace.",
					},
					"timeout_seconds": {
						Type:        "string",
						Description: "Execution timeout in seconds. Default: 30.",
					},
					"env": {
						Type:        "string",
						Description: "JSON object of environment variables (e.g. '{\"FOO\":\"bar\"}').",
					},
				},
				Required: []string{"session_id", "command"},
			},
		},
		{
			Name: "upload_file",
			Description: "Upload a file to the session workspace. " +
				"The file is immediately available at the given path inside the container. " +
				"Use this to upload scripts, data files, or configuration before running commands.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "The session ID."},
					"path": {
						Type:        "string",
						Description: "Destination path inside the workspace (e.g. '/script.py', '/data/input.csv').",
					},
					"content": {Type: "string", Description: "File content as a UTF-8 string."},
				},
				Required: []string{"session_id", "path", "content"},
			},
		},
		{
			Name: "read_file",
			Description: "Read the content of a file from the session workspace. " +
				"Use this to retrieve output files created by commands.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "The session ID."},
					"path": {
						Type:        "string",
						Description: "Path of the file to read (e.g. '/output.txt').",
					},
				},
				Required: []string{"session_id", "path"},
			},
		},
		{
			Name:        "list_files",
			Description: "List all files in the session workspace, including files created by running commands.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "The session ID."},
				},
				Required: []string{"session_id"},
			},
		},
		{
			Name:        "delete_file",
			Description: "Delete a file from the session workspace.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "The session ID."},
					"path":       {Type: "string", Description: "Path of the file to delete (e.g. '/output.txt')."},
				},
				Required: []string{"session_id", "path"},
			},
		},
		{
			Name:        "get_run",
			Description: "Get details of a specific command run including its stdout, stderr, exit code, and status.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"run_id": {Type: "string", Description: "The run ID to look up."},
				},
				Required: []string{"run_id"},
			},
		},
		{
			Name:        "list_runs",
			Description: "List all command runs for a session, showing their status, exit codes, and durations.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "The session ID."},
				},
				Required: []string{"session_id"},
			},
		},
		{
			Name: "create_snapshot",
			Description: "Save the current workspace state as a named snapshot archive. " +
				"Snapshots can be used to checkpoint progress or restore state later.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "The session ID."},
					"name":       {Type: "string", Description: "Human-readable name for the snapshot."},
				},
				Required: []string{"session_id", "name"},
			},
		},
		{
			Name:        "list_snapshots",
			Description: "List all workspace snapshots for a session.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "The session ID."},
				},
				Required: []string{"session_id"},
			},
		},
		{
			Name: "create_artifact",
			Description: "Promote a file from the session workspace to the shared artifact registry. " +
				"Artifacts are accessible across sessions and can be downloaded independently.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "The session ID containing the source file."},
					"file_path":  {Type: "string", Description: "Workspace path of the file to promote (e.g. '/output.csv')."},
					"name":       {Type: "string", Description: "Optional name for the artifact. Defaults to the filename."},
				},
				Required: []string{"session_id", "file_path"},
			},
		},
		{
			Name:        "list_artifacts",
			Description: "List all shared artifacts available across sessions.",
			InputSchema: inputSchema{Type: "object", Properties: map[string]schemaProp{}},
		},
		{
			Name: "list_audit_logs",
			Description: "Query the immutable audit trail. Returns a chronological log of all actions " +
				"(session creation, command execution, file uploads, etc.).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "Filter by session ID (optional)."},
					"limit":      {Type: "string", Description: "Maximum number of entries to return (default 20, max 100)."},
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Tool dispatch
// ---------------------------------------------------------------------------

func (s *server) callTool(ctx context.Context, name string, rawArgs json.RawMessage) (mcpToolResult, error) {
	args := make(map[string]string)
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return mcpToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
	}

	switch name {
	case "create_session":
		return s.toolCreateSession(ctx, args)
	case "list_sessions":
		return s.toolListSessions(ctx)
	case "get_session":
		return s.toolGetSession(ctx, args)
	case "delete_session":
		return s.toolDeleteSession(ctx, args)
	case "run_command":
		return s.toolRunCommand(ctx, args)
	case "upload_file":
		return s.toolUploadFile(ctx, args)
	case "read_file":
		return s.toolReadFile(ctx, args)
	case "list_files":
		return s.toolListFiles(ctx, args)
	case "delete_file":
		return s.toolDeleteFile(ctx, args)
	case "get_run":
		return s.toolGetRun(ctx, args)
	case "list_runs":
		return s.toolListRuns(ctx, args)
	case "create_snapshot":
		return s.toolCreateSnapshot(ctx, args)
	case "list_snapshots":
		return s.toolListSnapshots(ctx, args)
	case "create_artifact":
		return s.toolCreateArtifact(ctx, args)
	case "list_artifacts":
		return s.toolListArtifacts(ctx)
	case "list_audit_logs":
		return s.toolListAuditLogs(ctx, args)
	default:
		return mcpToolResult{}, fmt.Errorf("unknown tool %q", name)
	}
}

// ---------------------------------------------------------------------------
// Tool implementations
// ---------------------------------------------------------------------------

func (s *server) toolCreateSession(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	req := CreateSessionRequest{
		Image:          coalesce(args["image"], s.defaultImage),
		NetworkEnabled: args["network_enabled"] == "true",
	}
	if n := args["name"]; n != "" {
		req.Name = &n
	}
	if v := args["cpu_limit"]; v != "" {
		var f float64
		fmt.Sscanf(v, "%f", &f)
		req.CPULimit = f
	}
	if v := args["memory_limit_mb"]; v != "" {
		var n int
		fmt.Sscanf(v, "%d", &n)
		req.MemoryLimitMB = n
	}
	if v := args["timeout_seconds"]; v != "" {
		var n int
		fmt.Sscanf(v, "%d", &n)
		req.TimeoutSeconds = n
	}

	session, err := s.client.CreateSession(ctx, req)
	if err != nil {
		return mcpToolResult{}, err
	}

	text := fmt.Sprintf("Session created successfully.\n"+
		"session_id: %s\n"+
		"image: %s\n"+
		"status: %s\n"+
		"cpu_limit: %.1f\n"+
		"memory_limit_mb: %d\n"+
		"timeout_seconds: %d",
		session.ID, session.Image, session.Status,
		session.CPULimit, session.MemoryLimitMB, session.TimeoutSeconds)
	return textResult(text), nil
}

func (s *server) toolListSessions(ctx context.Context) (mcpToolResult, error) {
	sessions, err := s.client.ListSessions(ctx)
	if err != nil {
		return mcpToolResult{}, err
	}
	if len(sessions) == 0 {
		return textResult("No active sessions."), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d session(s):\n", len(sessions))
	for _, sess := range sessions {
		fmt.Fprintf(&sb, "  - %s  image=%s  status=%s  created=%s\n",
			sess.ID, sess.Image, sess.Status, sess.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return textResult(sb.String()), nil
}

func (s *server) toolGetSession(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	id := args["session_id"]
	if id == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	session, err := s.client.GetSession(ctx, id)
	if err != nil {
		return mcpToolResult{}, err
	}
	text := fmt.Sprintf("session_id: %s\nimage: %s\nstatus: %s\ncpu_limit: %.1f\nmemory_limit_mb: %d\ncreated: %s",
		session.ID, session.Image, session.Status,
		session.CPULimit, session.MemoryLimitMB, session.CreatedAt.Format("2006-01-02 15:04:05"))
	return textResult(text), nil
}

func (s *server) toolDeleteSession(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	id := args["session_id"]
	if id == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	if err := s.client.DeleteSession(ctx, id); err != nil {
		return mcpToolResult{}, err
	}
	return textResult(fmt.Sprintf("Session %s deleted successfully.", id)), nil
}

func (s *server) toolRunCommand(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	command := args["command"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	if command == "" {
		return mcpToolResult{}, fmt.Errorf("command is required")
	}

	req := RunRequest{
		Command:    command,
		WorkingDir: coalesce(args["working_dir"], "/workspace"),
	}

	if v := args["args"]; v != "" {
		var cmdArgs []string
		if err := json.Unmarshal([]byte(v), &cmdArgs); err != nil {
			return mcpToolResult{}, fmt.Errorf("args must be a JSON array of strings: %w", err)
		}
		req.Args = cmdArgs
	}

	if v := args["env"]; v != "" {
		var envMap map[string]string
		if err := json.Unmarshal([]byte(v), &envMap); err != nil {
			return mcpToolResult{}, fmt.Errorf("env must be a JSON object: %w", err)
		}
		req.Env = envMap
	}

	if v := args["timeout_seconds"]; v != "" {
		var n int
		fmt.Sscanf(v, "%d", &n)
		req.TimeoutSeconds = n
	}

	run, err := s.client.RunCommand(ctx, sessionID, req)
	if err != nil {
		return mcpToolResult{}, err
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "status: %s\n", run.Status)
	if run.ExitCode != nil {
		fmt.Fprintf(&sb, "exit_code: %d\n", *run.ExitCode)
	}
	fmt.Fprintf(&sb, "duration_ms: %d\n", run.DurationMS)
	if run.Stdout != nil && *run.Stdout != "" {
		fmt.Fprintf(&sb, "\n--- stdout ---\n%s", *run.Stdout)
	}
	if run.Stderr != nil && *run.Stderr != "" {
		fmt.Fprintf(&sb, "\n--- stderr ---\n%s", *run.Stderr)
	}
	if run.OutputTruncated {
		fmt.Fprintf(&sb, "\n[output truncated]")
	}

	isError := run.Status == "failed" || run.Status == "timeout"
	return mcpToolResult{
		Content: []mcpContent{{Type: "text", Text: sb.String()}},
		IsError: isError,
	}, nil
}

func (s *server) toolUploadFile(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	path := args["path"]
	content := args["content"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	if path == "" {
		return mcpToolResult{}, fmt.Errorf("path is required")
	}

	f, err := s.client.UploadFile(ctx, sessionID, path, content)
	if err != nil {
		return mcpToolResult{}, err
	}
	return textResult(fmt.Sprintf("File uploaded: %s (%d bytes)", f.Path, f.SizeBytes)), nil
}

func (s *server) toolReadFile(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	path := args["path"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	if path == "" {
		return mcpToolResult{}, fmt.Errorf("path is required")
	}
	content, err := s.client.DownloadFile(ctx, sessionID, path)
	if err != nil {
		return mcpToolResult{}, err
	}
	return textResult(content), nil
}

func (s *server) toolListFiles(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	files, err := s.client.ListFiles(ctx, sessionID)
	if err != nil {
		return mcpToolResult{}, err
	}
	if len(files) == 0 {
		return textResult("No files in workspace."), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d file(s):\n", len(files))
	for _, f := range files {
		fmt.Fprintf(&sb, "  %s  (%d bytes)  %s\n", f.Path, f.SizeBytes, f.ContentType)
	}
	return textResult(sb.String()), nil
}

func (s *server) toolDeleteFile(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	path := args["path"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	if path == "" {
		return mcpToolResult{}, fmt.Errorf("path is required")
	}
	if err := s.client.DeleteFile(ctx, sessionID, path); err != nil {
		return mcpToolResult{}, err
	}
	return textResult(fmt.Sprintf("File deleted: %s", path)), nil
}

func (s *server) toolGetRun(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	runID := args["run_id"]
	if runID == "" {
		return mcpToolResult{}, fmt.Errorf("run_id is required")
	}
	run, err := s.client.GetRun(ctx, runID)
	if err != nil {
		return mcpToolResult{}, err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "run_id: %s\nstatus: %s\n", run.ID, run.Status)
	if run.ExitCode != nil {
		fmt.Fprintf(&sb, "exit_code: %d\n", *run.ExitCode)
	}
	fmt.Fprintf(&sb, "duration_ms: %d\n", run.DurationMS)
	if run.Stdout != nil && *run.Stdout != "" {
		fmt.Fprintf(&sb, "\n--- stdout ---\n%s", *run.Stdout)
	}
	if run.Stderr != nil && *run.Stderr != "" {
		fmt.Fprintf(&sb, "\n--- stderr ---\n%s", *run.Stderr)
	}
	if run.OutputTruncated {
		fmt.Fprintf(&sb, "\n[output truncated]")
	}
	isError := run.Status == "failed" || run.Status == "timeout"
	return mcpToolResult{
		Content: []mcpContent{{Type: "text", Text: sb.String()}},
		IsError: isError,
	}, nil
}

func (s *server) toolListRuns(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	runs, err := s.client.ListRuns(ctx, sessionID)
	if err != nil {
		return mcpToolResult{}, err
	}
	if len(runs) == 0 {
		return textResult("No runs for this session."), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d run(s):\n", len(runs))
	for _, r := range runs {
		exitStr := "-"
		if r.ExitCode != nil {
			exitStr = fmt.Sprintf("%d", *r.ExitCode)
		}
		fmt.Fprintf(&sb, "  %s  cmd=%s  status=%s  exit=%s  duration=%dms\n",
			r.ID, r.Command, r.Status, exitStr, r.DurationMS)
	}
	return textResult(sb.String()), nil
}

func (s *server) toolCreateSnapshot(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	name := args["name"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	if name == "" {
		return mcpToolResult{}, fmt.Errorf("name is required")
	}
	snap, err := s.client.CreateSnapshot(ctx, sessionID, name)
	if err != nil {
		return mcpToolResult{}, err
	}
	return textResult(fmt.Sprintf("Snapshot created.\nsnapshot_id: %s\nname: %s\nsize_bytes: %d\ncreated: %s",
		snap.ID, snap.Name, snap.SizeBytes, snap.CreatedAt.Format("2006-01-02 15:04:05"))), nil
}

func (s *server) toolListSnapshots(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	snaps, err := s.client.ListSnapshots(ctx, sessionID)
	if err != nil {
		return mcpToolResult{}, err
	}
	if len(snaps) == 0 {
		return textResult("No snapshots for this session."), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d snapshot(s):\n", len(snaps))
	for _, sn := range snaps {
		fmt.Fprintf(&sb, "  %s  name=%s  size=%d bytes  created=%s\n",
			sn.ID, sn.Name, sn.SizeBytes, sn.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return textResult(sb.String()), nil
}

func (s *server) toolCreateArtifact(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	filePath := args["file_path"]
	name := args["name"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	if filePath == "" {
		return mcpToolResult{}, fmt.Errorf("file_path is required")
	}
	art, err := s.client.CreateArtifact(ctx, sessionID, filePath, name)
	if err != nil {
		return mcpToolResult{}, err
	}
	return textResult(fmt.Sprintf("Artifact created.\nartifact_id: %s\nname: %s\nsize_bytes: %d\ncreated: %s",
		art.ID, art.Name, art.SizeBytes, art.CreatedAt.Format("2006-01-02 15:04:05"))), nil
}

func (s *server) toolListArtifacts(ctx context.Context) (mcpToolResult, error) {
	arts, err := s.client.ListArtifacts(ctx)
	if err != nil {
		return mcpToolResult{}, err
	}
	if len(arts) == 0 {
		return textResult("No shared artifacts."), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d artifact(s):\n", len(arts))
	for _, a := range arts {
		fmt.Fprintf(&sb, "  %s  name=%s  size=%d bytes  type=%s  created=%s\n",
			a.ID, a.Name, a.SizeBytes, a.ContentType, a.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return textResult(sb.String()), nil
}

func (s *server) toolListAuditLogs(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	limit := 20
	if v := args["limit"]; v != "" {
		var n int
		fmt.Sscanf(v, "%d", &n)
		if n > 0 && n <= 100 {
			limit = n
		}
	}
	logs, err := s.client.ListAuditLogs(ctx, args["session_id"], limit)
	if err != nil {
		return mcpToolResult{}, err
	}
	if len(logs) == 0 {
		return textResult("No audit log entries found."), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d audit log entry(ies):\n", len(logs))
	for _, l := range logs {
		fmt.Fprintf(&sb, "  [%s]  actor=%s  action=%s  session=%s\n",
			l.Timestamp.Format("2006-01-02 15:04:05"), l.Actor, l.Action, l.SessionID)
	}
	return textResult(sb.String()), nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func textResult(text string) mcpToolResult {
	return mcpToolResult{Content: []mcpContent{{Type: "text", Text: text}}}
}

func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
