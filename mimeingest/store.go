package mimeingest

import (
	"context"
	"io"
)

// Attachment describes a leaf MIME part treated as an attachment.
type Attachment struct {
	Filename    string
	ContentType string
	// Size is the decoded size in bytes. It is zero when the object is
	// staged and filled in once the part has been fully decoded.
	Size int64
}

// Object is a staged, not yet durable, storage object.
type Object interface {
	io.Writer
	// Publish makes the staged object durable. It is called only after
	// the whole message has been validated and all scans are clean.
	Publish(ctx context.Context) error
	// Withdraw discards the staged object. It must be safe to call on
	// any object that has not been published.
	Withdraw(ctx context.Context) error
}

// Store stages attachment payloads before they are committed.
type Store interface {
	Stage(ctx context.Context, meta Attachment) (Object, error)
}
