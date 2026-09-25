package segstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrCorrupt means the on-disk state is inconsistent. The store never
// repairs this by silently falling back to an older manifest, because that
// would discard data already acknowledged by a published manifest.
var ErrCorrupt = errors.New("segstore: corrupt store")

// SegmentRef is one segment referenced by a manifest.
type SegmentRef struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest is the authoritative pointer from committed versions to segments.
type Manifest struct {
	Version  uint64       `json:"version"`
	Segments []SegmentRef `json:"segments"`
}

const manifestFile = "manifest.json"

// manifestOnDisk embeds a checksum of the manifest body so tampering or a
// torn manifest write is detectable.
type manifestOnDisk struct {
	Manifest Manifest `json:"manifest"`
	SHA256   string   `json:"sha256"`
}

func encodeManifest(m Manifest) ([]byte, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	return json.Marshal(manifestOnDisk{
		Manifest: m,
		SHA256:   hex.EncodeToString(sum[:]),
	})
}

func (s *Store) writeManifest(m Manifest) error {
	data, err := encodeManifest(m)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.dir, manifestFile, data)
}

// loadManifest validates the manifest and every segment it references.
// A missing referenced segment is corruption, never a trigger to fall back
// to a previous manifest.
func (s *Store) loadManifest(allowSegments bool) error {
	data, err := os.ReadFile(filepath.Join(s.dir, manifestFile))
	if errors.Is(err, fs.ErrNotExist) {
		leftovers, gerr := filepath.Glob(filepath.Join(s.dir, "seg-*.dat"))
		if gerr != nil {
			return gerr
		}
		if !allowSegments && len(leftovers) > 0 {
			return fmt.Errorf("%w: manifest missing while %d segment files exist",
				ErrCorrupt, len(leftovers))
		}
		s.manifest = Manifest{}
		return nil
	}
	if err != nil {
		return err
	}

	var onDisk manifestOnDisk
	if err := json.Unmarshal(data, &onDisk); err != nil {
		return fmt.Errorf("%w: unreadable manifest: %v", ErrCorrupt, err)
	}
	body, err := json.Marshal(onDisk.Manifest)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != onDisk.SHA256 {
		return fmt.Errorf("%w: manifest checksum mismatch", ErrCorrupt)
	}

	for _, ref := range onDisk.Manifest.Segments {
		st, err := os.Stat(filepath.Join(s.dir, ref.Name))
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: manifest v%d references missing segment %s",
				ErrCorrupt, onDisk.Manifest.Version, ref.Name)
		}
		if err != nil {
			return err
		}
		if st.Size() != ref.Size {
			return fmt.Errorf("%w: segment %s size mismatch", ErrCorrupt, ref.Name)
		}
	}

	s.manifest = onDisk.Manifest
	return nil
}

// Verify re-hashes every segment referenced by the manifest.
func (s *Store) Verify() error {
	for _, ref := range s.manifest.Segments {
		got, err := hashFile(filepath.Join(s.dir, ref.Name))
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: missing segment %s", ErrCorrupt, ref.Name)
		}
		if err != nil {
			return err
		}
		if got != ref.SHA256 {
			return fmt.Errorf("%w: segment %s checksum mismatch", ErrCorrupt, ref.Name)
		}
	}
	return nil
}

// Read returns the payload of the current (single-segment) manifest.
func (s *Store) Read() ([]byte, error) {
	if len(s.manifest.Segments) == 0 {
		return nil, nil
	}
	if len(s.manifest.Segments) != 1 {
		return nil, fmt.Errorf("%w: expected 1 current segment, got %d",
			ErrCorrupt, len(s.manifest.Segments))
	}
	return os.ReadFile(filepath.Join(s.dir, s.manifest.Segments[0].Name))
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
