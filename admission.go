// Package netsec provides a deduplication admission layer for a multi-tenant
// storage host.
//
// Physical blocks may be shared between tenants, but a tenant only receives a
// reference to a block after it has streamed the complete object to the host
// and the host has verified the object's digest. Existence checks are answered
// exclusively from the calling tenant's own verified index, so the presence
// of a block owned by another tenant can never be observed through this API.
package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Reference is an opaque handle to a block held by a BlockStore.
type Reference string

// Digest is the lowercase hex-encoded SHA-256 digest of a block.
type Digest string

// SHA256Hex returns the lowercase hex SHA-256 digest of data.
func SHA256Hex(data []byte) Digest {
	sum := sha256.Sum256(data)
	return Digest(hex.EncodeToString(sum[:]))
}

// Valid reports whether d is a well-formed SHA-256 hex digest.
func (d Digest) Valid() bool {
	raw, err := hex.DecodeString(string(d))
	return err == nil && len(raw) == sha256.Size
}

var (
	// ErrEmptyTenant is returned when an operation is requested with an empty
	// tenant identifier.
	ErrEmptyTenant = errors.New("netsec: empty tenant identifier")
	// ErrInvalidDigest is returned when a digest is not a valid SHA-256 hex
	// string.
	ErrInvalidDigest = errors.New("netsec: invalid digest")
	// ErrDigestMismatch is returned when the bytes received do not hash to
	// the digest declared by the tenant.
	ErrDigestMismatch = errors.New("netsec: uploaded content does not match declared digest")
	// ErrNotFound is returned when the tenant has no verified reference for
	// the requested digest.
	ErrNotFound = errors.New("netsec: no verified block for this tenant")
)

// BlockStore is the physical storage backend behind the admission layer.
//
// Implementations are free to deduplicate blocks by digest: uploading the
// same content twice may return the same Reference and persist the bytes only
// once, as long as reference counting keeps the block alive until every
// acquired reference has been released. The admission layer never asks the
// store whether a block exists, so a store implementation cannot create a
// cross-tenant existence oracle on its own.
type BlockStore interface {
	// Put stores data and returns a reference. If the digest is already
	// stored, the implementation may share the existing block instead of
	// writing the bytes again, but must account for the additional reference.
	Put(ctx context.Context, digest Digest, data []byte) (Reference, error)
	// Open opens a previously acquired reference for reading.
	Open(ctx context.Context, ref Reference) (io.ReadCloser, error)
	// Release decrements the reference count of a reference and may reclaim
	// the underlying block once it is unreferenced.
	Release(ctx context.Context, ref Reference) error
}

// UploadResult is the outcome of a fully verified upload.
type UploadResult struct {
	// Reference is the handle granted to the tenant after verification.
	Reference Reference
	// Digest is the verified digest of the stored block.
	Digest Digest
	// Received is the number of bytes read from the upload stream. It always
	// equals the object size on success, including when the block is
	// physically shared with another tenant. On a failed stream it reports
	// how many bytes had been received before the failure.
	Received int64
}

// AdmissionController gates tenant access to a shared, deduplicating
// BlockStore. The zero value is not usable; construct one with
// NewAdmissionController.
type AdmissionController struct {
	store BlockStore

	mu      sync.RWMutex
	tenants map[string]map[Digest]Reference
}

// NewAdmissionController builds an admission controller backed by store.
func NewAdmissionController(store BlockStore) *AdmissionController {
	return &AdmissionController{
		store:   store,
		tenants: make(map[string]map[Digest]Reference),
	}
}

