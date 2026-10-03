// Package tans implements a tabled Asymmetric Numeral Systems (tANS)
// finite-state entropy coder.
//
// tANS combines the compression ratio of arithmetic coding with the
// throughput of Huffman coding: the entire compression state is folded into
// a single integer, and every push (encode) / pop (decode) step is a single
// lookup into a precomputed state-transition table plus a few bit
// operations, giving near-constant-time streaming coding.
//
// The construction of the transition tables follows the FSE scheme
// (Yann Collet): symbol counts are normalized to a power-of-two table size
// M = 1<<tableLog, symbols are spread across the M states with an odd-step
// cyclic walk, and the encode/decode transitions are derived from the
// resulting dyadic tiling of the state interval [M, 2M).
package tans

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
)

const (
	// MinTableLog and MaxTableLog bound the state table size (1<<tableLog
	// states). Larger tables approach the entropy bound more closely at the
	// cost of table-build time and cache footprint.
	MinTableLog = 5
	MaxTableLog = 15
	// DefaultTableLog (4096 states) is a good speed/ratio trade-off.
	DefaultTableLog = 12
)

var magic = []byte{'t', 'A', 'N', 'S', 1}

// ErrCorrupt is returned by Decompress when the input is not a valid stream.
var ErrCorrupt = errors.New("tans: corrupt or invalid stream")

// maxOrigLen bounds the declared uncompressed size, as a sanity check
// against corrupt headers.
const maxOrigLen = 1 << 31

// Table holds the precomputed tANS state-transition tables for one
// normalized symbol distribution. States are the integers [0, Size) on the
// decoder side and [Size, 2*Size) on the encoder side.
type Table struct {
	// TableLog is log2 of the number of states.
	TableLog int
	// Size is the number of states, 1<<TableLog.
	Size int
	// Counts is the normalized frequency of each symbol; it sums to Size.
	Counts [256]int

	symbols   []byte   // decode: symbol emitted by each state
	nbBits    []uint8  // decode: bits to read per state
	newState  []uint16 // decode: base of the next state
	encTable  []uint16 // encode: next state per (symbol, sub-state)
	deltaNb   [256]int // encode: bit-count selector per symbol
	deltaFind [256]int // encode: sub-state index offset per symbol
}

// BuildTable normalizes freq to a power-of-two table and precomputes the
// encode/decode state transitions. tableLog is clamped to [MinTableLog,
// MaxTableLog] and raised if the alphabet does not fit.
func BuildTable(freq []uint64, tableLog int) *Table {
	if tableLog < MinTableLog {
		tableLog = MinTableLog
	}
	if tableLog > MaxTableLog {
		tableLog = MaxTableLog
	}
	distinct := 0
	for _, f := range freq {
		if f > 0 {
			distinct++
		}
	}
	for distinct > 1<<uint(tableLog) {
		tableLog++
	}
	return buildTable(normalizeCounts(freq, tableLog), tableLog)
}

// normalizeCounts scales the frequencies so they sum to exactly 1<<tableLog,
// keeping every present symbol at count >= 1.
func normalizeCounts(freq []uint64, tableLog int) [256]int {
	var counts [256]int
	size := 1 << uint(tableLog)
	var total uint64
	for _, f := range freq {
		total += f
	}
	if total == 0 {
		return counts
	}
	sum := 0
	for s, f := range freq {
		if f == 0 {
			continue
		}
		c := int(f * uint64(size) / total)
		if c == 0 {
			c = 1
		}
		counts[s] = c
		sum += c
	}
	for sum > size {
		// Trim the largest count > 1.
		best, bestCount := -1, 1
		for s, c := range counts {
			if c > bestCount {
				best, bestCount = s, c
			}
		}
		if best < 0 {
			break
		}
		counts[best]--
		sum--
	}
	for sum < size {
		// Grow the most frequent symbol.
		best, bestFreq := -1, uint64(0)
		for s, f := range freq {
			if f > bestFreq {
				best, bestFreq = s, f
			}
		}
		if best < 0 {
			break
		}
		counts[best]++
		sum++
	}
	return counts
}

