// Package graphindex implements a compressed static graph index.
//
// Layout: neighbors of each node are sorted, delta-encoded as uvarints,
// and every BlockSize-th neighbor is also stored as an absolute "skip"
// entry (absolute id + payload offset) so membership tests and decode
// can jump instead of scanning from the head. Parallel-edge multiplicities
// live in a separate sidecar section and do not change graph semantics.
package graphindex

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// BlockSize is the number of neighbors per skip-list block.
const BlockSize = 8

// ErrCorrupt is returned (wrapped) when encoded bytes fail validation.
var ErrCorrupt = errors.New("graphindex: corrupt encoding")

type skipEntry struct {
	absID  uint32 // absolute id of the first neighbor in the block
	offset uint32 // byte offset of the block's deltas within the payload
}

// nodeHeader is the parsed view of one node's section.
type nodeHeader struct {
	deg     uint32
	skips   []skipEntry
	payload []byte
}

// Builder accumulates edges (including parallel ones) before compression.
// The map lives only at build time; the built Graph holds no maps.
type Builder struct {
	n   uint32
	adj map[uint32]map[uint32]uint32 // u -> v -> multiplicity
}

func NewBuilder(numNodes uint32) *Builder {
	return &Builder{n: numNodes, adj: make(map[uint32]map[uint32]uint32)}
}

// AddEdge adds one edge instance; repeated calls accumulate multiplicity.
// Self-loops (u == v) are preserved.
func (b *Builder) AddEdge(u, v uint32) error {
	if u >= b.n || v >= b.n {
		return fmt.Errorf("graphindex: edge (%d,%d) out of range [0,%d)", u, v, b.n)
	}
	m := b.adj[u]
	if m == nil {
		m = make(map[uint32]uint32)
		b.adj[u] = m
	}
	m[v]++
	return nil
}

// Stats reports the real on-disk layout occupancy in bytes.
type Stats struct {
	NumNodes      uint32
	UniqueEdges   uint64 // sum of distinct neighbors per node
	EdgeInstances uint64 // sum of multiplicities (parallel edges counted)
	OffsetBytes   uint64 // offsets[] + multOff[] arrays (8 bytes each entry)
	HeaderBytes   uint64 // per-node deg/nskips varints
	SkipBytes     uint64 // skip table entries
	PayloadBytes  uint64 // delta payload
	MultBytes     uint64 // multiplicity sidecar
	TotalBytes    uint64 // everything above
	NaiveBytes    uint64 // baseline: 4 bytes per unique neighbor + same offsets
}

// Graph is the compressed index. It contains only byte slices and offset
// arrays; no per-edge Go maps or slices are kept.
type Graph struct {
	numNodes uint32
	offsets  []uint64 // n+1 offsets into data
	data     []byte   // concatenated node sections
	multOff  []uint64 // n+1 offsets into mult
	mult     []byte   // multiplicity sidecar sections
	stats    Stats
}

// Build compresses the accumulated edges.
func (b *Builder) Build() *Graph {
	g := &Graph{
		numNodes: b.n,
		offsets:  make([]uint64, uint64(b.n)+1),
		multOff:  make([]uint64, uint64(b.n)+1),
	}
	var data, mult []byte
	var hdrBytes, skipBytes, payBytes uint64
	var unique, instances uint64
	for u := uint32(0); u < b.n; u++ {
		g.offsets[u] = uint64(len(data))
		g.multOff[u] = uint64(len(mult))
		counts := b.adj[u]
		nbrs := make([]uint32, 0, len(counts))
		for v, c := range counts {
			nbrs = append(nbrs, v)
			instances += uint64(c)
		}
		sort.Slice(nbrs, func(i, j int) bool { return nbrs[i] < nbrs[j] })
		unique += uint64(len(nbrs))
		var h, s, p int
		data, h, s, p = encodeNode(data, nbrs)
		hdrBytes += uint64(h)
		skipBytes += uint64(s)
		payBytes += uint64(p)
		mult = encodeMult(mult, nbrs, counts)
	}
	g.offsets[b.n] = uint64(len(data))
	g.multOff[b.n] = uint64(len(mult))
	g.data = data
	g.mult = mult
	offBytes := uint64(len(g.offsets)+len(g.multOff)) * 8
	g.stats = Stats{
		NumNodes:      b.n,
		UniqueEdges:   unique,
		EdgeInstances: instances,
		OffsetBytes:   offBytes,
		HeaderBytes:   hdrBytes,
		SkipBytes:     skipBytes,
		PayloadBytes:  payBytes,
		MultBytes:     uint64(len(mult)),
		NaiveBytes:    unique*4 + offBytes,
	}
	g.stats.TotalBytes = offBytes + hdrBytes + skipBytes + payBytes + uint64(len(mult))
	return g
}

// encodeNode emits: deg, nskips, skip table (delta absID, delta offset),
// then the delta payload. The first neighbor of each block is the skip's
// absolute id; the remaining BlockSize-1 ids are uvarint deltas.
func encodeNode(dst []byte, nbrs []uint32) ([]byte, int, int, int) {
	deg := len(nbrs)
	head := binary.AppendUvarint(nil, uint64(deg))
	nskips := (deg + BlockSize - 1) / BlockSize
	head = binary.AppendUvarint(head, uint64(nskips))

	var payload []byte
	var skips []skipEntry
	for i := 0; i < deg; i++ {
		if i%BlockSize == 0 {
			skips = append(skips, skipEntry{absID: nbrs[i], offset: uint32(len(payload))})
			continue
		}
		payload = binary.AppendUvarint(payload, uint64(nbrs[i]-nbrs[i-1]))
	}
	var skipTbl []byte
	var prevAbs, prevOff uint32
	for _, s := range skips {
		skipTbl = binary.AppendUvarint(skipTbl, uint64(s.absID-prevAbs))
		skipTbl = binary.AppendUvarint(skipTbl, uint64(s.offset-prevOff))
		prevAbs, prevOff = s.absID, s.offset
	}
	dst = append(dst, head...)
	dst = append(dst, skipTbl...)
	dst = append(dst, payload...)
	return dst, len(head), len(skipTbl), len(payload)
}

// encodeMult stores (neighborIndex delta, extraCount) pairs for neighbors
// whose multiplicity is > 1. extraCount = count-1 >= 1.
func encodeMult(dst []byte, nbrs []uint32, counts map[uint32]uint32) []byte {
	type pair struct {
		idx   uint32
		extra uint32
	}
	var pairs []pair
	for i, v := range nbrs {
		if c := counts[v]; c > 1 {
			pairs = append(pairs, pair{uint32(i), c - 1})
		}
	}
	dst = binary.AppendUvarint(dst, uint64(len(pairs)))
	var prev uint32
	for _, p := range pairs {
		dst = binary.AppendUvarint(dst, uint64(p.idx-prev))
		dst = binary.AppendUvarint(dst, uint64(p.extra))
		prev = p.idx
	}
	return dst
}
