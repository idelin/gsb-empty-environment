package blockpack

import (
	"errors"
	"fmt"
)

// RangeProof proves the integrity of a contiguous range of blocks for a
// range read. It carries the package header, the chain and reference-graph
// commitments, the blocks in the range, and the Merkle siblings needed to
// recompute the package root. It verifies only against the exact root of
// the package it was built from, so a proof from an old package cannot be
// spliced into a new one.
type RangeProof struct {
	Header   Header
	Chain    Digest
	RefGraph Digest
	Start    int
	End      int // exclusive
	Blocks   []Block
	Siblings []Digest
}

// ProveRange builds an integrity proof for blocks [start, end).
func (p *Package) ProveRange(start, end int) (*RangeProof, error) {
	if start < 0 || end > len(p.Blocks) || start >= end {
		return nil, errRange
	}
	sibs, err := proveRangeHashes(p.blkDigs, start, end)
	if err != nil {
		return nil, err
	}
	blocks := make([]Block, end-start)
	copy(blocks, p.Blocks[start:end])
	return &RangeProof{
		Header:   p.Header,
		Chain:    p.chain,
		RefGraph: p.refGraph,
		Start:    start,
		End:      end,
		Blocks:   blocks,
		Siblings: sibs,
	}, nil
}

// Verify checks the proof against the expected package root: every block in
// the range must carry the expected root in its summary and match its
// committed digest, the Merkle path must reconstruct the committed block
// root, and the header plus chain and reference-graph commitments must
// reproduce the expected root.
func (rp *RangeProof) Verify(expectedRoot Digest) error {
	if len(rp.Blocks) != rp.End-rp.Start || rp.Start < 0 {
		return errors.New("blockpack: malformed range proof")
	}
	leaves := make([]Digest, len(rp.Blocks))
	for i, b := range rp.Blocks {
		if b.Index != rp.Start+i {
			return fmt.Errorf("blockpack: proof block %d at wrong position", b.Index)
		}
		if b.SummaryRoot != expectedRoot {
			return fmt.Errorf("blockpack: proof block %d carries a foreign root digest", b.Index)
		}
		d, err := recomputeBlockDigest(b)
		if err != nil {
			return err
		}
		if d != b.Digest {
			return fmt.Errorf("blockpack: proof block %d digest mismatch", b.Index)
		}
		leaves[i] = d
	}
	if !verifyRangeHashes(merkleRootFromProof(rp), int(rp.Header.BlockCount), rp.Start, rp.End, leaves, rp.Siblings) {
		return errors.New("blockpack: range proof Merkle path invalid")
	}
	root := computeRoot(rp.Header, rp.Chain, rp.RefGraph, merkleRootFromProof(rp))
	if root != expectedRoot {
		return errors.New("blockpack: range proof does not commit to the expected root")
	}
	return nil
}

// merkleRootFromProof recomputes the block Merkle root from the proof's
// blocks and siblings; it returns ok=false via the verify helper, so this is
// only used after verifyRangeHashes has succeeded, or recomputed inline.
func merkleRootFromProof(rp *RangeProof) Digest {
	leaves := make([]Digest, len(rp.Blocks))
	for i, b := range rp.Blocks {
		leaves[i] = b.Digest
	}
	n := 1
	for n < int(rp.Header.BlockCount) {
		n <<= 1
	}
	depth := 0
	for (1 << uint(depth)) < n {
		depth++
	}
	li, si := 0, 0
	root, ok := verifyRec(depth, 0, n, rp.Start, rp.End, leaves, &li, rp.Siblings, &si)
	if !ok || li != len(leaves) || si != len(rp.Siblings) {
		return Digest{}
	}
	return root
}
