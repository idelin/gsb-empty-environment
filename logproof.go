package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

const logCheckpointDomain = "netsec/log-checkpoint/v1"

// LogCheckpoint identifies a transparency-log tree state. A checkpoint only
// grows. Its digest is what contacts exchange in an observation receipt.
type LogCheckpoint struct {
	LogID    string `json:"logID,omitempty"`
	TreeSize uint64 `json:"treeSize"`
	RootHash Digest `json:"rootHash"`
}

// Checkpoint is an alias for LogCheckpoint.
type Checkpoint = LogCheckpoint

// Validate checks basic checkpoint shape.
func (c LogCheckpoint) Validate() error {
	if c.TreeSize > uint64(^uint(0)>>1) {
		return fmt.Errorf("%w: tree is too large", ErrProofInvalid)
	}
	if c.TreeSize == 0 {
		if c.RootHash != ZeroDigest && c.RootHash != MerkleRoot(nil) {
			return fmt.Errorf("%w: empty tree has an invalid root", ErrProofInvalid)
		}
		return nil
	}
	if c.RootHash == ZeroDigest {
		return fmt.Errorf("%w: non-empty tree has a zero root", ErrProofInvalid)
	}
	return nil
}

// Digest returns a domain-separated digest binding the log ID, tree size, and
// root. Receipts use this value so they do not need to carry the full root.
func (c LogCheckpoint) Digest() Digest {
	buf := make([]byte, 0, 8+len(logCheckpointDomain)+4+len(c.LogID)+8+len(c.RootHash))
	buf = appendLengthPrefixed(buf, []byte(logCheckpointDomain))
	buf = appendLengthPrefixed(buf, []byte(c.LogID))
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], c.TreeSize)
	buf = append(buf, size[:]...)
	buf = append(buf, c.RootHash[:]...)
	return sha256.Sum256(buf)
}

// CheckpointDigest is an alias for Digest.
func (c LogCheckpoint) CheckpointDigest() Digest {
	return c.Digest()
}

// Equal reports whether two checkpoints are identical.
func (c LogCheckpoint) Equal(other LogCheckpoint) bool {
	return c.LogID == other.LogID && c.TreeSize == other.TreeSize && c.RootHash == other.RootHash
}

// LogInclusionProof proves that a list digest is a leaf in the checkpointed
// transparency-log tree.
type LogInclusionProof struct {
	Checkpoint LogCheckpoint `json:"checkpoint"`
	LeafIndex  uint64        `json:"leafIndex"`
	LeafDigest Digest        `json:"leafDigest"`
	AuditPath  []Digest      `json:"auditPath"`
}

// InclusionProof is an alias for LogInclusionProof.
type InclusionProof = LogInclusionProof

// ProofVerifier is the host-injected transparency-log policy hook. The client
// always validates the Merkle path itself; this callback additionally verifies
// checkpoint signatures, log identity, witness policy, or freshness.
//
// Returning ErrProofPending or ErrLogUnavailable stages the update without
// committing it. Any other error rejects the proof.
type ProofVerifier interface {
	VerifyProof(checkpoint LogCheckpoint, proof LogInclusionProof, listDigest Digest) error
}

// ProofVerifierFunc adapts a function to ProofVerifier.
type ProofVerifierFunc func(checkpoint LogCheckpoint, proof LogInclusionProof, listDigest Digest) error

func (f ProofVerifierFunc) VerifyProof(checkpoint LogCheckpoint, proof LogInclusionProof, listDigest Digest) error {
	if f == nil {
		return ErrLogUnavailable
	}
	return f(checkpoint, proof, listDigest)
}

// BuiltinProofVerifier performs the local Merkle-path check. It is used when
// no host callback is supplied.
type BuiltinProofVerifier struct{}

func (BuiltinProofVerifier) VerifyProof(checkpoint LogCheckpoint, proof LogInclusionProof, listDigest Digest) error {
	if proof.LeafDigest != listDigest {
		return ErrProofMismatch
	}
	if !proof.Checkpoint.Equal(checkpoint) {
		return ErrProofInvalid
	}
	return VerifyInclusionProof(proof)
}

// LogLeafHash hashes a device-list digest for inclusion in the log. The
// 0x00 prefix is the RFC 6962 leaf domain separator.
func LogLeafHash(listDigest Digest) Digest {
	buf := make([]byte, 1+len(listDigest))
	buf[0] = 0
	copy(buf[1:], listDigest[:])
	return sha256.Sum256(buf)
}

// HashLogNodes hashes two child nodes with the RFC 6962 internal-node domain
// separator.
func HashLogNodes(left, right Digest) Digest {
	buf := make([]byte, 1+len(left)+len(right))
	buf[0] = 1
	copy(buf[1:], left[:])
	copy(buf[1+len(left):], right[:])
	return sha256.Sum256(buf)
}

// HashPair is an alias for HashLogNodes.
func HashPair(left, right Digest) Digest {
	return HashLogNodes(left, right)
}

