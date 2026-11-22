package disclosure

import (
	"crypto/ed25519"
	"fmt"
)

const (
	minInt64  = -1 << 63
	maxInt64  = 1<<63 - 1
	maxUint64 = ^uint64(0)
)

// Query asks for all effective records of Tenant whose event-time interval
// intersects [From, To]. Bounds are inclusive.
type Query struct {
	Tenant string `json:"tenant"`
	From   int64  `json:"from"`
	To     int64  `json:"to"`
}

// Validate checks the query shape.
func (q Query) Validate() error {
	if q.Tenant == "" || q.From > q.To {
		return ErrInvalidQuery
	}
	return nil
}

// Checkpoint is the signed commitment to one globally appended log prefix and
// its effective-record index. BaseSeq is the first sequence in this log, Size
// is the number of leaves, and IndexSize is the number of effective records.
type Checkpoint struct {
	BaseSeq   uint64 `json:"base_seq"`
	Size      uint64 `json:"size"`
	LogRoot   Hash   `json:"log_root"`
	IndexSize uint64 `json:"index_size"`
	IndexRoot Hash   `json:"index_root"`
	Signature []byte `json:"signature,omitempty"`
}

// LastSeq returns the last committed sequence, if the checkpoint is non-empty.
func (c Checkpoint) LastSeq() (uint64, bool) {
	if c.Size == 0 {
		return 0, false
	}
	return c.BaseSeq + c.Size - 1, true
}

// Digest hashes the checkpoint contents other than the signature. Signatures
// are made over this digest.
func (c Checkpoint) Digest() Hash {
	var e encoder
	e.str("audit-checkpoint/v1")
	e.u64(c.BaseSeq)
	e.u64(c.Size)
	e.hash(c.LogRoot)
	e.u64(c.IndexSize)
	e.hash(c.IndexRoot)
	return HashBytes(e.buf)
}

// Validate performs structural checkpoint checks. Authentication is a
// separate, caller-supplied step.
func (c Checkpoint) Validate() error {
	if c.Size == 0 {
		if c.LogRoot != (Hash{}) {
			return fmt.Errorf("%w: empty log has a non-empty root", ErrInvalidCheckpoint)
		}
	} else if c.BaseSeq > maxUint64-c.Size+1 {
		return fmt.Errorf("%w: sequence range overflows", ErrInvalidCheckpoint)
	} else if c.LogRoot.IsZero() {
		return fmt.Errorf("%w: non-empty log has an empty root", ErrInvalidCheckpoint)
	}
	if c.IndexSize == 0 {
		if c.IndexRoot != (Hash{}) {
			return fmt.Errorf("%w: empty index has a non-empty root", ErrInvalidCheckpoint)
		}
	} else if c.IndexRoot.IsZero() {
		return fmt.Errorf("%w: non-empty index has an empty root", ErrInvalidCheckpoint)
	}
	if c.IndexSize > c.Size {
		return fmt.Errorf("%w: index is larger than the log prefix", ErrInvalidCheckpoint)
	}
	return nil
}

// BuildCheckpoint builds an unsigned checkpoint from a complete log. Leaves
// must form a contiguous global sequence and an unambiguous correction chain.
func BuildCheckpoint(leaves []Leaf) (Checkpoint, error) {
	view, err := prepareLog(leaves)
	if err != nil {
		return Checkpoint{}, err
	}
	index, err := buildIndex(view)
	if err != nil {
		return Checkpoint{}, err
	}
	return Checkpoint{
		BaseSeq:   view.base,
		Size:      uint64(len(view.leaves)),
		LogRoot:   merkleRoot(view.leafHashes()),
		IndexSize: uint64(len(index)),
		IndexRoot: merkleRoot(indexHashes(index)),
	}, nil
}

// SignCheckpoint signs the checkpoint digest with Ed25519.
func SignCheckpoint(privateKey ed25519.PrivateKey, checkpoint *Checkpoint) error {
	if len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("disclosure: invalid Ed25519 private key")
	}
	if checkpoint == nil {
		return ErrInvalidCheckpoint
	}
	if err := checkpoint.Validate(); err != nil {
		return err
	}
	digest := checkpoint.Digest()
	checkpoint.Signature = ed25519.Sign(privateKey, digest[:])
	return nil
}

// Ed25519CheckpointVerifier returns a CheckpointVerifier for the supplied
// checkpoint-signing public key.
func Ed25519CheckpointVerifier(publicKey ed25519.PublicKey) func(Checkpoint) error {
	return func(checkpoint Checkpoint) error {
		if len(publicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: invalid Ed25519 public key", ErrCheckpointAuthentication)
		}
		digest := checkpoint.Digest()
		if len(checkpoint.Signature) != ed25519.SignatureSize ||
			!ed25519.Verify(publicKey, digest[:], checkpoint.Signature) {
			return ErrCheckpointAuthentication
		}
		return nil
	}
}
