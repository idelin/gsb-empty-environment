package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// RefSuffix is the mandatory algorithm suffix of an SSB blob reference.
const RefSuffix = ".sha256"

// refBodyLen is the number of standard Base64 characters encoding a
// 32-byte SHA-256 digest: ceil(32/3)*4 = 44, padding included.
const refBodyLen = 44

var (
	ErrInvalidRef   = errors.New("blobstore: invalid blob reference")
	ErrTooLarge     = errors.New("blobstore: declared size exceeds maximum")
	ErrSizeMismatch = errors.New("blobstore: received byte count does not match declared size")
	ErrHashMismatch = errors.New("blobstore: content hash does not match reference")
	ErrInFlight     = errors.New("blobstore: blob is already being received")
)

// ParseRef validates an SSB blob reference of the form
// "&" + 44 standard Base64 chars + ".sha256" and returns the 32-byte
// digest it carries. Anything else is rejected.
func ParseRef(ref string) ([32]byte, error) {
	var sum [32]byte
	if len(ref) != 1+refBodyLen+len(RefSuffix) ||
		ref[0] != '&' ||
		!strings.HasSuffix(ref, RefSuffix) {
		return sum, ErrInvalidRef
	}
	body := ref[1 : 1+refBodyLen]
	dec, err := base64.StdEncoding.DecodeString(body)
	if err != nil || len(dec) != sha256.Size ||
		base64.StdEncoding.EncodeToString(dec) != body {
		return sum, ErrInvalidRef
	}
	copy(sum[:], dec)
	return sum, nil
}

// Store receives and serves SSB blobs. The zero value is not usable;
// construct one with OpenStore.
type Store struct {
	dir string
	max int64

	mu       sync.Mutex
	inflight map[[32]byte]struct{}
}

// OpenStore creates a blob store rooted at dir. maxSize is the largest
// acceptable declared blob size; peers announcing more are rejected
// before a single byte is read.
func OpenStore(dir string, maxSize int64) (*Store, error) {
	if maxSize < 0 {
		return nil, errors.New("blobstore: negative maximum size")
	}
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		return nil, err
	}
	return &Store{
		dir:      dir,
		max:      maxSize,
		inflight: make(map[[32]byte]struct{}),
	}, nil
}

func (s *Store) path(sum [32]byte) string {
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".blob")
}

// Has reports whether the blob for ref is present and verified.
func (s *Store) Has(ref string) bool {
	sum, err := ParseRef(ref)
	if err != nil {
		return false
	}
	_, err = os.Stat(s.path(sum))
	return err == nil
}

// Open returns a reader for a stored blob.
func (s *Store) Open(ref string) (io.ReadCloser, error) {
	sum, err := ParseRef(ref)
	if err != nil {
		return nil, err
	}
	return os.Open(s.path(sum))
}

// Receive ingests a blob pushed by a peer. declared is the size the peer
// announced up front; r streams the content. The blob is committed
// atomically only if exactly declared bytes arrive and their SHA-256
// matches the reference. Any failure removes the temporary file, so no
// partial or unverified content is ever readable.
func (s *Store) Receive(ref string, declared int64, r io.Reader) error {
	sum, err := ParseRef(ref)
	if err != nil {
		return err
	}
	if declared < 0 {
		return fmt.Errorf("%w: negative declared size %d", ErrSizeMismatch, declared)
	}
	if declared > s.max {
		return fmt.Errorf("%w: declared %d, max %d", ErrTooLarge, declared, s.max)
	}

	final := s.path(sum)

	s.mu.Lock()
	if _, ok := s.inflight[sum]; ok {
		s.mu.Unlock()
		return ErrInFlight
	}
	if _, err := os.Stat(final); err == nil {
		// Already verified on disk; the first successful receiver won.
		s.mu.Unlock()
		return nil
	}
	s.inflight[sum] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, sum)
		s.mu.Unlock()
	}()

	tmp, err := os.CreateTemp(filepath.Join(s.dir, "tmp"), "recv-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// On every failure path the temp file is unlinked; after a
	// successful rename this is a no-op.
	defer os.Remove(tmpName)

	h := sha256.New()
	// Read at most declared+1 bytes: a peer sending more than it
	// announced is cut off immediately instead of streaming forever.
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, declared+1))
	if err != nil {
		tmp.Close()
		return fmt.Errorf("blobstore: reading blob content: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if n != declared {
		return fmt.Errorf("%w: declared %d, got %d", ErrSizeMismatch, declared, n)
	}
	if !bytes.Equal(h.Sum(nil), sum[:]) {
		return ErrHashMismatch
	}
	// Same-directory rename is atomic: readers only ever see either
	// nothing or the fully verified blob.
	if err := os.Rename(tmpName, final); err != nil {
		return fmt.Errorf("blobstore: committing blob: %w", err)
	}
	return nil
}
