// Package slz implements a sliding-window LZ77 compressor with
// minimum-bit (optimal) parsing, a bounded match index, overlapping
// back-copy, and a checkpointed self-describing stream format.
//
// Stream format (all integers big-endian unless noted):
//
//	header:  magic "SLZ" | version u8 | totalLen uvarint | crc32(payload) 4B
//	chunk*:  dictCRC 4B | tokenCount uvarint | token bits (byte aligned)
//
// dictCRC is crc32 of the decoder's current dictionary window (the last
// min(WindowSize, produced) output bytes) at the start of the chunk, so a
// chunk is bound to the exact dictionary state it was encoded against.
//
// Token bit encoding:
//
//	literal: 0 | byte(8)
//	match:   1 | gamma(len-MinMatch+1) | gamma(dist)
//
// Costs used by the optimal parser: literal = 9 bits,
// match(len,dist) = 1 + gammaBits(len-MinMatch+1) + gammaBits(dist).
package slz

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math/bits"
)

const (
	Version      = 1
	WindowSize   = 4096 // max match distance; older positions are evicted
	MinMatch     = 3
	MaxMatch     = 258
	DefaultChunk = 1 << 14
	maxChain     = 32 // bounded match index: chain walk limit
	hashBits     = 15
	literalCost  = 9 // 1 flag bit + 8 payload bits
)

var magic = []byte{'S', 'L', 'Z'}

func gammaBits(n int) int { // n >= 1
	k := bits.Len(uint(n)) - 1
	return 2*k + 1
}

func matchCost(l, d int) int {
	return 1 + gammaBits(l-MinMatch+1) + gammaBits(d)
}

type token struct {
	isMatch bool
	lit     byte
	length  int
	dist    int
}

func tokenCost(t token) int {
	if !t.isMatch {
		return literalCost
	}
	return matchCost(t.length, t.dist)
}

func parseCost(toks []token) int {
	c := 0
	for _, t := range toks {
		c += tokenCost(t)
	}
	return c
}

// matchIndex is a bounded hash-chain index over 3-byte prefixes.
type matchIndex struct {
	head [1 << hashBits]int
	prev []int
	data []byte
}

func newMatchIndex(data []byte) *matchIndex {
	m := &matchIndex{prev: make([]int, len(data)), data: data}
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.prev {
		m.prev[i] = -1
	}
	return m
}

func hash3(data []byte, i int) int {
	v := uint32(data[i]) | uint32(data[i+1])<<8 | uint32(data[i+2])<<16
	v *= 2654435761
	return int(v >> (32 - hashBits))
}

func (m *matchIndex) add(i int) {
	if i+MinMatch > len(m.data) {
		return
	}
	h := hash3(m.data, i)
	m.prev[i] = m.head[h]
	m.head[h] = i
}

type cand struct{ dist, maxLen int }

// candidates returns match candidates at position i, nearest first,
// bounded by maxChain steps and WindowSize. Overlapping matches
// (dist < length) are fully supported because the whole input is
// available to the encoder.
func (m *matchIndex) candidates(i int) []cand {
	data := m.data
	n := len(data)
	lim := n - i
	if lim > MaxMatch {
		lim = MaxMatch
	}
	var out []cand
	steps := 0
	for p := m.head[hash3(data, i)]; p >= 0 && steps < maxChain; p = m.prev[p] {
		steps++
		d := i - p
		if d > WindowSize {
			break // chain positions decrease, so distances only grow
		}
		l := 0
		for l < lim && data[p+l] == data[i+l] {
			l++
		}
		if l >= MinMatch {
			out = append(out, cand{dist: d, maxLen: l})
		}
	}
	return out
}

// optimalParse computes a minimum-bit parse of data via backward DP over
// the candidates offered by the bounded index. It is NOT longest-match
// greedy: every (length, distance) pair reachable at each position is
// costed. Fixed tie-break: on equal cost the option considered first
// wins — literal before match, then nearer distance, then shorter length.
func optimalParse(data []byte) []token {
	n := len(data)
	idx := newMatchIndex(data)
	cands := make([][]cand, n)
	for i := 0; i < n; i++ {
		if i+MinMatch <= n {
			cands[i] = idx.candidates(i)
			idx.add(i)
		}
	}
	dp := make([]int, n+1)
	choice := make([]token, n)
	for i := n - 1; i >= 0; i-- {
		best := literalCost + dp[i+1]
		tok := token{lit: data[i]}
		for _, c := range cands[i] {
			maxL := c.maxLen
			if maxL > n-i {
				maxL = n - i
			}
			for l := MinMatch; l <= maxL; l++ {
				if cost := matchCost(l, c.dist) + dp[i+l]; cost < best {
					best = cost
					tok = token{isMatch: true, length: l, dist: c.dist}
				}
			}
		}
		dp[i] = best
		choice[i] = tok
	}
	var toks []token
	for i := 0; i < n; {
		t := choice[i]
		toks = append(toks, t)
		if t.isMatch {
			i += t.length
		} else {
			i++
		}
	}
	return toks
}

// greedyParse is the classic longest-match heuristic (nearest distance on
// ties), kept for comparison against the optimal parse.
func greedyParse(data []byte) []token {
	n := len(data)
	idx := newMatchIndex(data)
	var toks []token
	addUpTo := 0
	for i := 0; i < n; {
		for ; addUpTo < i; addUpTo++ {
			idx.add(addUpTo)
		}
		var best cand
		if i+MinMatch <= n {
			for _, c := range idx.candidates(i) {
				if c.maxLen > best.maxLen {
					best = c
				}
			}
		}
		if best.maxLen >= MinMatch {
			toks = append(toks, token{isMatch: true, length: best.maxLen, dist: best.dist})
			i += best.maxLen
		} else {
			toks = append(toks, token{lit: data[i]})
			i++
		}
	}
	return toks
}

