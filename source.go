package netsec

import (
	"context"
	"fmt"
	"sync"
)

// BlobSource fetches content addressed by digest. Implementations must return
// the exact bytes stored under that digest; the resolver verifies them anyway.
type BlobSource interface {
	FetchBlob(ctx context.Context, digest string) ([]byte, error)
}

// BlobSourceFunc adapts a function to BlobSource.
type BlobSourceFunc func(ctx context.Context, digest string) ([]byte, error)

// FetchBlob calls the adapted function.
func (function BlobSourceFunc) FetchBlob(ctx context.Context, digest string) ([]byte, error) {
	return function(ctx, digest)
}

// TagSource maps mutable references to immutable content digests. The resolver
// consults it only for the root of a new resolution; it never caches tag state.
type TagSource interface {
	ResolveTag(ctx context.Context, reference string) (string, error)
}

// TagSourceFunc adapts a function to TagSource.
type TagSourceFunc func(ctx context.Context, reference string) (string, error)

// ResolveTag calls the adapted function.
func (function TagSourceFunc) ResolveTag(ctx context.Context, reference string) (string, error) {
	return function(ctx, reference)
}

// MemorySource is a concurrency-safe in-memory BlobSource and TagSource.
type MemorySource struct {
	mutex sync.RWMutex
	blobs map[string][]byte
	tags  map[string]string
}

// NewMemorySource returns an empty in-memory content source.
func NewMemorySource() *MemorySource {
	return &MemorySource{
		blobs: make(map[string][]byte),
		tags:  make(map[string]string),
	}
}

// PutBlob stores content under a digest. The digest syntax is validated, but
// content is intentionally not hashed here so tests can simulate corruption.
func (source *MemorySource) PutBlob(digest string, content []byte) error {
	canonical, parseErr := ParseDigest(digest)
	if parseErr != nil {
		return parseErr
	}

	source.mutex.Lock()
	defer source.mutex.Unlock()
	source.blobs[canonical.String()] = append([]byte(nil), content...)
	return nil
}

// PutBlobContent hashes and stores content, returning its canonical digest.
func (source *MemorySource) PutBlobContent(algorithm string, content []byte) (Digest, error) {
	digest, computeErr := ComputeDigest(algorithm, content)
	if computeErr != nil {
		return "", computeErr
	}
	if putErr := source.PutBlob(digest.String(), content); putErr != nil {
		return "", putErr
	}
	return digest, nil
}

// DeleteBlob removes content, allowing tests to model a missing layer.
func (source *MemorySource) DeleteBlob(digest string) {
	canonical, parseErr := ParseDigest(digest)
	if parseErr != nil {
		return
	}

	source.mutex.Lock()
	defer source.mutex.Unlock()
	delete(source.blobs, canonical.String())
}

// PutTag points a mutable reference at a digest.
func (source *MemorySource) PutTag(reference string, digest string) error {
	if reference == "" {
		return fmt.Errorf("%w: empty tag", ErrInvalidReference)
	}
	canonical, parseErr := ParseDigest(digest)
	if parseErr != nil {
		return parseErr
	}

	source.mutex.Lock()
	defer source.mutex.Unlock()
	source.tags[reference] = canonical.String()
	return nil
}

// DeleteTag removes a mutable reference.
func (source *MemorySource) DeleteTag(reference string) {
	source.mutex.Lock()
	defer source.mutex.Unlock()
	delete(source.tags, reference)
}

// FetchBlob implements BlobSource.
func (source *MemorySource) FetchBlob(ctx context.Context, digest string) ([]byte, error) {
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}

	canonical, parseErr := ParseDigest(digest)
	if parseErr != nil {
		return nil, parseErr
	}

	source.mutex.RLock()
	content, found := source.blobs[canonical.String()]
	source.mutex.RUnlock()
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrBlobNotFound, canonical)
	}
	return append([]byte(nil), content...), nil
}

// ResolveTag implements TagSource.
func (source *MemorySource) ResolveTag(ctx context.Context, reference string) (string, error) {
	if contextErr := ctx.Err(); contextErr != nil {
		return "", contextErr
	}

	source.mutex.RLock()
	digest, found := source.tags[reference]
	source.mutex.RUnlock()
	if !found {
		return "", fmt.Errorf("%w: tag %q", ErrTagResolutionFailed, reference)
	}
	return digest, nil
}
