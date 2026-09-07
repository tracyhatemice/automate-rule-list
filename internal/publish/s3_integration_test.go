//go:build integration

package publish

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// TestS3Integration needs a running S3-compatible store; see
// `make integration`, which starts MinIO and sets these variables.
func TestS3Integration(t *testing.T) {
	endpoint := os.Getenv("RULEGEN_TEST_S3_ENDPOINT")
	bucket := os.Getenv("RULEGEN_TEST_S3_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("RULEGEN_TEST_S3_ENDPOINT / RULEGEN_TEST_S3_BUCKET not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := NewS3(ctx, S3Options{Region: os.Getenv("AWS_REGION"), Endpoint: endpoint, ForcePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("# last updated: now\naddress /a.com/#\n")
	uri, err := p.Put(ctx, Object{Bucket: bucket, Key: "it/adblock.conf", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if uri != "s3://"+bucket+"/it/adblock.conf" {
		t.Fatalf("uri = %q", uri)
	}
	out, err := p.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String("it/adblock.conf")})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Body.Close()
	got, _ := io.ReadAll(out.Body)
	if string(got) != string(body) {
		t.Fatalf("body = %q", got)
	}
	if ct := aws.ToString(out.ContentType); ct != "text/plain; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
}
