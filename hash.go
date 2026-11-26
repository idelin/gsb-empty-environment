package kt

import (
	"crypto/sha256"
	"encoding/binary"
)

// Domain separation tags. Every hash invocation is prefixed with a unique
// one-byte tag so that a node of one structural role can never be replayed as
// a node of another role.
const (
	domainEmpty      byte = 0x01
	domainLeaf       byte = 0x02
	domainInterior   byte = 0x03
	domainEpochRoot  byte = 0x04
	domainMigration  byte = 0x05
	domainAuthRecord byte = 0x06
	domainSignedRoot byte = 0x07
	domainForkProof  byte = 0x08
)

// Hash is the 32-byte output of the domain-separated hash function.
type Hash [32]byte

func writeU16(b []byte, v int)    { binary.BigEndian.PutUint16(b, uint16(v)) }
func writeU32(b []byte, v uint32) { binary.BigEndian.PutUint32(b, v) }
func writeU64(b []byte, v uint64) { binary.BigEndian.PutUint64(b, v) }

// taggedHash computes SHA-256 over a length-prefixed, unambiguous encoding of
// its inputs: tag || len(prefix-encoded-part) ... Every variable-length field
// is length-prefixed so distinct argument tuples never collide.
func taggedHash(tag byte, parts ...[]byte) Hash {
	h := sha256.New()
	h.Write([]byte{tag})
	var lbuf [8]byte
	for _, p := range parts {
		writeU64(lbuf[:], uint64(len(p)))
		h.Write(lbuf[:])
		h.Write(p)
	}
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

func u16Bytes(v int) []byte {
	b := make([]byte, 2)
	writeU16(b, v)
	return b
}

func u32Bytes(v uint32) []byte {
	b := make([]byte, 4)
	writeU32(b, v)
	return b
}

func u64Bytes(v uint64) []byte {
	b := make([]byte, 8)
	writeU64(b, v)
	return b
}

// hashPrefix encodes a bit prefix as its canonical MarshalBinary form.
func hashPrefix(p BitPrefix) []byte {
	enc, err := p.MarshalBinary()
	if err != nil {
		panic(err)
	}
	return enc
}

// EmptyHash is the domain-tagged commitment to an empty subtree at a given
// (height, prefix, epoch). Empty subtrees are still bound to their position so
// that empty nodes cannot be relocated within or across trees.
func EmptyHash(epoch uint32, height int, prefix BitPrefix) Hash {
	return taggedHash(domainEmpty,
		u32Bytes(epoch),
		u16Bytes(height),
		hashPrefix(prefix))
}

// LeafHash binds a leaf node's type (leaf), height (always the label length),
// full bit-prefix label, epoch number and value commitment.
func LeafHash(epoch uint32, label BitPrefix, valueCommitment Hash) Hash {
	return taggedHash(domainLeaf,
		u32Bytes(epoch),
		u16Bytes(label.Len()),
		hashPrefix(label),
		valueCommitment[:])
}

// InteriorHash binds an interior node's type (interior), height (number of
// levels beneath it), bit prefix and epoch number together with its two
// children in fixed 0/1 order.
func InteriorHash(epoch uint32, height int, prefix BitPrefix, left, right Hash) Hash {
	return taggedHash(domainInterior,
		u32Bytes(epoch),
		u16Bytes(height),
		hashPrefix(prefix),
		left[:],
		right[:])
}
