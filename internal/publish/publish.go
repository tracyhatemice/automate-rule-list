// Package publish uploads rendered outputs to object storage.
package publish

import "context"

// Object is one upload.
type Object struct {
	Bucket      string
	Key         string
	Body        []byte
	ContentType string
}

// Publisher stores objects and returns a URI for logs and state.
type Publisher interface {
	Put(ctx context.Context, o Object) (uri string, err error)
}
