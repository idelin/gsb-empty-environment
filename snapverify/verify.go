package snapverify

import (
	"errors"
	"fmt"
)

// ErrOldRootMismatch is returned when the reconstructed old root does not
// equal the receiver's trusted old root: the proof, the delta, or the
// declared old values are inconsistent with the snapshot the receiver
// actually holds.
var ErrOldRootMismatch = errors.New("snapverify: reconstructed old root does not match trusted old root")

// ErrMissingNode indicates the proof lacks a required sibling hash.
type ErrMissingNode struct {
	Level uint32
	Index uint64
}

func (e *ErrMissingNode) Error() string {
	return fmt.Sprintf("snapverify: missing proof node at level %d index %d", e.Level, e.Index)
}

var errNoLeaves = errors.New("snapverify: no affected leaves")

// Verify checks that the snapshot committed by oldRoot, after applying
// exactly the declared delta, yields a new root. The receiver needs only the
// trusted old root, the proof, and the delta.
//
// Security properties:
//
//   - The proof header must bind the same old/new versions and key range as
//     the delta, so proofs cannot be replayed across versions or ranges.
//   - The old root is reconstructed from the declared old values plus the
//     proof nodes; any lie about deleted/inherited values breaks it.
//   - Keys untouched by the delta are pinned by proof hashes, so no
//     undeclared block can be swapped without changing the computed root.
//   - The new root is recomputed from scratch with every declared delete
//     applied; omitting one yields a different root.
//
// On success it returns the new root, which the receiver adopts as the
// trusted root for the next version.
func Verify(depth int, oldRoot Hash, p *Proof, d *Delta) (Hash, error) {
	if err := d.Validate(depth); err != nil {
		return Hash{}, err
	}
	if !p.headerMatches(d) {
		return Hash{}, errors.New("snapverify: proof header does not match delta versions/range")
	}
	nodes, err := p.nodeSet()
	if err != nil {
		return Hash{}, err
	}

	keys := d.keys()
	if len(keys) == 0 {
		// Empty delta: nothing changes, and no proof nodes are needed.
		if len(nodes) != 0 {
			return Hash{}, fmt.Errorf("snapverify: empty delta carries %d unexpected proof nodes", len(nodes))
		}
		return oldRoot, nil
	}

	// The proof must contain exactly the required sibling set: no missing
	// nodes (cannot reconstruct) and no extras (nothing may be smuggled in).
	required := requiredPositions(depth, keys)
	if len(nodes) != len(required) {
		return Hash{}, fmt.Errorf("snapverify: proof has %d nodes, need exactly %d", len(nodes), len(required))
	}
	for pos := range required {
		if _, ok := nodes[pos]; !ok {
			return Hash{}, &ErrMissingNode{Level: pos.level, Index: pos.index}
		}
	}

	empty := emptyLeafHash()
	oldLeaves := make(map[uint64]Hash, len(keys))
	newLeaves := make(map[uint64]Hash, len(keys))
	for _, e := range d.Adds {
		oldLeaves[e.Key] = empty
		newLeaves[e.Key] = leafHash(e.Key, e.Value)
	}
	for _, e := range d.Deletes {
		oldLeaves[e.Key] = leafHash(e.Key, e.Value)
		newLeaves[e.Key] = empty
	}
	for _, e := range d.Inherits {
		h := leafHash(e.Key, e.Value)
		oldLeaves[e.Key] = h
		newLeaves[e.Key] = h
	}

	computedOld, err := computeRoot(depth, oldLeaves, nodes)
	if err != nil {
		return Hash{}, err
	}
	if computedOld != oldRoot {
		return Hash{}, ErrOldRootMismatch
	}
	return computeRoot(depth, newLeaves, nodes)
}
