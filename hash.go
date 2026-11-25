package admission

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
)

// HashSize is the size, in bytes, of every digest used by this package.
const HashSize = sha256.Size

// LeafHashPrefix and NodeHashPrefix are the domain-separation bytes mandated
// by RFC 6962 and by the admission protocol.
const (
	LeafHashPrefix byte = 0
	NodeHashPrefix byte = 1
)

// EmptyRoot is the Merkle root of an empty tree.
func EmptyRoot() []byte {
	sum := sha256.Sum256(nil)
	return sum[:]
}

// HashLeaf hashes a leaf. The input is the raw leaf payload; the 0x00 prefix is
// added here.
func HashLeaf(data []byte) []byte {
	h := sha256.New()
	_, _ = h.Write([]byte{LeafHashPrefix})
	_, _ = h.Write(data)
	return h.Sum(nil)
}

// HashNode hashes an internal node from two already-hashed children. The 0x01
// prefix is added here.
func HashNode(left, right []byte) []byte {
	h := sha256.New()
	_, _ = h.Write([]byte{NodeHashPrefix})
	_, _ = h.Write(left)
	_, _ = h.Write(right)
	return h.Sum(nil)
}

// HashPair is a short alias for HashNode.
func HashPair(left, right []byte) []byte { return HashNode(left, right) }

// Checkpoint identifies a log state by tree size and root digest. A checkpoint
// is never identified by its size alone.
type Checkpoint struct {
	Size uint64 `json:"size"`
	Root []byte `json:"root"`
}

// NewCheckpoint returns a checkpoint with a defensive copy of root.
func NewCheckpoint(size uint64, root []byte) Checkpoint {
	return Checkpoint{Size: size, Root: cloneBytes(root)}
}

// Valid reports whether c is structurally a checkpoint.
func (c Checkpoint) Valid() bool {
	return len(c.Root) == HashSize
}

func (c Checkpoint) normalized() Checkpoint {
	return Checkpoint{Size: c.Size, Root: cloneBytes(c.Root)}
}

// Equal reports whether two checkpoints identify the same tree.
func (c Checkpoint) Equal(other Checkpoint) bool {
	return c.Size == other.Size && bytes.Equal(c.Root, other.Root)
}

// Key is the canonical checkpoint identity used by the evidence graph.
func (c Checkpoint) Key() string {
	return strconv.FormatUint(c.Size, 10) + ":" + hex.EncodeToString(c.Root)
}

func (c Checkpoint) String() string {
	return fmt.Sprintf("%d:%x", c.Size, c.Root)
}

// Module is the Go module identity and content digest supplied by the build
// host. ContentDigest is the digest of the module archive or module content;
// the admission tree leaf binds it together with Path and Version.
type Module struct {
	Path          string `json:"path"`
	Version       string `json:"version"`
	ContentDigest []byte `json:"content_digest"`
	// Digest is an accepted alias for ContentDigest.
	Digest []byte `json:"digest,omitempty"`
}

// LeafData is the canonical raw payload for a module. Callers that already
// have a canonical leaf representation may use Publication.LeafData instead.
//
// The encoding is deliberately unambiguous: each field is length-prefixed or
// fixed-size, and the module digest is encoded as hex.
func (m Module) LeafData() []byte {
	var b bytes.Buffer
	b.WriteString("go-module-admission/v1\n")
	writeField(&b, m.Path)
	writeField(&b, m.Version)
	digest := m.ContentDigest
	if digest == nil {
		digest = m.Digest
	}
	writeField(&b, hex.EncodeToString(digest))
	return b.Bytes()
}

// LeafHash returns the RFC 6962 leaf hash for the module.
func (m Module) LeafHash() []byte { return HashLeaf(m.LeafData()) }

func writeField(b *bytes.Buffer, value string) {
	b.WriteString(strconv.Itoa(len(value)))
	b.WriteByte(':')
	b.WriteString(value)
	b.WriteByte('\n')
}

// ModuleLeaf is the package-level form of Module.LeafData.
func ModuleLeaf(module Module) []byte { return module.LeafData() }

// Publication is a module together with the checkpoint at which its leaf is
// included. Target is the checkpoint being proposed for admission. The proof
// is the Merkle inclusion path from the module leaf at LeafIndex to Target.Root.
type Publication struct {
	Module         Module             `json:"module"`
	Target         Checkpoint         `json:"target"`
	LeafIndex      uint64             `json:"leaf_index"`
	InclusionProof [][]byte           `json:"inclusion_proof"`
	Signatures     []WitnessSignature `json:"signatures,omitempty"`
	LeafData       []byte             `json:"leaf_data,omitempty"`

	// The following aliases make it convenient to consume existing protocol
	// adapters. Target/LeafIndex/InclusionProof are canonical when set.
	Checkpoint Checkpoint `json:"checkpoint,omitempty"`
	Index      uint64     `json:"index,omitempty"`
	Proof      [][]byte   `json:"proof,omitempty"`
}

func (p Publication) checkpoint() Checkpoint {
	if p.Target.Size != 0 || len(p.Target.Root) != 0 {
		return p.Target
	}
	return p.Checkpoint
}

func (p Publication) leafIndex() uint64 {
	if p.Index != 0 {
		return p.Index
	}
	return p.LeafIndex
}

func (p Publication) inclusionProof() [][]byte {
	if p.InclusionProof != nil {
		return p.InclusionProof
	}
	return p.Proof
}

func (p Publication) leafData() ([]byte, error) {
	if p.LeafData == nil {
		return p.Module.LeafData(), nil
	}
	if p.Module.Path != "" || p.Module.Version != "" || len(p.Module.ContentDigest) != 0 || len(p.Module.Digest) != 0 {
		if !bytes.Equal(p.LeafData, p.Module.LeafData()) {
			return nil, fmt.Errorf("leaf data does not match module identity/version/content digest")
		}
	}
	return cloneBytes(p.LeafData), nil
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func cloneHashes(hashes [][]byte) [][]byte {
	if hashes == nil {
		return nil
	}
	out := make([][]byte, len(hashes))
	for i := range hashes {
		out[i] = cloneBytes(hashes[i])
	}
	return out
}

func equalHash(a, b []byte) bool { return len(a) == HashSize && bytes.Equal(a, b) }
