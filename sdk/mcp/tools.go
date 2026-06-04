// MCP tool definitions and dispatch.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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
		// ── Docker tools ──────────────────────────────────────────────────────────
		{
			Name:        "list_images",
			Description: "List Docker images available on the VaultRun host. Requires master API key.",
			InputSchema: inputSchema{Type: "object", Properties: map[string]schemaProp{}},
		},
		{
			Name:        "pull_image",
			Description: "Pull a Docker image from a registry. Requires master API key.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"image": {Type: "string", Description: "Docker image reference (e.g. 'python:3.12-slim')."},
				},
				Required: []string{"image"},
			},
		},
		{
			Name:        "get_session_stats",
			Description: "Get live CPU/memory/network stats for the session's container.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "The session ID."},
				},
				Required: []string{"session_id"},
			},
		},
		{
			Name:        "get_session_logs",
			Description: "Retrieve recent stdout+stderr from the session's container.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "The session ID."},
					"tail": {
						Type:        "string",
						Description: "Number of log lines to return (1–10000, default 100).",
					},
				},
				Required: []string{"session_id"},
			},
		},
		// ── GitHub tools ──────────────────────────────────────────────────────────
		{
			Name: "run_github_repo",
			Description: "Clone a GitHub repository into a new sandbox session and run commands sequentially.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"repo": {
						Type:        "string",
						Description: "GitHub repository in owner/repo format (e.g. 'python/cpython').",
					},
					"commands": {
						Type:        "string",
						Description: "JSON array of command arrays to run after cloning, e.g. '[[\"python\",\"main.py\"]]'.",
					},
					"branch": {
						Type:        "string",
						Description: "Branch or tag to clone. Defaults to the repository's default branch.",
					},
					"image": {
						Type:        "string",
						Description: "Docker image to use. Defaults to the server's default image.",
					},
					"working_dir": {
						Type:        "string",
						Description: "Working directory for running commands. Defaults to /workspace/repo.",
					},
					"keep_session": {
						Type:        "string",
						Enum:        []string{"true", "false"},
						Description: "Whether to keep the session after all commands finish. Default false.",
					},
				},
				Required: []string{"repo", "commands"},
			},
		},
		{
			Name:        "github_post_comment",
			Description: "Post a comment on a GitHub issue or pull request.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"repo": {
						Type:        "string",
						Description: "GitHub repository in owner/repo format.",
					},
					"number": {
						Type:        "string",
						Description: "Issue or pull request number.",
					},
					"body": {
						Type:        "string",
						Description: "Markdown body of the comment (max 65536 chars).",
					},
				},
				Required: []string{"repo", "number", "body"},
			},
		},
		// ── Filesystem tools ──────────────────────────────────────────────────────
		{
			Name:        "fs_read_file",
			Description: "Read a file from the local filesystem (MCP_FS_ALLOWED_PATHS must be set).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"path": {Type: "string", Description: "Absolute path to the file to read."},
				},
				Required: []string{"path"},
			},
		},
		{
			Name:        "fs_write_file",
			Description: "Write content to a file on the local filesystem.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"path":    {Type: "string", Description: "Absolute path to the file to write."},
					"content": {Type: "string", Description: "Content to write to the file."},
				},
				Required: []string{"path", "content"},
			},
		},
		{
			Name:        "fs_list_dir",
			Description: "List directory contents on the local filesystem.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"path": {Type: "string", Description: "Absolute path to the directory to list."},
				},
				Required: []string{"path"},
			},
		},
		{
			Name:        "fs_delete_file",
			Description: "Delete a file from the local filesystem.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"path": {Type: "string", Description: "Absolute path to the file to delete."},
				},
				Required: []string{"path"},
			},
		},
		// ── AWS S3 tools ──────────────────────────────────────────────────────
		{
			Name:        "s3_list_buckets",
			Description: "List all accessible S3 buckets. Requires AWS_REGION and valid credentials.",
			InputSchema: inputSchema{Type: "object", Properties: map[string]schemaProp{}},
		},
		{
			Name:        "s3_list_objects",
			Description: "List objects in an S3 bucket with optional prefix filter.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"bucket":   {Type: "string", Description: "S3 bucket name."},
					"prefix":   {Type: "string", Description: "Key prefix to filter by (optional)."},
					"max_keys": {Type: "string", Description: "Maximum number of objects to return (1–1000, default 100)."},
				},
				Required: []string{"bucket"},
			},
		},
		{
			Name:        "s3_get_object",
			Description: "Download the content of an S3 object (maximum 10 MB).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"bucket": {Type: "string", Description: "S3 bucket name."},
					"key":    {Type: "string", Description: "Object key."},
				},
				Required: []string{"bucket", "key"},
			},
		},
		{
			Name:        "s3_put_object",
			Description: "Upload text content to an S3 object, creating or overwriting it.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"bucket":       {Type: "string", Description: "S3 bucket name."},
					"key":          {Type: "string", Description: "Object key."},
					"content":      {Type: "string", Description: "Text content to upload."},
					"content_type": {Type: "string", Description: "MIME type (default: text/plain; charset=utf-8)."},
				},
				Required: []string{"bucket", "key", "content"},
			},
		},
		{
			Name:        "s3_delete_object",
			Description: "Delete an object from an S3 bucket.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"bucket": {Type: "string", Description: "S3 bucket name."},
					"key":    {Type: "string", Description: "Object key to delete."},
				},
				Required: []string{"bucket", "key"},
			},
		},
		{
			Name:        "s3_head_object",
			Description: "Get metadata for an S3 object (size, content-type, ETag, last-modified) without downloading it.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"bucket": {Type: "string", Description: "S3 bucket name."},
					"key":    {Type: "string", Description: "Object key."},
				},
				Required: []string{"bucket", "key"},
			},
		},
		// ── AWS SSM Parameter Store tools ─────────────────────────────────────
		{
			Name:        "ssm_get_parameter",
			Description: "Retrieve an SSM Parameter Store value by name.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"name":            {Type: "string", Description: "Parameter name or full path (e.g. '/myapp/db/password')."},
					"with_decryption": {Type: "string", Enum: []string{"true", "false"}, Description: "Decrypt SecureString values (default false)."},
				},
				Required: []string{"name"},
			},
		},
		{
			Name:        "ssm_put_parameter",
			Description: "Create or update an SSM Parameter Store value.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"name":      {Type: "string", Description: "Parameter name or full path."},
					"value":     {Type: "string", Description: "Parameter value."},
					"type":      {Type: "string", Enum: []string{"String", "StringList", "SecureString"}, Description: "Parameter type (default String)."},
					"overwrite": {Type: "string", Enum: []string{"true", "false"}, Description: "Overwrite existing value (default false)."},
				},
				Required: []string{"name", "value"},
			},
		},
		{
			Name:        "ssm_delete_parameter",
			Description: "Delete an SSM Parameter Store parameter.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"name": {Type: "string", Description: "Parameter name or full path to delete."},
				},
				Required: []string{"name"},
			},
		},
		{
			Name:        "ssm_list_parameters",
			Description: "List SSM Parameter Store parameters under a path hierarchy.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"path":        {Type: "string", Description: "Parameter path prefix (default '/'; returns all accessible parameters)."},
					"max_results": {Type: "string", Description: "Maximum number of results (1–100, default 50)."},
				},
			},
		},
		// ── AWS Secrets Manager tools ──────────────────────────────────────────
		{
			Name:        "sm_get_secret",
			Description: "Retrieve a secret value from AWS Secrets Manager.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"secret_id": {Type: "string", Description: "Secret name, ARN, or partial ARN."},
				},
				Required: []string{"secret_id"},
			},
		},
		{
			Name:        "sm_list_secrets",
			Description: "List secrets in AWS Secrets Manager.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"max_results": {Type: "string", Description: "Maximum number of secrets to return (1–100, default 20)."},
				},
			},
		},
		// ── AWS Lambda tools ───────────────────────────────────────────────────
		{
			Name:        "lambda_list_functions",
			Description: "List AWS Lambda functions in the configured region.",
			InputSchema: inputSchema{Type: "object", Properties: map[string]schemaProp{}},
		},
		{
			Name:        "lambda_invoke",
			Description: "Invoke an AWS Lambda function synchronously and return its response.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"function_name":   {Type: "string", Description: "Function name or ARN."},
					"payload":         {Type: "string", Description: "JSON payload to pass to the function (optional)."},
					"invocation_type": {Type: "string", Enum: []string{"RequestResponse", "Event", "DryRun"}, Description: "Invocation type (default RequestResponse)."},
				},
				Required: []string{"function_name"},
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
	case "list_images":
		return s.toolListImages(ctx)
	case "pull_image":
		return s.toolPullImage(ctx, args)
	case "get_session_stats":
		return s.toolGetSessionStats(ctx, args)
	case "get_session_logs":
		return s.toolGetSessionLogs(ctx, args)
	case "run_github_repo":
		return s.toolRunGithubRepo(ctx, args)
	case "github_post_comment":
		return s.toolGithubPostComment(ctx, args)
	case "fs_read_file":
		return s.toolFsReadFile(ctx, args)
	case "fs_write_file":
		return s.toolFsWriteFile(ctx, args)
	case "fs_list_dir":
		return s.toolFsListDir(ctx, args)
	case "fs_delete_file":
		return s.toolFsDeleteFile(ctx, args)
	case "s3_list_buckets":
		return s.toolS3ListBuckets(ctx)
	case "s3_list_objects":
		return s.toolS3ListObjects(ctx, args)
	case "s3_get_object":
		return s.toolS3GetObject(ctx, args)
	case "s3_put_object":
		return s.toolS3PutObject(ctx, args)
	case "s3_delete_object":
		return s.toolS3DeleteObject(ctx, args)
	case "s3_head_object":
		return s.toolS3HeadObject(ctx, args)
	case "ssm_get_parameter":
		return s.toolSSMGetParameter(ctx, args)
	case "ssm_put_parameter":
		return s.toolSSMPutParameter(ctx, args)
	case "ssm_delete_parameter":
		return s.toolSSMDeleteParameter(ctx, args)
	case "ssm_list_parameters":
		return s.toolSSMListParameters(ctx, args)
	case "sm_get_secret":
		return s.toolSMGetSecret(ctx, args)
	case "sm_list_secrets":
		return s.toolSMListSecrets(ctx, args)
	case "lambda_list_functions":
		return s.toolLambdaListFunctions(ctx)
	case "lambda_invoke":
		return s.toolLambdaInvoke(ctx, args)
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
		if f > 0 {
			req.CPULimit = f
		}
	}
	if v := args["memory_limit_mb"]; v != "" {
		var n int
		fmt.Sscanf(v, "%d", &n)
		if n > 0 {
			req.MemoryLimitMB = n
		}
	}
	if v := args["timeout_seconds"]; v != "" {
		var n int
		fmt.Sscanf(v, "%d", &n)
		if n > 0 {
			req.TimeoutSeconds = n
		}
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
		if n > 0 {
			req.TimeoutSeconds = n
		}
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
	if err := sanitizePath(path); err != nil {
		return mcpToolResult{}, err
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
	if err := sanitizePath(path); err != nil {
		return mcpToolResult{}, err
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
	if err := sanitizePath(path); err != nil {
		return mcpToolResult{}, err
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
	if err := sanitizePath(filePath); err != nil {
		return mcpToolResult{}, err
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
// Docker tool implementations
// ---------------------------------------------------------------------------

func (s *server) toolListImages(ctx context.Context) (mcpToolResult, error) {
	imgs, err := s.client.ListImages(ctx)
	if err != nil {
		return mcpToolResult{}, err
	}
	if len(imgs) == 0 {
		return textResult("No images."), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d image(s):\n", len(imgs))
	for _, img := range imgs {
		tags := strings.Join(img.Tags, ", ")
		if tags == "" {
			tags = "<none>"
		}
		sizeMB := float64(img.SizeBytes) / (1024 * 1024)
		fmt.Fprintf(&sb, "  %s  tags=[%s]  size=%.1fMB\n", img.ID, tags, sizeMB)
	}
	return textResult(sb.String()), nil
}

func (s *server) toolPullImage(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	image := args["image"]
	if image == "" {
		return mcpToolResult{}, fmt.Errorf("image is required")
	}
	if err := s.client.PullImage(ctx, image); err != nil {
		return mcpToolResult{}, err
	}
	return textResult(fmt.Sprintf("Image pulled: %s", image)), nil
}

func (s *server) toolGetSessionStats(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	stats, err := s.client.GetSessionStats(ctx, sessionID)
	if err != nil {
		return mcpToolResult{}, err
	}
	text := fmt.Sprintf(
		"cpu_percent: %.2f\n"+
			"memory_bytes: %d\n"+
			"memory_limit_bytes: %d\n"+
			"network_rx_bytes: %d\n"+
			"network_tx_bytes: %d",
		stats.CPUPercent,
		stats.MemoryBytes,
		stats.MemoryLimitBytes,
		stats.NetworkRxBytes,
		stats.NetworkTxBytes,
	)
	return textResult(text), nil
}

func (s *server) toolGetSessionLogs(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	tail := 100
	if v := args["tail"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 10000 {
			return mcpToolResult{}, fmt.Errorf("tail must be between 1 and 10000")
		}
		tail = n
	}
	logs, err := s.client.GetSessionLogs(ctx, sessionID, tail)
	if err != nil {
		return mcpToolResult{}, err
	}
	if logs == "" {
		return textResult("No logs."), nil
	}
	return textResult(logs), nil
}

// ---------------------------------------------------------------------------
// GitHub tool implementations
// ---------------------------------------------------------------------------

func (s *server) toolRunGithubRepo(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	repoArg := args["repo"]
	commandsArg := args["commands"]
	if repoArg == "" {
		return mcpToolResult{}, fmt.Errorf("repo is required")
	}
	if commandsArg == "" {
		return mcpToolResult{}, fmt.Errorf("commands is required")
	}

	owner, repo, err := parseOwnerRepo(repoArg)
	if err != nil {
		return mcpToolResult{}, err
	}

	var commands [][]string
	if err := json.Unmarshal([]byte(commandsArg), &commands); err != nil {
		return mcpToolResult{}, fmt.Errorf("commands must be JSON array of arrays, e.g. '[[\"python\",\"main.py\"]]'")
	}
	if len(commands) > 50 {
		return mcpToolResult{}, fmt.Errorf("max 50 commands")
	}
	for i, cmd := range commands {
		if len(cmd) == 0 {
			return mcpToolResult{}, fmt.Errorf("command %d is empty", i)
		}
		total := 0
		for _, part := range cmd {
			total += len(part) + 1
		}
		if total > 4096 {
			return mcpToolResult{}, fmt.Errorf("command %d too long", i)
		}
	}

	// Validate branch BEFORE creating a session (security critical).
	branch := args["branch"]
	token := s.githubToken

	if branch != "" {
		if err := validateGitRef(branch); err != nil {
			return mcpToolResult{}, err
		}
	} else {
		// Get default branch from GitHub API.
		gc := newGithubClient(token)
		b, err := gc.defaultBranch(ctx, owner, repo)
		if err != nil {
			branch = "main" // fallback
		} else {
			branch = b
		}
	}

	image := coalesce(args["image"], s.defaultImage)
	workingDir := coalesce(args["working_dir"], "/workspace/repo")
	keepSession := args["keep_session"] == "true"

	sess, err := s.client.CreateSession(ctx, CreateSessionRequest{
		Image:          image,
		NetworkEnabled: true,
	})
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("create session: %s", err.Error())
	}

	cleanup := func() {
		if !keepSession {
			_ = s.client.DeleteSession(ctx, sess.ID)
		}
	}

	cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)

	// Token-safe clone: inject as an HTTP extraheader so the token never appears
	// in a git remote URL or in GIT_CONFIG_KEY_* (which would embed it in the
	// API request body as a URL fragment and appear in any server-side logs).
	cloneEnv := map[string]string{
		"GIT_TERMINAL_PROMPT": "0",
	}
	if token != "" {
		cloneEnv["GIT_CONFIG_COUNT"] = "1"
		cloneEnv["GIT_CONFIG_KEY_0"] = "http.https://github.com/.extraheader"
		cloneEnv["GIT_CONFIG_VALUE_0"] = "Authorization: Bearer " + token
	}

	cloneRun, err := s.client.RunCommand(ctx, sess.ID, RunRequest{
		Command: "git",
		Args:    []string{"clone", "--branch", branch, "--depth", "1", "--", cloneURL, "/workspace/repo"},
		Env:     cloneEnv,
	})

	cloneOut := ""
	if err == nil {
		stdout := ""
		stderr := ""
		if cloneRun.Stdout != nil {
			stdout = *cloneRun.Stdout
		}
		if cloneRun.Stderr != nil {
			stderr = *cloneRun.Stderr
		}
		cloneOut = scrubToken(stdout+stderr, token)

		if cloneRun.ExitCode == nil || *cloneRun.ExitCode != 0 {
			cleanup()
			return mcpToolResult{
				Content: []mcpContent{{Type: "text", Text: "Clone failed:\n" + cloneOut}},
				IsError: true,
			}, nil
		}
	} else {
		cleanup()
		return mcpToolResult{}, fmt.Errorf("run clone: %s", scrubToken(err.Error(), token))
	}

	// Clean credentials from .git/config after successful clone.
	if token != "" {
		_, _ = s.client.RunCommand(ctx, sess.ID, RunRequest{
			Command:    "git",
			Args:       []string{"remote", "set-url", "origin", cloneURL},
			WorkingDir: "/workspace/repo",
		})
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "session_id: %s\n", sess.ID)
	if keepSession {
		fmt.Fprintf(&sb, "(session kept)\n")
	} else {
		fmt.Fprintf(&sb, "(session will be deleted)\n")
	}
	fmt.Fprintf(&sb, "\nCloned %s/%s@%s\n", owner, repo, branch)
	if cloneOut != "" {
		fmt.Fprintf(&sb, "%s\n", cloneOut)
	}

	isError := false
	for i, cmd := range commands {
		command := cmd[0]
		cmdArgs := cmd[1:]
		run, err := s.client.RunCommand(ctx, sess.ID, RunRequest{
			Command:    command,
			Args:       cmdArgs,
			WorkingDir: workingDir,
		})
		fmt.Fprintf(&sb, "\n--- Command %d: %s ---\n", i+1, strings.Join(cmd, " "))
		if err != nil {
			fmt.Fprintf(&sb, "error: %v\n", err)
			isError = true
			break
		}
		if run.ExitCode != nil {
			fmt.Fprintf(&sb, "exit_code: %d\n", *run.ExitCode)
		}
		if run.Stdout != nil && *run.Stdout != "" {
			fmt.Fprintf(&sb, "%s", *run.Stdout)
		}
		if run.Stderr != nil && *run.Stderr != "" {
			fmt.Fprintf(&sb, "%s", *run.Stderr)
		}
		if run.ExitCode != nil && *run.ExitCode != 0 {
			isError = true
			break
		}
	}

	cleanup()
	return mcpToolResult{Content: []mcpContent{{Type: "text", Text: sb.String()}}, IsError: isError}, nil
}

func (s *server) toolGithubPostComment(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	repoArg := args["repo"]
	numberStr := args["number"]
	body := args["body"]
	if repoArg == "" {
		return mcpToolResult{}, fmt.Errorf("repo is required")
	}
	if numberStr == "" {
		return mcpToolResult{}, fmt.Errorf("number is required")
	}
	if body == "" {
		return mcpToolResult{}, fmt.Errorf("body is required")
	}

	if len(body) > 65536 {
		return mcpToolResult{}, fmt.Errorf("body too long (max 65536 chars)")
	}

	owner, repo, err := parseOwnerRepo(repoArg)
	if err != nil {
		return mcpToolResult{}, err
	}

	number, err := strconv.Atoi(numberStr)
	if err != nil || number <= 0 || number > 1_000_000 {
		return mcpToolResult{}, fmt.Errorf("number must be a positive integer (1–1000000)")
	}

	gc := newGithubClient(s.githubToken)
	commentURL, err := gc.postComment(ctx, owner, repo, number, body)
	if err != nil {
		return mcpToolResult{}, err
	}
	return textResult(fmt.Sprintf("Comment posted: %s", commentURL)), nil
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

// sanitizePath is a defense-in-depth check at the MCP layer. The VaultRun API
// has its own path-traversal protection (URL-decode loop, ".." rejection,
// filepath.Clean, EvalSymlinks, O_NOFOLLOW), but catching the attempt here:
//   - gives the caller a clearer error message before any network round-trip
//   - prevents ".." components that might confuse path-escaping in client.go
//   - rejects null bytes and control characters that could confuse syscalls
func sanitizePath(p string) error {
	if p == "" {
		return fmt.Errorf("path must not be empty")
	}
	if len(p) > 4096 {
		return fmt.Errorf("path too long (max 4096 bytes)")
	}
	for _, ch := range p {
		if ch < 0x20 || ch == 0x7F {
			return fmt.Errorf("path %q: contains invalid control character", p)
		}
	}
	// Normalize backslashes then check every component.
	for _, part := range strings.Split(strings.ReplaceAll(p, "\\", "/"), "/") {
		if part == ".." {
			return fmt.Errorf("path %q: directory traversal not allowed", p)
		}
	}
	return nil
}
