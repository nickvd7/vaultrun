// MCP JSON-RPC 2.0 protocol types and server loop.
package main

import (
	"bufio"
	"bytes"
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
	githubToken  string
	fsConfig     fsConfig
	awsBundle    *awsBundle // nil when AWS is not configured
	mu           sync.Mutex // guards stdout writes
}

func newServer(client *vaultRunClient, defaultImage, githubToken string, fs fsConfig) *server {
	return &server{client: client, defaultImage: defaultImage, githubToken: githubToken, fsConfig: fs}
}

func (s *server) serve(ctx context.Context, in io.Reader, out io.Writer) error {
	const maxMsg = 4 * 1024 * 1024

	// Use ReadSlice('\n') rather than bufio.Scanner so we can drain and
	// continue after an oversized message instead of terminating the session.
	r := bufio.NewReaderSize(in, maxMsg+1)

	for {
		raw, err := r.ReadSlice('\n')

		if err == bufio.ErrBufferFull {
			// This line exceeds maxMsg — drain the remainder, send error, keep going.
			slog.Warn("mcp: message too large, discarding line")
			for err == bufio.ErrBufferFull {
				_, err = r.ReadSlice('\n')
			}
			if err != nil && err != io.EOF {
				return err
			}
			s.write(out, jsonRPCResponse{JSONRPC: "2.0", Error: &jsonRPCError{Code: errInvalidRequest, Message: "message too large (max 4 MB)"}})
			if err == io.EOF {
				return nil
			}
			continue
		}
		if err == io.EOF {
			if len(raw) == 0 {
				return nil
			}
			// Fall through to process a final line that has no trailing newline.
		} else if err != nil {
			return err
		}

		line := bytes.TrimRight(raw, "\r\n")
		if len(line) == 0 {
			continue
		}

		// raw is backed by r's internal buffer and valid until the next read.
		// json.Unmarshal is synchronous so no copy is needed here.
		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			slog.Warn("mcp: parse error", "err", err)
			s.write(out, jsonRPCResponse{JSONRPC: "2.0", Error: &jsonRPCError{Code: errParse, Message: "parse error"}})
			continue
		}

		slog.Debug("mcp: received", "method", req.Method, "id", req.ID)
		s.handle(ctx, out, &req)

		if err == io.EOF {
			return nil
		}
	}
}

func (s *server) handle(ctx context.Context, out io.Writer, req *jsonRPCRequest) {
	resp := s.handleRequest(ctx, req)
	if resp != nil {
		s.write(out, resp)
	}
}

// handleRequest processes a single JSON-RPC request and returns the response,
// or nil for notifications that require no response. It is transport-agnostic:
// both the stdio loop and the HTTP handler call this method.
func (s *server) handleRequest(ctx context.Context, req *jsonRPCRequest) *jsonRPCResponse {
	isNotification := req.ID == nil

	switch req.Method {
	case "initialize":
		if isNotification {
			return nil
		}
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpInitializeResult{
				ProtocolVersion: "2024-11-05",
				Capabilities: mcpCapabilities{
					Tools: &mcpToolsCapability{ListChanged: false},
				},
				ServerInfo: mcpServerInfo{Name: "vaultrun-mcp", Version: "0.1.0"},
				Instructions: "Use VaultRun tools to create isolated sandbox sessions, execute " +
					"code, and manage files within those sessions. Always delete sessions when " +
					"finished to free resources.",
			},
		}

	case "initialized":
		return nil

	case "ping":
		if isNotification {
			return nil
		}
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: struct{}{}}

	case "tools/list":
		if isNotification {
			return nil
		}
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: mcpToolsListResult{Tools: toolDefinitions()}}

	case "tools/call":
		if isNotification {
			return nil
		}
		var params mcpToolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &jsonRPCError{Code: errInvalidParams, Message: "invalid params: " + err.Error()},
			}
		}
		result, err := s.callTool(ctx, params.Name, params.Arguments)
		if err != nil {
			// Tool errors are returned as MCP tool results with isError=true,
			// NOT as JSON-RPC errors. This lets the AI model see the error message
			// and decide how to recover.
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: mcpToolResult{
					Content: []mcpContent{{Type: "text", Text: fmt.Sprintf("error: %s", err.Error())}},
					IsError: true,
				},
			}
		}
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}

	default:
		if isNotification {
			return nil
		}
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: errMethodNotFound, Message: fmt.Sprintf("method %q not found", req.Method)},
		}
	}
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
