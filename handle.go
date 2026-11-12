package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Handle is an immutable, verified snapshot of one published version. It binds
// the content bytes to the manifest digest and the security label; subsequent
// commits never mutate a returned handle.
type Handle struct {
	object  string
	version int64
	digest  string
	label   *Label
	content []byte
}

// Object returns the object name captured by the handle.
func (h *Handle) Object() string { return h.object }

// Version returns the immutable version number captured by the handle.
func (h *Handle) Version() int64 { return h.version }

// Digest returns the SHA-256 hex digest recorded in the version manifest.
func (h *Handle) Digest() string { return h.digest }

// Label returns a copy of the bound security label, or nil when the version
// carries no label (such versions are denied by Authorize).
func (h *Handle) Label() *Label { return copyLabel(h.label) }

// Content returns a defensive copy of the verified content bytes, so callers
// cannot mutate the bytes backing the immutable handle.
func (h *Handle) Content() []byte {
	out := make([]byte, len(h.content))
	copy(out, h.content)
	return out
}

// Authorize enforces default-deny semantics: a missing label is always denied;
// otherwise the reader's clearance must be at least the bound label level.
func (h *Handle) Authorize(clearance Level) error {
	if h.label == nil {
		return ErrMissingLabel
	}
	if clearance < h.label.Level {
		return fmt.Errorf("%w: clearance %s < label %s", ErrDenied, clearance, h.label.Level)
	}
	return nil
}

// Open returns an immutable handle for the current published version. Readers
// never take the store's write lock and read a self-contained version
// directory, so they always observe either a complete old or new version.
func (s *Store) Open(object string) (*Handle, error) {
	if err := validateName(object); err != nil {
		return nil, err
	}
	version, err := s.currentVersion(object)
	if err != nil {
		return nil, err
	}
	return s.openVersion(object, version)
}

// OpenVersion returns a handle pinned to an explicit historical version. The
// version is immutable and remains readable after newer versions are published.
func (s *Store) OpenVersion(object string, version int64) (*Handle, error) {
	if err := validateName(object); err != nil {
		return nil, err
	}
	if version < 1 {
		return nil, fmt.Errorf("netsec: version must be positive")
	}
	return s.openVersion(object, version)
}

func (s *Store) currentVersion(object string) (int64, error) {
	data, err := os.ReadFile(s.currentPath(object))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, ErrVersionNotFound
		}
		return 0, err
	}
	raw := strings.TrimSpace(string(data))
	if !strings.HasPrefix(raw, "v") {
		return 0, fmt.Errorf("%w: malformed current pointer", ErrTampered)
	}
	version, err := strconv.ParseInt(raw[1:], 10, 64)
	if err != nil || version < 1 {
		return 0, fmt.Errorf("%w: malformed current pointer", ErrTampered)
	}
	return version, nil
}

// readCurrentManifest loads only the manifest referenced by the current
// pointer; used by Commit to decide the next version and downgrade policy.
func (s *Store) readCurrentManifest(object string) (*manifest, error) {
	version, err := s.currentVersion(object)
	if err != nil {
		return nil, err
	}
	dir := s.versionDir(object, version)
	record, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	if record.Version != version || record.Object != object {
		return nil, ErrTampered
	}
	return record, nil
}

func readManifest(dir string) (*manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrVersionNotFound
		}
		return nil, err
	}
	var record manifest
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTampered, err)
	}
	return &record, nil
}

// openVersion loads and verifies a complete, self-contained version directory.
// It checks the manifest identity, recomputes the content digest and only then
// constructs the immutable handle that binds digest and label.
func (s *Store) openVersion(object string, version int64) (*Handle, error) {
	dir := s.versionDir(object, version)
	record, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	if record.Object != object || record.Version != version {
		return nil, ErrTampered
	}

	content, err := os.ReadFile(filepath.Join(dir, "content.bin"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrVersionNotFound
		}
		return nil, err
	}

	sum := sha256.Sum256(content)
	if got := hex.EncodeToString(sum[:]); got != record.Digest {
		return nil, fmt.Errorf("%w: object %s version %d", ErrDigestMismatch, object, version)
	}

	return &Handle{
		object:  object,
		version: version,
		digest:  record.Digest,
		label:   copyLabel(record.Label),
		content: content,
	}, nil
}
