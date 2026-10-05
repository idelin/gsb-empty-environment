// Package intersect implements a verifiable multi-list intersection protocol.
//
// Each sorted ID list is stored as a sequence of independent blocks. Every
// block has a summary (min, max, count, SHA-256 of its canonical encoding)
// and all summaries are committed into a single manifest root known to the
// verifier.
//
// The prover computes the intersection and emits a Proof. Blocks that cannot
// contain any result ID are skipped, and every skip carries a justification
// derived from the committed summaries (never from assumed range overlap of
// contents). Blocks that overlap the feasible range are included in full, so
// the verifier can recompute the intersection and confirm nothing is missing.
// Tampering with any summary or block content breaks the manifest root or the
// per-block hash and is rejected.
package intersect

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// Block is an independent chunk of a sorted ID list. IDs must be sorted in
// non-decreasing order; duplicates are allowed and are normalized (deduped)
// when the intersection is computed.
type Block struct {
	IDs []string
}

// BlockSummary commits to a block's contents without revealing them.
type BlockSummary struct {
	Min   string
	Max   string
	Count int
	Hash  [32]byte
}

// SkipReason explains why a block was excluded from the proof's evidence.
type SkipReason string

const (
	ReasonNone            SkipReason = "" // block is provided, not skipped
	ReasonInfeasibleRange SkipReason = "infeasible-range"
	ReasonEmptyBlock      SkipReason = "empty-block"
	ReasonBelowRange      SkipReason = "below-feasible-range"
	ReasonAboveRange      SkipReason = "above-feasible-range"
)

// BlockProof is the evidence for a single block: either its full contents or
// a skip justification. The committed summary is always present.
type BlockProof struct {
	Summary  BlockSummary
	Provided bool
	Reason   SkipReason
	Detail   string
	IDs      []string
}

// ListProof holds the per-block evidence of one input list, in block order.
type ListProof struct {
	Blocks []BlockProof
}

// Proof is the checkable evidence for an intersection result.
type Proof struct {
	Result []string
	Lists  []ListProof
}

