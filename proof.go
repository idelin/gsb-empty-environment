package netsec

import (
	"fmt"
)

// ProofQuery is either a claimed present value or a claimed absence.
type ProofQuery struct {
	Key     uint64
	Present bool
	Value   []byte
}

// ProofSide is the root of a non-empty subtree at a sibling boundary. Prefix is
// the numeric value of the first Depth path bits.
type ProofSide struct {
	Depth  uint8
	Prefix uint64
	Digest Digest
}

// MultiProof proves the presence or absence of an ordered set of keys.
type MultiProof struct {
	Height  uint8
	Queries []ProofQuery
	Sides   []ProofSide
}

// Proof is an alias for MultiProof.
type Proof = MultiProof

// Query is an alias for ProofQuery.
type Query = ProofQuery

// Side is an alias for ProofSide.
type Side = ProofSide

// Validate checks the proof's shape and side-branch coordinates. It does not
// check the trusted root; Verify does that.
func (p *MultiProof) Validate() error {
	if p == nil {
		return ErrNilProof
	}
	if !validHeight(p.Height) {
		return ErrHeight
	}
	if len(p.Queries) == 0 || len(p.Queries) > MaxQueries {
		return fmt.Errorf("%w: got %d", ErrQueryCount, len(p.Queries))
	}
	if len(p.Sides) > len(p.Queries)*int(p.Height) {
		return fmt.Errorf("%w: got %d", ErrTooManySides, len(p.Sides))
	}

	for index, query := range p.Queries {
		if !keyInRange(query.Key, p.Height) {
			return fmt.Errorf("%w: key %d at height %d", ErrQueryKey, query.Key, p.Height)
		}
		if index > 0 && query.Key <= p.Queries[index-1].Key {
			return fmt.Errorf("%w: key %d follows %d", ErrQueryOrder, query.Key, p.Queries[index-1].Key)
		}
		if !query.Present && len(query.Value) != 0 {
			return fmt.Errorf("%w: key %d", ErrQueryPresence, query.Key)
		}
	}

	coordinates := make(map[nodeCoord]struct{}, len(p.Sides))
	for _, side := range p.Sides {
		if side.Depth == 0 || side.Depth > p.Height {
			return fmt.Errorf("%w: side depth %d at height %d", ErrDepth, side.Depth, p.Height)
		}
		if !prefixFits(side.Prefix, side.Depth) {
			return fmt.Errorf("%w: depth %d prefix %d", ErrNonZeroPrefixBits, side.Depth, side.Prefix)
		}

		coord := nodeCoord{depth: side.Depth, prefix: side.Prefix}
		if _, ok := coordinates[coord]; ok {
			return fmt.Errorf("%w: depth %d prefix %d", ErrDuplicateCoordinate, side.Depth, side.Prefix)
		}
		coordinates[coord] = struct{}{}

		if side.Digest == emptyRoots[p.Height-side.Depth] {
			return fmt.Errorf("%w: depth %d prefix %d", ErrExplicitEmptySide, side.Depth, side.Prefix)
		}

		onBoundary := false
		for _, query := range p.Queries {
			queryPrefix := query.Key >> (p.Height - side.Depth)
			if queryPrefix == side.Prefix {
				return fmt.Errorf("%w: depth %d prefix %d key %d", ErrSideCoversQuery, side.Depth, side.Prefix, query.Key)
			}
			if queryPrefix^1 == side.Prefix {
				onBoundary = true
			}
		}
		if !onBoundary {
			return fmt.Errorf("%w: depth %d prefix %d", ErrSideNotSibling, side.Depth, side.Prefix)
		}
	}

	return nil
}

// Verify checks all claims against root. It accepts a proof only if every side
// branch is consumed and the reconstructed root equals root exactly.
func (p *MultiProof) Verify(root Digest) error {
	_, err := p.verifyOldLeaves(root)
	return err
}

// VerifyProof is an alias for MultiProof.Verify.
func VerifyProof(root Digest, proof *MultiProof) error {
	if proof == nil {
		return ErrNilProof
	}
	return proof.Verify(root)
}

// Verify is an alias for VerifyProof.
func Verify(root Digest, proof *MultiProof) error {
	return VerifyProof(root, proof)
}

func (p *MultiProof) verifyOldLeaves(root Digest) (map[uint64]Digest, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	leaves := make(map[uint64]Digest, len(p.Queries))
	for _, query := range p.Queries {
		if query.Present {
			leaves[query.Key] = LeafHash(query.Key, query.Value)
		} else {
			leaves[query.Key] = AbsentLeafHash()
		}
	}

	computed, err := p.computeRoot(leaves)
	if err != nil {
		return nil, err
	}
	if computed != root {
		return nil, fmt.Errorf("%w: got %s, want %s", ErrRootMismatch, computed, root)
	}
	return leaves, nil
}

func (p *MultiProof) sideMap() map[nodeCoord]Digest {
	sides := make(map[nodeCoord]Digest, len(p.Sides))
	for _, side := range p.Sides {
		sides[nodeCoord{depth: side.Depth, prefix: side.Prefix}] = side.Digest
	}
	return sides
}

