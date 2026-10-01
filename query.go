package graphindex

import (
	"encoding/binary"
	"fmt"
	"sort"
)

func (g *Graph) NumNodes() uint32 { return g.numNodes }

// Stats returns the byte-occupancy breakdown of the index.
func (g *Graph) Stats() Stats { return g.stats }

// section returns the raw encoded bytes of node u's adjacency section.
func (g *Graph) section(u uint32) []byte {
	return g.data[g.offsets[u]:g.offsets[u+1]]
}

// multSection returns the raw multiplicity sidecar bytes of node u.
func (g *Graph) multSection(u uint32) []byte {
	return g.mult[g.multOff[u]:g.multOff[u+1]]
}

// parseNode parses the header (deg, skip table) of one node section and
// validates structural invariants. payload bounds are checked lazily.
func parseNode(section []byte, numNodes uint32) (nodeHeader, error) {
	var h nodeHeader
	deg, m := binary.Uvarint(section)
	if m <= 0 {
		return h, fmt.Errorf("degree varint: %w", ErrCorrupt)
	}
	if deg > uint64(numNodes) {
		return h, fmt.Errorf("degree %d exceeds numNodes %d: %w", deg, numNodes, ErrCorrupt)
	}
	rest := section[m:]
	nskips, m := binary.Uvarint(rest)
	if m <= 0 {
		return h, fmt.Errorf("nskips varint: %w", ErrCorrupt)
	}
	if want := (deg + BlockSize - 1) / BlockSize; nskips != want {
		return h, fmt.Errorf("nskips %d, want %d for degree %d: %w", nskips, want, deg, ErrCorrupt)
	}
	rest = rest[m:]
	var prevAbs, prevOff uint64
	for i := uint64(0); i < nskips; i++ {
		da, m := binary.Uvarint(rest)
		if m <= 0 {
			return h, fmt.Errorf("skip %d absID varint: %w", i, ErrCorrupt)
		}
		rest = rest[m:]
		prevAbs += da
		if prevAbs >= uint64(numNodes) {
			return h, fmt.Errorf("skip %d absID %d out of range [0,%d): %w", i, prevAbs, numNodes, ErrCorrupt)
		}
		do, m := binary.Uvarint(rest)
		if m <= 0 {
			return h, fmt.Errorf("skip %d offset varint: %w", i, ErrCorrupt)
		}
		rest = rest[m:]
		prevOff += do
		if prevOff > uint64(len(section)) {
			return h, fmt.Errorf("skip %d offset %d beyond section: %w", i, prevOff, ErrCorrupt)
		}
		h.skips = append(h.skips, skipEntry{absID: uint32(prevAbs), offset: uint32(prevOff)})
	}
	h.deg = uint32(deg)
	h.payload = rest
	for i, s := range h.skips {
		if uint64(s.offset) > uint64(len(rest)) {
			return h, fmt.Errorf("skip %d offset %d beyond payload %d: %w", i, s.offset, len(rest), ErrCorrupt)
		}
		if i > 0 && s.absID <= h.skips[i-1].absID {
			return h, fmt.Errorf("skip %d absID not increasing: %w", i, ErrCorrupt)
		}
	}
	return h, nil
}

// decodeAll reconstructs the full sorted neighbor list, validating every
// delta: strictly increasing ids, all < numNodes, offsets inside payload.
func decodeAll(h *nodeHeader, numNodes uint32) ([]uint32, error) {
	ids := make([]uint32, 0, h.deg)
	pos := 0
	var prev uint32
	for i := uint32(0); i < h.deg; i++ {
		var id uint32
		if i%BlockSize == 0 {
			s := h.skips[i/BlockSize]
			if int(s.offset) > len(h.payload) {
				return nil, fmt.Errorf("block %d offset %d beyond payload: %w", i/BlockSize, s.offset, ErrCorrupt)
			}
			pos = int(s.offset)
			id = s.absID
		} else {
			if pos >= len(h.payload) {
				return nil, fmt.Errorf("payload truncated at neighbor %d: %w", i, ErrCorrupt)
			}
			d, m := binary.Uvarint(h.payload[pos:])
			if m <= 0 {
				return nil, fmt.Errorf("delta varint at neighbor %d: %w", i, ErrCorrupt)
			}
			pos += m
			if d == 0 {
				return nil, fmt.Errorf("zero delta at neighbor %d (duplicate id): %w", i, ErrCorrupt)
			}
			next := uint64(prev) + d
			if next >= uint64(numNodes) {
				return nil, fmt.Errorf("delta at neighbor %d reaches id %d, out of range [0,%d): %w",
					i, next, numNodes, ErrCorrupt)
			}
			id = uint32(next)
		}
		if i > 0 && id <= prev {
			return nil, fmt.Errorf("neighbor %d id %d not increasing after %d: %w", i, id, prev, ErrCorrupt)
		}
		ids = append(ids, id)
		prev = id
	}
	return ids, nil
}

