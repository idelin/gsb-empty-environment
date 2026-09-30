// Package cgraph implements a compressed, read-only adjacency index.
//
// Encoding layout (per node, one contiguous section in a shared buffer):
//
//	uvarint distinctCount          // number of distinct neighbors
//	uvarint totalDegree            // sum of multiplicities
//	record x distinctCount:
//	  uvarint delta                // first record: neighbor id itself;
//	                               // later records: id - prevId (>= 1)
//	  uvarint multMinus1           // parallel-edge multiplicity - 1
//
// Neighbors are sorted before encoding, so deltas are positive and the
// record stream is a merge-joinable sorted list. Self-loops are kept as a
// record whose neighbor id equals the node itself. Parallel edges are NOT
// dropped: their multiplicity is stored alongside the delta.
//
// Skip index: every SkipInterval-th record is sampled into
// (prevValue, offset) so membership queries can binary-search the samples
// and decode at most one block instead of the whole list. prevValue is the
// absolute id of the record just before the block, which makes deltas
// decodable from a cold start at the block boundary.
package cgraph

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// SkipInterval is the number of records between two skip-index samples.
const SkipInterval = 16

// ErrCorrupt is returned when the encoded stream is inconsistent:
// truncated varints, non-increasing deltas, or neighbor ids that run
// past the node space.
var ErrCorrupt = errors.New("cgraph: corrupt encoding")

// Edge is one input edge. Parallel edges and self-loops are allowed.
type Edge struct {
	From, To uint32
}

type skipEntry struct {
	prevValue uint32 // absolute id of the record just before this block (0 for block 0)
	offset    uint32 // absolute byte offset of the block's first record in the buffer
}

type nodeInfo struct {
	offset    uint32 // start of the node's section in buf
	count     uint32 // distinct neighbor count
	degree    uint64 // sum of multiplicities
	skipStart uint32 // first skip entry in Graph.skips
	skipCount uint32
}

// Graph is a read-only compressed adjacency index.
type Graph struct {
	numNodes uint32
	buf      []byte
	nodes    []nodeInfo
	skips    []skipEntry
}