func (p *MultiProof) computeRoot(leaves map[uint64]Digest) (Digest, error) {
	sides := p.sideMap()
	consumed := make(map[nodeCoord]struct{}, len(sides))
	current := make(map[uint64]Digest, len(leaves))
	for key, digest := range leaves {
		current[key] = digest
	}

	for depth := int(p.Height); depth >= 1; depth-- {
		emptyChild := emptyRoots[p.Height-uint8(depth)]
		next := make(map[uint64]Digest, len(current))

		for prefix, digest := range current {
			sibling := prefix ^ 1
			siblingDigest, siblingPresent := current[sibling]
			if siblingPresent {
				// The sibling subtree contains another proven query and is
				// reconstructed from that query, not from a side branch.
			} else {
				coord := nodeCoord{depth: uint8(depth), prefix: sibling}
				if side, ok := sides[coord]; ok {
					siblingDigest = side
					consumed[coord] = struct{}{}
				} else {
					siblingDigest = emptyChild
				}
			}

			parent := prefix >> 1
			var parentDigest Digest
			if prefix&1 == 0 {
				parentDigest = InternalHash(digest, siblingDigest)
			} else {
				parentDigest = InternalHash(siblingDigest, digest)
			}
			if prior, ok := next[parent]; ok && prior != parentDigest {
				return Digest{}, fmt.Errorf("%w: inconsistent children at depth %d", ErrRootMismatch, depth-1)
			}
			next[parent] = parentDigest
		}
		current = next
	}

	if len(current) != 1 {
		return Digest{}, fmt.Errorf("%w: %d roots reconstructed", ErrRootMismatch, len(current))
	}
	root, ok := current[0]
	if !ok {
		return Digest{}, fmt.Errorf("%w: missing root", ErrRootMismatch)
	}

	for coord := range sides {
		if _, ok := consumed[coord]; !ok {
			return Digest{}, fmt.Errorf("%w: depth %d prefix %d", ErrUnconsumedSide, coord.depth, coord.prefix)
		}
	}
	return root, nil
}

// Update describes one key change. Delete means absence regardless of Value;
// a non-nil or empty Value with Delete false is a present value.
type Update struct {
	Key    uint64
	Value  []byte
	Delete bool
}

// SetUpdate returns an update that inserts a present value.
func SetUpdate(key uint64, value []byte) Update {
	return Update{Key: key, Value: cloneBytes(value)}
}

// DeleteUpdate returns an update that makes a key absent.
func DeleteUpdate(key uint64) Update {
	return Update{Key: key, Delete: true}
}

// Update verifies the old proof against root, verifies that every update key is
// covered, applies the batch, and returns the resulting root. The supplied
// proof and root are never modified. On any error the returned digest is the
// zero value and no new root exists.
func (p *MultiProof) Update(root Digest, updates []Update) (Digest, error) {
	if err := p.Validate(); err != nil {
		return Digest{}, err
	}

	oldLeaves, err := p.verifyOldLeaves(root)
	if err != nil {
		return Digest{}, err
	}

	proven := make(map[uint64]struct{}, len(p.Queries))
	for _, query := range p.Queries {
		proven[query.Key] = struct{}{}
	}

	seen := make(map[uint64]struct{}, len(updates))
	newLeaves := make(map[uint64]Digest, len(oldLeaves))
	for key, digest := range oldLeaves {
		newLeaves[key] = digest
	}

	for _, update := range updates {
		if !keyInRange(update.Key, p.Height) {
			return Digest{}, fmt.Errorf("%w: key %d", ErrQueryKey, update.Key)
		}
		if _, ok := seen[update.Key]; ok {
			return Digest{}, fmt.Errorf("%w: key %d", ErrDuplicateUpdate, update.Key)
		}
		seen[update.Key] = struct{}{}

		if _, ok := proven[update.Key]; !ok {
			return Digest{}, fmt.Errorf("%w: key %d", ErrUpdateKeyNotProven, update.Key)
		}
		if update.Delete {
			if len(update.Value) != 0 {
				return Digest{}, fmt.Errorf("%w: delete key carries a value", ErrInvalidUpdate)
			}
			newLeaves[update.Key] = AbsentLeafHash()
			continue
		}
		newLeaves[update.Key] = LeafHash(update.Key, update.Value)
	}

	newRoot, err := p.computeRoot(newLeaves)
	if err != nil {
		return Digest{}, err
	}
	return newRoot, nil
}

// ComputeNewRoot is an alias for MultiProof.Update.
func ComputeNewRoot(root Digest, proof *MultiProof, updates []Update) (Digest, error) {
	if proof == nil {
		return Digest{}, ErrNilProof
	}
	return proof.Update(root, updates)
}

// UpdateRoot is an alias for ComputeNewRoot.
func UpdateRoot(root Digest, proof *MultiProof, updates []Update) (Digest, error) {
	return ComputeNewRoot(root, proof, updates)
}