// findIndex locates neighbor v via the skip table, walking at most
// BlockSize deltas. Returns the neighbor index and whether it was found.
func findIndex(h *nodeHeader, numNodes uint32, v uint32) (int, bool, error) {
	if h.deg == 0 || v < h.skips[0].absID {
		return 0, false, nil
	}
	// Last skip whose absID <= v: binary search over the skip table.
	bi := sort.Search(len(h.skips), func(i int) bool { return h.skips[i].absID > v }) - 1
	s := h.skips[bi]
	pos := int(s.offset)
	prev := s.absID
	base := bi * BlockSize
	if prev == v {
		return base, true, nil
	}
	end := base + BlockSize
	if d := int(h.deg); end > d {
		end = d
	}
	for i := base + 1; i < end; i++ {
		if pos >= len(h.payload) {
			return 0, false, fmt.Errorf("payload truncated at neighbor %d: %w", i, ErrCorrupt)
		}
		d, m := binary.Uvarint(h.payload[pos:])
		if m <= 0 {
			return 0, false, fmt.Errorf("delta varint at neighbor %d: %w", i, ErrCorrupt)
		}
		pos += m
		if d == 0 {
			return 0, false, fmt.Errorf("zero delta at neighbor %d: %w", i, ErrCorrupt)
		}
		next := uint64(prev) + d
		if next >= uint64(numNodes) {
			return 0, false, fmt.Errorf("delta at neighbor %d reaches id %d, out of range [0,%d): %w",
				i, next, numNodes, ErrCorrupt)
		}
		prev = uint32(next)
		if prev == v {
			return i, true, nil
		}
		if prev > v {
			return 0, false, nil
		}
	}
	return 0, false, nil
}

// header parses node u's section; u must be in range.
func (g *Graph) header(u uint32) (nodeHeader, error) {
	return parseNode(g.section(u), g.numNodes)
}

// Neighbors decodes the full sorted neighbor list of u (unique ids).
func (g *Graph) Neighbors(u uint32) ([]uint32, error) {
	if u >= g.numNodes {
		return nil, fmt.Errorf("node %d out of range [0,%d)", u, g.numNodes)
	}
	h, err := g.header(u)
	if err != nil {
		return nil, err
	}
	return decodeAll(&h, g.numNodes)
}

// Degree returns the number of distinct neighbors of u (self-loop counts
// once; parallel edges do not inflate it). O(size of header).
func (g *Graph) Degree(u uint32) (uint32, error) {
	if u >= g.numNodes {
		return 0, fmt.Errorf("node %d out of range [0,%d)", u, g.numNodes)
	}
	h, err := g.header(u)
	if err != nil {
		return 0, err
	}
	return h.deg, nil
}

// HasEdge reports whether v is a neighbor of u, using skip-table search.
func (g *Graph) HasEdge(u, v uint32) (bool, error) {
	if u >= g.numNodes || v >= g.numNodes {
		return false, fmt.Errorf("edge (%d,%d) out of range [0,%d)", u, v, g.numNodes)
	}
	h, err := g.header(u)
	if err != nil {
		return false, err
	}
	_, ok, err := findIndex(&h, g.numNodes, v)
	return ok, err
}

