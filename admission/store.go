package admission

import (
	"bytes"
	"context"
	"sync"
)

// CASStore is the content-addressed storage backend behind the guard.
// Implementations wrap the real remote cache; the in-memory implementation
// below is suitable for tests and embedding examples.
type CASStore interface {
	// BlobStatus reports whether a digest is present and, when present,
	// whether the stored blob length matches digest.Size.
	BlobStatus(ctx context.Context, digest Digest) (exists bool, sizeOK bool, err error)
	// PutBlob stores an already-verified blob under its content digest.
	PutBlob(ctx context.Context, digest Digest, data []byte) error
}

// ActionCacheStore persists ActionResult values keyed by action digest.
// Lookup must return (nil, nil) when nothing is stored yet.
type ActionCacheStore interface {
	GetActionResult(ctx context.Context, actionDigest Digest) (*ActionResult, error)
	PutActionResult(ctx context.Context, actionDigest Digest, result *ActionResult) error
}

// Store is the full backend surface the guard requires.
type Store interface {
	CASStore
	ActionCacheStore
}

// MemoryStore is a goroutine-safe in-memory Store. It also doubles as the
// reference implementation showing what a real adapter must guarantee.
type MemoryStore struct {
	mu       sync.RWMutex
	blobs    map[Digest][]byte
	actions  map[Digest]*ActionResult
	failWith error
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		blobs:   map[Digest][]byte{},
		actions: map[Digest]*ActionResult{},
	}
}

// SetFault injects a backend fault for exercising internal-error paths.
func (s *MemoryStore) SetFault(err error) {
	s.mu.Lock()
	s.failWith = err
	s.mu.Unlock()
}

// BlobStatus implements CASStore.
func (s *MemoryStore) BlobStatus(_ context.Context, digest Digest) (bool, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.failWith != nil {
		return false, false, s.failWith
	}
	data, ok := s.blobs[digest]
	if !ok {
		return false, false, nil
	}
	return true, int64(len(data)) == digest.Size, nil
}

// PutBlob implements CASStore.
func (s *MemoryStore) PutBlob(_ context.Context, digest Digest, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failWith != nil {
		return s.failWith
	}
	buf := bytes.NewBuffer(make([]byte, 0, len(data)))
	buf.Write(data)
	s.blobs[digest] = buf.Bytes()
	return nil
}

// GetActionResult implements ActionCacheStore.
func (s *MemoryStore) GetActionResult(_ context.Context, actionDigest Digest) (*ActionResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.failWith != nil {
		return nil, s.failWith
	}
	result := s.actions[actionDigest]
	if result == nil {
		return nil, nil
	}
	return cloneActionResult(result), nil
}

// PutActionResult implements ActionCacheStore.
func (s *MemoryStore) PutActionResult(_ context.Context, actionDigest Digest, result *ActionResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failWith != nil {
		return s.failWith
	}
	s.actions[actionDigest] = cloneActionResult(result)
	return nil
}

// HasBlob is a test helper reporting raw blob presence.
func (s *MemoryStore) HasBlob(digest Digest) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.blobs[digest]
	return ok
}

func cloneActionResult(in *ActionResult) *ActionResult {
	out := &ActionResult{
		OutputFiles:       append([]OutputFile(nil), in.OutputFiles...),
		OutputDirectories: append([]OutputDirectory(nil), in.OutputDirectories...),
		ExitCode:          in.ExitCode,
	}
	if in.StdoutDigest != nil {
		d := *in.StdoutDigest
		out.StdoutDigest = &d
	}
	if in.StderrDigest != nil {
		d := *in.StderrDigest
		out.StderrDigest = &d
	}
	return out
}
