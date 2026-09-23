package blockpack

import (
	"errors"
	"fmt"
	"sort"
)

// recomputeBlockDigest re-encodes the block's objects and recomputes the
// digest committed by the block summary.
func recomputeBlockDigest(b Block) (Digest, error) {
	digs := make([]Digest, 0, len(b.Objects))
	for _, o := range b.Objects {
		enc, err := EncodeObject(o)
		if err != nil {
			return Digest{}, err
		}
		digs = append(digs, objectDigest(enc))
	}
	return blockDigest(b.Index, digs), nil
}

// Verifier incrementally verifies a package while it is being read, block by
// block. Until Finish succeeds it only reports how many blocks are locally
// verified; it never declares the whole package complete early.
type Verifier struct {
	header   Header
	expected Digest

	objDigs []Digest
	blkDigs []Digest
	objs    []Object

	local int
	done  bool
}

// NewVerifier creates a streaming verifier for a package whose header was
// read first. expectedRoot must come from a trusted source (or from the
// header itself when the header is authenticated by other means).
func NewVerifier(h Header, expectedRoot Digest) *Verifier {
	return &Verifier{header: h, expected: expectedRoot}
}

// AddBlock verifies one block. Blocks must arrive in index order; a block
// whose summary carries a different object-level root, or whose digest does
// not match its contents, is rejected immediately.
func (v *Verifier) AddBlock(b Block) error {
	if v.done {
		return errors.New("blockpack: verifier already finished")
	}
	if b.Index != len(v.blkDigs) {
		return fmt.Errorf("blockpack: block %d out of order, expected block %d", b.Index, len(v.blkDigs))
	}
	if b.SummaryRoot != v.expected {
		return fmt.Errorf("blockpack: block %d carries a foreign root digest", b.Index)
	}
	d, err := recomputeBlockDigest(b)
	if err != nil {
		return err
	}
	if d != b.Digest {
		return fmt.Errorf("blockpack: block %d digest mismatch", b.Index)
	}
	for _, o := range b.Objects {
		enc, err := EncodeObject(o)
		if err != nil {
			return err
		}
		v.objDigs = append(v.objDigs, objectDigest(enc))
		v.objs = append(v.objs, o)
	}
	v.blkDigs = append(v.blkDigs, d)
	v.local++
	return nil
}

// LocallyVerified reports how many blocks have passed local verification so
// far. This is a local property only: it says nothing about package
// completeness until Finish succeeds.
func (v *Verifier) LocallyVerified() int {
	return v.local
}

// Complete reports whether Finish has succeeded. It is false while blocks
// are still being verified, even if every block seen so far was valid.
func (v *Verifier) Complete() bool {
	return v.done
}

// Finish confirms completeness: every committed block and object must have
// been presented, and the root recomputed from the accumulated evidence
// (object order, count, reference graph, and block Merkle root) must equal
// the expected root. A missing trailing block, a missing object, or any
// reordering makes this fail even if all presented blocks were valid.
func (v *Verifier) Finish() error {
	if v.done {
		return errors.New("blockpack: verifier already finished")
	}
	if uint32(len(v.blkDigs)) != v.header.BlockCount {
		return fmt.Errorf("blockpack: incomplete package: %d of %d blocks verified",
			len(v.blkDigs), v.header.BlockCount)
	}
	if uint32(len(v.objDigs)) != v.header.ObjectCount {
		return fmt.Errorf("blockpack: incomplete package: %d of %d objects verified",
			len(v.objDigs), v.header.ObjectCount)
	}
	chain := chainDigest(v.header.Version, v.header.ObjectCount, v.objDigs)
	root := computeRoot(v.header, chain, refGraphDigest(v.objs), merkleRoot(v.blkDigs))
	if root != v.expected {
		return errors.New("blockpack: root digest mismatch")
	}
	v.done = true
	return nil
}

// VerifySubset independently verifies an arbitrary subset of blocks against
// the expected root: each block's summary root must match and its digest
// must match its contents. It returns the indices of the blocks that fail,
// which can be compared against a tamper manifest. Blocks not present in the
// subset are simply not checked; this never asserts whole-package integrity.
func VerifySubset(expectedRoot Digest, blocks []Block) []int {
	var bad []int
	for _, b := range blocks {
		if b.SummaryRoot != expectedRoot {
			bad = append(bad, b.Index)
			continue
		}
		d, err := recomputeBlockDigest(b)
		if err != nil || d != b.Digest {
			bad = append(bad, b.Index)
		}
	}
	sort.Ints(bad)
	return bad
}

// MatchTamperManifest reports whether the set of blocks that failed
// verification exactly matches the expected tamper manifest.
func MatchTamperManifest(failed, manifest []int) bool {
	if len(failed) != len(manifest) {
		return false
	}
	m := append([]int(nil), manifest...)
	sort.Ints(m)
	f := append([]int(nil), failed...)
	sort.Ints(f)
	for i := range f {
		if f[i] != m[i] {
			return false
		}
	}
	return true
}
