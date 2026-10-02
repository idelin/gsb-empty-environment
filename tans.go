// Package tans implements a tANS (tabled Asymmetric Numeral Systems)
// finite-state entropy coder.
//
// tANS combines the compression ratio of arithmetic coding with the
// throughput of Huffman coding: the whole compression state is folded into
// a single integer, and encoding/decoding are driven by precomputed
// state-transition tables, giving near-constant-time Push/Pop per symbol.
//
// The package also contains a classic carry-less range coder (arith.go)
// used as a correctness cross-check and throughput baseline.
package tans

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

const (
	// DefaultTableLog is the default log2 of the state-table size.
	DefaultTableLog = 12
	// MaxTableLog bounds the state-table size (1 << MaxTableLog states).
	MaxTableLog = 16
	// MinTableLog is the smallest useful table size exponent.
	MinTableLog = 5
	// AlphabetSize is the number of possible symbol values (bytes).
	AlphabetSize = 256
)

// ErrInvalidStream is returned by Decode for truncated or malformed input.
var ErrInvalidStream = errors.New("tans: invalid or corrupt stream")

// Table holds the precomputed tANS state-transition tables shared by the
// encoder and the decoder. Build it with BuildTable.
type Table struct {
	tableLog uint
	size     int // 1 << tableLog

	// Decoding table, indexed by state x in [0, size):
	// emit sym[x], then x = baseline[x] + readBits(nbBits[x]).
	sym      []byte
	nbBits   []uint8
	baseline []uint16

	// Encoding table. For symbol s and current state x:
	//   k = maxBits[s]; if x < threshold[s] { k-- }
	//   writeBits(x, k)
	//   x = encStates[cumul[s] + int(x>>k) + base(s,k)]
	encStates []uint16
	cumul     [AlphabetSize + 1]int
	maxBits   [AlphabetSize]uint8
	threshold [AlphabetSize]uint32
	baseHi    [AlphabetSize]int
	baseLo    [AlphabetSize]int

	norm [AlphabetSize]uint16 // normalized frequencies summing to size
}

// CountSymbols returns a histogram of the byte values in data.
func CountSymbols(data []byte) []uint64 {
	var counts [AlphabetSize]uint64
	for _, b := range data {
		counts[b]++
	}
	return counts[:]
}

// BuildTable constructs the tANS state-transition tables from a symbol
// histogram. tableLog selects the table size (1<<tableLog states); pass 0
// for DefaultTableLog. Larger tables approach the entropy bound more
// closely at the cost of memory. Every symbol with a nonzero count is
// guaranteed at least one state, so any byte present in counts can be
// encoded.
func BuildTable(counts []uint64, tableLog uint) *Table {
	if tableLog == 0 {
		tableLog = DefaultTableLog
	}
	if tableLog < MinTableLog {
		tableLog = MinTableLog
	}
	if tableLog > MaxTableLog {
		tableLog = MaxTableLog
	}
	present := 0
	for _, c := range counts {
		if c > 0 {
			present++
		}
	}
	for tableLog < MaxTableLog && (1<<tableLog) < present {
		tableLog++
	}

	norm := normalizeCounts(counts, tableLog)

	t := &Table{tableLog: tableLog, size: 1 << tableLog}
	copy(t.norm[:], norm[:])
	t.build()
	return t
}

// TableLog returns the log2 of the number of states.
func (t *Table) TableLog() uint { return t.tableLog }

// NormalizedCounts returns the normalized symbol frequencies (sum == 1<<TableLog).
func (t *Table) NormalizedCounts() [AlphabetSize]uint16 { return t.norm }

// normalizeCounts scales the histogram so frequencies sum to 1<<tableLog,
// keeping every present symbol at frequency >= 1.
func normalizeCounts(counts []uint64, tableLog uint) [AlphabetSize]uint16 {
	var norm [AlphabetSize]uint16
	size := 1 << tableLog
	var total uint64
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return norm
	}
	sum := 0
	for s, c := range counts {
		if c == 0 {
			continue
		}
		n := int(float64(c) * float64(size) / float64(total))
		if n < 1 {
			n = 1
		}
		norm[s] = uint16(n)
		sum += n
	}
	for sum > size {
		// Remove from the most frequent symbol that can spare one.
		best := -1
		for s := range norm {
			if norm[s] > 1 && (best < 0 || norm[s] > norm[best]) {
				best = s
			}
		}
		if best < 0 {
			break // unreachable: sum > size >= number of present symbols
		}
		norm[best]--
		sum--
	}
	for sum < size {
		// Give to the most frequent source symbol.
		best := -1
		for s, c := range counts {
			if c > 0 && (best < 0 || c > counts[best]) {
				best = s
			}
		}
		norm[best]++
		sum++
	}
	return norm
}

