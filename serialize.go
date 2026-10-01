package graphindex

import (
	"encoding/binary"
	"fmt"
)

var magic = []byte{'G', 'I', 'D', 'X'}

// Marshal serializes the index:
//
//	magic(4) | numNodes | len(data) | data | len(mult) | mult |
//	(n+1) delta-encoded data offsets | (n+1) delta-encoded mult offsets
//
// All integers are uvarints.
func (g *Graph) Marshal() []byte {
	out := append([]byte{}, magic...)
	out = binary.AppendUvarint(out, uint64(g.numNodes))
	out = binary.AppendUvarint(out, uint64(len(g.data)))
	out = append(out, g.data...)
	out = binary.AppendUvarint(out, uint64(len(g.mult)))
	out = append(out, g.mult...)
	var prev uint64
	for _, o := range g.offsets {
		out = binary.AppendUvarint(out, o-prev)
		prev = o
	}
	prev = 0
	for _, o := range g.multOff {
		out = binary.AppendUvarint(out, o-prev)
		prev = o
	}
	return out
}

// Parse deserializes and fully validates the index. Any corruption —
// including deltas that decode to node ids >= numNodes — is rejected.
func Parse(buf []byte) (*Graph, error) {
	fail := func(format string, args ...any) (*Graph, error) {
		return nil, fmt.Errorf("graphindex: parse: %s: %w", fmt.Sprintf(format, args...), ErrCorrupt)
	}
	if len(buf) < len(magic) || string(buf[:len(magic)]) != string(magic) {
		return fail("bad magic")
	}
	rest := buf[len(magic):]
	n, m := binary.Uvarint(rest)
	if m <= 0 {
		return fail("numNodes varint")
	}
	rest = rest[m:]
	dlen, m := binary.Uvarint(rest)
	if m <= 0 || dlen > uint64(len(rest)) {
		return fail("data length %d", dlen)
	}
	rest = rest[m:]
	if uint64(len(rest)) < dlen {
		return fail("data truncated")
	}
	data := rest[:dlen]
	rest = rest[dlen:]
	mlen, m := binary.Uvarint(rest)
	if m <= 0 || mlen > uint64(len(rest)) {
		return fail("mult length %d", mlen)
	}
	rest = rest[m:]
	if uint64(len(rest)) < mlen {
		return fail("mult truncated")
	}
	mult := rest[:mlen]
	rest = rest[mlen:]

	readOffsets := func(limit uint64) ([]uint64, error) {
		off := make([]uint64, 0, n+1)
		var prev uint64
		for i := uint64(0); i <= n; i++ {
			d, m := binary.Uvarint(rest)
			if m <= 0 {
				return nil, fmt.Errorf("offset %d varint: %w", i, ErrCorrupt)
			}
			rest = rest[m:]
			prev += d
			if prev > limit {
				return nil, fmt.Errorf("offset %d value %d beyond limit %d: %w", i, prev, limit, ErrCorrupt)
			}
			off = append(off, prev)
		}
		if prev != limit {
			return nil, fmt.Errorf("last offset %d != section length %d: %w", prev, limit, ErrCorrupt)
		}
		return off, nil
	}
	offsets, err := readOffsets(dlen)
	if err != nil {
		return nil, err
	}
	multOff, err := readOffsets(mlen)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return fail("%d trailing bytes", len(rest))
	}

	g := &Graph{
		numNodes: uint32(n),
		offsets:  offsets,
		data:     append([]byte{}, data...),
		multOff:  multOff,
		mult:     append([]byte{}, mult...),
	}
	// Rebuild stats from the real buffers.
	offBytes := uint64(len(offsets)+len(multOff)) * 8
	g.stats.NumNodes = uint32(n)
	g.stats.OffsetBytes = offBytes
	g.stats.MultBytes = mlen
	g.stats.TotalBytes = offBytes + dlen + mlen
	for u := uint32(0); u < g.numNodes; u++ {
		h, err := parseNode(g.section(u), g.numNodes)
		if err != nil {
			return nil, fmt.Errorf("node %d: %w", u, err)
		}
		ids, err := decodeAll(&h, g.numNodes)
		if err != nil {
			return nil, fmt.Errorf("node %d: %w", u, err)
		}
		g.stats.UniqueEdges += uint64(h.deg)
		g.stats.HeaderBytes += uint64(len(g.section(u))) - uint64(len(h.payload)) - skipTableLen(h)
		g.stats.SkipBytes += skipTableLen(h)
		g.stats.PayloadBytes += uint64(len(h.payload))
		wd, err := g.WeightedDegree(u)
		if err != nil {
			return nil, fmt.Errorf("node %d: %w", u, err)
		}
		g.stats.EdgeInstances += wd
		_ = ids
	}
	g.stats.NaiveBytes = g.stats.UniqueEdges*4 + offBytes
	return g, nil
}

// skipTableLen returns the byte length of the skip table inside a section.
func skipTableLen(h nodeHeader) uint64 {
	if len(h.skips) == 0 {
		return 0
	}
	// Recompute: each entry is uvarint(absID delta) + uvarint(offset delta).
	var total uint64
	var prevAbs, prevOff uint32
	for _, s := range h.skips {
		var tmp [binary.MaxVarintLen64]byte
		total += uint64(binary.PutUvarint(tmp[:], uint64(s.absID-prevAbs)))
		total += uint64(binary.PutUvarint(tmp[:], uint64(s.offset-prevOff)))
		prevAbs, prevOff = s.absID, s.offset
	}
	return total
}
