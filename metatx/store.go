package metatx

import "io"

// Staged is an opaque handle to content staged in the publish area but not
// yet committed.
type Staged interface{}

// Store is the publish-area backend. Commit must atomically replace any
// existing version of a name with the staged content and the encoded
// attribute set, so readers never observe a partial update.
type Store interface {
	// Stage writes content to a temporary location and returns a handle.
	Stage(r io.Reader) (Staged, error)
	// Commit atomically publishes the staged content and the encoded
	// attributes under name.
	Commit(name string, staged Staged, attrs []byte) error
	// Abort discards staged content that will not be committed.
	Abort(staged Staged) error
}

// Content is a source object in the upload area. Open must return a fresh
// reader on every call; the publisher reads the content twice to detect
// mid-transaction modification.
type Content interface {
	Open() (io.ReadCloser, error)
}
