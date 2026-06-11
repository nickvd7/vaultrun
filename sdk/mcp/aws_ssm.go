// AWS SSM Parameter Store and Secrets Manager tools for the MCP server.
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// ── SSM Parameter Store ───────────────────────────────────────────────────────

func (s *server) toolSSMGetParameter(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	client, err := s.ssmOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}

	name := args["name"]
	if name == "" {
		return mcpToolResult{}, fmt.Errorf("name is required")
	}

	withDecryption := args["with_decryption"] == "true"
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(withDecryption),
	})
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("GetParameter %q: %w", name, err)
	}

	p := out.Parameter
	var sb strings.Builder
	fmt.Fprintf(&sb, "Name:    %s\n", aws.ToString(p.Name))
	fmt.Fprintf(&sb, "Type:    %s\n", string(p.Type))
	switch {
	case p.Type == ssmtypes.ParameterTypeSecureString && !withDecryption:
		fmt.Fprintf(&sb, "Value:   <encrypted — pass with_decryption=true to reveal>\n")
	case p.Type == ssmtypes.ParameterTypeSecureString && withDecryption:
		fmt.Fprintf(&sb, "Value:   %s\n", aws.ToString(p.Value))
		fmt.Fprintf(&sb, "WARNING: decrypted secret returned in plaintext — handle with care.\n")
	default:
		fmt.Fprintf(&sb, "Value:   %s\n", aws.ToString(p.Value))
	}
	fmt.Fprintf(&sb, "Version: %d\n", p.Version)
	if p.LastModifiedDate != nil {
		fmt.Fprintf(&sb, "Modified: %s\n", p.LastModifiedDate.Format("2006-01-02 15:04:05 UTC"))
	}
	return textResult(sb.String()), nil
}

func (s *server) toolSSMPutParameter(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	client, err := s.ssmOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}

	name, value := args["name"], args["value"]
	if name == "" || value == "" {
		return mcpToolResult{}, fmt.Errorf("name and value are required")
	}

	paramType := ssmtypes.ParameterTypeString
	switch args["type"] {
	case "StringList":
		paramType = ssmtypes.ParameterTypeStringList
	case "SecureString":
		paramType = ssmtypes.ParameterTypeSecureString
	}

	overwrite := args["overwrite"] == "true"
	out, err := client.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(name),
		Value:     aws.String(value),
		Type:      paramType,
		Overwrite: aws.Bool(overwrite),
	})
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("PutParameter %q: %w", name, err)
	}

	return textResult(fmt.Sprintf("Parameter %q saved (version %d, tier %s).",
		name, out.Version, string(out.Tier))), nil
}

func (s *server) toolSSMDeleteParameter(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	client, err := s.ssmOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}

	name := args["name"]
	if name == "" {
		return mcpToolResult{}, fmt.Errorf("name is required")
	}

	if _, err := client.DeleteParameter(ctx, &ssm.DeleteParameterInput{Name: aws.String(name)}); err != nil {
		return mcpToolResult{}, fmt.Errorf("DeleteParameter %q: %w", name, err)
	}
	return textResult(fmt.Sprintf("Deleted parameter %q.", name)), nil
}

func (s *server) toolSSMListParameters(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	client, err := s.ssmOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}

	path := args["path"]
	if path == "" {
		path = "/"
	}

	maxResults := int32(50)
	if v := args["max_results"]; v != "" {
		var n int32
		fmt.Sscanf(v, "%d", &n)
		if n > 0 && n <= 100 {
			maxResults = n
		}
	}

	out, err := client.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
		Path:       aws.String(path),
		MaxResults: aws.Int32(maxResults),
		Recursive:  aws.Bool(true),
	})
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("GetParametersByPath %q: %w", path, err)
	}

	if len(out.Parameters) == 0 {
		return textResult(fmt.Sprintf("No parameters found under path %q.", path)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d parameters under %s:\n", len(out.Parameters), path)
	for _, p := range out.Parameters {
		fmt.Fprintf(&sb, "  %s  type=%s  version=%d\n",
			aws.ToString(p.Name), string(p.Type), p.Version)
	}
	if out.NextToken != nil {
		fmt.Fprintf(&sb, "  ... (more results available)\n")
	}
	return textResult(sb.String()), nil
}

// ── Secrets Manager ───────────────────────────────────────────────────────────

func (s *server) toolSMGetSecret(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	client, err := s.smOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}

	secretID := args["secret_id"]
	if secretID == "" {
		return mcpToolResult{}, fmt.Errorf("secret_id is required")
	}

	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretID),
	})
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("GetSecretValue %q: %w", secretID, err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "ARN:     %s\n", aws.ToString(out.ARN))
	fmt.Fprintf(&sb, "Name:    %s\n", aws.ToString(out.Name))
	if out.VersionId != nil {
		fmt.Fprintf(&sb, "Version: %s\n", *out.VersionId)
	}
	if out.CreatedDate != nil {
		fmt.Fprintf(&sb, "Created: %s\n", out.CreatedDate.Format("2006-01-02 15:04:05 UTC"))
	}
	if out.SecretString != nil {
		fmt.Fprintf(&sb, "Value:   %s\n", *out.SecretString)
	} else if out.SecretBinary != nil {
		fmt.Fprintf(&sb, "Value:   <binary, %d bytes>\n", len(out.SecretBinary))
	}
	return textResult(sb.String()), nil
}

func (s *server) toolSMListSecrets(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	client, err := s.smOrErr()
	if err != nil {
		return mcpToolResult{}, err
	}

	maxResults := int32(20)
	if v := args["max_results"]; v != "" {
		var n int32
		fmt.Sscanf(v, "%d", &n)
		if n > 0 && n <= 100 {
			maxResults = n
		}
	}

	out, err := client.ListSecrets(ctx, &secretsmanager.ListSecretsInput{
		MaxResults: aws.Int32(maxResults),
	})
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("ListSecrets: %w", err)
	}

	if len(out.SecretList) == 0 {
		return textResult("No secrets found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d secrets:\n", len(out.SecretList))
	for _, sec := range out.SecretList {
		rotated := "never"
		if sec.LastRotatedDate != nil {
			rotated = sec.LastRotatedDate.Format("2006-01-02")
		}
		fmt.Fprintf(&sb, "  %s  last-rotated=%s\n", aws.ToString(sec.Name), rotated)
	}
	if out.NextToken != nil {
		fmt.Fprintf(&sb, "  ... (more results available)\n")
	}
	return textResult(sb.String()), nil
}
