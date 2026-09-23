package bitplane

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

type parsedHeader struct {
	signed    bool
	width     int
	count     uint64
	blockSize int
	numBlocks int
	dataLen   uint64
}

// parseHeader validates the fixed header before anything is allocated:
// magic, version, width range, reserved byte, structural consistency of
// count/blockSize/numBlocks, exact total length, and the header CRC.
func parseHeader(data []byte) (*parsedHeader, error) {
	if len(data) < headerSize {
		return nil, fmt.Errorf("bitplane: buffer too short for header: %d bytes", len(data))
	}
	if string(data[0:4]) != string(magic[:]) {
		return nil, fmt.Errorf("bitplane: bad magic %q", data[0:4])
	}
	if v := data[4]; v != Version {
		return nil, fmt.Errorf("bitplane: unsupported version %d (want %d)", v, Version)
	}
	flags := data[5]
	if flags & ^byte(flagSigned) != 0 {
		return nil, fmt.Errorf("bitplane: unknown flags 0x%02x", flags)
	}
	w := int(data[6])
	if w < 1 || w > maxWidth {
		return nil, fmt.Errorf("bitplane: width %d out of range [1,64]", w)
	}
	if data[7] != 0 {
		return nil, fmt.Errorf("bitplane: reserved header byte must be 0, got %d", data[7])
	}
	count := binary.LittleEndian.Uint64(data[8:])
	blockSize := binary.LittleEndian.Uint32(data[16:])
	numBlocks := binary.LittleEndian.Uint32(data[20:])
	indexLen := binary.LittleEndian.Uint64(data[24:])
	dataLen := binary.LittleEndian.Uint64(data[32:])
	crc := binary.LittleEndian.Uint32(data[40:])
	if crc32.ChecksumIEEE(data[0:40]) != crc {
		return nil, fmt.Errorf("bitplane: header CRC mismatch")
	}
	if blockSize < 1 {
		return nil, fmt.Errorf("bitplane: block size must be >= 1")
	}
	wantBlocks := (count + uint64(blockSize) - 1) / uint64(blockSize)
	if uint64(numBlocks) != wantBlocks {
		return nil, fmt.Errorf("bitplane: numBlocks %d inconsistent with count %d and block size %d", numBlocks, count, blockSize)
	}
	if indexLen != uint64(numBlocks)*indexEntrySize {
		return nil, fmt.Errorf("bitplane: index length %d inconsistent with %d blocks", indexLen, numBlocks)
	}
	total := uint64(headerSize) + indexLen + dataLen
	if total != uint64(len(data)) {
		return nil, fmt.Errorf("bitplane: buffer length %d does not match header total %d", len(data), total)
	}
	return &parsedHeader{
		signed:    flags&flagSigned != 0,
		width:     w,
		count:     count,
		blockSize: int(blockSize),
		numBlocks: int(numBlocks),
		dataLen:   dataLen,
	}, nil
}

type indexEntry struct {
	off uint64
	ln  uint32
	crc uint32
}

func readIndex(data []byte, numBlocks int) []indexEntry {
	entries := make([]indexEntry, numBlocks)
	for i := range entries {
		o := headerSize + i*indexEntrySize
		entries[i] = indexEntry{
			off: binary.LittleEndian.Uint64(data[o:]),
			ln:  binary.LittleEndian.Uint32(data[o+8:]),
			crc: binary.LittleEndian.Uint32(data[o+12:]),
		}
	}
	return entries
}

// decodeBlock decodes one block payload into w planes of bl bits each.
// The per-block CRC is verified first, so a corrupted block fails here
// without affecting any other block.
func decodeBlock(payload []byte, e indexEntry, w, bl int) ([][]uint64, error) {
	if uint64(len(payload)) < e.off+uint64(e.ln) {
		return nil, fmt.Errorf("bitplane: block data truncated")
	}
	raw := payload[e.off : e.off+uint64(e.ln)]
	if crc32.ChecksumIEEE(raw) != e.crc {
		return nil, fmt.Errorf("bitplane: block CRC mismatch")
	}
	planes := make([][]uint64, w)
	off := 0
	for p := 0; p < w; p++ {
		if off >= len(raw) {
			return nil, fmt.Errorf("bitplane: plane %d missing from block", p)
		}
		pl := make([]uint64, (bl+63)/64)
		var n int
		var err error
		switch mode := raw[off]; mode {
		case modeLiteral:
			n, err = decodeLiteral(raw[off+1:], pl, bl)
		case modeRLE:
			n, err = decodeRLE(raw[off+1:], pl, bl)
		case modeDelta:
			n, err = decodeDelta(raw[off+1:], pl, bl)
		default:
			return nil, fmt.Errorf("bitplane: plane %d has unknown mode %d", p, mode)
		}
		if err != nil {
			return nil, fmt.Errorf("bitplane: plane %d: %w", p, err)
		}
		off += 1 + n
		planes[p] = pl
	}
	if off != len(raw) {
		return nil, fmt.Errorf("bitplane: %d trailing bytes in block", len(raw)-off)
	}
	return planes, nil
}

// DecodeRange decodes values in [start, end) without touching blocks that
// do not overlap the interval. At most maxValues values may be produced;
// the quota is enforced before any output is allocated.
func DecodeRange(data []byte, start, end, maxValues int) ([]int64, error) {
	h, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	if start < 0 || end < start || uint64(end) > h.count {
		return nil, fmt.Errorf("bitplane: invalid range [%d, %d) for count %d", start, end, h.count)
	}
	if maxValues < 0 || end-start > maxValues {
		return nil, fmt.Errorf("%w: range needs %d values, limit is %d", ErrQuotaExceeded, end-start, maxValues)
	}
	out := make([]int64, 0, end-start)
	if start == end {
		return out, nil
	}
	index := readIndex(data, h.numBlocks)
	payload := data[headerSize+h.numBlocks*indexEntrySize:]
	firstBlock := start / h.blockSize
	lastBlock := (end - 1) / h.blockSize
	m := mask(h.width)
	for b := firstBlock; b <= lastBlock; b++ {
		blockStart := b * h.blockSize
		bl := h.blockSize
		if rem := int(h.count) - blockStart; rem < bl {
			bl = rem
		}
		planes, err := decodeBlock(payload, index[b], h.width, bl)
		if err != nil {
			return nil, fmt.Errorf("bitplane: block %d: %w", b, err)
		}
		lo := start - blockStart
		if lo < 0 {
			lo = 0
		}
		hi := end - blockStart
		if hi > bl {
			hi = bl
		}
		for i := lo; i < hi; i++ {
			var x uint64
			for p := 0; p < h.width; p++ {
				x |= bitAt(planes[p], i) << uint(p)
			}
			x &= m
			if h.signed {
				x = zigzagDecode(x, h.width)
				out = append(out, signExtend(x, h.width))
			} else {
				out = append(out, int64(x))
			}
		}
	}
	return out, nil
}

// Decode decodes the whole buffer. maxValues bounds the output size and is
// checked against the header count before anything is allocated.
func Decode(data []byte, maxValues int) ([]int64, error) {
	h, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	if maxValues < 0 || h.count > uint64(maxValues) {
		return nil, fmt.Errorf("%w: stream holds %d values, limit is %d", ErrQuotaExceeded, h.count, maxValues)
	}
	return DecodeRange(data, 0, int(h.count), maxValues)
}
