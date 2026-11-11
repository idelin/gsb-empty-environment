// Package gitaccess issues and enforces read capabilities for Git objects
// stored by an artifact service.
//
// A capability is always derived from an immutable snapshot of a fixed set of
// references. Later reference updates, including replacements of a reference
// with the same name, never widen the object set an already issued capability
// can read. Pack deltas are reconstructed and only returned after their
// reconstructed content passes the Git SHA-1 content digest check.
package gitaccess

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strconv"
)

// OIDSize is the size of a SHA-1 Git object identifier in bytes.
const OIDSize = sha1.Size

// ObjectID is the SHA-1 digest that uniquely names a Git object.
type ObjectID [OIDSize]byte

// ParseOID decodes a 40 character hexadecimal object identifier.
func ParseOID(s string) (ObjectID, error) {
	var id ObjectID
	if len(s) != 2*OIDSize {
		return id, fmt.Errorf("gitaccess: object id %q has wrong length", s)
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return id, fmt.Errorf("gitaccess: object id %q is not hexadecimal: %w", s, err)
	}
	copy(id[:], raw)
	return id, nil
}

// String renders the identifier as lowercase hexadecimal.
func (id ObjectID) String() string {
	return hex.EncodeToString(id[:])
}

// IsZero reports whether the identifier is the all-zero digest.
func (id ObjectID) IsZero() bool {
	return id == ObjectID{}
}

// ObjectType is the Git object type. The numeric values match the type
// encoding used inside packfile entry headers.
type ObjectType int

const (
	// CommitObject names a commit object.
	CommitObject ObjectType = 1
	// TreeObject names a tree object.
	TreeObject ObjectType = 2
	// BlobObject names a blob object.
	BlobObject ObjectType = 3
	// TagObject names an annotated tag object.
	TagObject ObjectType = 4
)

// Packfile entry kinds that are not standalone object types.
const (
	ofsDeltaKind ObjectType = 6
	refDeltaKind ObjectType = 7
)

// String returns the canonical Git name of the object type.
func (t ObjectType) String() string {
	switch t {
	case CommitObject:
		return "commit"
	case TreeObject:
		return "tree"
	case BlobObject:
		return "blob"
	case TagObject:
		return "tag"
	default:
		return fmt.Sprintf("unknown(%d)", int(t))
	}
}

// Object is a fully materialized Git object: its type and uncompressed,
// unframed content.
type Object struct {
	Type    ObjectType
	Content []byte
}

// frameObject renders the on-disk framing of an object:
// "<type> <size>\0<content>".
func frameObject(typ ObjectType, content []byte) []byte {
	frame := make([]byte, 0, len(typ.String())+1+20+1+len(content))
	frame = append(frame, typ.String()...)
	frame = append(frame, ' ')
	frame = strconv.AppendInt(frame, int64(len(content)), 10)
	frame = append(frame, 0)
	frame = append(frame, content...)
	return frame
}

// hashObject computes the content digest of an object exactly as Git does:
// SHA-1 over the framed object.
func hashObject(typ ObjectType, content []byte) ObjectID {
	return ObjectID(sha1.Sum(frameObject(typ, content)))
}
