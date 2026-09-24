package bitplane

import (
	"encoding/binary"
	"hash/crc32"
	"math"
	"math/bits"
)

type blockInfo struct {
	off, length int
	crc         uint32
}

type parsed struct {
	Header
	blocks  []blockInfo
	trailer uint32
}

// parse validates the header and index and locates every block. It checks
// the format version and bit width before anything else, and never
// allocates proportionally to attacker-controlled counts without bounding
// them against the actual input length.
func parse(data []byte) (*parsed, error) {
	if len(data) < 8+4+trailerLen {
		return nil, ErrFormat
	}
	if string(data[:4]) != string(magic) {
		return nil, ErrFormat
	}
	if data[4] != Version {
		return nil, ErrFormat
	}
	flags := data[5]
	if flags&^flagSigned != 0 {
		return nil, ErrFormat
	}
	bitWidth := int(data[6])
	if bitWidth < 1 || bitWidth > MaxBits {
		return nil, ErrFormat
	}
	if data[7] != 0 {
		return nil, ErrFormat
	}
	i := 8
	readUvarint := func() (uint64, error) {
		if i >= len(data) {
			return 0, ErrFormat
		}
		v, m := binary.Uvarint(data[i:])
		if m <= 0 {
			return 0, ErrFormat
		}
		i += m
		return v, nil
	}
	count, err := readUvarint()
	if err != nil {
		return nil, err
	}
	blockSize, err := readUvarint()
	if err != nil {
		return nil, err
	}
	numBlocks, err := readUvarint()
	if err != nil {
		return nil, err
	}
	if blockSize < 1 || blockSize > math.MaxInt ||
		count > math.MaxInt || numBlocks > math.MaxInt {
		return nil, ErrFormat
	}
	if count == 0 {
		if numBlocks != 0 {
			return nil, ErrFormat
		}
	} else if numBlocks != (count-1)/blockSize+1 {
		return nil, ErrFormat
	}
	// Every index entry costs at least 1+4 bytes; reject impossible counts
	// before allocating.
	if numBlocks > uint64(len(data))/5 {
		return nil, ErrFormat
	}
	if i+4 > len(data) {
		return nil, ErrFormat
	}
	if crc32.ChecksumIEEE(data[:i]) != binary.LittleEndian.Uint32(data[i:]) {
		return nil, ErrChecksum
	}
	i += 4

	blocks := make([]blockInfo, 0, numBlocks)
	for k := uint64(0); k < numBlocks; k++ {
		ln, err := readUvarint()
		if err != nil {
			return nil, err
		}
		if ln > math.MaxInt || i+4 > len(data) {
			return nil, ErrFormat
		}
		crc := binary.LittleEndian.Uint32(data[i:])
		i += 4
		blocks = append(blocks, blockInfo{off: int(ln), crc: crc}) // off is length for now
	}
	limit := len(data) - trailerLen
	if limit < i {
		return nil, ErrFormat
	}
	pos := i
	for k := range blocks {
		ln := blocks[k].off
		if ln > limit-pos {
			return nil, ErrFormat
		}
		blocks[k] = blockInfo{off: pos, length: ln, crc: blocks[k].crc}
		pos += ln
	}
	if pos != limit {
		return nil, ErrFormat
	}
	return &parsed{
		Header: Header{
			Version:   Version,
			Bits:      bitWidth,
			Signed:    flags&flagSigned != 0,
			Count:     int(count),
			BlockSize: int(blockSize),
			NumBlocks: int(numBlocks),
		},
		blocks:  blocks,
		trailer: binary.LittleEndian.Uint32(data[limit:]),
	}, nil
}

// decodeBlock verifies and decodes a single block into mapped values.
func (p *parsed) decodeBlock(data []byte, bi int) ([]int64, error) {
	b := p.blocks[bi]
	payload := data[b.off : b.off+b.length]
	if crc32.ChecksumIEEE(payload) != b.crc {
		return nil, ErrChecksum
	}
	n := p.Count - bi*p.BlockSize
	if n > p.BlockSize {
		n = p.BlockSize
	}
	mapped := make([]uint64, n)
	q := payload
	for pl := 0; pl < p.Bits; pl++ {
		if len(q) < 2 {
			return nil, ErrFormat
		}
		mode := q[0]
		v, m := binary.Uvarint(q[1:])
		if m <= 0 {
			return nil, ErrFormat
		}
		rest := q[1+m:]
		if v > uint64(len(rest)) {
			return nil, ErrFormat
		}
		planePayload := rest[:v]
		q = rest[v:]
		words := make([]uint64, (n+63)/64)
		if err := decodePlane(planePayload, mode, n, words); err != nil {
			return nil, err
		}
		for wi, w := range words {
			for w != 0 {
				k := wi*64 + bits.TrailingZeros64(w)
				if k < n {
					mapped[k] |= 1 << uint(pl)
				}
				w &= w - 1
			}
		}
	}
	if len(q) != 0 {
		return nil, ErrFormat
	}
	mask := maskFor(p.Bits)
	out := make([]int64, n)
	for i, u := range mapped {
		u &= mask
		if p.Signed {
			v := int64(u>>1) ^ -int64(u&1)
			out[i] = signExtend(uint64(v)&mask, p.Bits)
		} else {
			out[i] = int64(u)
		}
	}
	return out, nil
}

func valuesCRC(values []int64) uint32 {
	h := crc32.NewIEEE()
	var vb [8]byte
	for _, v := range values {
		binary.LittleEndian.PutUint64(vb[:], uint64(v))
		h.Write(vb[:])
	}
	return h.Sum32()
}

// DecodeAll decodes the whole stream. The header (version, bit width) and
// the output quota are validated before any block is decoded; maxValues
// must be non-negative and at least the stored value count.
func DecodeAll(data []byte, maxValues int) ([]int64, Header, error) {
	p, err := parse(data)
	if err != nil {
		return nil, Header{}, err
	}
	if maxValues < 0 || p.Count > maxValues {
		return nil, p.Header, ErrQuota
	}
	out := make([]int64, 0, p.Count)
	for bi := range p.blocks {
		vals, err := p.decodeBlock(data, bi)
		if err != nil {
			return nil, p.Header, err
		}
		out = append(out, vals...)
	}
	if valuesCRC(out) != p.trailer {
		return nil, p.Header, ErrChecksum
	}
	return out, p.Header, nil
}

// DecodeRange decodes values [lo,hi) by reading only the blocks that
// overlap the range; unrelated blocks are never touched, so corruption
// elsewhere does not affect the result. The range length must not exceed
// the maxValues quota.
func DecodeRange(data []byte, lo, hi int, maxValues int) ([]int64, Header, error) {
	p, err := parse(data)
	if err != nil {
		return nil, Header{}, err
	}
	if lo < 0 || hi < lo || hi > p.Count {
		return nil, p.Header, ErrRange
	}
	if maxValues < 0 || hi-lo > maxValues {
		return nil, p.Header, ErrQuota
	}
	out := make([]int64, 0, hi-lo)
	if hi == lo {
		return out, p.Header, nil
	}
	b0 := lo / p.BlockSize
	b1 := (hi - 1) / p.BlockSize
	for bi := b0; bi <= b1; bi++ {
		vals, err := p.decodeBlock(data, bi)
		if err != nil {
			return nil, p.Header, err
		}
		s := bi * p.BlockSize
		a := lo - s
		if a < 0 {
			a = 0
		}
		b := hi - s
		if b > len(vals) {
			b = len(vals)
		}
		out = append(out, vals[a:b]...)
	}
	return out, p.Header, nil
}
