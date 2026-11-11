package manifestbind

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrBadSignature is returned when the manifest signature does not
	// verify against the signer public key.
	ErrBadSignature = errors.New("manifestbind: manifest signature invalid")
	// ErrUnlistedEntry is returned when the archive yields an entry that
	// is not bound in the signed manifest.
	ErrUnlistedEntry = errors.New("manifestbind: archive entry not in manifest")
	// ErrMissingEntry is returned when a manifest entry never appears in
	// the archive stream.
	ErrMissingEntry = errors.New("manifestbind: manifest entry missing from archive")
	// ErrDigestMismatch is returned when the digest computed over the
	// decompressed entry bytes differs from the manifest.
	ErrDigestMismatch = errors.New("manifestbind: entry digest mismatch")
)

// ManifestEntry binds one archive member to the digest of its
// decompressed bytes.
type ManifestEntry struct {
	Path   string
	Size   int64
	SHA256 [sha256.Size]byte
}

// Manifest is the signed list of entries an archive must contain,
// nothing more and nothing less.
type Manifest struct {
	Entries []ManifestEntry
}

// NewManifest validates the entry paths and returns a manifest ready to
// be signed. Entries are stored sorted by path bytes so the canonical
// encoding is independent of input order.
func NewManifest(entries []ManifestEntry) (*Manifest, error) {
	sorted := make([]ManifestEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Path < sorted[j].Path
	})
	seen := newPathSet()
	for _, e := range sorted {
		if e.Size < 0 {
			return nil, fmt.Errorf("manifestbind: negative size for %q", e.Path)
		}
		if err := seen.Add(e.Path); err != nil {
			return nil, err
		}
	}
	return &Manifest{Entries: sorted}, nil
}

// canonical encodes the manifest deterministically: for each entry, a
// uvarint length-prefixed path, the 32-byte digest, and a uvarint size.
func (m *Manifest) canonical() []byte {
	var buf bytes.Buffer
	var tmp [binary.MaxVarintLen64]byte
	for _, e := range m.Entries {
		n := binary.PutUvarint(tmp[:], uint64(len(e.Path)))
		buf.Write(tmp[:n])
		buf.WriteString(e.Path)
		buf.Write(e.SHA256[:])
		n = binary.PutUvarint(tmp[:], uint64(e.Size))
		buf.Write(tmp[:n])
	}
	return buf.Bytes()
}

// Sign returns the ed25519 signature over the canonical manifest
// encoding.
func (m *Manifest) Sign(key ed25519.PrivateKey) []byte {
	return ed25519.Sign(key, m.canonical())
}

// Verify checks the signature over the canonical manifest encoding.
func (m *Manifest) Verify(key ed25519.PublicKey, sig []byte) error {
	if !ed25519.Verify(key, m.canonical(), sig) {
		return ErrBadSignature
	}
	return nil
}
