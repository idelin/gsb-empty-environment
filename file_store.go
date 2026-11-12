package netsec

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileBlobStore is a real on-disk BlobStore. Envelopes live at
// <root>/blobs/<objectID>/<digest> and are written atomically via a temp file
// plus rename, so a crash never leaves a partially written envelope visible.
type FileBlobStore struct {
	root string
	mu   sync.Mutex
}

// NewFileBlobStore creates (or opens) a blob directory under root.
func NewFileBlobStore(root string) (*FileBlobStore, error) {
	dir := filepath.Join(root, "blobs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("netsec: create blob dir: %w", err)
	}
	return &FileBlobStore{root: dir}, nil
}

func safeName(objectID string) (string, error) {
	if err := validateObjectID(objectID); err != nil {
		return "", err
	}
	return hex.EncodeToString([]byte(objectID)), nil
}

func (f *FileBlobStore) path(objectID string, digest []byte) (string, error) {
	name, err := safeName(objectID)
	if err != nil {
		return "", err
	}
	if len(digest) == 0 {
		return "", ErrTampered
	}
	return filepath.Join(f.root, name, hex.EncodeToString(digest)), nil
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("netsec: create blob parent: %w", err)
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("netsec: temp suffix: %w", err)
	}
	tmp := path + ".tmp-" + hex.EncodeToString(suffix)
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("netsec: write temp blob: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("netsec: commit blob: %w", err)
	}
	return nil
}

// Put stores an envelope. Re-storing identical content at the same digest is
// idempotent; different content at the same digest path conflicts.
func (f *FileBlobStore) Put(ctx context.Context, objectID string, digest, envelope []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := f.path(objectID, digest)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		if !bytes.Equal(existing, envelope) {
			return fmt.Errorf("%w: digest key already holds different envelope", ErrConflict)
		}
		return nil
	case errors.Is(err, os.ErrNotExist):
		return writeAtomic(path, envelope)
	default:
		return fmt.Errorf("netsec: read existing blob: %w", err)
	}
}

func (f *FileBlobStore) Get(ctx context.Context, objectID string, digest []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := f.path(objectID, digest)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: blob %x", ErrNotFound, digest)
	}
	if err != nil {
		return nil, fmt.Errorf("netsec: read blob: %w", err)
	}
	return raw, nil
}

func (f *FileBlobStore) Has(ctx context.Context, objectID string, digest []byte) (bool, error) {
	path, err := f.path(objectID, digest)
	if err != nil {
		return false, err
	}
	switch _, err := os.Stat(path); {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("netsec: stat blob: %w", err)
	}
}

// persistedPointer is the on-disk JSON form of a Pointer.
type persistedPointer struct {
	Version uint64 `json:"version"`
	Digest  string `json:"digest"`
}

// FileMetadataStore persists current pointers as JSON files at
// <root>/meta/<objectID>.json. Writes are atomic (temp file + rename), and
// compare-and-swap is enforced across processes by re-reading the file under a
// per-object lock. It also supports fault injection to simulate a crash at
// defined stages of a commit, which is how the interruptible-metadata tests
// exercise recovery.
type FileMetadataStore struct {
	root  string
	mu    sync.Mutex
	locks map[string]*sync.Mutex

	// FaultHook, if non-nil, is invoked at each FaultStage. Returning an error
	// aborts the commit before the rename; returning nil proceeds normally.
	FaultHook func(stage FaultStage, objectID string) error
}

// FaultStage identifies a point in the metadata commit sequence.
type FaultStage string

const (
	// StageBeforeWrite fires before the temp pointer file is written.
	StageBeforeWrite FaultStage = "before_write"
	// StageAfterTempWrite fires after the temp file is durable but before rename.
	StageAfterTempWrite FaultStage = "after_temp_write"
	// StageAfterRename fires after the pointer has been atomically moved.
	StageAfterRename FaultStage = "after_rename"
)

