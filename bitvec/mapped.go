package bitvec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// Mapped is a read-only view over a serialized bitmap. Queries read
// index entries and block payloads directly from the underlying bytes
// (mmap-style); nothing is eagerly decompressed. The caller must not
// mutate the mapped bytes.
type Mapped struct {
	buf        []byte
	bitLen     uint64
	totalOnes  uint64
	numBlocks  uint32
	numSupers  uint32
	blocksPer  uint32
	selRate    uint32
	numSamples uint32
	dirOff     uint64
	superOff   uint64
	selOff     uint64
	dataOff    uint64
	dataLen    uint64
}

// BitLen returns the number of valid bits. Tail padding in the final
// block is excluded.
func (m *Mapped) BitLen() uint64 { return m.bitLen }

// TotalOnes returns the number of one bits.
func (m *Mapped) TotalOnes() uint64 { return m.totalOnes }

// Layout reports the byte occupancy of each serialized section.
func (m *Mapped) Layout() Layout {
	return Layout{
		Header:      headerSize,
		Directory:   int(m.superOff - m.dirOff),
		SuperIndex:  int(m.selOff - m.superOff),
		SelectIndex: int(m.dataOff - m.selOff),
		Data:        int(m.dataLen),
		Total:       len(m.buf),
	}
}

// Map validates buf and returns a read-only mapped bitmap. Validation
// independently recomputes every section offset from the recorded
// counts, checks all CRC32s, and audits the directory, rank superblock
// index, and select samples for internal consistency; stored offsets
// are never trusted on their own.
func Map(buf []byte) (*Mapped, error) {
	if uint64(len(buf)) < headerSize {
		return nil, errors.New("bitvec: buffer smaller than header")
	}
	h := buf[:headerSize]
	if string(h[0:4]) != string(magic[:]) {
		return nil, errors.New("bitvec: bad magic")
	}
	if v := binary.LittleEndian.Uint32(h[4:8]); v != Version {
		return nil, fmt.Errorf("bitvec: unsupported version %d", v)
	}
	if crc32.ChecksumIEEE(h[:112]) != binary.LittleEndian.Uint32(h[112:116]) {
		return nil, errors.New("bitvec: header checksum mismatch")
	}
	m := &Mapped{
		buf:        buf,
		bitLen:     binary.LittleEndian.Uint64(h[8:16]),
		numBlocks:  binary.LittleEndian.Uint32(h[16:20]),
		numSupers:  binary.LittleEndian.Uint32(h[20:24]),
		blocksPer:  binary.LittleEndian.Uint32(h[24:28]),
		selRate:    binary.LittleEndian.Uint32(h[28:32]),
		totalOnes:  binary.LittleEndian.Uint64(h[32:40]),
		numSamples: binary.LittleEndian.Uint32(h[40:44]),
		dirOff:     binary.LittleEndian.Uint64(h[48:56]),
		superOff:   binary.LittleEndian.Uint64(h[56:64]),
		selOff:     binary.LittleEndian.Uint64(h[64:72]),
		dataOff:    binary.LittleEndian.Uint64(h[72:80]),
		dataLen:    binary.LittleEndian.Uint64(h[80:88]),
	}
	totalLen := binary.LittleEndian.Uint64(h[88:96])

	// Independently recompute the layout from the counts.
	if m.blocksPer == 0 || m.blocksPer > 1<<20 {
		return nil, fmt.Errorf("bitvec: invalid superblock rate %d", m.blocksPer)
	}
	if m.selRate == 0 || m.selRate > 1<<30 {
		return nil, fmt.Errorf("bitvec: invalid select rate %d", m.selRate)
	}
	if want := (m.bitLen + BlockBits - 1) / BlockBits; uint64(m.numBlocks) != want {
		return nil, fmt.Errorf("bitvec: block count %d, want %d", m.numBlocks, want)
	}
	if want := (uint64(m.numBlocks) + uint64(m.blocksPer) - 1) / uint64(m.blocksPer); uint64(m.numSupers) != want {
		return nil, fmt.Errorf("bitvec: superblock count %d, want %d", m.numSupers, want)
	}
	wantSamples := uint64(0)
	if m.totalOnes > 0 {
		wantSamples = (m.totalOnes + uint64(m.selRate) - 1) / uint64(m.selRate)
	}
	if uint64(m.numSamples) != wantSamples {
		return nil, fmt.Errorf("bitvec: sample count %d, want %d", m.numSamples, wantSamples)
	}
	if m.dirOff != headerSize ||
		m.superOff != m.dirOff+uint64(m.numBlocks)*dirEntrySize ||
		m.selOff != m.superOff+uint64(m.numSupers)*superEntrySize ||
		m.dataOff != m.selOff+uint64(m.numSamples)*sampleEntrySize {
		return nil, errors.New("bitvec: section offsets inconsistent with counts")
	}
	if totalLen != m.dataOff+m.dataLen || totalLen != uint64(len(buf)) {
		return nil, errors.New("bitvec: total length mismatch")
	}

	// Section checksums.
	if crc32.ChecksumIEEE(buf[m.dirOff:m.superOff]) != binary.LittleEndian.Uint32(h[96:100]) ||
		crc32.ChecksumIEEE(buf[m.superOff:m.selOff]) != binary.LittleEndian.Uint32(h[100:104]) ||
		crc32.ChecksumIEEE(buf[m.selOff:m.dataOff]) != binary.LittleEndian.Uint32(h[104:108]) ||
		crc32.ChecksumIEEE(buf[m.dataOff:]) != binary.LittleEndian.Uint32(h[108:112]) {
		return nil, errors.New("bitvec: section checksum mismatch")
	}

	if err := m.audit(); err != nil {
		return nil, err
	}
	return m, nil
}

