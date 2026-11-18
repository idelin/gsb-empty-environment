package content

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Store is a content-addressable blob store with resumable write sessions.
//
// Layout on disk:
//
//	<root>/blobs/<algo>/<hex>   committed, read-only blobs
//	<root>/sessions/<ref>/      in-progress sessions (data + offset)
type Store struct {
	root string
}

// OpenStore opens (creating if needed) the store rooted at root.
func OpenStore(root string) (*Store, error) {
	for _, dir := range []string{
		root,
		filepath.Join(root, "blobs"),
		filepath.Join(root, "sessions"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{root: root}, nil
}

// Begin starts a new write session named ref, hashing content with algo.
// It fails if a session with the same ref already exists; use Resume to
// continue it.
func (s *Store) Begin(ref string, algo Algorithm) (*Session, error) {
	if err := validateRef(ref); err != nil {
		return nil, err
	}
	switch algo {
	case SHA256, SHA512:
	default:
		return nil, fmt.Errorf("unsupported algorithm %q", algo)
	}
	dir := s.sessionDir(ref)
	if err := os.Mkdir(dir, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("session %q already exists", ref)
		}
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "data"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	sess := &Session{store: s, ref: ref, file: f, hash: Digest{Algo: algo}.newHasher()}
	if err := sess.persistOffset(); err != nil {
		f.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	return sess, nil
}

// Resume reopens the session named ref. The digest state is rebuilt from
// scratch by re-reading the persisted data; nothing about the previous
// session's hash is trusted. Any bytes beyond the persisted offset are
// discarded.
func (s *Store) Resume(ref string, algo Algorithm) (*Session, error) {
	if err := validateRef(ref); err != nil {
		return nil, err
	}
	switch algo {
	case SHA256, SHA512:
	default:
		return nil, fmt.Errorf("unsupported algorithm %q", algo)
	}
	dir := s.sessionDir(ref)
	off, err := readOffset(filepath.Join(dir, "offset"))
	if err != nil {
		return nil, fmt.Errorf("resume %q: %w", ref, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "data"), os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("resume %q: %w", ref, err)
	}
	fail := func(err error) (*Session, error) {
		f.Close()
		return nil, fmt.Errorf("resume %q: %w", ref, err)
	}
	st, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if st.Size() < off {
		return fail(fmt.Errorf("data file truncated: size %d < persisted offset %d", st.Size(), off))
	}
	// Recompute the digest state from the persisted bytes, from zero.
	h := Digest{Algo: algo}.newHasher()
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	if _, err := io.CopyN(h, f, off); err != nil {
		return fail(fmt.Errorf("recomputing digest state: %w", err))
	}
	// Drop anything written after the persisted offset (e.g. a torn write).
	if err := f.Truncate(off); err != nil {
		return fail(err)
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return fail(err)
	}
	return &Session{store: s, ref: ref, file: f, hash: h, off: off}, nil
}

// SessionOffset reports the persisted write offset of the session ref.
func (s *Store) SessionOffset(ref string) (int64, error) {
	if err := validateRef(ref); err != nil {
		return 0, err
	}
	return readOffset(filepath.Join(s.sessionDir(ref), "offset"))
}

// HasBlob reports whether a blob for d has been committed.
func (s *Store) HasBlob(d Digest) bool {
	_, err := os.Stat(s.blobPath(d))
	return err == nil
}

// OpenBlob opens a committed blob for reading.
func (s *Store) OpenBlob(d Digest) (*os.File, error) {
	return os.Open(s.blobPath(d))
}

func (s *Store) sessionDir(ref string) string {
	return filepath.Join(s.root, "sessions", ref)
}

func (s *Store) blobPath(d Digest) string {
	return filepath.Join(s.root, "blobs", string(d.Algo), d.Hex)
}

func validateRef(ref string) error {
	if ref == "" || ref == "." || ref == ".." {
		return fmt.Errorf("invalid session ref %q", ref)
	}
	if strings.ContainsAny(ref, "/\\") || strings.ContainsRune(ref, 0) {
		return fmt.Errorf("invalid session ref %q: path separators not allowed", ref)
	}
	return nil
}

func readOffset(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var off int64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &off); err != nil {
		return 0, fmt.Errorf("corrupt offset file: %w", err)
	}
	if off < 0 {
		return 0, fmt.Errorf("corrupt offset file: negative offset %d", off)
	}
	return off, nil
}
