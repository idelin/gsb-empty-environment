package mph

import (
	"encoding/binary"
	"fmt"
)

// Encoded byte layout of an Index (all integers little-endian):
//
//	offset  size  field
//	0       4     magic "MPH1"
//	4       4     uint32 numKeys
//	8       4     uint32 numBuckets
//	12      4     uint32 attempts (seed tries consumed by the winning build)
//	16      8     uint64 seed (winning build seed)
//	24      4     uint32 keyPoolLen (total bytes of the key pool)
//	28      4     reserved, zero
//	32      2*numBuckets  displacements, uint16 per bucket
//	..      1*numKeys     fingerprints, uint8 per slot
//	..      4*numKeys     key offsets, uint32 per slot (offset into key pool)
//	..      keyPoolLen    key pool: original keys concatenated in slot order
//
// The key at slot i spans keyPool[offsets[i] : end], where end is
// offsets[i+1] or keyPoolLen for the last slot.
const (
	magic0    = 'M'
	magic1    = 'P'
	magic2    = 'H'
	magic3    = '1'
	headerLen = 32
)

// Footprint reports the exact encoded size of an Index, broken down by
// section. All values are bytes.
type Footprint struct {
	Header        int // fixed 32-byte header
	Displacements int // 2 bytes per bucket
	Fingerprints  int // 1 byte per key
	KeyOffsets    int // 4 bytes per key
	KeyPool       int // original key bytes, no per-key overhead
	Total         int
}

// BytesPerKey returns the amortized encoded size per indexed key.
func (f Footprint) BytesPerKey(n int) float64 {
	if n == 0 {
		return 0
	}
	return float64(f.Total) / float64(n)
}

// Footprint computes the encoded size breakdown of the index.
func (ix *Index) Footprint() Footprint {
	pool := 0
	for _, k := range ix.keys {
		pool += len(k)
	}
	f := Footprint{
		Header:        headerLen,
		Displacements: 2 * int(ix.numBuckets),
		Fingerprints:  int(ix.numKeys),
		KeyOffsets:    4 * int(ix.numKeys),
		KeyPool:       pool,
	}
	f.Total = f.Header + f.Displacements + f.Fingerprints + f.KeyOffsets + f.KeyPool
	return f
}

// MarshalBinary encodes the index into the layout documented above.
func (ix *Index) MarshalBinary() []byte {
	fp := ix.Footprint()
	buf := make([]byte, fp.Total)
	buf[0] = magic0
	buf[1] = magic1
	buf[2] = magic2
	buf[3] = magic3
	binary.LittleEndian.PutUint32(buf[4:8], ix.numKeys)
	binary.LittleEndian.PutUint32(buf[8:12], ix.numBuckets)
	binary.LittleEndian.PutUint32(buf[12:16], ix.attempts)
	binary.LittleEndian.PutUint64(buf[16:24], ix.seed)
	binary.LittleEndian.PutUint32(buf[24:28], uint32(fp.KeyPool))
	// buf[28:32] reserved, already zero.
	off := headerLen
	for _, d := range ix.displ {
		binary.LittleEndian.PutUint16(buf[off:off+2], d)
		off += 2
	}
	copy(buf[off:off+len(ix.fprints)], ix.fprints)
	off += len(ix.fprints)
	poolOff := off + 4*int(ix.numKeys)
	pos := 0
	for _, k := range ix.keys {
		binary.LittleEndian.PutUint32(buf[off:off+4], uint32(pos))
		off += 4
		copy(buf[poolOff+pos:], k)
		pos += len(k)
	}
	return buf
}

// Unmarshal decodes an index produced by MarshalBinary. It validates the
// magic, all section lengths, and the key offset table.
func Unmarshal(data []byte) (*Index, error) {
	if len(data) < headerLen {
		return nil, fmt.Errorf("mph: decode: too short for header (%d bytes)", len(data))
	}
	if data[0] != magic0 || data[1] != magic1 || data[2] != magic2 || data[3] != magic3 {
		return nil, fmt.Errorf("mph: decode: bad magic %q", string(data[0:4]))
	}
	numKeys := binary.LittleEndian.Uint32(data[4:8])
	numBuckets := binary.LittleEndian.Uint32(data[8:12])
	attempts := binary.LittleEndian.Uint32(data[12:16])
	seed := binary.LittleEndian.Uint64(data[16:24])
	poolLen := binary.LittleEndian.Uint32(data[24:28])

	want := headerLen + 2*int(numBuckets) + int(numKeys) + 4*int(numKeys) + int(poolLen)
	if len(data) != want {
		return nil, fmt.Errorf("mph: decode: length mismatch: header implies %d bytes, got %d", want, len(data))
	}
	ix := &Index{
		numKeys:    numKeys,
		numBuckets: numBuckets,
		seed:       seed,
		attempts:   attempts,
		displ:      make([]uint16, numBuckets),
		fprints:    make([]uint8, numKeys),
		keys:       make([]string, numKeys),
	}
	off := headerLen
	for i := range ix.displ {
		ix.displ[i] = binary.LittleEndian.Uint16(data[off : off+2])
		off += 2
	}
	copy(ix.fprints, data[off:off+int(numKeys)])
	off += int(numKeys)
	offsets := make([]uint32, numKeys)
	for i := range offsets {
		offsets[i] = binary.LittleEndian.Uint32(data[off : off+4])
		off += 4
		if offsets[i] > poolLen || (i > 0 && offsets[i] < offsets[i-1]) {
			return nil, fmt.Errorf("mph: decode: key offset table corrupt at slot %d", i)
		}
	}
	pool := data[off : off+int(poolLen)]
	for i := range ix.keys {
		end := poolLen
		if i+1 < len(offsets) {
			end = offsets[i+1]
		}
		ix.keys[i] = string(pool[offsets[i]:end])
	}
	return ix, nil
}
