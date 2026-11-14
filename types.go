package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"
)

// ExportID is the UUID of one export incarnation.  The UUID identifies the
// tenant namespace; the export generation in a handle identifies the lifetime
// of that UUID.
type ExportID [16]byte

// NewExportID returns a random RFC 4122 version 4 UUID.
func NewExportID() (ExportID, error) {
	var id ExportID
	if _, err := rand.Read(id[:]); err != nil {
		return ExportID{}, fmt.Errorf("netsec: generate export UUID: %w", err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id, nil
}

// ParseExportID parses a canonical UUID (with or without hyphens).
func ParseExportID(s string) (ExportID, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), "-", "")
	if len(s) != 32 {
		return ExportID{}, fmt.Errorf("netsec: invalid export UUID %q", s)
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return ExportID{}, fmt.Errorf("netsec: invalid export UUID: %w", err)
	}
	var id ExportID
	copy(id[:], raw)
	return id, nil
}

// String returns the canonical lowercase UUID representation.
func (id ExportID) String() string {
	var b [36]byte
	hex.Encode(b[0:8], id[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], id[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], id[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], id[8:10])
	b[23] = '-'
	hex.Encode(b[24:36], id[10:16])
	return string(b[:])
}

// IsZero reports whether the UUID is the all-zero UUID.
func (id ExportID) IsZero() bool {
	return id == ExportID{}
}

// StableObjectID is the backend's immutable object identity.  It must not be
// derived from a reusable (fsid, inode) pair.
type StableObjectID string

// ObjectType is the NFS object type committed by a file handle.
type ObjectType uint8

const (
	ObjectTypeFile ObjectType = iota + 1
	ObjectTypeDirectory
	ObjectTypeSymlink
	ObjectTypeSpecial
)

// Convenience aliases for callers that prefer the shorter spelling.
const (
	TypeFile      = ObjectTypeFile
	TypeDirectory = ObjectTypeDirectory
	TypeSymlink   = ObjectTypeSymlink
	TypeSpecial   = ObjectTypeSpecial
)

func (t ObjectType) valid() bool {
	return t >= ObjectTypeFile && t <= ObjectTypeSpecial
}

func (t ObjectType) String() string {
	switch t {
	case ObjectTypeFile:
		return "file"
	case ObjectTypeDirectory:
		return "directory"
	case ObjectTypeSymlink:
		return "symlink"
	case ObjectTypeSpecial:
		return "special"
	default:
		return fmt.Sprintf("object-type(%d)", uint8(t))
	}
}

// Op is a bit set of NFS operations checked against the tenant policy carried
// by a handle.  A handle is never a bearer token for an operation that the
// current policy does not allow.
type Op uint8

const (
	OpLookup Op = 1 << iota
	OpOpen
	OpRead
	OpWrite
)

// OpAll is the union of all operations understood by this package.
const OpAll = OpLookup | OpOpen | OpRead | OpWrite

// Aliases for callers that use an Access* naming convention.
const (
	AccessLookup = OpLookup
	AccessOpen   = OpOpen
	AccessRead   = OpRead
	AccessWrite  = OpWrite
)

func (o Op) valid() bool {
	return o != 0 && o&^OpAll == 0
}

func (o Op) String() string {
	if o == 0 {
		return "none"
	}
	names := make([]string, 0, 4)
	if o&OpLookup != 0 {
		names = append(names, "lookup")
	}
	if o&OpOpen != 0 {
		names = append(names, "open")
	}
	if o&OpRead != 0 {
		names = append(names, "read")
	}
	if o&OpWrite != 0 {
		names = append(names, "write")
	}
	return strings.Join(names, "|")
}

// Policy is the tenant policy committed by a handle.  Version permits an
// application to rotate policy semantics without changing Tenant or Allowed.
type Policy struct {
	Tenant  string
	Version uint64
	Allowed Op
}

// Allows reports whether every operation in op is permitted.
func (p Policy) Allows(op Op) bool {
	return op.valid() && op&^p.Allowed == 0
}

// Digest returns the canonical digest placed in a handle.  It is
// domain-separated and length-prefixes the tenant name.
func (p Policy) Digest() [32]byte {
	h := sha256.New()
	writeDigestString(h, "netsec/nfs-handle/policy/v1")
	writeDigestString(h, p.Tenant)
	writeDigestUint64(h, p.Version)
	writeDigestUint64(h, uint64(p.Allowed))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func writeDigestString(h hash.Hash, s string) {
	writeDigestUint64(h, uint64(len(s)))
	_, _ = h.Write([]byte(s))
}

func writeDigestUint64(h hash.Hash, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	_, _ = h.Write(b[:])
}

// Location is an NFS-visible (fsid, inode) alias.  Locations may be reused by
// a completely different backend object and therefore never identify an
// object on their own.
type Location struct {
	Fsid  uint64
	Inode uint64
}

// Claims is the authenticated metadata that is encrypted into a handle.
// Decoding a handle intentionally does not expose Claims; callers receive an
// opaque Capability and must pass it through Manager for authorization.
type Claims struct {
	ExportID         ExportID
	ExportGeneration uint64
	ObjectID         StableObjectID
	ObjectGeneration uint64
	ObjectType       ObjectType
	PolicyDigest     [32]byte
}

// ExportInfo is the public, non-secret description of an export.
type ExportInfo struct {
	ID         ExportID
	Tenant     string
	Fsid       uint64
	Generation uint64
	Active     bool
}

// ObjectInfo is the public, non-secret description of an object.  Returning it
// after an authorized operation is safe; the handle itself remains opaque.
type ObjectInfo struct {
	ExportID   ExportID
	StableID   StableObjectID
	Generation uint64
	Type       ObjectType
	Policy     Policy
	Links      []Location
}