// audit re-derives the rank superblock index and the select samples
// from the directory and compares them against the stored index, and
// bounds-checks every block payload against the data section.
func (m *Mapped) audit() error {
	var cum uint64
	nextTarget := uint64(0)
	si := uint32(0)
	for b := uint32(0); b < m.numBlocks; b++ {
		if b%m.blocksPer == 0 {
			got := binary.LittleEndian.Uint64(m.buf[m.superOff+uint64(b/m.blocksPer)*superEntrySize:])
			if got != cum {
				return fmt.Errorf("bitvec: superblock %d rank %d, recomputed %d", b/m.blocksPer, got, cum)
			}
		}
		e := m.buf[m.dirOff+uint64(b)*dirEntrySize:]
		typ := e[0]
		pop := uint64(binary.LittleEndian.Uint16(e[1:3]))
		off := uint64(binary.LittleEndian.Uint32(e[4:8]))
		if pop > BlockBits {
			return fmt.Errorf("bitvec: block %d popcount %d out of range", b, pop)
		}
		var size uint64
		switch typ {
		case blkZero, blkOne:
			size = 0
		case blkRaw:
			size = BlockBytes
		case blkSparse:
			if off >= m.dataLen {
				return fmt.Errorf("bitvec: block %d sparse payload out of bounds", b)
			}
			n := uint64(m.buf[m.dataOff+off])
			if n == 0 || n > sparseMax {
				return fmt.Errorf("bitvec: block %d sparse count %d out of range", b, n)
			}
			size = 1 + 2*n
		default:
			return fmt.Errorf("bitvec: block %d unknown type %d", b, typ)
		}
		if size > 0 && (off >= m.dataLen || off+size > m.dataLen) {
			return fmt.Errorf("bitvec: block %d payload [%d,%d) exceeds data section %d", b, off, off+size, m.dataLen)
		}
		for nextTarget < cum+pop && si < m.numSamples {
			se := m.buf[m.selOff+uint64(si)*sampleEntrySize:]
			blk := binary.LittleEndian.Uint32(se[0:4])
			base := binary.LittleEndian.Uint64(se[8:16])
			if blk != b || base != cum {
				return fmt.Errorf("bitvec: select sample %d = (block %d, rank %d), recomputed (%d, %d)",
					si, blk, base, b, cum)
			}
			si++
			nextTarget += uint64(m.selRate)
		}
		cum += pop
	}
	if cum != m.totalOnes {
		return fmt.Errorf("bitvec: directory popcounts sum to %d, header says %d", cum, m.totalOnes)
	}
	if si != m.numSamples {
		return fmt.Errorf("bitvec: consumed %d select samples, header says %d", si, m.numSamples)
	}
	return nil
}

