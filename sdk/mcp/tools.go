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
