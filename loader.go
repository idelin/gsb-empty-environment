package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Loader is the independent consumer of a signed manifest. It reads only
// objects listed in the manifest, addressed by their exact canonical
// relative path, and re-verifies type, mode, and digest on every read.
type Loader struct {
	root string
	man  *Manifest
}

// NewLoader returns a Loader serving objects under root per manifest m.
func NewLoader(root string, m *Manifest) *Loader {
	return &Loader{root: root, man: m}
}

// ReadFile returns the verified contents of the manifest object rel.
func (l *Loader) ReadFile(rel string) ([]byte, error) {
	canon, err := canonicalRel(rel)
	if err != nil {
		return nil, err
	}
	e, ok := l.man.Lookup(canon)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotInManifest, rel)
	}
	full := filepath.Join(l.root, filepath.FromSlash(canon))

	lst, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrObjectMissing, rel)
	}
	if err != nil {
		return nil, err
	}
	if !lst.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, rel)
	}

	f, err := os.Open(full)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrObjectUnreadable, rel)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrObjectUnreadable, rel)
	}
	if !os.SameFile(lst, st) || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, rel)
	}
	if modeBits(st.Mode()) != e.Mode {
		return nil, fmt.Errorf("%w: %s: have %#o, want %#o", ErrModeMismatch, rel, modeBits(st.Mode()), e.Mode)
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrObjectUnreadable, rel)
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != e.Size || digestPrefix+hex.EncodeToString(sum[:]) != e.Digest {
		return nil, fmt.Errorf("%w: %s", ErrDigestMismatch, rel)
	}
	return data, nil
}

// Open returns a reader over the verified contents of the manifest object
// rel. The digest is checked before the reader is returned.
func (l *Loader) Open(rel string) (io.ReadCloser, error) {
	data, err := l.ReadFile(rel)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// VerifyAll re-checks every manifest object against disk, failing fast on
// the first violation. Hosts typically call it once at startup.
func (l *Loader) VerifyAll() error {
	for _, e := range l.man.Entries {
		if _, err := l.ReadFile(e.Path); err != nil {
			return err
		}
	}
	return nil
}