// --- bounds-checked primitive readers; these are the only paths that
// --- touch the mapping, so they also drive the query-cost metrics.

func (m *Mapped) u8(off uint64, mx *Metrics) (uint8, bool) {
	if off+1 > uint64(len(m.buf)) {
		return 0, false
	}
	mx.addBytes(1)
	return m.buf[off], true
}

func (m *Mapped) u16(off uint64, mx *Metrics) (uint16, bool) {
	if off+2 > uint64(len(m.buf)) {
		return 0, false
	}
	mx.addBytes(2)
	return binary.LittleEndian.Uint16(m.buf[off:]), true
}

func (m *Mapped) u32(off uint64, mx *Metrics) (uint32, bool) {
	if off+4 > uint64(len(m.buf)) {
		return 0, false
	}
	mx.addBytes(4)
	return binary.LittleEndian.Uint32(m.buf[off:]), true
}

func (m *Mapped) u64(off uint64, mx *Metrics) (uint64, bool) {
	if off+8 > uint64(len(m.buf)) {
		return 0, false
	}
	mx.addBytes(8)
	return binary.LittleEndian.Uint64(m.buf[off:]), true
}

// dirEntry reads directory entry b: type, popcount, data offset.
func (m *Mapped) dirEntry(b uint32, mx *Metrics) (typ uint8, pop uint16, off uint32, ok bool) {
	if b >= m.numBlocks {
		return 0, 0, 0, false
	}
	base := m.dirOff + uint64(b)*dirEntrySize
	t, ok1 := m.u8(base, mx)
	p, ok2 := m.u16(base+1, mx)
	o, ok3 := m.u32(base+4, mx)
	// account for the reserved byte so metrics reflect the full 8 B entry
	mx.addBytes(1)
	return t, p, o, ok1 && ok2 && ok3
}

// cumBeforeBlock returns the number of one bits in blocks [0, b) using
// the two-level rank index: one superblock lookup plus at most
// blocksPer-1 directory entries.
func (m *Mapped) cumBeforeBlock(b uint32, mx *Metrics) (uint64, bool) {
	if b == 0 {
		return 0, true
	}
	if b > m.numBlocks {
		return 0, false
	}
	s := uint64((b - 1) / m.blocksPer)
	cum, ok := m.u64(m.superOff+s*superEntrySize, mx)
	if !ok {
		return 0, false
	}
	for i := uint32(s) * m.blocksPer; i < b; i++ {
		_, pop, _, ok := m.dirEntry(i, mx)
		if !ok {
			return 0, false
		}
		cum += uint64(pop)
	}
	return cum, true
}

// blockWords decodes exactly one compressed block into 8 words. Tail
// bits beyond bitLen are masked off so polluted padding can never leak
// into query results.
func (m *Mapped) blockWords(b uint32, mx *Metrics) ([BlockBits / 64]uint64, bool) {
	var w [BlockBits / 64]uint64
	typ, _, off, ok := m.dirEntry(b, mx)
	if !ok {
		return w, false
	}
	mx.addBlock()
	base := m.dataOff + uint64(off)
	switch typ {
	case blkZero:
	case blkOne:
		for i := range w {
			w[i] = ^uint64(0)
		}
	case blkRaw:
		for i := range w {
			v, ok := m.u64(base+uint64(i)*8, mx)
			if !ok {
				return w, false
			}
			w[i] = v
		}
	case blkSparse:
		n, ok := m.u8(base, mx)
		if !ok {
			return w, false
		}
		for j := uint8(0); j < n; j++ {
			p, ok := m.u16(base+1+uint64(j)*2, mx)
			if !ok {
				return w, false
			}
			if p < BlockBits { // corrupt-safe: ignore out-of-range positions
				w[p/64] |= 1 << (p % 64)
			}
		}
	default:
		return w, false
	}
	// Mask tail padding bits in the final block.
	if valid := m.bitLen - uint64(b)*BlockBits; valid < BlockBits {
		for i := range w {
			rem := int64(valid) - int64(i)*64
			switch {
			case rem <= 0:
				w[i] = 0
			case rem < 64:
				w[i] &= (uint64(1) << rem) - 1
			}
		}
	}
	return w, true
}
