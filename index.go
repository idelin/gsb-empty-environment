package blockindex

import (
	"errors"
	"fmt"
)

const (
	// DefaultBlockCapacity is the maximum number of entries per block.
	DefaultBlockCapacity = 32
	// DefaultFanout is the maximum number of children per summary node.
	DefaultFanout = 8
)

// RootSummary is the trusted commitment a verifier needs. Everything else
// (blocks, summary nodes) is untrusted and must be proven against it.
type RootSummary struct {
	Version      uint64
	Height       int // number of summary levels; 0 for an empty index
	TotalEntries uint64
	TotalBlocks  uint64
	RootDigest   [DigestLen]byte
}

// emptyRootDigest commits to the empty index.
var emptyRootDigest = digestOf([]byte("blockindex-empty-root-v1"))

// Index is a sorted block index plus its summary tree. Blocks and nodes are
// stored in their serialized wire form; the in-memory blockInfo mirrors the
// leaf-level ChildInfo for prover convenience.
type Index struct {
	Version   uint64
	Fanout    int
	BlockCap  int
	Blocks    [][]byte   // serialized blocks, in key order
	Levels    [][][]byte // Levels[0] is summary level 1 (directly above blocks)
	Root      RootSummary
	blockInfo []ChildInfo
}

// Build packs sorted, unique-keyed entries into blocks of at most blockCap
// entries and builds the summary tree with the given fanout.
func Build(version uint64, entries []Entry, blockCap, fanout int) (*Index, error) {
	if blockCap < 1 {
		return nil, errors.New("blockindex: block capacity must be >= 1")
	}
	if fanout < 2 {
		return nil, errors.New("blockindex: fanout must be >= 2")
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].Key <= entries[i-1].Key {
			return nil, fmt.Errorf("blockindex: entries not sorted/unique at %d", i)
		}
	}
	idx := &Index{Version: version, Fanout: fanout, BlockCap: blockCap}
	if len(entries) == 0 {
		idx.Root = RootSummary{Version: version, RootDigest: emptyRootDigest}
		return idx, nil
	}
	for i := 0; i < len(entries); i += blockCap {
		end := i + blockCap
		if end > len(entries) {
			end = len(entries)
		}
		raw := EncodeBlock(version, entries[i:end])
		idx.Blocks = append(idx.Blocks, raw)
		idx.blockInfo = append(idx.blockInfo, ChildInfo{
			MinKey:  entries[i].Key,
			MaxKey:  entries[end-1].Key,
			Entries: uint64(end - i),
			Blocks:  1,
			Digest:  BlockDigest(raw),
		})
	}
	// Build summary levels until a single root node remains. Even a single
	// block gets one summary level so the root always commits to counts.
	cur := idx.blockInfo
	level := uint64(1)
	for {
		var nodes [][]byte
		var next []ChildInfo
		for i := 0; i < len(cur); i += fanout {
			end := i + fanout
			if end > len(cur) {
				end = len(cur)
			}
			children := cur[i:end]
			raw := EncodeNode(version, level, children)
			nodes = append(nodes, raw)
			agg := ChildInfo{
				MinKey: children[0].MinKey,
				MaxKey: children[len(children)-1].MaxKey,
				Digest: NodeDigest(raw),
			}
			for _, c := range children {
				agg.Entries += c.Entries
				agg.Blocks += c.Blocks
			}
			next = append(next, agg)
		}
		idx.Levels = append(idx.Levels, nodes)
		if len(nodes) == 1 {
			idx.Root = RootSummary{
				Version:      version,
				Height:       int(level),
				TotalEntries: next[0].Entries,
				TotalBlocks:  next[0].Blocks,
				RootDigest:   next[0].Digest,
			}
			return idx, nil
		}
		cur = next
		level++
	}
}

// NumBlocks returns the number of leaf blocks.
func (idx *Index) NumBlocks() int { return len(idx.Blocks) }

// SummaryBytes returns the total serialized size of all summary levels.
func (idx *Index) SummaryBytes() int {
	total := 0
	for _, lvl := range idx.Levels {
		for _, n := range lvl {
			total += len(n)
		}
	}
	return total
}

// BlockBytes returns the total serialized size of all blocks.
func (idx *Index) BlockBytes() int {
	total := 0
	for _, b := range idx.Blocks {
		total += len(b)
	}
	return total
}

// MaxBlockBytes returns the size of the largest serialized block.
func (idx *Index) MaxBlockBytes() int {
	max := 0
	for _, b := range idx.Blocks {
		if len(b) > max {
			max = len(b)
		}
	}
	return max
}
