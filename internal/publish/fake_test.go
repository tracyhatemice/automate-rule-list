package publish

import (
	"context"
	"errors"
	"testing"
)

func TestFake(t *testing.T) {
	f := &Fake{}
	uri, err := f.Put(context.Background(), Object{Bucket: "b", Key: "dir/k.txt", Body: []byte("x"), ContentType: "text/plain"})
	if err != nil || uri != "s3://b/dir/k.txt" {
		t.Fatalf("Put = %q %v", uri, err)
	}
	if got := string(f.Objects["b/dir/k.txt"]); got != "x" {
		t.Fatalf("stored = %q", got)
	}
	f.Fail = errors.New("nope")
	if _, err := f.Put(context.Background(), Object{Bucket: "b", Key: "k"}); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := (&Fake{}).Put(context.Background(), Object{Key: "k"}); err == nil {
		t.Fatal("missing bucket should error")
	}
}