// build constructs the encode/decode transition tables from norm.
func (t *Table) build() {
	size := t.size
	t.sym = make([]byte, size)
	t.nbBits = make([]uint8, size)
	t.baseline = make([]uint16, size)
	t.encStates = make([]uint16, size)

	// Cumulative offsets of each symbol's slice of encStates.
	t.cumul[0] = 0
	for s := 0; s < AlphabetSize; s++ {
		t.cumul[s+1] = t.cumul[s] + int(t.norm[s])
	}

	// Spread symbols across the state range with an odd step (FSE-style),
	// which visits every position exactly once.
	spread := make([]byte, size)
	step := (size >> 1) + (size >> 3) + 3
	pos := 0
	for s := 0; s < AlphabetSize; s++ {
		for j := 0; j < int(t.norm[s]); j++ {
			spread[pos] = byte(s)
			pos = (pos + step) & (size - 1)
		}
	}

	// Decoding table + inverse encoding states. For the j-th occurrence of
	// symbol s (j in [0, n)), t = n+j is the "next state" in [n, 2n):
	//   k = tableLog - floor(log2(t)), baseline = (t<<k) - size.
	var symbolNext [AlphabetSize]int
	for s := 0; s < AlphabetSize; s++ {
		symbolNext[s] = int(t.norm[s])
	}
	for x := 0; x < size; x++ {
		s := spread[x]
		n := int(t.norm[s])
		sn := symbolNext[s]
		symbolNext[s]++
		k := uint(t.tableLog) - uint(bits.Len32(uint32(sn))-1)
		t.sym[x] = s
		t.nbBits[x] = uint8(k)
		t.baseline[x] = uint16((sn << k) - size)
		t.encStates[t.cumul[s]+sn-n] = uint16(x)
	}

	// Per-symbol encoder constants. With n = norm[s], hb = floor(log2(n)):
	//   maxBits   = tableLog - hb
	//   threshold = (n << maxBits) - size   (state < threshold => use maxBits-1)
	for s := 0; s < AlphabetSize; s++ {
		n := int(t.norm[s])
		if n == 0 {
			continue
		}
		hb := uint(bits.Len32(uint32(n)) - 1)
		maxBits := t.tableLog - hb
		t.maxBits[s] = uint8(maxBits)
		t.threshold[s] = uint32((n << maxBits) - size)
		t.baseHi[s] = (size >> maxBits) - n
		t.baseLo[s] = (size >> (maxBits - 1)) - n // only used when threshold > 0
	}
}

// headerSize is the byte length of the stream header written by Encode:
// 8 bytes of symbol count + 8 bytes of total bit count.
const headerSize = 16

// Encode compresses src and appends the result to dst, returning the
// extended slice. The output is self-delimiting: it stores the symbol
// count and bit length in a fixed 16-byte header. The Table itself must
// be transmitted separately (see MarshalBinary) and must assign a nonzero
// frequency to every byte value present in src.
func (t *Table) Encode(dst, src []byte) []byte {
	start := len(dst)
	// Reserve enough capacity for the worst case (tableLog bits per symbol)
	// so the bit writer never reallocates.
	need := headerSize + (len(src)*int(t.tableLog)+7)/8 + 8
	if cap(dst)-len(dst) < need {
		nd := make([]byte, len(dst), len(dst)+need)
		copy(nd, dst)
		dst = nd
	}
	dst = dst[:len(dst)+headerSize]

	// Encode in reverse so the decoder can read the stream backward and
	// emit symbols in forward order.
	maxBits := t.maxBits[:]
	threshold := t.threshold[:]
	baseHi := t.baseHi[:]
	baseLo := t.baseLo[:]
	cumul := t.cumul[:]
	encStates := t.encStates

	buf := dst
	var acc uint64
	var nbits, total uint
	state := uint32(0)
	for i := len(src) - 1; i >= 0; i-- {
		s := src[i]
		k := uint(maxBits[s])
		base := baseHi[s]
		if state < threshold[s] {
			k--
			base = baseLo[s]
		}
		acc |= uint64(state&((1<<k)-1)) << nbits
		nbits += k
		for nbits >= 8 {
			buf = append(buf, byte(acc))
			acc >>= 8
			nbits -= 8
		}
		total += k
		state = uint32(encStates[cumul[s]+int(state>>k)+base])
	}
	acc |= uint64(state) << nbits
	nbits += t.tableLog
	total += t.tableLog
	for nbits > 0 {
		buf = append(buf, byte(acc))
		acc >>= 8
		nbits = uint(max(0, int(nbits)-8))
	}

	binary.LittleEndian.PutUint64(buf[start:], uint64(len(src)))
	binary.LittleEndian.PutUint64(buf[start+8:], uint64(total))
	return buf
}

