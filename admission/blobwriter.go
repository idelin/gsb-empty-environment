package admission

import (
	"bytes"
	"context"
	"hash"
)

// BlobWriter accumulates a ByteStream upload. It is a plain io.Writer with
// an explicit Commit so it can be driven directly from a gRPC streaming
// handler: feed each WriteRequest payload to Write, then call Commit on
// WriteCompleted. Nothing touches the store before a successful Commit.
type BlobWriter struct {
	guard        *Guard
	ctx          context.Context
	resourceName string
	digest       Digest
	hasher       hash.Hash
	buf          bytes.Buffer
	committed    bool
}

// Write buffers and hashes a chunk. Returning an error here also lets the
// gRPC layer abort an oversized upload early.
func (w *BlobWriter) Write(p []byte) (int, error) {
	if w.committed {
		return 0, fail("bytestream.write", ErrInvalidDigest, "writer already committed")
	}
	if w.digest.Size >= 0 && int64(w.buf.Len()+len(p)) > w.digest.Size {
		return 0, fail("bytestream.write", ErrSizeMismatch,
			"upload exceeds declared size %d", w.digest.Size)
	}
	w.hasher.Write(p)
	return w.buf.Write(p)
}

// Commit verifies the full upload against the resource-name digest and, on
// success, persists it. A failed commit is logged and leaves the store
// untouched.
func (w *BlobWriter) Commit() (Digest, error) {
	const op = "bytestream.write"
	if w.committed {
		return w.digest, fail(op, ErrInvalidDigest, "writer already committed")
	}
	w.committed = true

	data := w.buf.Bytes()
	if err := VerifyBlob(w.digest, data); err != nil {
		w.guard.reject(op, IdentityFromContext(w.ctx), w.digest, w.resourceName, err)
		return w.digest, err
	}
	if err := w.guard.Store.PutBlob(w.ctx, w.digest, data); err != nil {
		return w.digest, fail(op, ErrInternal, "store put failed: %v", err)
	}
	return w.digest, nil
}

// Digest returns the digest claimed by the upload resource name.
func (w *BlobWriter) Digest() Digest { return w.digest }