// Multiplicity returns how many parallel edge instances (u,v) exist.
// Returns 0 when v is not a neighbor of u.
func (g *Graph) Multiplicity(u, v uint32) (uint32, error) {
	if u >= g.numNodes || v >= g.numNodes {
		return 0, fmt.Errorf("edge (%d,%d) out of range [0,%d)", u, v, g.numNodes)
	}
	h, err := g.header(u)
	if err != nil {
		return 0, err
	}
	idx, ok, err := findIndex(&h, g.numNodes, v)
	if err != nil || !ok {
		return 0, err
	}
	extra, err := lookupExtra(g.multSection(u), uint32(idx), h.deg)
	if err != nil {
		return 0, err
	}
	return 1 + extra, nil
}

// WeightedDegree returns the total number of edge instances incident to u
// (distinct neighbors plus extra parallel copies).
func (g *Graph) WeightedDegree(u uint32) (uint64, error) {
	if u >= g.numNodes {
		return 0, fmt.Errorf("node %d out of range [0,%d)", u, g.numNodes)
	}
	h, err := g.header(u)
	if err != nil {
		return 0, err
	}
	total := uint64(h.deg)
	sec := g.multSection(u)
	n, m := binary.Uvarint(sec)
	if m <= 0 {
		return 0, fmt.Errorf("mult count varint: %w", ErrCorrupt)
	}
	rest := sec[m:]
	var prev uint64
	for i := uint64(0); i < n; i++ {
		d, m := binary.Uvarint(rest)
		if m <= 0 {
			return 0, fmt.Errorf("mult idx varint: %w", ErrCorrupt)
		}
		rest = rest[m:]
		prev += d
		if prev >= uint64(h.deg) {
			return 0, fmt.Errorf("mult idx %d >= degree %d: %w", prev, h.deg, ErrCorrupt)
		}
		e, m := binary.Uvarint(rest)
		if m <= 0 {
			return 0, fmt.Errorf("mult extra varint: %w", ErrCorrupt)
		}
		rest = rest[m:]
		if e == 0 {
			return 0, fmt.Errorf("mult extra 0 (should be >=1): %w", ErrCorrupt)
		}
		total += e
	}
	return total, nil
}

// lookupExtra scans the sidecar for the extra count of neighbor index idx.
func lookupExtra(sec []byte, idx uint32, deg uint32) (uint32, error) {
	n, m := binary.Uvarint(sec)
	if m <= 0 {
		return 0, fmt.Errorf("mult count varint: %w", ErrCorrupt)
	}
	rest := sec[m:]
	var prev uint64
	for i := uint64(0); i < n; i++ {
		d, m := binary.Uvarint(rest)
		if m <= 0 {
			return 0, fmt.Errorf("mult idx varint: %w", ErrCorrupt)
		}
		rest = rest[m:]
		prev += d
		if prev >= uint64(deg) {
			return 0, fmt.Errorf("mult idx %d >= degree %d: %w", prev, deg, ErrCorrupt)
		}
		e, m := binary.Uvarint(rest)
		if m <= 0 {
			return 0, fmt.Errorf("mult extra varint: %w", ErrCorrupt)
		}
		rest = rest[m:]
		if uint32(prev) == idx {
			if e == 0 {
				return 0, fmt.Errorf("mult extra 0: %w", ErrCorrupt)
			}
			return uint32(e), nil
		}
		if uint32(prev) > idx {
			return 0, nil
		}
	}
	return 0, nil
}

// Intersect returns the sorted common-neighbor list of u and v via a
// merge join over the two decoded lists.
func (g *Graph) Intersect(u, v uint32) ([]uint32, error) {
	a, err := g.Neighbors(u)
	if err != nil {
		return nil, err
	}
	b, err := g.Neighbors(v)
	if err != nil {
		return nil, err
	}
	var out []uint32
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out, nil
}

// Validate fully decodes every node section and sidecar, rejecting any
// corruption (out-of-range ids, broken deltas, bad offsets, bad mult).
func (g *Graph) Validate() error {
	for u := uint32(0); u < g.numNodes; u++ {
		h, err := parseNode(g.section(u), g.numNodes)
		if err != nil {
			return fmt.Errorf("node %d: %w", u, err)
		}
		if _, err := decodeAll(&h, g.numNodes); err != nil {
			return fmt.Errorf("node %d: %w", u, err)
		}
		if _, err := g.WeightedDegree(u); err != nil {
			return fmt.Errorf("node %d: %w", u, err)
		}
	}
	return nil
}
