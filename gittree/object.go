// Package gittree verifies single-file membership proofs against a Git tree.
//
// A membership proof is a path through a chain of Git tree objects rooted at
// a trusted object ID. The verifier re-hashes every object it reads, so an
// untrusted object source can never cause an object with the wrong content
// digest to be trusted. Only objects whose digest has been recomputed and
// matched are admitted to the verifier's cache, which means a corrupted
// proof cannot cause another proof to reuse an unverified tree.
package gittree

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
)

// ObjectID is the content digest of a Git object, encoded as 40 (SHA-1) or
// 64 (SHA-256) lowercase hexadecimal characters.
type ObjectID string

// ObjectType names one of the four Git object types.
type ObjectType string

const (
	TypeBlob     ObjectType = "blob"
	TypeTree     ObjectType = "tree"
	TypeCommit   ObjectType = "commit"
	TypeTag      ObjectType = "tag"
	sha1HexLen              = sha1.Size * 2
	sha256HexLen            = sha256.Size * 2
)

// EmptyBlobID is the well-known SHA-1 object ID of the empty blob.
const EmptyBlobID ObjectID = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"

// EmptyTreeID is the well-known SHA-1 object ID of the empty tree.
const EmptyTreeID ObjectID = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Algorithm identifies the hash function used by a repository.
type Algorithm int

const (
	SHA1 Algorithm = iota + 1
	SHA256
)

// String returns the conventional Git hash algorithm name.
func (a Algorithm) String() string {
	switch a {
	case SHA1:
		return "sha1"
	case SHA256:
		return "sha256"
	default:
		return "unknown"
	}
}

// HexLen returns the length of a hex-encoded object ID for the algorithm.
func (a Algorithm) HexLen() int {
	switch a {
	case SHA1:
		return sha1HexLen
	case SHA256:
		return sha256HexLen
	default:
		return 0
	}
}

func (a Algorithm) newHash() hash.Hash {
	switch a {
	case SHA1:
		return sha1.New()
	case SHA256:
		return sha256.New()
	default:
		panic(fmt.Sprintf("gittree: unsupported hash algorithm %d", a))
	}
}

// ParseID validates a hex-encoded object ID and reports its algorithm.
// Mixed or unknown-length IDs are rejected.
func ParseID(s string) (ObjectID, Algorithm, error) {
	id := ObjectID(s)
	switch len(s) {
	case sha1HexLen:
		return id, SHA1, validateHex(s)
	case sha256HexLen:
		return id, SHA256, validateHex(s)
	default:
		return "", 0, fmt.Errorf("gittree: object id %q has unsupported length %d", s, len(s))
	}
}

func validateHex(s string) error {
	for _, c := range []byte(s) {
		isDigit := c >= '0' && c <= '9'
		isLowerHex := c >= 'a' && c <= 'f'
		if !isDigit && !isLowerHex {
			return fmt.Errorf("gittree: object id %q is not lowercase hexadecimal", s)
		}
	}
	return nil
}

// Algorithm reports the hash algorithm implied by the ID's length.
func (id ObjectID) Algorithm() (Algorithm, error) {
	_, algo, err := ParseID(string(id))
	return algo, err
}

// HashObject computes the Git object ID for the given type and content:
//
//	hex(hash("<type> SP <size> NUL <content>"))
func HashObject(algo Algorithm, typ ObjectType, content []byte) ObjectID {
	h := algo.newHash()
	fmt.Fprintf(h, "%s %d\x00", typ, len(content))
	h.Write(content)
	return ObjectID(hex.EncodeToString(h.Sum(nil)))
}

// verifyDigest recomputes the object ID from type and content and compares it
// with the claimed ID. The hash algorithm is taken from the claimed ID, so
// callers cannot be tricked into validating an object under the wrong hash.
func verifyDigest(claimed ObjectID, typ ObjectType, content []byte) (Algorithm, error) {
	id, algo, err := ParseID(string(claimed))
	if err != nil {
		return 0, err
	}
	actual := HashObject(algo, typ, content)
	if actual != id {
		return 0, fmt.Errorf("%w: claimed %s, computed %s", ErrDigestMismatch, id, actual)
	}
	return algo, nil
}

var (
	// ErrDigestMismatch indicates that an object's recomputed content digest
	// differs from the ID under which it was offered.
	ErrDigestMismatch = errors.New("gittree: object digest mismatch")
	// ErrObjectNotFound indicates that the source does not hold an object.
	ErrObjectNotFound = errors.New("gittree: object not found")
)
