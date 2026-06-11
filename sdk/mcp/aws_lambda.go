// AWS Lambda tools for the MCP server.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

func (s *server) toolLambdaListFunctions(ctx context.Context) (mcpToolResult, error) {
	client, err := s.lambdaOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}

	out, err := client.ListFunctions(ctx, &lambda.ListFunctionsInput{
		MaxItems: aws.Int32(50),
	})
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("ListFunctions: %w", err)
	}
	if len(out.Functions) == 0 {
		return textResult("No Lambda functions found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d Lambda functions:\n", len(out.Functions))
	for _, fn := range out.Functions {
		modified := ""
		if fn.LastModified != nil {
			modified = *fn.LastModified
		}
		fmt.Fprintf(&sb, "  %-40s  runtime=%-16s  memory=%dMB  modified=%s\n",
			aws.ToString(fn.FunctionName),
			string(fn.Runtime),
			aws.ToInt32(fn.MemorySize),
			modified)
	}
	if out.NextMarker != nil {
		fmt.Fprintf(&sb, "  ... (more functions available)\n")
	}
	return textResult(sb.String()), nil
}

func (s *server) toolLambdaInvoke(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	client, err := s.lambdaOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}

	functionName := args["function_name"]
	if functionName == "" {
		return mcpToolResult{}, fmt.Errorf("function_name is required")
	}

	// Validate invocation type.
	invType := lambdatypes.InvocationTypeRequestResponse
	switch args["invocation_type"] {
	case "Event":
		invType = lambdatypes.InvocationTypeEvent
	case "DryRun":
		invType = lambdatypes.InvocationTypeDryRun
	case "RequestResponse", "":
		// default
	default:
		return mcpToolResult{}, fmt.Errorf("invocation_type must be RequestResponse, Event, or DryRun")
	}

	// Validate payload JSON when provided. The Lambda synchronous invocation
	// limit is 6 MB for the combined request+response payload.
	const lambdaMaxPayload = 6 * 1024 * 1024
	var payload []byte
	if p := args["payload"]; p != "" {
		if len(p) > lambdaMaxPayload {
			return mcpToolResult{}, fmt.Errorf("payload exceeds Lambda limit (%d bytes, max 6 MB)", len(p))
		}
		if !json.Valid([]byte(p)) {
			return mcpToolResult{}, fmt.Errorf("payload must be valid JSON")
		}
		payload = []byte(p)
	}

	out, err := client.Invoke(ctx, &lambda.InvokeInput{
		FunctionName:   aws.String(functionName),
		InvocationType: invType,
		Payload:        payload,
	})
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("invoke %q: %w", functionName, err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Function:    %s\n", functionName)
	fmt.Fprintf(&sb, "Status code: %d\n", out.StatusCode)
	if out.FunctionError != nil {
		fmt.Fprintf(&sb, "Error:       %s\n", *out.FunctionError)
	}
	if out.ExecutedVersion != nil {
		fmt.Fprintf(&sb, "Version:     %s\n", *out.ExecutedVersion)
	}
	if len(out.Payload) > 0 {
		// Pretty-print JSON payloads; fall back to raw text.
		var pretty json.RawMessage
		if err := json.Unmarshal(out.Payload, &pretty); err == nil {
			formatted, _ := json.MarshalIndent(pretty, "", "  ")
			fmt.Fprintf(&sb, "Response:\n%s\n", formatted)
		} else {
			fmt.Fprintf(&sb, "Response: %s\n", out.Payload)
		}
	}
	return textResult(sb.String()), nil
}
