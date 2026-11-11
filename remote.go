package cdcupload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ChunkEntry is one element of an upload manifest.
type ChunkEntry struct {
	Index  int64  `json:"index"`
	Offset int64  `json:"offset"`
	Length int    `json:"length"`
	Digest string `json:"digest"` // hex-encoded SHA-256 of the chunk
}

// Manifest is the committed description of one uploaded file. A manifest only
// becomes visible after every referenced chunk exists, and it is published
// with an atomic rename, so readers never observe a partial upload.
type Manifest struct {
	FileID    string       `json:"file_id"`
	FileName  string       `json:"file_name"`
	Size      int64        `json:"size"`
	FileHash  string       `json:"file_hash"` // hex-encoded SHA-256 of the whole file
	ChunkSize int          `json:"chunk_size"`
	Chunks    []ChunkEntry `json:"chunks"`
}

// Validate checks the manifest for internal consistency.
func (m *Manifest) Validate() error {
	if len(m.Chunks) != m.ChunkSize {
		return fmt.Errorf("%w: chunk count mismatch", ErrSessionCorrupt)
	}
	var offset int64
	for i, ch := range m.Chunks {
		if ch.Index != int64(i) || ch.Offset != offset || ch.Length <= 0 {
			return fmt.Errorf("%w: chunk entry %d malformed", ErrSessionCorrupt, i)
		}
		if len(ch.Digest) != sha256.Size*2 {
			return fmt.Errorf("%w: bad digest length", ErrSessionCorrupt)
		}
		offset += int64(ch.Length)
	}
	if offset != m.Size {
		return fmt.Errorf("%w: total chunk size != file size", ErrSessionCorrupt)
	}
	return nil
}

// Uploader is the transport-facing interface. All methods must be safe for
// concurrent use. PutChunk and CommitManifest must be idempotent: resuming an
// interrupted upload re-issues calls whose result was never observed.
type Uploader interface {
	// HasChunk reports whether a chunk with the given hex digest is stored.
	HasChunk(ctx context.Context, digest string) (bool, error)
	// PutChunk uploads one chunk, addressed by its hex digest.
	PutChunk(ctx context.Context, digest string, data []byte) error
	// CommitManifest publishes the manifest atomically, after all chunks.
	CommitManifest(ctx context.Context, m *Manifest) error
}

// RemoteStore is a filesystem-backed implementation of Uploader that mimics a
// content-addressed object store: blobs live under blobs/<digest> and
// manifests under manifests/<fileid>.json, each written via a temp file plus
// an atomic rename.
type RemoteStore struct {
	root string
	mu   sync.Mutex

	putCalls    int
	commitCalls int
}

// NewRemoteStore opens (creating if needed) a store rooted at root.
func NewRemoteStore(root string) (*RemoteStore, error) {
	if err := os.MkdirAll(filepath.Join(root, "blobs"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "manifests"), 0o755); err != nil {
		return nil, err
	}
	return &RemoteStore{root: root}, nil
}

func (s *RemoteStore) blobPath(digest string) string {
	return filepath.Join(s.root, "blobs", digest)
}

func (s *RemoteStore) manifestPath(fileID string) string {
	return filepath.Join(s.root, "manifests", fileID+".json")
}

// HasChunk implements Uploader.
func (s *RemoteStore) HasChunk(_ context.Context, digest string) (bool, error) {
	if _, err := os.Stat(s.blobPath(digest)); err == nil {
		return true, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else {
		return false, err
	}
}

// PutChunk implements Uploader. It is idempotent and content-checked: an
// existing blob is only skipped when its stored content matches the digest.
func (s *RemoteStore) PutChunk(_ context.Context, digest string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.blobPath(digest)
	if _, err := os.Stat(path); err == nil {
		stored, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Equal(stored, data) {
			return nil // already uploaded, resume after a lost acknowledgement
		}
		return fmt.Errorf("%w: digest %s exists with different content", ErrIntegrity, digest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != digest {
		return fmt.Errorf("%w: digest does not match chunk data", ErrIntegrity)
	}
	if err := atomicWriteFile(path, data, 0o644); err != nil {
		return err
	}
	s.putCalls++
	return nil
}

// CommitManifest implements Uploader. The manifest is validated and written
// atomically; concurrent or retried commits of the same content are no-ops.
func (s *RemoteStore) CommitManifest(_ context.Context, m *Manifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := m.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := s.manifestPath(m.FileID)
	if existing, err := os.ReadFile(path); err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return fmt.Errorf("%w: manifest %s already committed with different content",
			ErrIntegrity, m.FileID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := atomicWriteFile(path, data, 0o644); err != nil {
		return err
	}
	s.commitCalls++
	return nil
}

// ReadManifest reads back a committed manifest.
func (s *RemoteStore) ReadManifest(fileID string) (*Manifest, error) {
	data, err := os.ReadFile(s.manifestPath(fileID))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// ReadChunk returns the raw content of a stored chunk.
func (s *RemoteStore) ReadChunk(digest string) ([]byte, error) {
	return os.ReadFile(s.blobPath(digest))
}

// Stats reports transport call counts, useful in tests to prove dedup.
func (s *RemoteStore) Stats() (putCalls, commitCalls int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putCalls, s.commitCalls
}

// atomicWriteFile writes data to a temp file in the same directory, fsyncs it
// and renames it into place, so the destination is never partially visible.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