// buildTable derives the state transitions from normalized counts.
func buildTable(counts [256]int, tableLog int) *Table {
	size := 1 << uint(tableLog)
	t := &Table{TableLog: tableLog, Size: size, Counts: counts}
	t.symbols = make([]byte, size)
	t.nbBits = make([]uint8, size)
	t.newState = make([]uint16, size)
	t.encTable = make([]uint16, size)

	total := 0
	for _, c := range counts {
		total += c
	}
	if total != size {
		// Degenerate (empty) table; never usable for coding.
		return t
	}

	// Spread symbols across the states. The step is odd, hence coprime with
	// the power-of-two table size, so the walk visits every state once.
	step := (size >> 1) + (size >> 3) + 3
	pos := 0
	for s := 0; s < 256; s++ {
		for i := 0; i < counts[s]; i++ {
			t.symbols[pos] = byte(s)
			pos = (pos + step) & (size - 1)
		}
	}

	// Decoding transitions. The j-th occurrence of symbol s (in spread
	// order) is assigned v = count[s]+j; its nb = tableLog - highbit(v)
	// bits cover the previous-state interval [(v<<nb)-size, +1<<nb), and
	// these intervals tile [0, size) exactly.
	var symbolNext [256]int
	copy(symbolNext[:], counts[:])
	for u := 0; u < size; u++ {
		s := t.symbols[u]
		v := symbolNext[s]
		symbolNext[s]++
		nb := uint(tableLog) - (uint(bits.Len(uint(v))) - 1)
		t.nbBits[u] = uint8(nb)
		t.newState[u] = uint16((v << nb) - size)
	}

	// Encoding transitions, the exact inverse of the decoding table.
	var cumul [257]int
	for s := 0; s < 256; s++ {
		cumul[s+1] = cumul[s] + counts[s]
	}
	var next [256]int
	copy(next[:], cumul[:256])
	for u := 0; u < size; u++ {
		s := t.symbols[u]
		t.encTable[next[s]] = uint16(size + u)
		next[s]++
	}
	for s := 0; s < 256; s++ {
		n := counts[s]
		if n == 0 {
			continue
		}
		maxBitsOut := tableLog
		if n > 1 {
			maxBitsOut = tableLog - (bits.Len(uint(n-1)) - 1)
		}
		minStatePlus := n << uint(maxBitsOut)
		t.deltaNb[s] = (maxBitsOut << 16) - minStatePlus
		t.deltaFind[s] = cumul[s] - n
	}
	return t
}

// Encoder pushes symbols onto a single integer ANS state. Symbols must be
// pushed in the reverse of the desired decoding order (the state is a
// stack). It is not safe for concurrent use.
type Encoder struct {
	t     *Table
	state int
	bw    bitWriter
}

// NewEncoder returns an Encoder using t, with the state initialized to the
// canonical value Size.
func NewEncoder(t *Table) *Encoder {
	return &Encoder{t: t, state: t.Size}
}

// Encode pushes one symbol onto the state in near-constant time: one table
// lookup plus a variable-length bit emission.
func (e *Encoder) Encode(s byte) {
	nb := uint(e.state+e.t.deltaNb[s]) >> 16
	e.bw.write(uint64(e.state), nb)
	e.state = int(e.t.encTable[(e.state>>nb)+e.t.deltaFind[s]])
}

// Finish flushes the final state (TableLog bits) and the bit stream. The
// returned payload and padBits are everything a Decoder needs.
func (e *Encoder) Finish() (payload []byte, padBits uint) {
	e.bw.write(uint64(e.state-e.t.Size), uint(e.t.TableLog))
	padBits = e.bw.flush()
	return e.bw.buf, padBits
}

// Decoder pops symbols from the ANS state, in the opposite order in which
// they were pushed. It is not safe for concurrent use.
type Decoder struct {
	t     *Table
	state int
	br    *bitReader
}

