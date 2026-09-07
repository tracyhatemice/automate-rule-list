package publish

import (
	"context"
	"errors"
	"sync"
)

// Fake is an in-memory Publisher for tests.
type Fake struct {
	mu      sync.Mutex
	Objects map[string][]byte // keyed by "bucket/key"
	Fail    error             // returned by Put when set
}

// Put stores the object body.
func (f *Fake) Put(_ context.Context, o Object) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return "", f.Fail
	}
	if o.Bucket == "" {
		return "", errors.New("bucket is required")
	}
	if f.Objects == nil {
		f.Objects = map[string][]byte{}
	}
	f.Objects[o.Bucket+"/"+o.Key] = append([]byte(nil), o.Body...)
	return "s3://" + o.Bucket + "/" + o.Key, nil
}
