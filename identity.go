package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// Digest is a SHA-256 digest.
type Digest [sha256.Size]byte

// DigestBytes returns the SHA-256 digest of b.
func DigestBytes(b []byte) Digest {
	return Digest(sha256.Sum256(b))
}

// ParseDigest parses a 64-character hexadecimal SHA-256 digest.
func ParseDigest(s string) (Digest, error) {
	var d Digest
	b, err := hex.DecodeString(s)
	if err != nil {
		return d, fmt.Errorf("netsec: parse digest: %w", err)
	}
	if len(b) != sha256.Size {
		return d, fmt.Errorf("netsec: parse digest: got %d bytes, want %d", len(b), sha256.Size)
	}
	copy(d[:], b)
	return d, nil
}

func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// IsZero reports whether d is the all-zero digest.
func (d Digest) IsZero() bool {
	return d == Digest{}
}

// MarshalText implements encoding.TextMarshaler.
func (d Digest) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Digest) UnmarshalText(text []byte) error {
	parsed, err := ParseDigest(string(text))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Identity is the complete security identity of a cached STS result.
//
// In particular, RoleID is the exact role identifier and must not be replaced
// by a role display name. ProviderGeneration identifies one immutable version
// of the credential-provider configuration.
type Identity struct {
	TenantID           string
	RoleID             string
	PolicyDigest       Digest
	SessionTagsDigest  Digest
	ExternalIdentityID string
	Audience           string
	ProviderGeneration uint64
}

// Validate checks the fields that must not be empty for an identity to be
// usable. An empty tenant or role is never a safe cache key component.
func (i Identity) Validate() error {
	switch {
	case i.TenantID == "":
		return errors.New("netsec: identity has empty tenant ID")
	case i.RoleID == "":
		return errors.New("netsec: identity has empty role ID")
	case i.PolicyDigest.IsZero():
		return errors.New("netsec: identity has zero policy digest")
	case i.SessionTagsDigest.IsZero():
		return errors.New("netsec: identity has zero session-tags digest")
	case i.ExternalIdentityID == "":
		return errors.New("netsec: identity has empty external identity ID")
	case i.Audience == "":
		return errors.New("netsec: identity has empty audience")
	default:
		return nil
	}
}

const cacheKeyDomain = "netsec.credential-cache.identity.v1"

// CacheKey returns the canonical, collision-resistant cache key for i.
//
// Every field is encoded with a fixed tag and a big-endian uint64 length. The
// generation is encoded as one big-endian uint64 value. The complete encoding
// is hashed with SHA-256.
func (i Identity) CacheKey() Digest {
	return DigestBytes(i.CanonicalBytes())
}

// Key is an alias for CacheKey.
func (i Identity) Key() Digest { return i.CacheKey() }

// CanonicalBytes returns the exact byte string that CacheKey hashes.
func (i Identity) CanonicalBytes() []byte {
	var dst []byte
	dst = appendTaggedBytes(dst, "domain", []byte(cacheKeyDomain))
	dst = appendTaggedBytes(dst, "tenant-id", []byte(i.TenantID))
	dst = appendTaggedBytes(dst, "role-id", []byte(i.RoleID))
	dst = appendTaggedBytes(dst, "policy-digest", i.PolicyDigest[:])
	dst = appendTaggedBytes(dst, "session-tags-digest", i.SessionTagsDigest[:])
	dst = appendTaggedBytes(dst, "external-identity-id", []byte(i.ExternalIdentityID))
	dst = appendTaggedBytes(dst, "audience", []byte(i.Audience))

	var generation [8]byte
	binary.BigEndian.PutUint64(generation[:], i.ProviderGeneration)
	dst = appendTaggedBytes(dst, "provider-generation", generation[:])
	return dst
}

func appendTaggedBytes(dst []byte, tag string, value []byte) []byte {
	dst = binary.BigEndian.AppendUint64(dst, uint64(len(tag)))
	dst = append(dst, tag...)
	dst = binary.BigEndian.AppendUint64(dst, uint64(len(value)))
	dst = append(dst, value...)
	return dst
}
