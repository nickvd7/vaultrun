// Agent memory tools — persistent key/value notes in the sandbox workspace
// under .vaultrun/memory/. Survives within a session and via workspace snapshots.
package main

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
)

const memoryRoot = ".vaultrun/memory"
const memoryMaxValueBytes = 256 * 1024

var memoryKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,198}[a-zA-Z0-9]$|^[a-zA-Z0-9]$`)

func memoryToolDefinitions() []mcpTool {
	return []mcpTool{
		{
			Name: "memory_set",
			Description: "Store a persistent agent memory note in the session workspace " +
				"(.vaultrun/memory/<key>). Survives within the session and when a snapshot is taken.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "Sandbox session ID."},
					"key": {
						Type:        "string",
						Description: "Memory key (letters, digits, . _ / -; max 200 chars; no '.' components).",
					},
					"value": {Type: "string", Description: "Text value to store (max 256 KiB)."},
				},
				Required: []string{"session_id", "key", "value"},
			},
		},
		{
			Name:        "memory_get",
			Description: "Read a persistent agent memory note from .vaultrun/memory/<key>.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "Sandbox session ID."},
					"key":        {Type: "string", Description: "Memory key."},
				},
				Required: []string{"session_id", "key"},
			},
		},
		{
			Name:        "memory_list",
			Description: "List memory keys stored under .vaultrun/memory/ in the session workspace.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "Sandbox session ID."},
				},
				Required: []string{"session_id"},
			},
		},
		{
			Name:        "memory_delete",
			Description: "Delete a persistent agent memory note.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {Type: "string", Description: "Sandbox session ID."},
					"key":        {Type: "string", Description: "Memory key."},
				},
				Required: []string{"session_id", "key"},
			},
		},
	}
}

func memoryPathForKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("key is required")
	}
	if strings.Contains(key, "..") || strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") {
		return "", fmt.Errorf("invalid memory key %q", key)
	}
	if strings.Contains(key, "//") || strings.Contains(key, "/./") || strings.HasPrefix(key, "./") {
		return "", fmt.Errorf("invalid memory key %q", key)
	}
	if !memoryKeyPattern.MatchString(key) {
		return "", fmt.Errorf("invalid memory key %q (use letters, digits, . _ / -)", key)
	}
	clean := path.Clean(key)
	if clean != key || clean == "." || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("invalid memory key %q", key)
	}
	return memoryRoot + "/" + clean, nil
}

func memoryKeyFromPath(p string) (string, bool) {
	p = strings.TrimPrefix(p, "/")
	prefix := memoryRoot + "/"
	if !strings.HasPrefix(p, prefix) {
		return "", false
	}
	key := strings.TrimPrefix(p, prefix)
	if _, err := memoryPathForKey(key); err != nil {
		return "", false
	}
	return key, true
}

func (s *server) toolMemorySet(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	mpath, err := memoryPathForKey(args["key"])
	if err != nil {
		return mcpToolResult{}, err
	}
	if err := sanitizePath(mpath); err != nil {
		return mcpToolResult{}, err
	}
	if len(args["value"]) > memoryMaxValueBytes {
		return mcpToolResult{}, fmt.Errorf("value exceeds maximum size of %d bytes", memoryMaxValueBytes)
	}
	f, err := s.client.UploadFile(ctx, sessionID, mpath, args["value"])
	if err != nil {
		return mcpToolResult{}, err
	}
	return textResult(fmt.Sprintf("memory set: %s (%d bytes)", args["key"], f.SizeBytes)), nil
}

func (s *server) toolMemoryGet(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	mpath, err := memoryPathForKey(args["key"])
	if err != nil {
		return mcpToolResult{}, err
	}
	if err := sanitizePath(mpath); err != nil {
		return mcpToolResult{}, err
	}
	content, err := s.client.DownloadFile(ctx, sessionID, mpath)
	if err != nil {
		return mcpToolResult{}, err
	}
	return textResult(content), nil
}

func (s *server) toolMemoryList(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	files, err := s.client.ListFiles(ctx, sessionID)
	if err != nil {
		return mcpToolResult{}, err
	}
	var keys []string
	for _, f := range files {
		if k, ok := memoryKeyFromPath(f.Path); ok {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return textResult("No memory keys."), nil
	}
	return textResult(strings.Join(keys, "\n")), nil
}

func (s *server) toolMemoryDelete(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}
	mpath, err := memoryPathForKey(args["key"])
	if err != nil {
		return mcpToolResult{}, err
	}
	if err := sanitizePath(mpath); err != nil {
		return mcpToolResult{}, err
	}
	if err := s.client.DeleteFile(ctx, sessionID, mpath); err != nil {
		return mcpToolResult{}, err
	}
	return textResult(fmt.Sprintf("memory deleted: %s", args["key"])), nil
}
