package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultMaxDocumentBytes limits how much data a single job may contain. It
// protects the print service from unbounded reads and writes.
const DefaultMaxDocumentBytes int64 = 64 << 20

// Storage is the job-isolated document backend used by a Service. Every method
// receives a Scope, never a caller-supplied URI, so an implementation cannot
// accidentally retrieve a document outside the job's own namespace.
type Storage interface {
	WriteJob(ctx context.Context, scope Scope, documentName string, content []byte) error
	ReadJob(ctx context.Context, scope Scope, documentName string) ([]byte, error)
	DeleteJob(ctx context.Context, scope Scope) error
}

// FileStorage stores each job in its own directory under hashed scope
// components:
//
//	<root>/<H(queue)>/<H(submitter)>/<H(job-id)>/<document-name>
//
// The logical scope components are validated single path segments. Hashing
// them keeps physical directories independent of filesystem case folding or
// Unicode normalization, so two logically distinct principals cannot share a
// directory. The final path is also checked to remain under root.
type FileStorage struct {
	root             string
	MaxDocumentBytes int64
}

// NewFileStorage creates (if necessary) and resolves root, then returns a
// filesystem-backed Storage.
func NewFileStorage(root string) (*FileStorage, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("%w: empty storage root", ErrUnsafeStoragePath)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("netsec: resolve storage root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("netsec: create storage root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("netsec: resolve storage root symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("netsec: stat storage root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: storage root is not a directory", ErrUnsafeStoragePath)
	}
	return &FileStorage{root: resolved, MaxDocumentBytes: DefaultMaxDocumentBytes}, nil
}

// Root returns the resolved storage root. It is exposed for operational
// inspection only; document reads should go through ReadJob.
func (f *FileStorage) Root() string {
	if f == nil {
		return ""
	}
	return f.root
}

// JobDirectory returns the root of one job's isolated storage directory.
func (f *FileStorage) JobDirectory(scope Scope) (string, error) {
	if f == nil || f.root == "" {
		return "", fmt.Errorf("%w: uninitialized file storage", ErrUnsafeStoragePath)
	}
	if err := scope.Validate(); err != nil {
		return "", err
	}
	dir := filepath.Join(f.root, storageComponent(scope.Queue), storageComponent(scope.Submitter), storageComponent(scope.JobID))
	rel, err := filepath.Rel(f.root, dir)
	if err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: job directory escapes root", ErrUnsafeStoragePath)
	}
	return dir, nil
}