// Upload consumes the entire request body and only grants a reference after
// the received bytes hash to want.
//
// The full body is always read, even when the same digest is already stored
// physically for another tenant (or for this same tenant): physical block
// reuse never reduces the bytes a tenant must upload. A failed stream or a
// digest mismatch grants no reference and creates neither a tenant index
// entry nor a physical block.
//
// A production host may spool the incoming bytes to temporary storage instead
// of buffering them in memory; the admission guarantees are unchanged as long
// as verification completes before any reference is granted.
func (c *AdmissionController) Upload(ctx context.Context, tenant string, want Digest, body io.Reader) (UploadResult, error) {
	if tenant == "" {
		return UploadResult{}, ErrEmptyTenant
	}
	if !want.Valid() {
		return UploadResult{}, ErrInvalidDigest
	}
	if body == nil {
		return UploadResult{}, errors.New("netsec: nil upload body")
	}
	if err := ctx.Err(); err != nil {
		return UploadResult{}, err
	}

	hasher := sha256.New()
	// Read every byte before touching the store. The counting writer tracks
	// bytes actually pulled from the stream (rather than buffer capacity,
	// which io.ReadAll may over-report when the stream fails).
	counter := &countingWriter{}
	data, err := io.ReadAll(io.TeeReader(body, io.MultiWriter(hasher, counter)))
	received := counter.n
	if err != nil {
		return UploadResult{Received: received}, fmt.Errorf("netsec: upload stream failed: %w", err)
	}
	if got := Digest(hex.EncodeToString(hasher.Sum(nil))); got != want {
		return UploadResult{Received: received}, ErrDigestMismatch
	}

	ref, err := c.store.Put(ctx, want, data)
	if err != nil {
		return UploadResult{Received: received}, err
	}

	// Commit to this tenant's verified index. If a concurrent upload for the
	// same tenant and digest won the race, release the extra store reference
	// so the refcount stays balanced and hand back the existing reference.
	c.mu.Lock()
	index := c.tenants[tenant]
	if index == nil {
		index = make(map[Digest]Reference)
		c.tenants[tenant] = index
	}
	if existing, ok := index[want]; ok {
		c.mu.Unlock()
		if releaseErr := c.store.Release(ctx, ref); releaseErr != nil {
			return UploadResult{Received: received}, releaseErr
		}
		return UploadResult{Reference: existing, Digest: want, Received: received}, nil
	}
	index[want] = ref
	c.mu.Unlock()

	return UploadResult{Reference: ref, Digest: want, Received: received}, nil
}

type countingWriter struct {
	n int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

// Exists reports whether the tenant itself has a verified reference for want.
// The answer derives solely from the tenant's own index: blocks stored on
// behalf of other tenants are never visible here, including while another
// tenant's upload is still in flight. Invalid inputs report false rather than
// erroring, so the probe itself cannot become an oracle.
func (c *AdmissionController) Exists(tenant string, want Digest) bool {
	if tenant == "" || !want.Valid() {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	index, ok := c.tenants[tenant]
	if !ok {
		return false
	}
	_, ok = index[want]
	return ok
}

// Open returns a reader for the block identified by want, but only if the
// tenant has previously verified and holds a reference for it.
func (c *AdmissionController) Open(ctx context.Context, tenant string, want Digest) (io.ReadCloser, error) {
	if tenant == "" {
		return nil, ErrEmptyTenant
	}
	if !want.Valid() {
		return nil, ErrInvalidDigest
	}
	c.mu.RLock()
	index, ok := c.tenants[tenant]
	var ref Reference
	if ok {
		ref = index[want]
	}
	c.mu.RUnlock()
	if !ok || ref == "" {
		return nil, ErrNotFound
	}
	return c.store.Open(ctx, ref)
}

// Delete removes the tenant's verified reference for want and releases it in
// the physical store. Other tenants sharing the underlying block keep their
// access. It returns ErrNotFound if the tenant has no such reference.
func (c *AdmissionController) Delete(ctx context.Context, tenant string, want Digest) error {
	if tenant == "" {
		return ErrEmptyTenant
	}
	if !want.Valid() {
		return ErrInvalidDigest
	}
	c.mu.Lock()
	index, ok := c.tenants[tenant]
	var ref Reference
	if ok {
		ref = index[want]
		delete(index, want)
	}
	c.mu.Unlock()
	if !ok || ref == "" {
		return ErrNotFound
	}
	return c.store.Release(ctx, ref)
}
