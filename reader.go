package netsec

import (
	"bytes"
	"fmt"
	"sync"
)

// Reader tracks the newest verified tree head for one append-only log and
// stores only blocks whose inclusion proofs reduce to that head's root.
//
// A Reader is safe for concurrent use.
type Reader struct {
	mu       sync.RWMutex
	verify   SignatureVerifier
	head     TreeHead
	signed   SignedTreeHead
	haveHead bool
	frozen   bool
	evidence []SignedTreeHead
	blocks   map[uint64][]byte
}

// NewReader returns a Reader that uses verify to authenticate every signed
// tree head. verify may be nil, in which case all updates fail with
// ErrNoVerifier.
func NewReader(verify SignatureVerifier) *Reader {
	return &Reader{
		verify: verify,
		blocks: make(map[uint64][]byte),
	}
}

// Accept verifies s and advances the Reader's state if it is a legitimate
// successor of the current head.
//
//   - The first valid head establishes the current fork.
//   - For the same fork, a shorter length is rejected with ErrRollback.
//   - For the same fork and length, a different root is same-length writer
//     equivocation: the Reader freezes and stores both signed heads as
//     evidence.
//   - A greater fork is accepted only after its signature passes and resets
//     all state derived from the old fork, including stored blocks.
//   - An earlier fork is rejected with ErrForkRollback.
func (r *Reader) Accept(s SignedTreeHead) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frozen {
		return ErrFrozen
	}
	if r.verify == nil {
		return ErrNoVerifier
	}
	if err := r.verify(s.Head, s.Signature); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSignature, err)
	}

	incoming := s.Clone()
	if !r.haveHead {
		r.adopt(incoming)
		return nil
	}

	if incoming.Head.Fork < r.head.Fork {
		return &ForkRollbackError{
			KnownFork:    r.head.Fork,
			ReceivedFork: incoming.Head.Fork,
		}
	}
	if incoming.Head.Fork > r.head.Fork {
		r.adopt(incoming)
		return nil
	}

	if incoming.Head.Length < r.head.Length {
		return &RollbackError{
			Fork:           r.head.Fork,
			KnownLength:    r.head.Length,
			ReceivedLength: incoming.Head.Length,
		}
	}
	if incoming.Head.Length == r.head.Length {
		if incoming.Head.Root == r.head.Root {
			return nil
		}
		existing := r.signed.Clone()
		r.frozen = true
		r.evidence = []SignedTreeHead{existing, incoming.Clone()}
		return &EquivocationError{
			Existing: existing,
			Incoming: incoming.Clone(),
		}
	}

	r.head = incoming.Head
	r.signed = incoming
	return nil
}

// VerifyTreeHead is an alias for Accept.
func (r *Reader) VerifyTreeHead(s SignedTreeHead) error {
	return r.Accept(s)
}

func (r *Reader) adopt(s SignedTreeHead) {
	r.head = s.Head
	r.signed = s.Clone()
	r.haveHead = true
	r.blocks = make(map[uint64][]byte)
}

// Head returns the latest verified tree head.
func (r *Reader) Head() (TreeHead, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.head, r.haveHead
}

// State returns the verified fork, length, and root.
func (r *Reader) State() (fork uint64, length uint64, root Hash, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.head.Fork, r.head.Length, r.head.Root, r.haveHead
}

// Frozen reports whether same-length equivocation froze the Reader.
func (r *Reader) Frozen() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

// Evidence returns deep copies of the two signed tree heads retained when
// same-length equivocation was detected. It returns nil otherwise.
func (r *Reader) Evidence() []SignedTreeHead {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.evidence) == 0 {
		return nil
	}
	out := make([]SignedTreeHead, len(r.evidence))
	for i := range r.evidence {
		out[i] = r.evidence[i].Clone()
	}
	return out
}

// AddBlock verifies data at index against the current signed tree head using
// proof. It stores a private copy only if the proof folds the leaf up to the
// verified root.
//
// A different body for an already stored index is rejected with
// ErrBlockConflict; the first verified body is retained.
func (r *Reader) AddBlock(index uint64, data []byte, proof []Hash) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frozen {
		return ErrFrozen
	}
	if !r.haveHead {
		return ErrNoVerifiedHead
	}
	if index >= r.head.Length {
		return fmt.Errorf(
			"%w: block %d is outside tree of length %d",
			ErrBlockRange, index, r.head.Length,
		)
	}
	if !VerifyInclusion(r.head.Root, r.head.Length, index, data, proof) {
		return fmt.Errorf("%w: block %d", ErrInclusionProof, index)
	}

	if stored, ok := r.blocks[index]; ok {
		if bytes.Equal(stored, data) {
			return nil
		}
		return fmt.Errorf("%w: block %d", ErrBlockConflict, index)
	}
	r.blocks[index] = cloneBytes(data)
	return nil
}

// StoreBlock is an alias for AddBlock.
func (r *Reader) StoreBlock(index uint64, data []byte, proof []Hash) error {
	return r.AddBlock(index, data, proof)
}

// Block returns a copy of a stored block.
func (r *Reader) Block(index uint64) ([]byte, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	data, ok := r.blocks[index]
	if !ok {
		return nil, false
	}
	return cloneBytes(data), true
}

// BlockCount returns the number of verified blocks currently stored.
func (r *Reader) BlockCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.blocks)
}

// Blocks returns deep copies of all stored blocks.
func (r *Reader) Blocks() map[uint64][]byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[uint64][]byte, len(r.blocks))
	for index, data := range r.blocks {
		out[index] = cloneBytes(data)
	}
	return out
}