// MerkleRoot computes the RFC 6962 Merkle tree root for the supplied device
// list digests. An empty log has the SHA-256 hash of the empty string.
func MerkleRoot(leaves []Digest) Digest {
	if len(leaves) == 0 {
		return sha256.Sum256(nil)
	}
	return merkleRootRange(leaves, 0, len(leaves))
}

func merkleRootRange(leaves []Digest, start, count int) Digest {
	if count == 1 {
		return LogLeafHash(leaves[start])
	}
	leftCount := largestPowerOfTwoLessThan(count)
	left := merkleRootRange(leaves, start, leftCount)
	right := merkleRootRange(leaves, start+leftCount, count-leftCount)
	return HashLogNodes(left, right)
}

func largestPowerOfTwoLessThan(n int) int {
	k := 1
	for k<<1 < n {
		k <<= 1
	}
	return k
}

// BuildInclusionProof constructs an inclusion proof for the leaf at index in
// the supplied leaves and attaches the provided checkpoint.
func BuildInclusionProof(leaves []Digest, index uint64, checkpoint LogCheckpoint) (LogInclusionProof, error) {
	if len(leaves) == 0 {
		return LogInclusionProof{}, fmt.Errorf("%w: empty log", ErrProofInvalid)
	}
	if index >= uint64(len(leaves)) {
		return LogInclusionProof{}, fmt.Errorf("%w: leaf index %d is outside log", ErrProofInvalid, index)
	}
	path := make([]Digest, 0, 64)
	buildAuditPath(leaves, 0, len(leaves), index, &path)
	return LogInclusionProof{
		Checkpoint: checkpoint,
		LeafIndex:  index,
		LeafDigest: leaves[index],
		AuditPath:  path,
	}, nil
}

func buildAuditPath(leaves []Digest, start, count int, index uint64, path *[]Digest) {
	if count == 1 {
		return
	}
	leftCount := largestPowerOfTwoLessThan(count)
	if index < uint64(leftCount) {
		*path = append(*path, merkleRootRange(leaves, start+leftCount, count-leftCount))
		buildAuditPath(leaves, start, leftCount, index, path)
		return
	}
	*path = append(*path, merkleRootRange(leaves, start, leftCount))
	buildAuditPath(leaves, start+leftCount, count-leftCount, index-uint64(leftCount), path)
}

// VerifyInclusionProof verifies the RFC 6962 Merkle path against the
// checkpoint root.
func VerifyInclusionProof(proof LogInclusionProof) error {
	if err := proof.Checkpoint.Validate(); err != nil {
		return err
	}
	if proof.Checkpoint.TreeSize == 0 || proof.LeafIndex >= proof.Checkpoint.TreeSize {
		return fmt.Errorf("%w: leaf index %d, tree size %d", ErrProofInvalid, proof.LeafIndex, proof.Checkpoint.TreeSize)
	}
	if len(proof.AuditPath) > 64 {
		return fmt.Errorf("%w: audit path is too long", ErrProofInvalid)
	}
	root, err := rootFromAuditPath(LogLeafHash(proof.LeafDigest), proof.LeafIndex, proof.Checkpoint.TreeSize, proof.AuditPath)
	if err != nil {
		return err
	}
	if root != proof.Checkpoint.RootHash {
		return fmt.Errorf("%w: computed root %s, checkpoint root %s", ErrProofInvalid, root, proof.Checkpoint.RootHash)
	}
	return nil
}

func rootFromAuditPath(leaf Digest, index uint64, treeSize uint64, path []Digest) (Digest, error) {
	if treeSize == 0 {
		return Digest{}, fmt.Errorf("%w: empty tree", ErrProofInvalid)
	}
	if treeSize == 1 {
		if len(path) != 0 {
			return Digest{}, fmt.Errorf("%w: unexpected audit path for a one-leaf tree", ErrProofInvalid)
		}
		if index != 0 {
			return Digest{}, fmt.Errorf("%w: invalid index for a one-leaf tree", ErrProofInvalid)
		}
		return leaf, nil
	}
	if len(path) == 0 {
		return Digest{}, fmt.Errorf("%w: truncated audit path", ErrProofInvalid)
	}
	leftCount := uint64(largestPowerOfTwoLessThan(int(treeSize)))
	if index < leftCount {
		child, err := rootFromAuditPath(leaf, index, leftCount, path[1:])
		if err != nil {
			return Digest{}, err
		}
		return HashLogNodes(child, path[0]), nil
	}
	child, err := rootFromAuditPath(leaf, index-leftCount, treeSize-leftCount, path[1:])
	if err != nil {
		return Digest{}, err
	}
	return HashLogNodes(path[0], child), nil
}

// NewCheckpoint constructs a checkpoint over the supplied leaves.
func NewCheckpoint(logID string, leaves []Digest) LogCheckpoint {
	return LogCheckpoint{
		LogID:    logID,
		TreeSize: uint64(len(leaves)),
		RootHash: MerkleRoot(leaves),
	}
}

// ValidateListProof performs the local list/leaf and Merkle checks.
func ValidateListProof(proof LogInclusionProof, listDigest Digest) error {
	if proof.LeafDigest != listDigest {
		return ErrProofMismatch
	}
	return VerifyInclusionProof(proof)
}