// Encode compresses data and writes the stream to w. Chunks are cut at
// token boundaries as soon as they reach chunkSize input bytes.
func Encode(w io.Writer, data []byte, chunkSize int) error {
	if chunkSize <= 0 {
		chunkSize = DefaultChunk
	}
	hdr := append([]byte{}, magic...)
	hdr = append(hdr, Version)
	hdr = binary.AppendUvarint(hdr, uint64(len(data)))
	var crcB [4]byte
	binary.BigEndian.PutUint32(crcB[:], crc32.ChecksumIEEE(data))
	hdr = append(hdr, crcB[:]...)
	if _, err := w.Write(hdr); err != nil {
		return err
	}

	toks := optimalParse(data)
	pos, ti := 0, 0
	for pos < len(data) {
		start := ti
		p := pos
		for p < pos+chunkSize && ti < len(toks) {
			t := toks[ti]
			ti++
			if t.isMatch {
				p += t.length
			} else {
				p++
			}
		}
		chunk := toks[start:ti]

		ws := pos - WindowSize
		if ws < 0 {
			ws = 0
		}
		var out []byte
		binary.BigEndian.PutUint32(crcB[:], crc32.ChecksumIEEE(data[ws:pos]))
		out = append(out, crcB[:]...)
		out = binary.AppendUvarint(out, uint64(len(chunk)))
		bw := &bitWriter{}
		for _, t := range chunk {
			if t.isMatch {
				bw.writeBit(1)
				bw.writeGamma(t.length - MinMatch + 1)
				bw.writeGamma(t.dist)
			} else {
				bw.writeBit(0)
				bw.writeBits(uint32(t.lit), 8)
			}
		}
		bw.flush()
		out = append(out, bw.buf...)
		if _, err := w.Write(out); err != nil {
			return err
		}
		pos = p
	}
	return nil
}

// Decode reads a stream from r and returns the decompressed payload.
// maxOut bounds the output: a stream declaring more than maxOut bytes is
// rejected up front, and input reads are capped proportionally.
func Decode(r io.Reader, maxOut int) ([]byte, error) {
	if maxOut < 0 {
		return nil, errors.New("slz: maxOut must be >= 0")
	}
	raw, err := io.ReadAll(io.LimitReader(r, int64(maxOut)*16+1<<20))
	if err != nil {
		return nil, err
	}
	if len(raw) < len(magic)+1 {
		return nil, errors.New("slz: stream too short")
	}
	for i, b := range magic {
		if raw[i] != b {
			return nil, errors.New("slz: bad magic")
		}
	}
	if raw[len(magic)] != Version {
		return nil, fmt.Errorf("slz: unsupported version %d", raw[len(magic)])
	}
	totalLen64, n := binary.Uvarint(raw[len(magic)+1:])
	if n <= 0 {
		return nil, errors.New("slz: bad length field")
	}
	off := len(magic) + 1 + n
	if len(raw) < off+4 {
		return nil, errUnexpectedEOF
	}
	crcWant := binary.BigEndian.Uint32(raw[off:])
	off += 4
	if totalLen64 > uint64(maxOut) {
		return nil, fmt.Errorf("slz: declared length %d exceeds output limit %d", totalLen64, maxOut)
	}
	totalLen := int(totalLen64)

	br := &bitReader{data: raw[off:]}
	out := make([]byte, 0, totalLen)
	for len(out) < totalLen {
		br.alignByte()
		var chk uint32
		for i := 0; i < 4; i++ {
			b, err := br.readByteAligned()
			if err != nil {
				return nil, err
			}
			chk = chk<<8 | uint32(b)
		}
		ws := len(out) - WindowSize
		if ws < 0 {
			ws = 0
		}
		if crc32.ChecksumIEEE(out[ws:]) != chk {
			return nil, fmt.Errorf("slz: dictionary checkpoint mismatch at output offset %d", len(out))
		}
		ntok, err := br.readUvarint()
		if err != nil {
			return nil, err
		}
		for k := uint64(0); k < ntok; k++ {
			flag, err := br.readBit()
			if err != nil {
				return nil, err
			}
			if flag == 0 {
				b, err := br.readBits(8)
				if err != nil {
					return nil, err
				}
				if len(out) >= totalLen {
					return nil, errors.New("slz: token stream overruns declared length")
				}
				out = append(out, byte(b))
				continue
			}
			gl, err := br.readGamma()
			if err != nil {
				return nil, err
			}
			l := gl - 1 + MinMatch
			if l > MaxMatch {
				return nil, fmt.Errorf("slz: match length %d exceeds MaxMatch", l)
			}
			d, err := br.readGamma()
			if err != nil {
				return nil, err
			}
			if d > WindowSize {
				return nil, fmt.Errorf("slz: distance %d refers beyond evicted window", d)
			}
			if d > len(out) {
				return nil, fmt.Errorf("slz: distance %d references not-yet-produced bytes (have %d)", d, len(out))
			}
			if len(out)+l > totalLen {
				return nil, errors.New("slz: token stream overruns declared length")
			}
			// Byte-by-byte copy so overlapping matches (dist < length,
			// e.g. distance 1 run extension) expand correctly.
			for j := 0; j < l; j++ {
				out = append(out, out[len(out)-d])
			}
		}
	}
	if crc32.ChecksumIEEE(out) != crcWant {
		return nil, errors.New("slz: payload checksum mismatch")
	}
	return out, nil
}
