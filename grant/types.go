package grant

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
)

// Status is the lifecycle state of a grant.
type Status uint8

const (
	StatusUnknown Status = iota
	// StatusPending is persisted but not yet active. Its token is usable only
	// inside the caller-authorized propagation window.
	StatusPending
	// StatusActive is the normal usable state.
	StatusActive
	// StatusRetired is terminal; every token minted for an earlier generation
	// is invalid.
	StatusRetired
)

func (s Status) String() string {
	switch s {
	case StatusPending:
		return "Pending"
	case StatusActive:
		return "Active"
	case StatusRetired:
		return "Retired"
	default:
		return "Unknown"
	}
}

// Operation is a KMS operation name, e.g. Decrypt, Encrypt, GenerateDataKey.
type Operation string

const (
	OpDecrypt         Operation = "Decrypt"
	OpEncrypt         Operation = "Encrypt"
	OpGenerateDataKey Operation = "GenerateDataKey"
	OpSign            Operation = "Sign"
)

// Principal is an authenticated caller identity.
type Principal string

// Context is an encryption/authorization context. Keys are unique within a
// value; callers must not rely on repeated keys.
type Context map[string]string

// Grant is the full grant record. Every field participates in Digest, so a
// token is cryptographically bound to the complete grant, including its
// generation and status.
type Grant struct {
	GrantID           string
	KeyID             string
	KeyVersion        uint64
	GranteePrincipal  Principal
	RetiringPrincipal Principal
	Operations        []Operation
	// Constraints are equality constraints: every listed key/value must be
	// present and equal in the call context.
	Constraints Context
	// Subset, when non-nil, requires the call context to contain every listed
	// key/value; additional caller-supplied keys are allowed.
	Subset Context
	// Generation is monotonically incremented on every Retire.
	Generation uint64
	Status     Status
}

// Clone returns a deep copy that is safe for callers to retain.
func (g *Grant) Clone() *Grant {
	if g == nil {
		return nil
	}
	cp := *g
	cp.Operations = append([]Operation(nil), g.Operations...)
	cp.Constraints = g.Constraints.Clone()
	cp.Subset = g.Subset.Clone()
	return &cp
}

// Clone returns a deep copy of the context.
func (c Context) Clone() Context {
	if c == nil {
		return nil
	}
	cp := make(Context, len(c))
	for k, v := range c {
		cp[k] = v
	}
	return cp
}

// writeString writes a length-prefixed UTF-8 string. The 4-byte big-endian
// length makes the encoding unambiguous even when keys or values contain
// arbitrary bytes.
func writeString(b []byte, s string) []byte {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(s)))
	b = append(b, lenBuf[:]...)
	b = append(b, s...)
	return b
}

func writeUint64(b []byte, v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return append(b, buf[:]...)
}

// canonicalContext encodes a context canonically: keys are sorted, then each
// key and value is length-prefixed. A leading count and a domain tag prevent
// collisions between a nil/empty context and other encodings.
func canonicalContext(tag byte, c Context) []byte {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	b := []byte{tag}
	b = writeUint64(b, uint64(len(keys)))
	for _, k := range keys {
		b = writeString(b, k)
		b = writeString(b, c[k])
	}
	return b
}

func canonicalOps(ops []Operation) []byte {
	sorted := append([]Operation(nil), ops...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	b := make([]byte, 0, 1+8*len(sorted))
	b = writeUint64(b, uint64(len(sorted)))
	for _, op := range sorted {
		b = writeString(b, string(op))
	}
	return b
}

// Digest returns the canonical SHA-256 digest over the complete grant.
//
// The encoding is domain-separated per field so that no two distinct grants
// produce the same digest. Status and generation are included, which is what
// makes every token issued before a Retire cryptographically invalid: a
// retired record has a different digest.
func (g *Grant) Digest() [sha256.Size]byte {
	b := make([]byte, 0, 256)
	b = append(b, 'G') // domain separator for "grant digest v1"
	b = writeString(b, g.GrantID)
	b = writeString(b, g.KeyID)
	b = writeUint64(b, g.KeyVersion)
	b = writeString(b, string(g.GranteePrincipal))
	b = writeString(b, string(g.RetiringPrincipal))
	b = append(b, canonicalOps(g.Operations)...)
	b = append(b, canonicalContext('E', g.Constraints)...)
	b = append(b, canonicalContext('S', g.Subset)...)
	b = writeUint64(b, g.Generation)
	b = append(b, byte(g.Status))
	return sha256.Sum256(b)
}
