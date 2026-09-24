package dod

import (
	"encoding/binary"
	"math"
)

// segment is a run of delta-of-delta values sharing one bit width.
type segment struct {
	width uint
	count int
}

// streamDP incrementally computes the optimal segmentation of a zigzag
// delta-of-delta stream. Cost is exact: 8 bits width tag + varint count
// per segment header, plus width*count payload bits. Ties in bits are
// broken toward fewer segments so the nseg varint length is deterministic.
type streamDP struct {
	zz     []uint64
	bits   []int64 // optimal cost in bits for each prefix length
	nseg   []int
	choice []int
}

func newStreamDP() *streamDP {
	return &streamDP{bits: []int64{0}, nseg: []int{0}, choice: []int{-1}}
}

func (s *streamDP) push(z uint64) {
	s.zz = append(s.zz, z)
	m := len(s.zz)
	best := int64(math.MaxInt64)
	bestN, bestC := 0, m-1
	var runMax uint64
	for ip := m - 1; ip >= 0; ip-- {
		if s.zz[ip] > runMax {
			runMax = s.zz[ip]
		}
		cnt := int64(m - ip)
		b := s.bits[ip] + 8 + 8*int64(uvlen(uint64(cnt))) + int64(widthFor(runMax))*cnt
		ns := s.nseg[ip] + 1
		if b < best || (b == best && ns < bestN) {
			best, bestN, bestC = b, ns, ip
		}
	}
	s.bits = append(s.bits, best)
	s.nseg = append(s.nseg, bestN)
	s.choice = append(s.choice, bestC)
}

func (s *streamDP) segments() []segment {
	var rev []segment
	for m := len(s.zz); m > 0; {
		ip := s.choice[m]
		var mx uint64
		for _, z := range s.zz[ip:m] {
			if z > mx {
				mx = z
			}
		}
		rev = append(rev, segment{width: widthFor(mx), count: m - ip})
		m = ip
	}
	segs := make([]segment, len(rev))
	for i, g := range rev {
		segs[len(rev)-1-i] = g
	}
	return segs
}

// encodeStream encodes delta-of-delta values with optimal segment widths.
func encodeStream(dd []int64) []byte {
	if len(dd) == 0 {
		return binary.AppendUvarint(nil, 0)
	}
	s := newStreamDP()
	for _, x := range dd {
		s.push(zigzag(x))
	}
	segs := s.segments()
	out := binary.AppendUvarint(nil, uint64(len(segs)))
	for _, g := range segs {
		out = append(out, byte(g.width))
		out = binary.AppendUvarint(out, uint64(g.count))
	}
	var bw bitWriter
	k := 0
	for _, g := range segs {
		for c := 0; c < g.count; c++ {
			bw.write(zigzag(dd[k]), g.width)
			k++
		}
	}
	return append(out, bw.bytes()...)
}

// decodeStream reverses encodeStream. maxCount bounds the output length.
func decodeStream(data []byte, maxCount int) ([]int64, error) {
	nseg64, k := binary.Uvarint(data)
	if k <= 0 {
		return nil, ErrFormat
	}
	if nseg64 == 0 {
		return nil, nil
	}
	if nseg64 > uint64(maxCount) {
		return nil, ErrTooLarge
	}
	p := data[k:]
	type hdr struct {
		w uint
		c int
	}
	hdrs := make([]hdr, 0, nseg64)
	total := 0
	var totalBits int64
	for s := uint64(0); s < nseg64; s++ {
		if len(p) < 1 {
			return nil, ErrTruncated
		}
		w := uint(p[0])
		p = p[1:]
		if !validWidth(w) {
			return nil, ErrFormat
		}
		c, k2 := binary.Uvarint(p)
		if k2 <= 0 {
			return nil, ErrFormat
		}
		p = p[k2:]
		total += int(c)
		if total > maxCount {
			return nil, ErrTooLarge
		}
		totalBits += int64(w) * int64(c)
		hdrs = append(hdrs, hdr{w, int(c)})
	}
	need := int((totalBits + 7) / 8)
	if len(p) < need {
		return nil, ErrTruncated
	}
	br := bitReader{buf: p[:need]}
	out := make([]int64, 0, total)
	for _, h := range hdrs {
		for c := 0; c < h.c; c++ {
			out = append(out, unzigzag(br.read(h.w)))
		}
	}
	if br.err != nil {
		return nil, br.err
	}
	return out, nil
}
