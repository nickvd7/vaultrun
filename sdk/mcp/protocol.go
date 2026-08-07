// MCP shared protocol types, server state, and helpers.
//
// Production transports (stdio + HTTP) use the official Go MCP SDK — see
// bridge.go, main.go, and http.go. The legacy line-delimited JSON-RPC loop
// lives in protocol_legacy.go for unit tests only.
package main

import (
	"encoding/json"
	"sync"
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
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603

	// MCP-spec reserved codes (2026-07-28).
	errHeaderMismatch             = -32020
	errMissingClientCapability    = -32021
	errUnsupportedProtocolVersion = -32022
)

// Protocol versions this server speaks.
// Prefer 2026-07-28; keep 2024-11-05 for legacy initialize/stdio clients.
const (
	protocolVersionCurrent = "2026-07-28"
	protocolVersionLegacy  = "2024-11-05"

	mcpServerName    = "vaultrun-mcp"
	mcpServerVersion = "0.1.0"

	metaKeyProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaKeyClientInfo         = "io.modelcontextprotocol/clientInfo"
	metaKeyClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaKeyServerInfo         = "io.modelcontextprotocol/serverInfo"

	// Cache hints for list/discover (tool catalog is process-static).
	toolsListTTLMs = 300_000   // 5 minutes
	discoverTTLMs  = 3_600_000 // 1 hour
)

func supportedProtocolVersions() []string {
	return []string{protocolVersionCurrent, protocolVersionLegacy}
}

func isSupportedProtocolVersion(v string) bool {
	for _, s := range supportedProtocolVersions() {
		if s == v {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// MCP protocol types
// ---------------------------------------------------------------------------

type mcpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type mcpCapabilities struct {
	Tools *mcpToolsCapability `json:"tools,omitempty"`
}

type mcpToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type mcpInitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    mcpCapabilities `json:"capabilities"`
	ServerInfo      mcpServerInfo   `json:"serverInfo"`
	Instructions    string          `json:"instructions,omitempty"`
}

// mcpDiscoverResult is the 2026-07-28 server/discover response.
type mcpDiscoverResult struct {
	ResultType        string          `json:"resultType"`
	SupportedVersions []string        `json:"supportedVersions"`
	Capabilities      mcpCapabilities `json:"capabilities"`
	Meta              map[string]any  `json:"_meta,omitempty"`
	Instructions      string          `json:"instructions,omitempty"`
	TTLMs             int             `json:"ttlMs"`
	CacheScope        string          `json:"cacheScope"`
}

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

type mcpToolsListResult struct {
	ResultType string         `json:"resultType,omitempty"`
	Tools      []mcpTool      `json:"tools"`
	Meta       map[string]any `json:"_meta,omitempty"`
	TTLMs      int            `json:"ttlMs,omitempty"`
	CacheScope string         `json:"cacheScope,omitempty"`
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

// requestMeta is the subset of params._meta we care about.
type requestMeta struct {
	ProtocolVersion    string
	ClientInfo         *mcpServerInfo
	ClientCapabilities json.RawMessage // present (possibly {}) when set
	HasMeta            bool
}

func defaultServerInfo() mcpServerInfo {
	return mcpServerInfo{Name: mcpServerName, Version: mcpServerVersion}
}

func defaultCapabilities() mcpCapabilities {
	return mcpCapabilities{Tools: &mcpToolsCapability{ListChanged: false}}
}

func serverInstructions() string {
	return "Use VaultRun tools to create isolated sandbox sessions, execute " +
		"code, and manage files within those sessions. Always delete sessions when " +
		"finished to free resources. Application state is carried via explicit " +
		"session_id tool arguments (stateless MCP protocol)."
}

func serverInfoMeta() map[string]any {
	return map[string]any{metaKeyServerInfo: defaultServerInfo()}
}

func parseRequestMeta(params json.RawMessage) requestMeta {
	if len(params) == 0 {
		return requestMeta{}
	}
	var envelope struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil || envelope.Meta == nil {
		return requestMeta{}
	}
	rm := requestMeta{HasMeta: true}
	if raw, ok := envelope.Meta[metaKeyProtocolVersion]; ok {
		_ = json.Unmarshal(raw, &rm.ProtocolVersion)
	}
	if raw, ok := envelope.Meta[metaKeyClientInfo]; ok {
		var info mcpServerInfo
		if err := json.Unmarshal(raw, &info); err == nil {
			rm.ClientInfo = &info
		}
	}
	if raw, ok := envelope.Meta[metaKeyClientCapabilities]; ok {
		rm.ClientCapabilities = raw
	}
	return rm
}

func unsupportedVersionError(id *json.RawMessage, requested string) *jsonRPCResponse {
	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &jsonRPCError{
			Code:    errUnsupportedProtocolVersion,
			Message: "unsupported protocol version",
			Data: map[string]any{
				"supported": supportedProtocolVersions(),
				"requested": requested,
			},
		},
	}
}

func withToolResultMeta(r mcpToolResult) mcpToolResult {
	r.ResultType = "complete"
	if r.Meta == nil {
		r.Meta = serverInfoMeta()
	}
	return r
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
	mu           sync.Mutex   // guards stdout writes
}

func newServer(client *vaultRunClient, defaultImage, githubToken string, fs fsConfig) *server {
	return &server{client: client, defaultImage: defaultImage, githubToken: githubToken, fsConfig: fs}
}
