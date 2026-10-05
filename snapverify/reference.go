package snapverify

import (
	"bytes"
	"fmt"
	"sort"
)

// Reference is a naive full-map implementation used as the ground truth:
// it keeps every block and rebuilds the entire Merkle tree from scratch on
// every Root() call. Verification results are compared against it in tests.
// It is only practical for small key spaces.
type Reference struct {
	depth int
	data  map[uint64][]byte
}

// NewReference creates an empty reference snapshot over 2^depth keys.
func NewReference(depth int) *Reference {
	return &Reference{depth: depth, data: make(map[uint64][]byte)}
}

// Set inserts or replaces a block.
func (r *Reference) Set(key uint64, value []byte) {
	r.data[key] = value
}

// Get returns the block at key, if present.
func (r *Reference) Get(key uint64) ([]byte, bool) {
	v, ok := r.data[key]
	return v, ok
}

// Delete removes a block.
func (r *Reference) Delete(key uint64) {
	delete(r.data, key)
}

// buildLevels materializes every tree level; levels[0] has 2^depth leaves
// and levels[depth] holds the single root.
func (r *Reference) buildLevels() [][]Hash {
	size := 1 << uint(r.depth)
	empty := emptyHashes(r.depth)
	levels := make([][]Hash, r.depth+1)
	levels[0] = make([]Hash, size)
	for i := 0; i < size; i++ {
		if v, ok := r.data[uint64(i)]; ok {
			levels[0][i] = leafHash(uint64(i), v)
		} else {
			levels[0][i] = empty[0]
		}
	}
	for lvl := 1; lvl <= r.depth; lvl++ {
		prev := levels[lvl-1]
		cur := make([]Hash, len(prev)/2)
		for i := range cur {
			cur[i] = interiorHash(prev[2*i], prev[2*i+1])
		}
		levels[lvl] = cur
	}
	return levels
}

// Root recomputes the commitment by rebuilding the whole tree.
func (r *Reference) Root() Hash {
	return r.buildLevels()[r.depth][0]
}

// ApplyDelta mutates the snapshot, enforcing the same semantics the
// verifier assumes: deletes and inherits must match the stored block, adds
// must target empty keys.
func (r *Reference) ApplyDelta(d *Delta) error {
	if err := d.Validate(r.depth); err != nil {
		return err
	}
	for _, e := range d.Deletes {
		cur, ok := r.data[e.Key]
		if !ok || !bytes.Equal(cur, e.Value) {
			return fmt.Errorf("snapverify: delete of key %d does not match stored block", e.Key)
		}
	}
	for _, e := range d.Adds {
		if _, ok := r.data[e.Key]; ok {
			return fmt.Errorf("snapverify: add of already occupied key %d", e.Key)
		}
	}
	for _, e := range d.Inherits {
		cur, ok := r.data[e.Key]
		if !ok || !bytes.Equal(cur, e.Value) {
			return fmt.Errorf("snapverify: inherit of key %d does not match stored block", e.Key)
		}
	}
	for _, e := range d.Deletes {
		delete(r.data, e.Key)
	}
	for _, e := range d.Adds {
		r.data[e.Key] = e.Value
	}
	return nil
}

// BuildProof acts as the prover: given the delta it is about to publish, it
// extracts exactly the sibling nodes the verifier will require, sorted
// canonically by (level, index).
func (r *Reference) BuildProof(d *Delta) (*Proof, error) {
	if err := d.Validate(r.depth); err != nil {
		return nil, err
	}
	p := &Proof{
		OldVersion: d.OldVersion,
		NewVersion: d.NewVersion,
		RangeLo:    d.RangeLo,
		RangeHi:    d.RangeHi,
	}
	keys := d.keys()
	if len(keys) == 0 {
		return p, nil
	}
	required := requiredPositions(r.depth, keys)
	levels := r.buildLevels()
	positions := make([]nodePos, 0, len(required))
	for pos := range required {
		positions = append(positions, pos)
	}
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].level != positions[j].level {
			return positions[i].level < positions[j].level
		}
		return positions[i].index < positions[j].index
	})
	for _, pos := range positions {
		p.Nodes = append(p.Nodes, ProofNode{
			Level: pos.level,
			Index: pos.index,
			Hash:  levels[pos.level][pos.index],
		})
	}
	return p, nil
}
