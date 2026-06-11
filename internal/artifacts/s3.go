package artifacts

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Store persists artifacts in an Amazon S3 or S3-compatible (e.g. MinIO) bucket.
type S3Store struct {
	client *s3.Client
	bucket string
	prefix string
}

// S3Config holds the parameters for creating an S3Store.
type S3Config struct {
	Bucket          string
	Region          string
	Prefix          string // optional key prefix, e.g. "artifacts/" — appended before each key
	Endpoint        string // custom endpoint for MinIO or other S3-compatible stores
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool // required for MinIO and most S3-compatible stores
}

// NewS3Store creates an S3Store. cfg.Bucket must be non-empty.
func NewS3Store(ctx context.Context, cfg S3Config) (*S3Store, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3: bucket name is required")
	}

	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("s3: load aws config: %w", err)
	}

	s3opts := []func(*s3.Options){}
	if cfg.Endpoint != "" {
		s3opts = append(s3opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		})
	}
	if cfg.ForcePathStyle {
		s3opts = append(s3opts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	return &S3Store{
		client: s3.NewFromConfig(awsCfg, s3opts...),
		bucket: cfg.Bucket,
		prefix: cfg.Prefix,
	}, nil
}

func (s *S3Store) objectKey(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + key
}

func (s *S3Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(s.objectKey(key)),
		Body:          r,
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(size),
	})
	return err
}

func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, fmt.Errorf("artifact not found in storage")
		}
		return nil, err
	}
	return out.Body, nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	return err
}
