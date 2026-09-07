package publish

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Options configures the S3 publisher. Credentials come from the SDK's
// default chain (environment, shared config, instance role).
type S3Options struct {
	Region         string
	Endpoint       string // custom endpoint for MinIO, R2, …
	ForcePathStyle bool
}

// S3 publishes to Amazon S3 or any S3-compatible store.
type S3 struct {
	client *s3.Client
}

// NewS3 builds a publisher from the default credential chain.
func NewS3(ctx context.Context, opt S3Options) (*S3, error) {
	var loadOpts []func(*awsconfig.LoadOptions) error
	if opt.Region != "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion(opt.Region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = opt.ForcePathStyle
		if opt.Endpoint != "" {
			o.BaseEndpoint = aws.String(opt.Endpoint)
		}
	})
	return &S3{client: client}, nil
}

// Put uploads the object with PutObject.
func (p *S3) Put(ctx context.Context, o Object) (string, error) {
	if o.Bucket == "" {
		return "", errors.New("bucket is required")
	}
	ct := o.ContentType
	if ct == "" {
		ct = "text/plain; charset=utf-8"
	}
	_, err := p.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(o.Bucket),
		Key:         aws.String(o.Key),
		Body:        bytes.NewReader(o.Body),
		ContentType: aws.String(ct),
	})
	if err != nil {
		return "", fmt.Errorf("s3 put %s/%s: %w", o.Bucket, o.Key, err)
	}
	return "s3://" + o.Bucket + "/" + o.Key, nil
}
