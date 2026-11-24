package nvmerecov

import (
	"encoding/binary"
	"math"
)

type zseg struct {
	start  uint64
	end    uint64
	blocks Blocks
}

type zstate struct {
	wp   uint64
	segs []zseg
}

func (z zstate) clone() zstate {
	out := zstate{wp: z.wp}
	if len(z.segs) != 0 {
		out.segs = make([]zseg, len(z.segs))
		for i, seg := range z.segs {
			out.segs[i] = zseg{
				start:  seg.start,
				end:    seg.end,
				blocks: append(Blocks(nil), seg.blocks...),
			}
		}
	}
	return out
}

func initialState(m *nmodel, cons *constraints) []zstate {
	out := make([]zstate, len(m.zones))
	progress := make([]int, len(m.queues))
	for zi, zone := range m.zones {
		wp, _ := zone.initial.Length()
		state := zstate{wp: wp}
		for _, iv := range activeRelevant(m, cons, progress, zi) {
			lo := iv.start
			hi := iv.end
			if hi > wp {
				hi = wp
			}
			if lo >= hi {
				continue
			}
			part, ok := zone.initial.Slice(lo, hi-lo)
			if !ok {
				panic("nvmerecov: internal initial slice failure")
			}
			state.addSeg(lo, part)
		}
		out[zi] = state
	}
	return out
}

func activeRelevant(m *nmodel, cons *constraints, progress []int, zi int) []interval {
	for _, ri := range m.resetsByZone[zi] {
		ref := m.reqs[ri]
		if cons.mustExecute[ri] && !executed(progress, ref) {
			return m.zones[zi].readRelevant
		}
	}
	return m.zones[zi].relevant
}

// addSeg appends a projected segment. Because all writes happen at the current
// write pointer, projected intervals encountered here always lie at or after
// the end of existing retained content.
func (z *zstate) addSeg(start uint64, part Blocks) {
	if len(part) == 0 {
		return
	}
	length := part.MustLength()
	if len(z.segs) != 0 {
		last := &z.segs[len(z.segs)-1]
		if last.end == start {
			last.blocks = concatBlocks(last.blocks, part)
			last.end += length
			return
		}
	}
	z.segs = append(z.segs, zseg{
		start:  start,
		end:    start + length,
		blocks: append(Blocks(nil), part...),
	})
}

func (z *zstate) append(zone nzone, data Blocks, relevant []interval) (uint64, bool) {
	length, ok := data.Length()
	if !ok {
		return 0, false
	}
	if z.wp > zone.capacity || length > zone.capacity-z.wp {
		return 0, false
	}
	start := z.wp
	end := start + length
	for _, iv := range relevant {
		lo := iv.start
		if lo < start {
			lo = start
		}
		hi := iv.end
		if hi > end {
			hi = end
		}
		if lo >= hi {
			continue
		}
		part, sliceOK := data.Slice(lo-start, hi-lo)
		if !sliceOK {
			return 0, false
		}
		z.addSeg(lo, part)
	}
	z.wp = end
	return start, true
}

func (z *zstate) reset() {
	z.wp = 0
	z.segs = nil
}

func blocksEqual(a, b Blocks) bool {
	return a.Equal(b)
}

// rangeAt returns the exact blocks in [start,end), with offsets at or beyond
// the current write pointer represented by the empty marker.
func (z zstate) rangeAt(capacity, start, length uint64) (Blocks, bool) {
	if start > math.MaxUint64-length || start+length > capacity {
		return nil, false
	}
	if length == 0 {
		return nil, true
	}
	end := start + length
	if start >= z.wp {
		return EmptyBlocks(length), true
	}
	writtenEnd := end
	if writtenEnd > z.wp {
		writtenEnd = z.wp
	}

	out := make(Blocks, 0, 2)
	pos := start
	for pos < writtenEnd {
		var seg *zseg
		for i := range z.segs {
			if z.segs[i].start <= pos && pos < z.segs[i].end {
				seg = &z.segs[i]
				break
			}
		}
		if seg == nil {
			// This means the address was not retained as observation-relevant,
			// so exact content cannot be recovered.
			return nil, false
		}
		takeEnd := seg.end
		if takeEnd > writtenEnd {
			takeEnd = writtenEnd
		}
		part, ok := seg.blocks.Slice(pos-seg.start, takeEnd-pos)
		if !ok {
			return nil, false
		}
		out = append(out, part...)
		pos = takeEnd
	}
	if writtenEnd < end {
		out = append(out, EmptyBlocks(end-writtenEnd)...)
	}
	return Normalize(out), true
}

func (z zstate) blockAt(capacity, address uint64) (Block, bool) {
	if address >= capacity {
		return Block{}, false
	}
	if address >= z.wp {
		return EmptyBlock(), true
	}
	for i := range z.segs {
		seg := &z.segs[i]
		if seg.start <= address && address < seg.end {
			return seg.blocks.At(address - seg.start)
		}
	}
	return Block{}, false
}

func stateKey(progress []int, zones []zstate) string {
	size := 8 + len(progress)*8 + len(zones)*16
	for _, z := range zones {
		for _, seg := range z.segs {
			size += 16 + 8
			for _, run := range seg.blocks {
				size += 1 + 8 + 8 + len(run.Value)
			}
		}
	}
	buf := make([]byte, 0, size)
	buf = append(buf, uint64Bytes(uint64(len(progress)))...)
	for _, p := range progress {
		buf = append(buf, uint64Bytes(uint64(p))...)
	}
	buf = append(buf, uint64Bytes(uint64(len(zones)))...)
	for _, z := range zones {
		buf = append(buf, uint64Bytes(z.wp)...)
		buf = append(buf, uint64Bytes(uint64(len(z.segs)))...)
		for _, seg := range z.segs {
			buf = append(buf, uint64Bytes(seg.start)...)
			buf = append(buf, uint64Bytes(uint64(len(seg.blocks)))...)
			for _, run := range seg.blocks {
				if run.Empty {
					buf = append(buf, 1)
				} else {
					buf = append(buf, 0)
				}
				buf = append(buf, uint64Bytes(run.Count)...)
				buf = append(buf, uint64Bytes(uint64(len(run.Value)))...)
				buf = append(buf, run.Value...)
			}
		}
	}
	return string(buf)
}

func uint64Bytes(value uint64) []byte {
	var out [8]byte
	binary.LittleEndian.PutUint64(out[:], value)
	return out[:]
}
