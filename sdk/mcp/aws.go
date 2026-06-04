// AWS S3 tools for the MCP server.
// Access requires AWS credentials via environment variables or an IAM role.
//
// Environment variables:
//
//	AWS_REGION                  AWS region (default: us-east-1)
//	AWS_ACCESS_KEY_ID           Access key ID (optional — falls back to IAM role)
//	AWS_SECRET_ACCESS_KEY       Secret access key (required if access key ID is set)
//	AWS_ENDPOINT_URL            Override endpoint URL (for MinIO, LocalStack, etc.)
//	MCP_S3_FORCE_PATH_STYLE     Use path-style addressing (required for MinIO)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const s3MaxReadBytes = 10 * 1024 * 1024 // 10 MB

// newS3Client builds an S3 client from environment variables.
// Returns (nil, nil) when no AWS configuration is detected, meaning S3 tools
// will be available but return a helpful "not configured" error at call time.
func newS3Client(ctx context.Context) (*s3.Client, error) {
	region := getEnvOrDefault("AWS_REGION", "us-east-1")
	endpoint := os.Getenv("AWS_ENDPOINT_URL")
	accessKeyID := os.Getenv("AWS_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	forcePathStyle := os.Getenv("MCP_S3_FORCE_PATH_STYLE") == "true"

	// No AWS config detected — treat as disabled.
	if os.Getenv("AWS_REGION") == "" && endpoint == "" && accessKeyID == "" {
		return nil, nil
	}

	var opts []func(*awsconfig.LoadOptions) error
	opts = append(opts, awsconfig.WithRegion(region))
	if accessKeyID != "" && secretAccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	var s3Opts []func(*s3.Options)
	if endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint) })
	}
	if forcePathStyle {
		s3Opts = append(s3Opts, func(o *s3.Options) { o.UsePathStyle = true })
	}

	return s3.NewFromConfig(awsCfg, s3Opts...), nil
}

var errS3Disabled = errors.New("S3 is not configured — set AWS_REGION (and optionally " +
	"AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_ENDPOINT_URL)")

func (s *server) s3OrErr() (*s3.Client, error) {
	if s.s3Client == nil {
		return nil, errS3Disabled
	}
	return s.s3Client, nil
}

// ── Tool handlers ─────────────────────────────────────────────────────────────

func (s *server) toolS3ListBuckets(ctx context.Context) (mcpToolResult, error) {
	client, err := s.s3OrErr()
	if err != nil {
		return mcpToolResult{}, err
	}

	out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("ListBuckets: %w", err)
	}

	if len(out.Buckets) == 0 {
		return textResult("No buckets found."), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d buckets:\n", len(out.Buckets))
	for _, b := range out.Buckets {
		created := ""
		if b.CreationDate != nil {
			created = b.CreationDate.Format("2006-01-02")
		}
		fmt.Fprintf(&sb, "  %s  created=%s\n", aws.ToString(b.Name), created)
	}
	return textResult(sb.String()), nil
}

