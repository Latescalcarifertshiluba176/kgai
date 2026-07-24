package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	neturl "net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// newS3Remote parses s3://bucket[/prefix][?profile=NAME&region=REGION] and returns a
// segment-protocol remote over that bucket. Credentials/region resolve the standard AWS
// way (env vars, shared config/profile, IMDS); any S3-compatible service works via
// AWS_ENDPOINT_URL. The optional ?profile= selects a named shared-config profile for THIS
// remote — including an SSO profile (run `aws sso login --profile NAME` first) — so the
// choice is pinned in the store instead of relying on a global AWS_PROFILE.
func newS3Remote(url string) (Remote, error) {
	rest := strings.TrimPrefix(url, "s3://")
	var profile, region string
	if base, query, ok := strings.Cut(rest, "?"); ok {
		rest = base
		if vals, err := neturl.ParseQuery(query); err == nil {
			profile = vals.Get("profile")
			region = vals.Get("region")
		}
	}
	bucket, prefix, _ := strings.Cut(rest, "/")
	if bucket == "" {
		return nil, fmt.Errorf("invalid S3 remote %q (want s3://bucket[/prefix][?profile=NAME&region=REGION])", url)
	}
	client, err := newS3Client(profile, region)
	if err != nil {
		return nil, err
	}
	return &objectRemote{
		os:     &s3Store{client: client, bucket: bucket},
		prefix: strings.Trim(prefix, "/"),
		name:   url,
	}, nil
}

// newS3Client builds an S3 client. A non-empty profile selects a named shared-config
// profile (env AWS_PROFILE still applies when profile is empty); a non-empty region
// overrides the profile/env region.
func newS3Client(profile, region string) (*s3.Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	opts := []func(*awsconfig.LoadOptions) error{}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1" // harmless default; buckets redirect as needed
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		// Custom endpoints (MinIO, R2, LocalStack) usually need path-style addressing.
		if os.Getenv("AWS_ENDPOINT_URL") != "" || os.Getenv("AWS_ENDPOINT_URL_S3") != "" {
			o.UsePathStyle = true
		}
	}), nil
}

// s3Store adapts the AWS S3 client to the ObjectStore interface.
type s3Store struct {
	client *s3.Client
	bucket string
}

func (s *s3Store) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

func (s *s3Store) List(prefix string) ([]string, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	var keys []string
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(strings.Trim(prefix, "/")),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range page.Contents {
			if o.Key != nil {
				keys = append(keys, *o.Key)
			}
		}
	}
	return keys, nil
}

func (s *s3Store) Get(key string) ([]byte, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

func (s *s3Store) Put(key string, data []byte) error {
	ctx, cancel := s.ctx()
	defer cancel()
	// Segments are write-once: If-None-Match:* makes S3 reject an overwrite, which
	// would only happen on a reused installID racing itself.
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        strings.NewReader(string(data)),
		IfNoneMatch: aws.String("*"),
	})
	if err != nil && isPreconditionFailed(err) {
		return fmt.Errorf("segment %s already exists on the remote (concurrent push from a duplicated install?)", key)
	}
	return err
}

func isPreconditionFailed(err error) bool {
	var ae interface{ ErrorCode() string }
	if errors.As(err, &ae) {
		code := ae.ErrorCode()
		return code == "PreconditionFailed" || code == "ConditionalRequestConflict"
	}
	return false
}
