package snapverify

import "fmt"

// ProofNode is one sibling hash needed to fold the affected leaves up to
// the root. Nodes may arrive in any order; the verifier keys them by
// (Level, Index).
type ProofNode struct {
	Level uint32
	Index uint64
	Hash  Hash
}

// Proof carries everything the receiver needs, besides the trusted old
// root, to verify a delta. The header binds the proof to the old/new
// versions and the key range; it must match the delta exactly.
type Proof struct {
	OldVersion uint64
	NewVersion uint64
	RangeLo    uint64
	RangeHi    uint64
	Nodes      []ProofNode
}

// headerMatches reports whether the proof is bound to the same versions and
// range as the delta.
func (p *Proof) headerMatches(d *Delta) bool {
	return p.OldVersion == d.OldVersion &&
		p.NewVersion == d.NewVersion &&
		p.RangeLo == d.RangeLo &&
		p.RangeHi == d.RangeHi
}

// nodeSet indexes the proof nodes by position, rejecting duplicates.
func (p *Proof) nodeSet() (map[nodePos]Hash, error) {
	out := make(map[nodePos]Hash, len(p.Nodes))
	for _, n := range p.Nodes {
		pos := nodePos{level: n.Level, index: n.Index}
		if _, dup := out[pos]; dup {
			return nil, fmt.Errorf("snapverify: duplicate proof node at level %d index %d", n.Level, n.Index)
		}
		out[pos] = n.Hash
	}
	return out, nil
}
