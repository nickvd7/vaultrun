// MCP shared protocol types, server state, and helpers.
//
// Production transports (stdio + HTTP) use the official Go MCP SDK — see
// bridge.go, main.go, and http.go.
package main

import (
	"encoding/json"
)

// ---------------------------------------------------------------------------
// JSON-RPC types
// ---------------------------------------------------------------------------

type jsonRPCRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"` // nil for notifications
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *jsonRPCError    `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC error codes.
const (
	// MCP-spec reserved codes used in HTTP/SDK test assertions.
	errHeaderMismatch             = -32020
	errUnsupportedProtocolVersion = -32022
)

// Protocol versions this server speaks.
// Prefer 2026-07-28; keep 2024-11-05 for legacy initialize/stdio clients.
const (
	protocolVersionCurrent = "2026-07-28"
	protocolVersionLegacy  = "2024-11-05"

	mcpServerName    = "vaultrun-mcp"
	mcpServerVersion = "0.1.0"
)

func supportedProtocolVersions() []string {
	return []string{protocolVersionCurrent, protocolVersionLegacy}
}

// ---------------------------------------------------------------------------
// MCP protocol types
// ---------------------------------------------------------------------------

type mcpTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema inputSchema `json:"inputSchema"`
}

type inputSchema struct {
	Type       string                `json:"type"`
	Properties map[string]schemaProp `json:"properties,omitempty"`
	Required   []string              `json:"required,omitempty"`
}

type schemaProp struct {
	Type        string      `json:"type"`
	Description string      `json:"description,omitempty"`
	Enum        []string    `json:"enum,omitempty"`
	Items       *schemaProp `json:"items,omitempty"`
	Default     any         `json:"default,omitempty"`
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	ResultType string         `json:"resultType,omitempty"`
	Content    []mcpContent   `json:"content"`
	IsError    bool           `json:"isError,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

func serverInstructions() string {
	return "Use VaultRun tools to create isolated sandbox sessions, execute " +
		"code, and manage files within those sessions. Always delete sessions when " +
		"finished to free resources. Application state is carried via explicit " +
		"session_id tool arguments (stateless MCP protocol)."
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

type server struct {
	client       *vaultRunClient
	defaultImage string
	githubToken  string
	fsConfig     fsConfig
	awsBundle    *awsBundle   // nil when AWS is not configured
	db           *dbBundle    // nil when no DB is configured
	flowd        *flowdConfig // nil when Flowd is not enabled
}

func newServer(client *vaultRunClient, defaultImage, githubToken string, fs fsConfig) *server {
	return &server{client: client, defaultImage: defaultImage, githubToken: githubToken, fsConfig: fs}
}