func storageComponent(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (f *FileStorage) limit() int64 {
	if f == nil || f.MaxDocumentBytes <= 0 {
		return DefaultMaxDocumentBytes
	}
	return f.MaxDocumentBytes
}

// WriteJob atomically installs the document inside the job's own directory. It
// refuses to overwrite an existing job document.
func (f *FileStorage) WriteJob(ctx context.Context, scope Scope, documentName string, content []byte) error {
	if f == nil {
		return fmt.Errorf("%w: uninitialized file storage", ErrUnsafeStoragePath)
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := validateDocumentName(documentName); err != nil {
		return err
	}
	if int64(len(content)) > f.limit() {
		return fmt.Errorf("%w: %d bytes", ErrDocumentTooLarge, len(content))
	}
	dir, err := f.JobDirectory(scope)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("netsec: create job directory: %w", err)
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil || !dirInfo.IsDir() {
		return fmt.Errorf("%w: job directory is not a regular directory", ErrUnsafeStoragePath)
	}

	finalPath := filepath.Join(dir, documentName)
	if _, err := os.Lstat(finalPath); err == nil {
		return fmt.Errorf("%w: document already exists", ErrJobExists)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("netsec: inspect job document: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".incoming-*")
	if err != nil {
		return fmt.Errorf("netsec: create temporary document: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("netsec: set document permissions: %w", err)
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("netsec: write document: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("netsec: sync document: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("netsec: close document: %w", err)
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if _, err := os.Lstat(finalPath); err == nil {
		return fmt.Errorf("%w: document already exists", ErrJobExists)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("netsec: inspect job document: %w", err)
	}
	// Hard-linking the completed temporary file publishes it atomically and,
	// unlike rename, fails if another writer created the destination first.
	if err := os.Link(tmpName, finalPath); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%w: document already exists", ErrJobExists)
		}
		return fmt.Errorf("netsec: install document: %w", err)
	}
	return nil
}

// ReadJob reads exactly the named document from the job's isolated directory.
// Symbolic links and non-regular files are rejected.
func (f *FileStorage) ReadJob(ctx context.Context, scope Scope, documentName string) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("%w: uninitialized file storage", ErrUnsafeStoragePath)
	}
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if err := validateDocumentName(documentName); err != nil {
		return nil, err
	}
	dir, err := f.JobDirectory(scope)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, documentName)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("netsec: stat job document: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: document is not a regular file", ErrUnsafeStoragePath)
	}
	if info.Size() > f.limit() {
		return nil, fmt.Errorf("%w: %d bytes", ErrDocumentTooLarge, info.Size())
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("netsec: open job document: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("netsec: stat opened document: %w", err)
	}
	if !openedInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: opened document is not a regular file", ErrUnsafeStoragePath)
	}
	if openedInfo.Size() > f.limit() {
		return nil, fmt.Errorf("%w: %d bytes", ErrDocumentTooLarge, openedInfo.Size())
	}

	content, err := io.ReadAll(io.LimitReader(file, f.limit()+1))
	if err != nil {
		return nil, fmt.Errorf("netsec: read job document: %w", err)
	}
	if int64(len(content)) > f.limit() {
		return nil, fmt.Errorf("%w: %d bytes", ErrDocumentTooLarge, len(content))
	}
	return content, nil
}

// DeleteJob removes one job's isolated directory.
func (f *FileStorage) DeleteJob(ctx context.Context, scope Scope) error {
	if f == nil {
		return fmt.Errorf("%w: uninitialized file storage", ErrUnsafeStoragePath)
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	dir, err := f.JobDirectory(scope)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("netsec: delete job directory: %w", err)
	}
	return nil
}

// MemoryStorage is an in-memory Storage useful for tests and embedded print
// services. It preserves the same job scoping rules as FileStorage.
type MemoryStorage struct {
	mu               sync.RWMutex
	documents        map[string]memoryDocument
	MaxDocumentBytes int64
}

type memoryDocument struct {
	name    string
	content []byte
}

// NewMemoryStorage returns an empty in-memory document store.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		documents:        make(map[string]memoryDocument),
		MaxDocumentBytes: DefaultMaxDocumentBytes,
	}
}

func (m *MemoryStorage) limit() int64 {
	if m == nil || m.MaxDocumentBytes <= 0 {
		return DefaultMaxDocumentBytes
	}
	return m.MaxDocumentBytes
}

func scopeKey(scope Scope) string {
	return scope.Queue + "\x00" + scope.Submitter + "\x00" + scope.JobID
}

func (m *MemoryStorage) WriteJob(ctx context.Context, scope Scope, documentName string, content []byte) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("netsec: uninitialized memory storage")
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	if err := validateDocumentName(documentName); err != nil {
		return err
	}
	if int64(len(content)) > m.limit() {
		return fmt.Errorf("%w: %d bytes", ErrDocumentTooLarge, len(content))
	}
	copied := append([]byte(nil), content...)

	m.mu.Lock()
	defer m.mu.Unlock()
	key := scopeKey(scope)
	if existing, ok := m.documents[key]; ok {
		if existing.name == documentName {
			return fmt.Errorf("%w: document already exists", ErrJobExists)
		}
		return fmt.Errorf("%w: job already has a document", ErrJobExists)
	}
	m.documents[key] = memoryDocument{name: documentName, content: copied}
	return nil
}

func (m *MemoryStorage) ReadJob(ctx context.Context, scope Scope, documentName string) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("netsec: uninitialized memory storage")
	}
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if err := validateDocumentName(documentName); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	document, ok := m.documents[scopeKey(scope)]
	if !ok || document.name != documentName {
		return nil, fmt.Errorf("%w: document is not present in job storage", os.ErrNotExist)
	}
	return append([]byte(nil), document.content...), nil
}

func (m *MemoryStorage) DeleteJob(ctx context.Context, scope Scope) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("netsec: uninitialized memory storage")
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.documents, scopeKey(scope))
	return nil
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
