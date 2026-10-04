package snapdelta

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrVersionMismatch: proof.OldVersion does not match the receiver's
	// current version.
	ErrVersionMismatch = errors.New("snapdelta: old version mismatch")
	// ErrBadVersionChain: NewVersion is not greater than OldVersion.
	ErrBadVersionChain = errors.New("snapdelta: new version must be greater than old version")
	// ErrOutOfRange: an operation or old entry lies outside the proven range.
	ErrOutOfRange = errors.New("snapdelta: key outside proven range")
	// ErrMalformedProof: structural problem (ordering, duplicates, conflicts).
	ErrMalformedProof = errors.New("snapdelta: malformed proof")
	// ErrMalformedDelta: structural problem in the delta itself.
	ErrMalformedDelta = errors.New("snapdelta: malformed delta")
	// ErrOldRootMismatch: recomputed old root != receiver's root; the proof
	// or old entries were tampered with (e.g. an inherited block swapped).
	ErrOldRootMismatch = errors.New("snapdelta: old root mismatch")
	// ErrNewRootMismatch: applying the declared ops does not yield the
	// claimed new root (e.g. a deletion was omitted).
	ErrNewRootMismatch = errors.New("snapdelta: new root mismatch")
)

// Verify checks that applying delta to the receiver's current state
// (identified by oldRoot and oldVersion) produces exactly delta.NewRoot,
// and returns the verified new root and version.
//
// The receiver only needs its current root, version, the delta and the
// proof. Verification guarantees:
//   - every declared op key lies inside the proven range;
//   - the pre-delta state of every touched key is exactly as listed in
//     the proof (otherwise the old root would not match);
//   - every untouched subtree is bit-identical before and after, pinned
//     by an inherited-block proof node or the canonical empty hash;
//   - the declared ops, applied in order, yield exactly the claimed new
//     root, so no deletion (or any other op) can be silently dropped.
func Verify(oldRoot Hash, oldVersion uint64, delta *Delta, proof *Proof) (newRoot Hash, newVersion uint64, err error) {
	if delta == nil || proof == nil {
		return Hash{}, 0, ErrMalformedDelta
	}
	if proof.OldVersion != oldVersion {
		return Hash{}, 0, fmt.Errorf("%v: have %d, proof says %d", ErrVersionMismatch, oldVersion, proof.OldVersion)
	}
	if proof.NewVersion <= proof.OldVersion {
		return Hash{}, 0, ErrBadVersionChain
	}
	if proof.RangeStart > proof.RangeEnd {
		return Hash{}, 0, fmt.Errorf("%v: empty range [%d, %d]", ErrMalformedProof, proof.RangeStart, proof.RangeEnd)
	}

	// Collect touched keys and validate ops.
	touched := make(map[uint16]bool)
	for i, op := range delta.Ops {
		if op.Type != OpPut && op.Type != OpDelete {
			return Hash{}, 0, fmt.Errorf("%v: op %d has unknown type %d", ErrMalformedDelta, i, op.Type)
		}
		if op.Type == OpDelete && len(op.Value) != 0 {
			return Hash{}, 0, fmt.Errorf("%v: delete op %d carries a value", ErrMalformedDelta, i)
		}
		if op.Key < proof.RangeStart || op.Key > proof.RangeEnd {
			return Hash{}, 0, fmt.Errorf("%v: op key %d not in [%d, %d]", ErrOutOfRange, op.Key, proof.RangeStart, proof.RangeEnd)
		}
		touched[op.Key] = true
	}
	touchedSorted := make([]uint16, 0, len(touched))
	for k := range touched {
		touchedSorted = append(touchedSorted, k)
	}
	sort.Slice(touchedSorted, func(i, j int) bool { return touchedSorted[i] < touchedSorted[j] })

	// Validate old entries: strictly sorted, all touched, all in range.
	oldByKey := make(map[uint16][]byte, len(proof.OldEntries))
	for i, e := range proof.OldEntries {
		if i > 0 && proof.OldEntries[i-1].Key >= e.Key {
			return Hash{}, 0, fmt.Errorf("%v: old entries not strictly sorted at index %d", ErrMalformedProof, i)
		}
		if !touched[e.Key] {
			return Hash{}, 0, fmt.Errorf("%v: old entry for untouched key %d", ErrMalformedProof, e.Key)
		}
		if e.Key < proof.RangeStart || e.Key > proof.RangeEnd {
			return Hash{}, 0, fmt.Errorf("%v: old entry key %d not in [%d, %d]", ErrOutOfRange, e.Key, proof.RangeStart, proof.RangeEnd)
		}
		oldByKey[e.Key] = e.Value
	}

	// Validate proof nodes: canonical order, in-bounds, disjoint from
	// touched keys.
	nodes := make(map[nodeID]Hash, len(proof.Nodes))
	for i, n := range proof.Nodes {
		if n.Depth > Depth || n.Index >= uint32(1)<<uint(n.Depth) {
			return Hash{}, 0, fmt.Errorf("%v: node %d at invalid position (depth=%d, index=%d)", ErrMalformedProof, i, n.Depth, n.Index)
		}
		if i > 0 {
			prev := proof.Nodes[i-1]
			if prev.Depth > n.Depth || (prev.Depth == n.Depth && prev.Index >= n.Index) {
				return Hash{}, 0, fmt.Errorf("%v: nodes not in canonical order at index %d", ErrMalformedProof, i)
			}
		}
		shift := uint(Depth - n.Depth)
		lo := n.Index << shift
		hi := lo + (uint32(1) << shift)
		j := sort.Search(len(touchedSorted), func(j int) bool { return uint32(touchedSorted[j]) >= lo })
		if j < len(touchedSorted) && uint32(touchedSorted[j]) < hi {
			return Hash{}, 0, fmt.Errorf("%v: node (depth=%d, index=%d) covers touched key %d", ErrMalformedProof, n.Depth, n.Index, touchedSorted[j])
		}
		nodes[nodeID{n.Depth, n.Index}] = n.Hash
	}

	// Recompute the old root: touched leaves come from OldEntries
	// (absent = canonical empty hash), everything else from proof nodes
	// or default hashes.
	oldLeaves := make(map[uint16]Hash, len(touchedSorted))
	for _, k := range touchedSorted {
		if v, ok := oldByKey[k]; ok {
			oldLeaves[k] = leafHash(k, v)
		} else {
			oldLeaves[k] = Hash{}
		}
	}
	computedOld, err := computeRoot(oldLeaves, nodes)
	if err != nil {
		return Hash{}, 0, err
	}
	if computedOld != oldRoot {
		return Hash{}, 0, ErrOldRootMismatch
	}

	// Apply the declared ops in order (duplicate keys: last one wins).
	state := make(map[uint16][]byte, len(oldByKey))
	for k, v := range oldByKey {
		state[k] = v
	}
	for _, op := range delta.Ops {
		if op.Type == OpPut {
			state[op.Key] = op.Value
		} else {
			delete(state, op.Key)
		}
	}
	newLeaves := make(map[uint16]Hash, len(touchedSorted))
	for _, k := range touchedSorted {
		if v, ok := state[k]; ok {
			newLeaves[k] = leafHash(k, v)
		} else {
			newLeaves[k] = Hash{}
		}
	}
	computedNew, err := computeRoot(newLeaves, nodes)
	if err != nil {
		return Hash{}, 0, err
	}
	if computedNew != delta.NewRoot {
		return Hash{}, 0, ErrNewRootMismatch
	}

	return delta.NewRoot, proof.NewVersion, nil
}