// hashIDs hashes the canonical length-prefixed encoding of ids.
func hashIDs(ids []string) [32]byte {
	h := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(len(ids)))
	h.Write(buf[:])
	for _, id := range ids {
		binary.BigEndian.PutUint64(buf[:], uint64(len(id)))
		h.Write(buf[:])
		h.Write([]byte(id))
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// Summarize computes the committed summary of a block. It fails if the block
// is not sorted, since min/max would otherwise be meaningless.
func Summarize(b Block) (BlockSummary, error) {
	if !sort.StringsAreSorted(b.IDs) {
		return BlockSummary{}, errors.New("block IDs are not sorted")
	}
	s := BlockSummary{Count: len(b.IDs), Hash: hashIDs(b.IDs)}
	if len(b.IDs) > 0 {
		s.Min = b.IDs[0]
		s.Max = b.IDs[len(b.IDs)-1]
	}
	return s, nil
}

// ManifestRoot commits to the exact sequence of all block summaries of all
// lists. The verifier trusts only this root; every summary in a proof must
// hash back to it.
func ManifestRoot(lists [][]BlockSummary) [32]byte {
	h := sha256.New()
	var buf [8]byte
	writeU64 := func(v uint64) {
		binary.BigEndian.PutUint64(buf[:], v)
		h.Write(buf[:])
	}
	writeStr := func(s string) {
		writeU64(uint64(len(s)))
		h.Write([]byte(s))
	}
	writeU64(uint64(len(lists)))
	for _, summaries := range lists {
		writeU64(uint64(len(summaries)))
		for _, s := range summaries {
			writeStr(s.Min)
			writeStr(s.Max)
			writeU64(uint64(s.Count))
			h.Write(s.Hash[:])
		}
	}
	var root [32]byte
	copy(root[:], h.Sum(nil))
	return root
}

// feasibleRange computes the only ID range that can belong to every list:
// [max of per-list minimums, min of per-list maximums]. Any ID outside it is
// provably absent from at least one list. ok is false when some list has no
// non-empty block or the ranges do not overlap.
func feasibleRange(lists [][]BlockSummary) (lo, hi string, ok bool) {
	first := true
	for _, summaries := range lists {
		listLo, listHi, listOK := "", "", false
		for _, s := range summaries {
			if s.Count == 0 {
				continue
			}
			if !listOK || s.Min < listLo {
				listLo = s.Min
			}
			if !listOK || s.Max > listHi {
				listHi = s.Max
			}
			listOK = true
		}
		if !listOK {
			return "", "", false
		}
		if first || listLo > lo {
			lo = listLo
		}
		if first || listHi < hi {
			hi = listHi
		}
		first = false
	}
	if first || lo > hi {
		return "", "", false
	}
	return lo, hi, true
}

// decide determines, from a committed summary alone, whether a block must be
// provided as evidence or may be skipped, and the justification for a skip.
func decide(s BlockSummary, lo, hi string, feasible bool) (provide bool, reason SkipReason) {
	switch {
	case !feasible:
		return false, ReasonInfeasibleRange
	case s.Count == 0:
		return false, ReasonEmptyBlock
	case s.Max < lo:
		return false, ReasonBelowRange
	case s.Min > hi:
		return false, ReasonAboveRange
	default:
		return true, ReasonNone
	}
}

func detail(reason SkipReason, s BlockSummary, lo, hi string) string {
	switch reason {
	case ReasonInfeasibleRange:
		return "no common feasible range exists (a list is empty or list ranges are disjoint), so no ID can belong to every list"
	case ReasonEmptyBlock:
		return "block contains no IDs"
	case ReasonBelowRange:
		return fmt.Sprintf("block max %q is below the feasible range lower bound %q", s.Max, lo)
	case ReasonAboveRange:
		return fmt.Sprintf("block min %q is above the feasible range upper bound %q", s.Min, hi)
	default:
		return ""
	}
}

// firstBlockAtOrAfter returns the index of the first block whose committed
// Max is >= target, or len(summaries) if none qualifies. summaries must be
// sorted by Max. A target exactly equal to a block's Max selects that block:
// equality means the block may still contain the target and must not be
// jumped over.
func firstBlockAtOrAfter(summaries []BlockSummary, target string) int {
	lo, hi := 0, len(summaries)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if summaries[mid].Max < target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// sortedByMax reports whether summaries are ordered by Max, which allows the
// prover to binary-search the first candidate block instead of scanning.
func sortedByMax(summaries []BlockSummary) bool {
	for i := 1; i < len(summaries); i++ {
		if summaries[i-1].Max > summaries[i].Max {
			return false
		}
	}
	return true
}

// intersectSets returns the sorted, deduplicated intersection of sets.
func intersectSets(sets []map[string]struct{}) []string {
	if len(sets) == 0 {
		return nil
	}
	smallest := 0
	for i := range sets {
		if len(sets[i]) < len(sets[smallest]) {
			smallest = i
		}
	}
	var out []string
	for id := range sets[smallest] {
		inAll := true
		for i, s := range sets {
			if i == smallest {
				continue
			}
			if _, ok := s[id]; !ok {
				inAll = false
				break
			}
		}
		if inAll {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Prove computes the intersection of lists and emits the evidence proving
// the result is complete. It also returns the manifest root that the
// verifier must be given through a trusted channel.
func Prove(lists [][]Block) (Proof, [32]byte, error) {
	summaries := make([][]BlockSummary, len(lists))
	for i, blocks := range lists {
		summaries[i] = make([]BlockSummary, len(blocks))
		for j, b := range blocks {
			s, err := Summarize(b)
			if err != nil {
				return Proof{}, [32]byte{}, fmt.Errorf("list %d block %d: %w", i, j, err)
			}
			summaries[i][j] = s
		}
	}
	root := ManifestRoot(summaries)
	lo, hi, feasible := feasibleRange(summaries)

	proof := Proof{Lists: make([]ListProof, len(lists))}
	sets := make([]map[string]struct{}, len(lists))
	for i, blocks := range lists {
		sets[i] = make(map[string]struct{})
		lp := ListProof{Blocks: make([]BlockProof, len(blocks))}

		// Jump directly to the first block that can contain the feasible
		// lower bound; every block before it is provably below the range
		// and is skipped on summary evidence alone, without reading IDs.
		start := 0
		if feasible && sortedByMax(summaries[i]) {
			start = firstBlockAtOrAfter(summaries[i], lo)
		}
		for j := 0; j < start && j < len(blocks); j++ {
			s := summaries[i][j]
			_, reason := decide(s, lo, hi, feasible)
			lp.Blocks[j] = BlockProof{Summary: s, Reason: reason, Detail: detail(reason, s, lo, hi)}
		}
		for j := start; j < len(blocks); j++ {
			s := summaries[i][j]
			provide, reason := decide(s, lo, hi, feasible)
			bp := BlockProof{Summary: s, Provided: provide, Reason: reason, Detail: detail(reason, s, lo, hi)}
			if provide {
				bp.IDs = blocks[j].IDs
				for _, id := range blocks[j].IDs {
					if id >= lo && id <= hi {
						sets[i][id] = struct{}{}
					}
				}
			}
			lp.Blocks[j] = bp
		}
		proof.Lists[i] = lp
	}
	proof.Result = intersectSets(sets)
	return proof, root, nil
}

// Verify checks a proof against the trusted manifest root and returns the
// confirmed intersection. It rejects tampered summaries, tampered or missing
// block contents, unjustified skips, and results with missing or extra IDs.
func Verify(root [32]byte, proof Proof) ([]string, error) {
	summaries := make([][]BlockSummary, len(proof.Lists))
	for i, lp := range proof.Lists {
		summaries[i] = make([]BlockSummary, len(lp.Blocks))
		for j, bp := range lp.Blocks {
			summaries[i][j] = bp.Summary
		}
	}
	if got := ManifestRoot(summaries); got != root {
		return nil, errors.New("manifest root mismatch: a block summary was tampered with or a block was dropped")
	}

	lo, hi, feasible := feasibleRange(summaries)
	sets := make([]map[string]struct{}, len(proof.Lists))
	for i, lp := range proof.Lists {
		sets[i] = make(map[string]struct{})
		for j, bp := range lp.Blocks {
			mustProvide, reason := decide(bp.Summary, lo, hi, feasible)
			if bp.Provided {
				s, err := Summarize(Block{IDs: bp.IDs})
				if err != nil {
					return nil, fmt.Errorf("list %d block %d: %w", i, j, err)
				}
				if s != bp.Summary {
					return nil, fmt.Errorf("list %d block %d: contents do not match the committed summary", i, j)
				}
				for _, id := range bp.IDs {
					if id >= lo && id <= hi {
						sets[i][id] = struct{}{}
					}
				}
				continue
			}
			if mustProvide {
				return nil, fmt.Errorf("list %d block %d: overlaps the feasible range [%q, %q] but was not provided", i, j, lo, hi)
			}
			if bp.Reason != reason {
				return nil, fmt.Errorf("list %d block %d: invalid skip justification: got %q, want %q", i, j, bp.Reason, reason)
			}
		}
	}

	want := intersectSets(sets)
	if !equalIDs(want, proof.Result) {
		return nil, fmt.Errorf("result mismatch: proof claims %v, evidence yields %v", proof.Result, want)
	}
	return want, nil
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
