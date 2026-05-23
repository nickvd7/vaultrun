// MCP JSON-RPC 2.0 protocol types and server loop.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
)

// ---------------------------------------------------------------------------
// JSON-RPC types
// ---------------------------------------------------------------------------

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"` // nil for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
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
}

// JSON-RPC error codes.
const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

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

type mcpTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema inputSchema `json:"inputSchema"`
}

type inputSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]schemaProp     `json:"properties,omitempty"`
	Required   []string                  `json:"required,omitempty"`
}

type schemaProp struct {
	Type        string            `json:"type"`
	Description string            `json:"description,omitempty"`
	Enum        []string          `json:"enum,omitempty"`
	Items       *schemaProp       `json:"items,omitempty"`
	Default     any               `json:"default,omitempty"`
}

type mcpToolsListResult struct {
	Tools []mcpTool `json:"tools"`
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
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

type server struct {
	client       *vaultRunClient
	defaultImage string
	mu           sync.Mutex // guards stdout writes
}

func newServer(client *vaultRunClient, defaultImage string) *server {
	return &server{client: client, defaultImage: defaultImage}
}

func (s *server) serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024) // 4 MB max message

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			slog.Warn("mcp: parse error", "err", err)
			s.writeError(out, nil, errParse, "parse error")
			continue
		}

		slog.Debug("mcp: received", "method", req.Method, "id", req.ID)
		s.handle(ctx, out, &req)
	}
	return scanner.Err()
}

func (s *server) handle(ctx context.Context, out io.Writer, req *jsonRPCRequest) {
	// Notifications have no ID and require no response.
	isNotification := req.ID == nil

	switch req.Method {
	case "initialize":
		if isNotification {
			return
		}
		s.writeResult(out, req.ID, mcpInitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities: mcpCapabilities{
				Tools: &mcpToolsCapability{ListChanged: false},
			},
			ServerInfo: mcpServerInfo{Name: "vaultrun-mcp", Version: "0.1.0"},
			Instructions: "Use VaultRun tools to create isolated sandbox sessions, execute " +
				"code, and manage files within those sessions. Always delete sessions when " +
				"finished to free resources.",
		})

	case "initialized":
		// Notification from client — no response required.
		return

	case "ping":
		if isNotification {
			return
		}
		s.writeResult(out, req.ID, struct{}{})

	case "tools/list":
		if isNotification {
			return
		}
		s.writeResult(out, req.ID, mcpToolsListResult{Tools: toolDefinitions()})

	case "tools/call":
		if isNotification {
			return
		}
		var params mcpToolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeError(out, req.ID, errInvalidParams, "invalid params: "+err.Error())
			return
		}
		result, err := s.callTool(ctx, params.Name, params.Arguments)
		if err != nil {
			// Tool errors are returned as MCP tool results with isError=true,
			// NOT as JSON-RPC errors. This lets the AI model see the error message
			// and decide how to recover.
			s.writeResult(out, req.ID, mcpToolResult{
				Content: []mcpContent{{Type: "text", Text: fmt.Sprintf("error: %s", err.Error())}},
				IsError: true,
			})
			return
		}
		s.writeResult(out, req.ID, result)

	default:
		if isNotification {
			return
		}
		s.writeError(out, req.ID, errMethodNotFound, fmt.Sprintf("method %q not found", req.Method))
	}
}

func (s *server) writeResult(out io.Writer, id *json.RawMessage, result any) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	s.write(out, resp)
}

func (s *server) writeError(out io.Writer, id *json.RawMessage, code int, msg string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: msg},
	}
	s.write(out, resp)
}

func (s *server) write(out io.Writer, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("mcp: marshal response", "err", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// MCP over stdio: one JSON object per line, terminated with \n.
	_, _ = out.Write(b)
	_, _ = out.Write([]byte("\n"))
}
