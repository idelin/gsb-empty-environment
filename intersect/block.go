// Package intersect implements a multi-list intersection tool with
// checkable evidence. Each sorted ID list is stored in independent
// blocks. Every block carries a summary (min, max, count, hash).
// The prover may skip blocks whose range cannot contain any common
// ID, but must record the justification. Range overlap alone never
// implies an actual hit: overlapping blocks are always read and their
// contents inspected.
package intersect

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
)

// Summary describes a block without revealing (or requiring) its
// contents. Hash commits to the exact normalized ID sequence, so any
// tampering with either the contents or the summary is detectable.
type Summary struct {
	Min   uint64
	Max   uint64
	Count int
	Hash  [sha256.Size]byte
}

// Block is an independent, sorted, duplicate-free run of IDs plus its
// summary.
type Block struct {
	IDs     []uint64
	Summary Summary
}

// hashIDs commits to the exact ID sequence (length-prefixed).
func hashIDs(ids []uint64) [sha256.Size]byte {
	h := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(len(ids)))
	h.Write(buf[:])
	for _, id := range ids {
		binary.BigEndian.PutUint64(buf[:], id)
		h.Write(buf[:])
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Normalize sorts ids and removes duplicates, returning a fresh slice.
func Normalize(ids []uint64) []uint64 {
	if len(ids) == 0 {
		return nil
	}
	sorted := make([]uint64, len(ids))
	copy(sorted, ids)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	out := sorted[:1]
	for _, id := range sorted[1:] {
		if id != out[len(out)-1] {
			out = append(out, id)
		}
	}
	return out
}

// Summarize recomputes the summary of an already-normalized ID slice.
func Summarize(ids []uint64) Summary {
	s := Summary{Count: len(ids), Hash: hashIDs(ids)}
	if len(ids) > 0 {
		s.Min = ids[0]
		s.Max = ids[len(ids)-1]
	}
	return s
}

// NewBlock normalizes ids (sort + dedupe) and builds a block with a
// consistent summary.
func NewBlock(ids []uint64) Block {
	norm := Normalize(ids)
	return Block{IDs: norm, Summary: Summarize(norm)}
}

// validateList checks per-block integrity (sorted, unique, consistent
// stored summary) and that non-empty blocks are ordered and disjoint.
func validateList(list []Block) error {
	prevMax := uint64(0)
	hasPrev := false
	for i, b := range list {
		for j := 1; j < len(b.IDs); j++ {
			if b.IDs[j] <= b.IDs[j-1] {
				return fmt.Errorf("block %d: IDs not strictly increasing", i)
			}
		}
		if Summarize(b.IDs) != b.Summary {
			return fmt.Errorf("block %d: stored summary does not match contents", i)
		}
		if b.Summary.Count == 0 {
			continue
		}
		if hasPrev && b.Summary.Min <= prevMax {
			return fmt.Errorf("block %d: overlaps or precedes previous block", i)
		}
		prevMax, hasPrev = b.Summary.Max, true
	}
	return nil
}
