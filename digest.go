package readproof

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// Digest is the SHA-256 identity of a canonically encoded manifest node.
type Digest [sha256.Size]byte

// String returns the lowercase hexadecimal digest.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// ParseDigest decodes a 64-character hexadecimal node identity.
func ParseDigest(s string) (Digest, error) {
	var d Digest
	if len(s) != hex.EncodedLen(len(d)) {
		return d, errors.New("readproof: digest must contain 64 hexadecimal characters")
	}
	if _, err := hex.Decode(d[:], []byte(s)); err != nil {
		return d, fmt.Errorf("readproof: invalid digest: %w", err)
	}
	return d, nil
}

const (
	canonicalLeafTag     byte = 0
	canonicalInternalTag byte = 1
)

// Canonical returns the exact byte string whose SHA-256 is the node identity.
//
// The encoding is:
//
//	leaf:     0x00 || uint64be(block) || uint64be(length)
//	internal: 0x01 || uint64be(child count) ||
//	          (digest[32] || uint64be(offset) || uint64be(length))...
//
// Child order is significant.  The encoding deliberately contains no
// version-specific domain prefix; the leading tag separates the two forms.
func (n Node) Canonical() ([]byte, error) {
	switch n.Kind {
	case LeafNode:
		out := make([]byte, 1+8+8)
		out[0] = canonicalLeafTag
		binary.BigEndian.PutUint64(out[1:9], n.Leaf.Block)
		binary.BigEndian.PutUint64(out[9:17], n.Leaf.Length)
		return out, nil
	case InternalNode:
		const childSize = sha256.Size + 8 + 8
		if uint64(len(n.Children)) > (^uint64(0)-17)/childSize {
			return nil, fmt.Errorf("%w: too many children", ErrLengthOverflow)
		}
		out := make([]byte, 1+8+len(n.Children)*childSize)
		out[0] = canonicalInternalTag
		binary.BigEndian.PutUint64(out[1:9], uint64(len(n.Children)))
		at := 9
		for _, child := range n.Children {
			copy(out[at:at+sha256.Size], child.Digest[:])
			at += sha256.Size
			binary.BigEndian.PutUint64(out[at:at+8], child.Offset)
			at += 8
			binary.BigEndian.PutUint64(out[at:at+8], child.Length)
			at += 8
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: unknown node kind %d", ErrInvalidNode, n.Kind)
	}
}

// Digest returns the SHA-256 identity of the canonical encoding.
func (n Node) Digest() (Digest, error) {
	encoded, err := n.Canonical()
	if err != nil {
		return Digest{}, err
	}
	return Digest(sha256.Sum256(encoded)), nil
}

// MustDigest is Digest for statically constructed, already validated nodes.
// It panics if the node cannot be canonically encoded.
func (n Node) MustDigest() Digest {
	d, err := n.Digest()
	if err != nil {
		panic(err)
	}
	return d
}
