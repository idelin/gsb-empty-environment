package netsec

import (
	"encoding/binary"
)

const treeHeadDomain = "hypercore-tree-head/v1\x00"

// TreeHead is the authenticated identity of an append-only log version.
type TreeHead struct {
	// Fork identifies a log lineage. A fork increase deliberately abandons
	// all state derived from earlier forks.
	Fork uint64
	// Length is the number of leaves (blocks) in the tree.
	Length uint64
	// Root is the RFC 9162 Merkle root over those leaves.
	Root Hash
}

// Equal reports whether both heads have identical fork, length, and root.
func (h TreeHead) Equal(other TreeHead) bool {
	return h.Fork == other.Fork && h.Length == other.Length && h.Root == other.Root
}

// SigningBytes returns a canonical, domain-separated encoding of the head for
// signature schemes that use it. The encoding binds the fork, length, and
// root, so a signature for one fork cannot be replayed as another fork.
func (h TreeHead) SigningBytes() []byte {
	out := make([]byte, 0, len(treeHeadDomain)+8+8+HashSize)
	out = append(out, treeHeadDomain...)
	out = binary.BigEndian.AppendUint64(out, h.Fork)
	out = binary.BigEndian.AppendUint64(out, h.Length)
	out = append(out, h.Root[:]...)
	return out
}

// SignedTreeHead is a tree head together with the writer's signature.
type SignedTreeHead struct {
	Head      TreeHead
	Signature []byte
}

// Clone returns a deep copy of s.
func (s SignedTreeHead) Clone() SignedTreeHead {
	return SignedTreeHead{
		Head:      s.Head,
		Signature: cloneBytes(s.Signature),
	}
}

// SignatureVerifier authenticates a tree head and its detached signature.
//
// Implementations must authenticate the entire TreeHead, especially Fork.
// This is what prevents an attacker from relabeling a replayed older signed
// head as belonging to a newer fork. Returning nil means the signature is
// valid.
type SignatureVerifier func(head TreeHead, signature []byte) error

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}
