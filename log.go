package disclosure

import (
	"fmt"
	"sort"
)

type logView struct {
	base           uint64
	leaves         []Leaf
	bySeq          map[uint64]Leaf
	correctionNext map[uint64]uint64
	tombstones     map[uint64][]uint64
}

func prepareLog(input []Leaf) (*logView, error) {
	leaves := append([]Leaf(nil), input...)
	sort.Slice(leaves, func(i, j int) bool { return leaves[i].Seq < leaves[j].Seq })

	view := &logView{
		bySeq:          make(map[uint64]Leaf, len(leaves)),
		correctionNext: make(map[uint64]uint64),
		tombstones:     make(map[uint64][]uint64),
	}
	if len(leaves) > 0 {
		view.base = leaves[0].Seq
	}

	for i := range leaves {
		leaf := leaves[i]
		if err := validateLeafShape(leaf); err != nil {
			return nil, fmt.Errorf("%w: seq %d: %w", ErrInvalidLeaf, leaf.Seq, err)
		}
		if uint64(i) > maxUint64-view.base {
			return nil, fmt.Errorf("%w: seq range overflows", ErrInvalidLeaf)
		}
		expected := view.base + uint64(i)
		if leaf.Seq != expected {
			return nil, fmt.Errorf("%w: seq %d, expected contiguous seq %d", ErrInvalidLeaf, leaf.Seq, expected)
		}
		if _, exists := view.bySeq[leaf.Seq]; exists {
			return nil, fmt.Errorf("%w: duplicate seq %d", ErrInvalidLeaf, leaf.Seq)
		}
		view.bySeq[leaf.Seq] = leaf
	}

	for _, leaf := range leaves {
		if leaf.RefSeq == nil {
			continue
		}
		target, ok := view.bySeq[*leaf.RefSeq]
		if !ok {
			return nil, fmt.Errorf("%w: seq %d references missing seq %d", ErrInvalidLeaf, leaf.Seq, *leaf.RefSeq)
		}
		if *leaf.RefSeq >= leaf.Seq {
			return nil, fmt.Errorf("%w: seq %d does not precede seq %d", ErrInvalidLeaf, *leaf.RefSeq, leaf.Seq)
		}
		if target.Digest() != leaf.PrevDigest {
			return nil, fmt.Errorf("%w: seq %d predecessor digest mismatch", ErrInvalidLeaf, leaf.Seq)
		}
		switch leaf.Kind {
		case KindCorrection:
			if target.Kind == KindTombstone {
				return nil, fmt.Errorf("%w: correction seq %d references a tombstone", ErrInvalidLeaf, leaf.Seq)
			}
			if previous, exists := view.correctionNext[target.Seq]; exists {
				return nil, fmt.Errorf("%w: seq %d and seq %d branch from seq %d", ErrInvalidLeaf, previous, leaf.Seq, target.Seq)
			}
			view.correctionNext[target.Seq] = leaf.Seq
		case KindTombstone:
			if target.Kind == KindTombstone {
				return nil, fmt.Errorf("%w: tombstone seq %d references a tombstone", ErrInvalidLeaf, leaf.Seq)
			}
			view.tombstones[target.Seq] = append(view.tombstones[target.Seq], leaf.Seq)
		}
	}

	for _, leaf := range leaves {
		if leaf.Kind != KindCorrection {
			continue
		}
		seen := make(map[uint64]struct{})
		cur := leaf
		for {
			if _, duplicate := seen[cur.Seq]; duplicate {
				return nil, fmt.Errorf("%w: version chain cycle at seq %d", ErrInvalidLeaf, cur.Seq)
			}
			seen[cur.Seq] = struct{}{}
			if cur.Kind == KindEvent {
				break
			}
			if cur.RefSeq == nil {
				return nil, fmt.Errorf("%w: version chain from seq %d has no event root", ErrInvalidLeaf, leaf.Seq)
			}
			next, ok := view.bySeq[*cur.RefSeq]
			if !ok {
				return nil, fmt.Errorf("%w: version chain references missing seq %d", ErrInvalidLeaf, *cur.RefSeq)
			}
			cur = next
		}
	}

	view.leaves = leaves
	return view, nil
}

func (v *logView) leafHashes() []Hash {
	out := make([]Hash, len(v.leaves))
	for i := range v.leaves {
		out[i] = v.leaves[i].Digest()
	}
	return out
}

func (v *logView) correctionChainLeaf(seq uint64) ([]Leaf, error) {
	var chain []Leaf
	seen := make(map[uint64]struct{})
	for {
		leaf, ok := v.bySeq[seq]
		if !ok {
			return nil, fmt.Errorf("%w: seq %d", ErrMissingLeaf, seq)
		}
		if _, duplicate := seen[seq]; duplicate {
			return nil, fmt.Errorf("%w: cycle at seq %d", ErrInvalidLeaf, seq)
		}
		seen[seq] = struct{}{}
		chain = append(chain, leaf)
		if leaf.Kind == KindEvent {
			return chain, nil
		}
		if leaf.Kind != KindCorrection || leaf.RefSeq == nil {
			return nil, fmt.Errorf("%w: seq %d cannot be a version chain node", ErrInvalidLeaf, seq)
		}
		seq = *leaf.RefSeq
	}
}