// Build constructs an undirected compressed index over numNodes nodes.
// Each edge (u,v) with u != v is stored in both directions; a self-loop
// (u,u) is stored once in u's own list. Parallel edges increase the stored
// multiplicity instead of being dropped.
func Build(numNodes uint32, edges []Edge) (*Graph, error) {
	agg := make([]map[uint32]uint64, numNodes)
	for i, e := range edges {
		if e.From >= numNodes || e.To >= numNodes {
			return nil, fmt.Errorf("cgraph: edge %d (%d,%d) references node >= %d", i, e.From, e.To, numNodes)
		}
		if agg[e.From] == nil {
			agg[e.From] = make(map[uint32]uint64)
		}
		agg[e.From][e.To]++
		if e.From != e.To {
			if agg[e.To] == nil {
				agg[e.To] = make(map[uint32]uint64)
			}
			agg[e.To][e.From]++
		}
	}

	g := &Graph{numNodes: numNodes, nodes: make([]nodeInfo, numNodes)}
	var tmp [binary.MaxVarintLen64]byte
	put := func(v uint64) {
		n := binary.PutUvarint(tmp[:], v)
		g.buf = append(g.buf, tmp[:n]...)
	}

	for u := uint32(0); u < numNodes; u++ {
		m := agg[u]
		ids := make([]uint32, 0, len(m))
		for v := range m {
			ids = append(ids, v)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

		ni := nodeInfo{offset: uint32(len(g.buf)), count: uint32(len(ids)), skipStart: uint32(len(g.skips))}
		for _, v := range ids {
			ni.degree += m[v]
		}
		put(uint64(ni.count))
		put(ni.degree)

		prev := uint32(0)
		for i, v := range ids {
			if i%SkipInterval == 0 {
				g.skips = append(g.skips, skipEntry{prevValue: prev, offset: uint32(len(g.buf))})
				ni.skipCount++
			}
			delta := uint64(v)
			if i > 0 {
				delta = uint64(v - prev)
			}
			put(delta)
			put(m[v] - 1)
			prev = v
		}
		g.nodes[u] = ni
	}
	return g, nil
}

// NumNodes returns the number of nodes in the graph.
func (g *Graph) NumNodes() uint32 { return g.numNodes }

// EncodedBytes returns the size of the delta-encoded adjacency buffer.
func (g *Graph) EncodedBytes() int { return len(g.buf) }

// SkipIndexBytes returns the in-memory size of the skip index.
func (g *Graph) SkipIndexBytes() int { return len(g.skips) * 8 }

// NodeIndexBytes returns the in-memory size of the per-node directory.
func (g *Graph) NodeIndexBytes() int { return len(g.nodes) * 24 }

// TotalBytes returns the total in-memory footprint of the index.
func (g *Graph) TotalBytes() int {
	return g.EncodedBytes() + g.SkipIndexBytes() + g.NodeIndexBytes()
}

// RawBuffer exposes the encoded buffer for diagnostics and fuzzing.
// Mutating it corrupts the index; Validate detects such corruption.
func (g *Graph) RawBuffer() []byte { return g.buf }

func (g *Graph) section(u uint32) []byte {
	start := g.nodes[u].offset
	end := uint32(len(g.buf))
	if u+1 < g.numNodes {
		end = g.nodes[u+1].offset
	}
	return g.buf[start:end]
}

// Iter decodes one node's adjacency records lazily.
type Iter struct {
	section []byte
	limit   uint32 // decoded ids must stay below this (numNodes)
	count   uint32
	pos     int
	idx     uint32
	prev    uint32
	cur     uint32
	mult    uint64
	err     error
}

func (g *Graph) newIter(u uint32) (*Iter, error) {
	sec := g.section(u)
	count, n1 := binary.Uvarint(sec)
	if n1 <= 0 {
		return nil, ErrCorrupt
	}
	_, n2 := binary.Uvarint(sec[n1:])
	if n2 <= 0 {
		return nil, ErrCorrupt
	}
	return &Iter{section: sec, limit: g.numNodes, count: uint32(count), pos: n1 + n2}, nil
}

// seek positions the iterator at record idx, whose delta is relative to prev.
func (it *Iter) seek(pos int, idx uint32, prev uint32) {
	it.pos = pos
	it.idx = idx
	it.prev = prev
}

// Next advances to the next record. It returns false at the end of the
// list or on corruption; check Err to distinguish the two.
func (it *Iter) Next() bool {
	if it.err != nil || it.idx >= it.count {
		return false
	}
	d, n := binary.Uvarint(it.section[it.pos:])
	if n <= 0 {
		it.err = ErrCorrupt
		return false
	}
	it.pos += n
	m, n := binary.Uvarint(it.section[it.pos:])
	if n <= 0 {
		it.err = ErrCorrupt
		return false
	}
	it.pos += n
	var v uint64
	if it.idx == 0 {
		v = d
	} else {
		if d == 0 {
			it.err = ErrCorrupt // non-increasing neighbor ids
			return false
		}
		v = uint64(it.prev) + d
	}
	if v >= uint64(it.limit) {
		it.err = ErrCorrupt // delta ran past the node space
		return false
	}
	it.cur = uint32(v)
	it.mult = m + 1
	it.prev = it.cur
	it.idx++
	return true
}

// Value returns the current neighbor id and its edge multiplicity.
func (it *Iter) Value() (uint32, uint64) { return it.cur, it.mult }

// Err returns the corruption error that stopped the iterator, if any.
func (it *Iter) Err() error { return it.err }

// Degree returns the total edge multiplicity incident to u (O(1)).
// A self-loop contributes its multiplicity once.
func (g *Graph) Degree(u uint32) (uint64, error) {
	if u >= g.numNodes {
		return 0, fmt.Errorf("cgraph: node %d out of range (%d nodes)", u, g.numNodes)
	}
	return g.nodes[u].degree, nil
}

// DistinctNeighbors returns the number of distinct neighbors of u (O(1)).
func (g *Graph) DistinctNeighbors(u uint32) (uint32, error) {
	if u >= g.numNodes {
		return 0, fmt.Errorf("cgraph: node %d out of range (%d nodes)", u, g.numNodes)
	}
	return g.nodes[u].count, nil
}

// Multiplicity returns the number of parallel edges between u and v.
// It binary-searches the skip index, then decodes at most one block.
func (g *Graph) Multiplicity(u, v uint32) (uint64, error) {
	if u >= g.numNodes {
		return 0, fmt.Errorf("cgraph: node %d out of range (%d nodes)", u, g.numNodes)
	}
	if v >= g.numNodes {
		return 0, nil
	}
	ni := g.nodes[u]
	if ni.count == 0 {
		return 0, nil
	}
	it, err := g.newIter(u)
	if err != nil {
		return 0, fmt.Errorf("cgraph: node %d: %v", u, err)
	}
	lo, hi := ni.skipStart, ni.skipStart+ni.skipCount
	// Last block whose prevValue < v may contain v.
	k := sort.Search(int(hi-lo), func(i int) bool {
		return g.skips[lo+uint32(i)].prevValue >= v
	}) - 1
	if k > 0 {
		sk := g.skips[lo+uint32(k)]
		it.seek(int(sk.offset-ni.offset), uint32(k)*SkipInterval, sk.prevValue)
	}
	for it.Next() {
		cv, m := it.Value()
		if cv == v {
			return m, nil
		}
		if cv > v {
			return 0, nil
		}
	}
	if err := it.Err(); err != nil {
		return 0, fmt.Errorf("cgraph: node %d: %v", u, err)
	}
	return 0, nil
}

// HasEdge reports whether v is a neighbor of u.
func (g *Graph) HasEdge(u, v uint32) (bool, error) {
	m, err := g.Multiplicity(u, v)
	return m > 0, err
}

// Neighbors decodes u's full adjacency list: sorted neighbor ids and the
// matching multiplicities.
func (g *Graph) Neighbors(u uint32) ([]uint32, []uint64, error) {
	if u >= g.numNodes {
		return nil, nil, fmt.Errorf("cgraph: node %d out of range (%d nodes)", u, g.numNodes)
	}
	it, err := g.newIter(u)
	if err != nil {
		return nil, nil, fmt.Errorf("cgraph: node %d: %v", u, err)
	}
	var ids []uint32
	var mults []uint64
	for it.Next() {
		v, m := it.Value()
		ids = append(ids, v)
		mults = append(mults, m)
	}
	if err := it.Err(); err != nil {
		return nil, nil, fmt.Errorf("cgraph: node %d: %v", u, err)
	}
	return ids, mults, nil
}

// Intersection returns the sorted common neighbors of u and w by
// merge-joining the two encoded lists without fully materializing them.
func (g *Graph) Intersection(u, w uint32) ([]uint32, error) {
	if u >= g.numNodes || w >= g.numNodes {
		return nil, fmt.Errorf("cgraph: node out of range (%d nodes)", g.numNodes)
	}
	iu, err := g.newIter(u)
	if err != nil {
		return nil, fmt.Errorf("cgraph: node %d: %v", u, err)
	}
	iw, err := g.newIter(w)
	if err != nil {
		return nil, fmt.Errorf("cgraph: node %d: %v", w, err)
	}
	var out []uint32
	oku, okw := iu.Next(), iw.Next()
	for oku && okw {
		cu, _ := iu.Value()
		cw, _ := iw.Value()
		switch {
		case cu < cw:
			oku = iu.Next()
		case cu > cw:
			okw = iw.Next()
		default:
			out = append(out, cu)
			oku = iu.Next()
			okw = iw.Next()
		}
	}
	if err := iu.Err(); err != nil {
		return nil, fmt.Errorf("cgraph: node %d: %v", u, err)
	}
	if err := iw.Err(); err != nil {
		return nil, fmt.Errorf("cgraph: node %d: %v", w, err)
	}
	return out, nil
}

// Validate fully decodes every node section and rejects corruption:
// truncated varints, zero/negative deltas, neighbor ids out of range,
// header/directory mismatches, and trailing garbage bytes.
func (g *Graph) Validate() error {
	for u := uint32(0); u < g.numNodes; u++ {
		ni := g.nodes[u]
		sec := g.section(u)
		it, err := g.newIter(u)
		if err != nil {
			return fmt.Errorf("cgraph: node %d: %v", u, err)
		}
		if it.count != ni.count {
			return fmt.Errorf("cgraph: node %d: header count %d != directory count %d", u, it.count, ni.count)
		}
		var total uint64
		for it.Next() {
			_, m := it.Value()
			total += m
		}
		if err := it.Err(); err != nil {
			return fmt.Errorf("cgraph: node %d: %v", u, err)
		}
		if total != ni.degree {
			return fmt.Errorf("cgraph: node %d: decoded degree %d != directory degree %d", u, total, ni.degree)
		}
		if it.pos != len(sec) {
			return fmt.Errorf("cgraph: node %d: %d trailing bytes after %d records", u, len(sec)-it.pos, ni.count)
		}
	}
	return nil
}