// NewFileMetadataStore creates (or re-opens) a metadata directory under root.
func NewFileMetadataStore(root string) (*FileMetadataStore, error) {
	dir := filepath.Join(root, "meta")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("netsec: create meta dir: %w", err)
	}
	return &FileMetadataStore{root: dir, locks: make(map[string]*sync.Mutex)}, nil
}

func (m *FileMetadataStore) lockFor(objectID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, ok := m.locks[objectID]
	if !ok {
		lock = &sync.Mutex{}
		m.locks[objectID] = lock
	}
	return lock
}

func (m *FileMetadataStore) path(objectID string) (string, error) {
	name, err := safeName(objectID)
	if err != nil {
		return "", err
	}
	return filepath.Join(m.root, name+".json"), nil
}

func (m *FileMetadataStore) readLocked(objectID string) (*Pointer, error) {
	path, err := m.path(objectID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("netsec: read pointer: %w", err)
	}
	var persisted persistedPointer
	if err := json.Unmarshal(raw, &persisted); err != nil {
		return nil, fmt.Errorf("%w: pointer file unreadable: %v", ErrTampered, err)
	}
	digest, err := hex.DecodeString(persisted.Digest)
	if err != nil || len(digest) == 0 {
		return nil, fmt.Errorf("%w: pointer digest malformed", ErrTampered)
	}
	return &Pointer{Version: persisted.Version, Digest: digest}, nil
}

func (m *FileMetadataStore) Get(ctx context.Context, objectID string) (*Pointer, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	lock := m.lockFor(objectID)
	lock.Lock()
	defer lock.Unlock()
	pointer, err := m.readLocked(objectID)
	if err != nil {
		return nil, false, err
	}
	return pointer, pointer != nil, nil
}

// CAS moves the pointer only when the stored pointer matches old, and only
// forward to a strictly greater version. A nil old means object creation.
func (m *FileMetadataStore) CAS(ctx context.Context, objectID string, old, next *Pointer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if next == nil {
		return fmt.Errorf("netsec: CAS requires a new pointer")
	}
	lock := m.lockFor(objectID)
	lock.Lock()
	defer lock.Unlock()

	current, err := m.readLocked(objectID)
	if err != nil {
		return err
	}
	if !samePointer(current, old) {
		return fmt.Errorf("%w: expected version %d, found %v", ErrConflict, expectedVersion(old), versionOrNil(current))
	}
	if current != nil && next.Version <= current.Version {
		return fmt.Errorf("%w: pointer cannot move backwards (v%d -> v%d)", ErrConflict, current.Version, next.Version)
	}

	payload, err := json.Marshal(persistedPointer{
		Version: next.Version,
		Digest:  hex.EncodeToString(next.Digest),
	})
	if err != nil {
		return fmt.Errorf("netsec: marshal pointer: %w", err)
	}
	path, err := m.path(objectID)
	if err != nil {
		return err
	}

	if m.FaultHook != nil {
		if err := m.FaultHook(StageBeforeWrite, objectID); err != nil {
			return err
		}
	}

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("netsec: temp suffix: %w", err)
	}
	tmp := path + ".tmp-" + hex.EncodeToString(suffix)
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return fmt.Errorf("netsec: write temp pointer: %w", err)
	}

	if m.FaultHook != nil {
		if err := m.FaultHook(StageAfterTempWrite, objectID); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("netsec: commit pointer: %w", err)
	}

	if m.FaultHook != nil {
		if err := m.FaultHook(StageAfterRename, objectID); err != nil {
			return err
		}
	}
	return nil
}

func samePointer(a, b *Pointer) bool {
	switch {
	case a == nil || b == nil:
		return a == b
	case a.Version != b.Version:
		return false
	default:
		return equalDigest(a.Digest, b.Digest)
	}
}

func expectedVersion(p *Pointer) uint64 {
	if p == nil {
		return 0
	}
	return p.Version
}

func versionOrNil(p *Pointer) any {
	if p == nil {
		return nil
	}
	return p.Version
}
