// Bridge between VaultRun tool handlers and the official MCP Go SDK.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	extTasks = "io.modelcontextprotocol/tasks"
	extApps  = "io.modelcontextprotocol/ui"
)

// buildMCPServer constructs an official-SDK server that exposes all VaultRun
// tools, optional Tasks custom methods, and the MCP Apps UI resource.
func buildMCPServer(srv *server, tasks *taskStore, appsEnabled bool) *mcpsdk.Server {
	caps := &mcpsdk.ServerCapabilities{
		Tools: &mcpsdk.ToolCapabilities{ListChanged: false},
	}
	caps.AddExtension(extTasks, map[string]any{
		"list":   false,
		"cancel": true,
		"update": true,
	})
	if appsEnabled {
		caps.AddExtension(extApps, map[string]any{
			"mimeTypes": []string{"text/html;profile=mcp-app"},
		})
	}

	opts := &mcpsdk.ServerOptions{
		Instructions: serverInstructions(),
		Capabilities: caps,
		Logger:       slog.Default(),
	}
	sdk := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    mcpServerName,
		Version: mcpServerVersion,
	}, opts)

	registerVaultRunTools(sdk, srv, tasks)
	if err := registerTaskMethods(sdk, tasks); err != nil {
		slog.Error("vaultrun-mcp: register task methods", "err", err)
	}
	if appsEnabled {
		registerAppsResources(sdk)
	}
	return sdk
}

func registerVaultRunTools(sdk *mcpsdk.Server, srv *server, tasks *taskStore) {
	for _, def := range toolDefinitions() {
		def := def
		schema, err := inputSchemaToAny(def.InputSchema)
		if err != nil {
			slog.Error("vaultrun-mcp: skip tool with bad schema", "tool", def.Name, "err", err)
			continue
		}
		tool := &mcpsdk.Tool{
			Name:        def.Name,
			Description: def.Description,
			InputSchema: schema,
		}
		if uiMeta := toolUIMeta(def.Name); uiMeta != nil {
			tool.Meta = uiMeta
		}
		sdk.AddTool(tool, func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
			return dispatchTool(ctx, srv, tasks, req)
		})
	}
}

func dispatchTool(ctx context.Context, srv *server, tasks *taskStore, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	name := ""
	var args json.RawMessage
	if req != nil && req.Params != nil {
		name = req.Params.Name
		args = req.Params.Arguments
	}
	if name == "" {
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "error: tool name is required"}},
			IsError: true,
		}, nil
	}

	// Optional async path for long-running tools when the Tasks extension is used.
	if tasks != nil && wantsAsyncTask(req, args) && isTaskableTool(name) {
		return tasks.startToolTask(ctx, srv, name, args), nil
	}

	result, err := srv.callTool(ctx, name, args)
	if err != nil {
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: fmt.Sprintf("error: %s", err.Error())}},
			IsError: true,
		}, nil
	}
	return toSDKToolResult(result), nil
}

func toSDKToolResult(r mcpToolResult) *mcpsdk.CallToolResult {
	out := &mcpsdk.CallToolResult{IsError: r.IsError}
	for _, c := range r.Content {
		out.Content = append(out.Content, &mcpsdk.TextContent{Text: c.Text})
	}
	return out
}

func inputSchemaToAny(s inputSchema) (any, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m["type"] == nil {
		m["type"] = "object"
	}
	return m, nil
}

func wantsAsyncTask(req *mcpsdk.CallToolRequest, args json.RawMessage) bool {
	if len(args) > 0 {
		var m map[string]any
		if err := json.Unmarshal(args, &m); err == nil {
			switch v := m["async"].(type) {
			case bool:
				if v {
					return true
				}
			case string:
				if v == "true" || v == "1" {
					return true
				}
			}
		}
	}
	if req == nil {
		return false
	}
	caps := req.ClientCapabilities()
	if caps == nil || caps.Extensions == nil {
		return false
	}
	_, ok := caps.Extensions[extTasks]
	return ok
}

func isTaskableTool(name string) bool {
	switch name {
	case "run_command", "run_github_repo", "pull_image", "browser_navigate", "browser_pdf":
		return true
	default:
		return false
	}
}
