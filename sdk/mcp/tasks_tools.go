package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// taskToolDefinitions exposes Tasks poll/update/cancel as ordinary MCP tools.
// Most hosts (Cursor, Claude Code, Claude Desktop tools path) only call
// tools/call — without these wrappers the custom tasks/* methods are unreachable.
func taskToolDefinitions() []mcpTool {
	return []mcpTool{
		{
			Name: "get_task",
			Description: "Poll an async MCP Task started with async=true on a tool (e.g. run_command). " +
				"Returns status, progress, result/error, and any pending inputRequests. " +
				"Prefer this over the tasks/get custom method when the host only supports tools.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"task_id": {
						Type:        "string",
						Description: "Task ID returned when the async tool started (task_<uuid>).",
					},
				},
				Required: []string{"task_id"},
			},
		},
		{
			Name: "update_task",
			Description: "Update a working MCP Task: optional progress/message, or answer input_required " +
				"(approve/decline confirm, or JSON input_responses). Prefer this over tasks/update when " +
				"the host only supports tools.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"task_id": {
						Type:        "string",
						Description: "Task ID (task_<uuid>).",
					},
					"message": {
						Type:        "string",
						Description: "Optional progress message for the task.",
					},
					"progress": {
						Type:        "string",
						Description: "Optional progress 0..1 (e.g. '0.5').",
					},
					"approve": {
						Type:        "string",
						Enum:        []string{"true", "false"},
						Description: "When the task is input_required for confirm: true accepts, false declines.",
					},
					"input_responses": {
						Type: "string",
						Description: "Optional JSON object of inputResponses keyed by request id " +
							`(e.g. '{"confirm":{"action":"accept","content":{"approved":true}}}').`,
					},
				},
				Required: []string{"task_id"},
			},
		},
		{
			Name:        "cancel_task",
			Description: "Cancel a working MCP Task. Prefer this over tasks/cancel when the host only supports tools.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"task_id": {
						Type:        "string",
						Description: "Task ID (task_<uuid>).",
					},
				},
				Required: []string{"task_id"},
			},
		},
	}
}

func isTaskControlTool(name string) bool {
	switch name {
	case "get_task", "update_task", "cancel_task":
		return true
	default:
		return false
	}
}

func dispatchTaskTool(ctx context.Context, tasks *taskStore, name string, args json.RawMessage) *mcpsdk.CallToolResult {
	if tasks == nil {
		return taskToolError("tasks store not available")
	}
	m := map[string]string{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &m); err != nil {
			return taskToolError("invalid arguments: " + err.Error())
		}
	}
	taskID := strings.TrimSpace(m["task_id"])
	if taskID == "" {
		taskID = strings.TrimSpace(m["taskId"])
	}
	if taskID == "" {
		return taskToolError("task_id is required")
	}

	switch name {
	case "get_task":
		res, err := tasks.getTaskResult(ctx, taskID)
		if err != nil {
			return taskToolError(err.Error())
		}
		return taskToolJSON(res)
	case "cancel_task":
		res, err := tasks.cancelTaskResult(ctx, taskID)
		if err != nil {
			return taskToolError(err.Error())
		}
		return taskToolJSON(res)
	case "update_task":
		params := &tasksUpdateParams{TaskID: taskID, Message: m["message"]}
		if p := strings.TrimSpace(m["progress"]); p != "" {
			f, err := strconv.ParseFloat(p, 64)
			if err != nil {
				return taskToolError("progress must be a number")
			}
			params.Progress = f
		}
		if raw := strings.TrimSpace(m["input_responses"]); raw != "" {
			var responses map[string]taskInputResponse
			if err := json.Unmarshal([]byte(raw), &responses); err != nil {
				return taskToolError("input_responses must be a JSON object: " + err.Error())
			}
			params.InputResponses = responses
		}
		switch strings.ToLower(strings.TrimSpace(m["approve"])) {
		case "true", "1", "yes":
			if params.InputResponses == nil {
				params.InputResponses = map[string]taskInputResponse{}
			}
			if _, exists := params.InputResponses[taskConfirmRequestKey]; !exists {
				content, _ := json.Marshal(map[string]any{"approved": true})
				params.InputResponses[taskConfirmRequestKey] = taskInputResponse{Action: "accept", Content: content}
			}
		case "false", "0", "no":
			if params.InputResponses == nil {
				params.InputResponses = map[string]taskInputResponse{}
			}
			if _, exists := params.InputResponses[taskConfirmRequestKey]; !exists {
				params.InputResponses[taskConfirmRequestKey] = taskInputResponse{Action: "decline"}
			}
		}
		res, err := tasks.updateTaskResult(ctx, params)
		if err != nil {
			return taskToolError(err.Error())
		}
		return taskToolJSON(res)
	default:
		return taskToolError(fmt.Sprintf("unknown task tool %q", name))
	}
}

func taskToolError(msg string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "error: " + msg}},
		IsError: true,
	}
}

func taskToolJSON(v any) *mcpsdk.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return taskToolError(err.Error())
	}
	return &mcpsdk.CallToolResult{
		Content:           []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
		StructuredContent: v,
	}
}
