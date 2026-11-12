package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// ManifestVersion is the encoding version emitted by Build and accepted by
// Decode.
const ManifestVersion = 1

const digestPrefix = "sha256:"

// Entry binds one release object: its canonical relative path, permission
// bits (including setuid/setgid/sticky), size, and SHA-256 digest.
type Entry struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

// Manifest is the signed closure of every object readable under one
// configuration. Entries are sorted by Path. The host signs Canonical()
// bytes; consumers must obtain the manifest through Decode.
type Manifest struct {
	Version  int      `json:"version"`
	Platform string   `json:"platform"` // "goos/goarch" of this configuration
	Optional []string `json:"optional,omitempty"`
	Entries  []Entry  `json:"entries"`

	index map[string]int
}

// canonicalRel validates that p is already in canonical slash-separated
// relative form. Any aliasing spelling of the same object — "./a", "a//b",
// "a/./b", "a/../b", "a/", absolute paths, backslashes — is rejected so
// that exactly one string names each object.
func canonicalRel(p string) (string, error) {
	if p == "" || p == "." || path.IsAbs(p) || strings.ContainsRune(p, '\\') {
		return "", fmt.Errorf("%w: %q", ErrPathAlias, p)
	}
	if c := path.Clean(p); c != p || c == ".." || strings.HasPrefix(p, "../") {
		return "", fmt.Errorf("%w: %q", ErrPathAlias, p)
	}
	return p, nil
}

// modeBits keeps permission and setuid/setgid/sticky bits; type bits are
// implied because every manifest object is a regular file.
func modeBits(m fs.FileMode) uint32 {
	return uint32(m & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky))
}

func (m *Manifest) buildIndex() {
	m.index = make(map[string]int, len(m.Entries))
	for i := range m.Entries {
		m.index[m.Entries[i].Path] = i
	}
}

// Lookup returns the entry for a canonical relative path.
func (m *Manifest) Lookup(rel string) (Entry, bool) {
	if m.index == nil {
		m.buildIndex()
	}
	i, ok := m.index[rel]
	if !ok {
		return Entry{}, false
	}
	return m.Entries[i], true
}

// Canonical returns the deterministic encoding the release host signs.
func (m *Manifest) Canonical() []byte {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err) // Manifest contains only JSON-safe fields.
	}
	return b
}

// Digest returns the SHA-256 digest of the canonical encoding, suitable as
// the signed payload identifier.
func (m *Manifest) Digest() string {
	sum := sha256.Sum256(m.Canonical())
	return digestPrefix + hex.EncodeToString(sum[:])
}

// Encode writes the canonical encoding to w.
func (m *Manifest) Encode(w io.Writer) error {
	_, err := w.Write(m.Canonical())
	return err
}

// Decode parses a manifest and enforces its invariants: known version,
// canonical paths, sorted and duplicate-free entries, well-formed digests.
func Decode(r io.Reader) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("netsec: decode manifest: %w", err)
	}
	if m.Version != ManifestVersion {
		return nil, fmt.Errorf("netsec: unsupported manifest version %d", m.Version)
	}
	seen := make(map[string]bool, len(m.Entries))
	for i := range m.Entries {
		e := &m.Entries[i]
		if _, err := canonicalRel(e.Path); err != nil {
			return nil, fmt.Errorf("netsec: manifest entry %d: %w", i, err)
		}
		if i > 0 && m.Entries[i-1].Path >= e.Path {
			return nil, fmt.Errorf("netsec: manifest entries not sorted at %q", e.Path)
		}
		if seen[e.Path] {
			return nil, fmt.Errorf("netsec: duplicate manifest entry %q", e.Path)
		}
		seen[e.Path] = true
		if !strings.HasPrefix(e.Digest, digestPrefix) || len(e.Digest) != len(digestPrefix)+sha256.Size*2 {
			return nil, fmt.Errorf("netsec: manifest entry %q: malformed digest", e.Path)
		}
		if _, err := hex.DecodeString(e.Digest[len(digestPrefix):]); err != nil {
			return nil, fmt.Errorf("netsec: manifest entry %q: malformed digest", e.Path)
		}
		if e.Size < 0 {
			return nil, fmt.Errorf("netsec: manifest entry %q: negative size", e.Path)
		}
	}
	m.Optional = append([]string(nil), m.Optional...)
	sort.Strings(m.Optional)
	m.buildIndex()
	return &m, nil
}