// NewDecoder restores the initial state from the tail of payload.
func NewDecoder(t *Table, payload []byte, padBits uint) *Decoder {
	br := newBitReader(payload, padBits)
	return &Decoder{t: t, br: br, state: int(br.read(uint(t.TableLog)))}
}

// Decode pops the next symbol in near-constant time: one table lookup plus
// a variable-length bit read.
func (d *Decoder) Decode() byte {
	u := d.state
	s := d.t.symbols[u]
	d.state = int(d.t.newState[u]) + int(d.br.read(uint(d.t.nbBits[u])))
	return s
}

// Compress encodes src into a self-contained stream: a small header with
// the normalized distribution, followed by the tANS payload.
func Compress(src []byte, tableLog int) []byte {
	var freq [256]uint64
	for _, b := range src {
		freq[b]++
	}
	t := BuildTable(freq[:], tableLog)

	out := append([]byte{}, magic...)
	out = append(out, byte(t.TableLog))
	out = binary.AppendUvarint(out, uint64(len(src)))
	present := 0
	for _, c := range t.Counts {
		if c > 0 {
			present++
		}
	}
	out = binary.AppendUvarint(out, uint64(present))
	for s := 0; s < 256; s++ {
		if t.Counts[s] > 0 {
			out = append(out, byte(s))
			out = binary.AppendUvarint(out, uint64(t.Counts[s]))
		}
	}

	enc := NewEncoder(t)
	for i := len(src) - 1; i >= 0; i-- {
		enc.Encode(src[i])
	}
	payload, pad := enc.Finish()
	out = append(out, payload...)
	out = append(out, byte(pad))
	return out
}

// Decompress decodes a stream produced by Compress.
func Decompress(data []byte) ([]byte, error) {
	if len(data) < len(magic)+2 || !bytes.Equal(data[:len(magic)], magic) {
		return nil, ErrCorrupt
	}
	pos := len(magic)
	tableLog := int(data[pos])
	pos++
	if tableLog < MinTableLog || tableLog > MaxTableLog {
		return nil, ErrCorrupt
	}
	origLen, n := binary.Uvarint(data[pos:])
	if n <= 0 || origLen > maxOrigLen {
		return nil, ErrCorrupt
	}
	pos += n
	present, n := binary.Uvarint(data[pos:])
	if n <= 0 || present > 256 {
		return nil, ErrCorrupt
	}
	pos += n
	var counts [256]int
	sum := 0
	for i := 0; i < int(present); i++ {
		if pos >= len(data) {
			return nil, ErrCorrupt
		}
		s := data[pos]
		pos++
		c, n := binary.Uvarint(data[pos:])
		if n <= 0 || c == 0 || c > 1<<uint(tableLog) || counts[s] != 0 {
			return nil, ErrCorrupt
		}
		pos += n
		counts[s] = int(c)
		sum += int(c)
	}
	if origLen == 0 {
		return []byte{}, nil
	}
	if sum != 1<<uint(tableLog) {
		return nil, ErrCorrupt
	}
	if pos >= len(data) {
		return nil, ErrCorrupt
	}
	payload := data[pos : len(data)-1]
	pad := uint(data[len(data)-1])
	if pad > 7 || len(payload) == 0 {
		return nil, ErrCorrupt
	}
	t := buildTable(counts, tableLog)
	dec := NewDecoder(t, payload, pad)
	capHint := origLen
	if capHint > 1<<20 {
		capHint = 1 << 20
	}
	out := make([]byte, 0, capHint)
	for i := uint64(0); i < origLen; i++ {
		if dec.br.starved {
			return nil, ErrCorrupt
		}
		out = append(out, dec.Decode())
	}
	return out, nil
}

// EntropyBits returns the order-0 entropy, in bits, of a message with the
// given symbol frequencies: the theoretical compression bound.
func EntropyBits(freq []uint64) float64 {
	var total uint64
	for _, f := range freq {
		total += f
	}
	if total == 0 {
		return 0
	}
	h := 0.0
	for _, f := range freq {
		if f == 0 {
			continue
		}
		p := float64(f) / float64(total)
		h -= p * math.Log2(p)
	}
	return h * float64(total)
}