func (s *server) toolS3ListObjects(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	client, err := s.s3OrErr()
	if err != nil {
		return mcpToolResult{}, err
	}

	bucket := args["bucket"]
	if bucket == "" {
		return mcpToolResult{}, fmt.Errorf("bucket is required")
	}

	maxKeys := int32(100)
	if v := args["max_keys"]; v != "" {
		var n int32
		fmt.Sscanf(v, "%d", &n)
		if n > 0 && n <= 1000 {
			maxKeys = n
		}
	}

	input := &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucket),
		MaxKeys: aws.Int32(maxKeys),
	}
	if prefix := args["prefix"]; prefix != "" {
		input.Prefix = aws.String(prefix)
	}

	out, err := client.ListObjectsV2(ctx, input)
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("ListObjectsV2 s3://%s: %w", bucket, err)
	}

	if len(out.Contents) == 0 {
		return textResult(fmt.Sprintf("No objects found in s3://%s (prefix=%q).", bucket, args["prefix"])), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d objects in s3://%s/%s:\n", len(out.Contents), bucket, args["prefix"])
	for _, obj := range out.Contents {
		size := int64(0)
		if obj.Size != nil {
			size = *obj.Size
		}
		modified := ""
		if obj.LastModified != nil {
			modified = obj.LastModified.Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(&sb, "  %s  size=%d  modified=%s\n", aws.ToString(obj.Key), size, modified)
	}
	if aws.ToBool(out.IsTruncated) {
		fmt.Fprintf(&sb, "  ... (results truncated; use a more specific prefix or increase max_keys)\n")
	}
	return textResult(sb.String()), nil
}

func (s *server) toolS3GetObject(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	client, err := s.s3OrErr()
	if err != nil {
		return mcpToolResult{}, err
	}

	bucket, key := args["bucket"], args["key"]
	if bucket == "" || key == "" {
		return mcpToolResult{}, fmt.Errorf("bucket and key are required")
	}

	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var noKey *s3types.NoSuchKey
		if errors.As(err, &noKey) {
			return mcpToolResult{}, fmt.Errorf("object s3://%s/%s not found", bucket, key)
		}
		return mcpToolResult{}, fmt.Errorf("GetObject s3://%s/%s: %w", bucket, key, err)
	}
	defer out.Body.Close()

	limited := io.LimitReader(out.Body, s3MaxReadBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("read s3://%s/%s: %w", bucket, key, err)
	}
	if int64(len(data)) > s3MaxReadBytes {
		return mcpToolResult{}, fmt.Errorf("object s3://%s/%s exceeds the 10 MB read limit", bucket, key)
	}

	return textResult(string(data)), nil
}

func (s *server) toolS3PutObject(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	client, err := s.s3OrErr()
	if err != nil {
		return mcpToolResult{}, err
	}

	bucket, key, content := args["bucket"], args["key"], args["content"]
	if bucket == "" || key == "" {
		return mcpToolResult{}, fmt.Errorf("bucket and key are required")
	}

	contentType := args["content_type"]
	if contentType == "" {
		contentType = "text/plain; charset=utf-8"
	}

	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          strings.NewReader(content),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(int64(len(content))),
	}); err != nil {
		return mcpToolResult{}, fmt.Errorf("PutObject s3://%s/%s: %w", bucket, key, err)
	}

	return textResult(fmt.Sprintf("Written %d bytes to s3://%s/%s", len(content), bucket, key)), nil
}

func (s *server) toolS3DeleteObject(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	client, err := s.s3OrErr()
	if err != nil {
		return mcpToolResult{}, err
	}

	bucket, key := args["bucket"], args["key"]
	if bucket == "" || key == "" {
		return mcpToolResult{}, fmt.Errorf("bucket and key are required")
	}

	if _, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}); err != nil {
		return mcpToolResult{}, fmt.Errorf("DeleteObject s3://%s/%s: %w", bucket, key, err)
	}

	return textResult(fmt.Sprintf("Deleted s3://%s/%s", bucket, key)), nil
}

func (s *server) toolS3HeadObject(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	client, err := s.s3OrErr()
	if err != nil {
		return mcpToolResult{}, err
	}

	bucket, key := args["bucket"], args["key"]
	if bucket == "" || key == "" {
		return mcpToolResult{}, fmt.Errorf("bucket and key are required")
	}

	out, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("HeadObject s3://%s/%s: %w", bucket, key, err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "s3://%s/%s\n", bucket, key)
	if out.ContentLength != nil {
		fmt.Fprintf(&sb, "  size:          %d bytes\n", *out.ContentLength)
	}
	if out.ContentType != nil {
		fmt.Fprintf(&sb, "  content-type:  %s\n", *out.ContentType)
	}
	if out.ETag != nil {
		fmt.Fprintf(&sb, "  etag:          %s\n", *out.ETag)
	}
	if out.LastModified != nil {
		fmt.Fprintf(&sb, "  last-modified: %s\n", out.LastModified.Format("2006-01-02 15:04:05 UTC"))
	}
	if out.StorageClass != "" {
		fmt.Fprintf(&sb, "  storage-class: %s\n", string(out.StorageClass))
	}

	return textResult(sb.String()), nil
}
