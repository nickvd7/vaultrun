// Legacy custom JSON-RPC serve loop.
//
// Production transports use the official Go MCP SDK (see bridge.go, main.go,
// http.go). This file remains only for a small set of edge-case unit tests
// (server/discover, unsupported protocolVersion in _meta, notifications,
// oversized messages). Core protocol coverage lives in sdk_protocol_test.go
// on the official SDK in-memory transport.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
)

// serve runs the legacy line-delimited JSON-RPC loop. Used by tests only;
// production stdio uses mcpsdk.StdioTransport via buildMCPServer.
func (s *server) serve(ctx context.Context, in io.Reader, out io.Writer) error {
	const maxMsg = 4 * 1024 * 1024

	// Use ReadSlice('\n') rather than bufio.Scanner so we can drain and
	// continue after an oversized message instead of terminating the session.
	r := bufio.NewReaderSize(in, maxMsg+1)

	for {
		raw, err := r.ReadSlice('\n')

		if err == bufio.ErrBufferFull {
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
		} else if err != nil {
			return err
		}

		line := bytes.TrimRight(raw, "\r\n")
		if len(line) == 0 {
			continue
		}

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

// handleRequest is the legacy JSON-RPC method dispatcher (tests / compat only).
func (s *server) handleRequest(ctx context.Context, req *jsonRPCRequest) *jsonRPCResponse {
	isNotification := req.ID == nil
	meta := parseRequestMeta(req.Params)

	if meta.ProtocolVersion != "" && req.Method != "server/discover" && !isSupportedProtocolVersion(meta.ProtocolVersion) {
		if isNotification {
			return nil
		}
		return unsupportedVersionError(req.ID, meta.ProtocolVersion)
	}

	switch req.Method {
	case "initialize":
		if isNotification {
			return nil
		}
		version := protocolVersionLegacy
		var initParams struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &initParams)
			if isSupportedProtocolVersion(initParams.ProtocolVersion) {
				version = initParams.ProtocolVersion
			}
		}
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpInitializeResult{
				ProtocolVersion: version,
				Capabilities:    defaultCapabilities(),
				ServerInfo:      defaultServerInfo(),
				Instructions:    serverInstructions(),
			},
		}

	case "initialized":
		return nil

	case "server/discover":
		if isNotification {
			return nil
		}
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpDiscoverResult{
				ResultType:        "complete",
				SupportedVersions: supportedProtocolVersions(),
				Capabilities:      defaultCapabilities(),
				Meta:              serverInfoMeta(),
				Instructions:      serverInstructions(),
				TTLMs:             discoverTTLMs,
				CacheScope:        "public",
			},
		}

	case "ping":
		if isNotification {
			return nil
		}
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"resultType": "complete",
			"_meta":      serverInfoMeta(),
		}}

	case "tools/list":
		if isNotification {
			return nil
		}
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpToolsListResult{
				ResultType: "complete",
				Tools:      toolDefinitions(),
				Meta:       serverInfoMeta(),
				TTLMs:      toolsListTTLMs,
				CacheScope: "public",
			},
		}

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
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: withToolResultMeta(mcpToolResult{
					Content: []mcpContent{{Type: "text", Text: fmt.Sprintf("error: %s", err.Error())}},
					IsError: true,
				}),
			}
		}
		return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: withToolResultMeta(result)}

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
	_, _ = out.Write(b)
	_, _ = out.Write([]byte("\n"))
}
