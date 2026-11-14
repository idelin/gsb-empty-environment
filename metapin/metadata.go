// Package metapin provides content-addressed metadata pinning for digital
// collectibles. Metadata is committed on-chain by content digest; external
// URIs serve only as retrieval locations and are never trusted as the
// source of truth.
package metapin

import (
	"crypto/sha256"
	"encoding/json"
)

// Digest is the SHA-256 content commitment of a canonical metadata document.
type Digest [sha256.Size]byte

// Attribute is a single metadata trait.
type Attribute struct {
	TraitType string `json:"trait_type"`
	Value     string `json:"value"`
}

// Metadata describes a collectible. The on-chain commitment binds to the
// canonical encoding of this struct, so any change to image or attributes
// changes the digest.
type Metadata struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Image       string      `json:"image"`
	Attributes  []Attribute `json:"attributes,omitempty"`
}

// CanonicalBytes returns the deterministic encoding of m used for hashing.
func CanonicalBytes(m Metadata) ([]byte, error) {
	return json.Marshal(m)
}

// DigestOf computes the content commitment for raw canonical bytes.
func DigestOf(data []byte) Digest {
	return Digest(sha256.Sum256(data))
}

// Commitment computes the content commitment for m.
func Commitment(m Metadata) (Digest, []byte, error) {
	raw, err := CanonicalBytes(m)
	if err != nil {
		return Digest{}, nil, err
	}
	return DigestOf(raw), raw, nil
}
