package content

import (
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

var (
	// ErrSizeMismatch is returned by Commit when the declared size does
	// not match the bytes actually written.
	ErrSizeMismatch = errors.New("content: size mismatch")
	// ErrDigestMismatch is returned by Commit when the written content
	// does not hash to the expected digest.
	ErrDigestMismatch = errors.New("content: digest mismatch")
)

// Session is a resumable write to the store, named by a ref. It is not
// safe for concurrent use.
type Session struct {
	store  *Store
	ref    string
	file   *os.File
	hash   hash.Hash
	off    int64
	closed bool
}

// Ref returns the session name.
func (s *Session) Ref() string { return s.ref }

// Offset returns the number of bytes written so far.
func (s *Session) Offset() int64 { return s.off }

// Write appends p at the current offset and persists the new offset, so
// an interrupted session can be resumed exactly where it left off.
func (s *Session) Write(p []byte) (int, error) {
	if s.closed {
		return 0, fmt.Errorf("session %q is closed", s.ref)
	}
	n, err := s.file.Write(p)
	if n > 0 {
		s.hash.Write(p[:n])
		s.off += int64(n)
	}
	if err != nil {
		return n, err
	}
	// Persist data before the offset that covers it.
	if err := s.file.Sync(); err != nil {
		return n, err
	}
	if err := s.persistOffset(); err != nil {
		return n, err
	}
	return n, nil
}

// Commit verifies the written content against want and size, then
// publishes it as a read-only blob. On any mismatch the session is
// deleted and no blob is created under the claimed digest. If a blob for
// the same digest already exists, its content is verified for equality
// and the session is simply discarded.
func (s *Session) Commit(want Digest, size int64) error {
	if s.closed {
		return fmt.Errorf("session %q is closed", s.ref)
	}
	if s.off != size {
		s.abort()
		return fmt.Errorf("%w: declared %d, wrote %d", ErrSizeMismatch, size, s.off)
	}
	sum := s.hash.Sum(nil)
	got := Digest{Algo: want.Algo, Hex: fmt.Sprintf("%x", sum)}
	if got != want {
		s.abort()
		return fmt.Errorf("%w: want %s, got %s", ErrDigestMismatch, want, got)
	}
	if err := s.file.Close(); err != nil {
		return err
	}
	s.closed = true

	blobPath := s.store.blobPath(want)
	dataPath := filepath.Join(s.store.sessionDir(s.ref), "data")

	if _, err := os.Stat(blobPath); err == nil {
		// Duplicate commit for an existing digest: blobs are immutable,
		// so only verify the committed content is consistent.
		if err := verifyBlob(blobPath, want); err != nil {
			os.RemoveAll(s.store.sessionDir(s.ref))
			return err
		}
		os.RemoveAll(s.store.sessionDir(s.ref))
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(blobPath), 0o755); err != nil {
		return err
	}
	// Make the data read-only before it becomes the blob.
	if err := os.Chmod(dataPath, 0o444); err != nil {
		return err
	}
	if err := os.Rename(dataPath, blobPath); err != nil {
		return err
	}
	os.RemoveAll(s.store.sessionDir(s.ref))
	return nil
}

// Abort closes the session and deletes all of its persisted state.
func (s *Session) Abort() { s.abort() }

func (s *Session) abort() {
	if !s.closed {
		s.file.Close()
		s.closed = true
	}
	os.RemoveAll(s.store.sessionDir(s.ref))
}

func (s *Session) persistOffset() error {
	dir := s.store.sessionDir(s.ref)
	tmp := filepath.Join(dir, "offset.tmp")
	final := filepath.Join(dir, "offset")
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(s.off, 10)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// verifyBlob re-reads a committed blob and checks it still hashes to d.
func verifyBlob(path string, d Digest) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := d.newHasher()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := fmt.Sprintf("%x", h.Sum(nil)); got != d.Hex {
		return fmt.Errorf("%w: existing blob %s does not match %s", ErrDigestMismatch, path, d)
	}
	return nil
}
