// Package permverifier is an importable acceptance verifier for an in-process
// JIT memory host whose logical mappings may alias the same physical pages.
//
// Mappings are described by object generation and half-open offset intervals.
// The model supports shared frames, copy-on-write views that split only the
// pages actually written, segmented remapping, and arbitrary aliases between
// distinct views. CPUs may briefly retain old write or execute permission; a
// revocation is released only after an AckOp arrives from every holding CPU
// for the matching (CPU, object, generation) pair.
//
// The safety condition is W^X over physical bytes: no byte may be writable
// through any effective view while it is executable through any effective view
// on any CPU. Callers submit a batch of operations without a global order,
// together with dependency edges. Verify explores every linear interleaving
// permitted by that DAG and either proves the invariant for all of them or
// returns the shortest failing prefix with the first conflicting physical
// interval. Exhaustion of the configured state or step budget yields
// VerdictUnknown instead of an unsound verdict.
//
// All ranges are native uint64 half-open intervals, including ranges whose
// exclusive end is exactly 2^64. SmallPageTable provides an independent
// byte-at-a-time reference model for cross-checking symbolic results.
package permverifier

// Safe is a small fluent builder for batches; it is optional and every value
// it produces is one of the exported Op payload types.
type Builder struct {
	nextID uint64
	ops    []Op
}

// NewBuilder starts a batch whose first op gets id 1.
func NewBuilder() *Builder { return &Builder{nextID: 1} }

// Add appends a prepared op, assigning the next sequential id when the caller
// leaves ID zero.
func (b *Builder) Add(op Op) Op {
	if op.ID == 0 {
		op.ID = b.nextID
	}
	b.nextID = op.ID + 1
	b.ops = append(b.ops, op)
	return op
}

// Batch returns the accumulated operations.
func (b *Builder) Batch() []Op { return b.ops }