// Decode decompresses a stream produced by Encode, appending the restored
// symbols to dst and returning the extended slice.
func (t *Table) Decode(dst, src []byte) ([]byte, error) {
	if len(src) < headerSize {
		return nil, ErrInvalidStream
	}
	n := binary.LittleEndian.Uint64(src)
	totalBits := binary.LittleEndian.Uint64(src[8:])
	payload := src[headerSize:]
	if totalBits > uint64(len(payload))*8 || totalBits < uint64(t.tableLog) || n > 1<<40 {
		return nil, ErrInvalidStream
	}

	// Copy the payload into a buffer with slack so the reverse reader can
	// always load a 3-byte window.
	buf := make([]byte, len(payload)+4)
	copy(buf, payload)

	base := len(dst)
	if cap(dst)-len(dst) < int(n) {
		dst = append(dst, make([]byte, int(n))...)
	} else {
		dst = dst[:len(dst)+int(n)]
	}

	sym := t.sym
	nbBits := t.nbBits
	baseline := t.baseline
	pos := totalBits

	// Read the final encoder state (last tableLog bits written).
	pos -= uint64(t.tableLog)
	state := uint32(0)
	{
		bytePos := pos >> 3
		shift := pos & 7
		chunk := uint64(buf[bytePos]) | uint64(buf[bytePos+1])<<8 | uint64(buf[bytePos+2])<<16
		state = uint32(chunk>>shift) & ((1 << t.tableLog) - 1)
	}
	for i := 0; i < int(n); i++ {
		x := int(state)
		dst[base+i] = sym[x]
		k := uint(nbBits[x])
		if k == 0 {
			state = uint32(baseline[x])
			continue
		}
		pos -= uint64(k)
		bytePos := pos >> 3
		shift := pos & 7
		chunk := uint64(buf[bytePos]) | uint64(buf[bytePos+1])<<8 | uint64(buf[bytePos+2])<<16
		state = uint32(baseline[x]) + (uint32(chunk>>shift) & ((1 << k) - 1))
	}
	return dst, nil
}

// MarshalBinary serializes the table as 1 byte of tableLog followed by 256
// little-endian uint16 normalized frequencies (513 bytes).
func (t *Table) MarshalBinary() ([]byte, error) {
	out := make([]byte, 1+2*AlphabetSize)
	out[0] = byte(t.tableLog)
	for s := 0; s < AlphabetSize; s++ {
		binary.LittleEndian.PutUint16(out[1+2*s:], t.norm[s])
	}
	return out, nil
}

// UnmarshalTable rebuilds a Table from MarshalBinary output.
func UnmarshalTable(data []byte) (*Table, error) {
	if len(data) != 1+2*AlphabetSize {
		return nil, ErrInvalidStream
	}
	tableLog := uint(data[0])
	if tableLog < MinTableLog || tableLog > MaxTableLog {
		return nil, ErrInvalidStream
	}
	t := &Table{tableLog: tableLog, size: 1 << tableLog}
	sum := 0
	for s := 0; s < AlphabetSize; s++ {
		t.norm[s] = binary.LittleEndian.Uint16(data[1+2*s:])
		sum += int(t.norm[s])
	}
	if sum != t.size {
		return nil, ErrInvalidStream
	}
	t.build()
	return t, nil
}
