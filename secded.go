// Package secded implements an extended Hamming SECDED code
// (Single Error Correction, Double Error Detection) with erasure
// support, plus an interleaved on-disk block format for burst-error
// tolerance. Only the Go standard library is used.
//
// Bit-position mapping (documented contract, see README.md):
//
//	A codeword is stored in the low n bits of a uint64, n = k + r + 1.
//	Bit index 0            : overall even-parity bit covering bits 1..n-1
//	                         (total popcount of the codeword is even).
//	Bit index j (1..n-1)   : Hamming position j. Positions that are powers
//	                         of two (1,2,4,8,...) hold Hamming parity bits;
//	                         all other positions hold data bits, filled in
//	                         increasing order (first data bit -> lowest
//	                         non-power-of-two position).
//
// The Hamming syndrome of a received word is the XOR of the positions of
// all set bits in 1..n-1; the overall-parity check is the XOR of all bits
// including bit 0. Each codeword bit therefore owns a unique "column"
// (position value, parity contribution 1), which is what makes erasure
// solving by syndrome matching unambiguous within the declared capability.
//
// Declared capability (minimum Hamming distance 4):
//   - 0 errors            -> reported clean
//   - 1 bit error         -> corrected (data, Hamming parity, or the
//     overall parity bit itself)
//   - 2 bit errors        -> detected, never "corrected"
//   - up to 3 erasures (known positions), no unknown errors -> recovered
//   - 1 erasure + 1 unknown error                          -> recovered
//
// Beyond capability (documented, not hidden): 3 or more bit errors can
// alias onto a valid codeword and be silently mis-"corrected"; 4 errors
// can be completely undetectable; 2 erasures + 1 unknown error may be
// mis-decoded. A Clean/Corrected status is a syndrome-consistent outcome,
// never a proof that the original data was recovered.
package secded

import "fmt"

// MaxDataBits is the largest supported k; n = k+r+1 must fit in uint64.
const MaxDataBits = 57

// Params describes one extended Hamming SECDED code.
type Params struct {
	DataBits   int // k: data bits per codeword
	ParityBits int // r: Hamming parity bits
	TotalBits  int // n = k + r + 1, including overall parity bit 0
}

// NewParams derives the smallest extended Hamming code holding k data bits.
func NewParams(k int) (Params, error) {
	if k < 1 || k > MaxDataBits {
		return Params{}, fmt.Errorf("secded: data bits %d out of range [1,%d]", k, MaxDataBits)
	}
	r := 0
	for (1 << r) < k+r+1 {
		r++
	}
	return Params{DataBits: k, ParityBits: r, TotalBits: k + r + 1}, nil
}

// dataPositions returns the codeword bit indices holding data, in order.
func (p Params) dataPositions() []int {
	pos := make([]int, 0, p.DataBits)
	for j := 1; j < p.TotalBits && len(pos) < p.DataBits; j++ {
		if j&(j-1) != 0 { // not a power of two
			pos = append(pos, j)
		}
	}
	return pos
}

// DataPositions exposes the documented bit-position mapping: DataPositions()[i]
// is the codeword bit index carrying data bit i.
func (p Params) DataPositions() []int { return p.dataPositions() }

// Encode packs the low k bits of data into a SECDED codeword.
func (p Params) Encode(data uint64) uint64 {
	var cw uint64
	for i, pos := range p.dataPositions() {
		if (data>>uint(i))&1 == 1 {
			cw |= 1 << uint(pos)
		}
	}
	// Hamming parity bits at positions 1,2,4,... (even parity).
	for j := 0; j < p.ParityBits; j++ {
		ppos := 1 << uint(j)
		par := 0
		for pos := 1; pos < p.TotalBits; pos++ {
			if pos&ppos != 0 {
				par ^= int((cw >> uint(pos)) & 1)
			}
		}
		if par == 1 {
			cw |= 1 << uint(ppos)
		}
	}
	// Overall parity bit 0: make total popcount even.
	if popcount(cw)&1 == 1 {
		cw |= 1
	}
	return cw
}

// ExtractData recovers the k data bits from a codeword (no validation).
func (p Params) ExtractData(cw uint64) uint64 {
	var data uint64
	for i, pos := range p.dataPositions() {
		if (cw>>uint(pos))&1 == 1 {
			data |= 1 << uint(i)
		}
	}
	return data
}

// Syndrome returns the Hamming syndrome (XOR of set-bit positions 1..n-1)
// and the overall-parity check (XOR of all n bits). Both zero means the
// word is a valid codeword.
func (p Params) Syndrome(cw uint64) (syn uint64, overall int) {
	par := 0
	for pos := 1; pos < p.TotalBits; pos++ {
		if (cw>>uint(pos))&1 == 1 {
			syn ^= uint64(pos)
			par ^= 1
		}
	}
	return syn, par ^ int(cw&1)
}

// Status is the syndrome-consistent outcome of decoding one codeword.
type Status int

const (
	StatusClean            Status = iota // no error indicated
	StatusCorrectedSingle                // one bit flipped (data or parity bit)
	StatusRecoveredErasure               // erasure pattern (optionally +1 error) resolved
	StatusDoubleError                    // two errors indicated; NOT corrected
	StatusUncorrectable                  // beyond capability or ambiguous
)

func (s Status) String() string {
	switch s {
	case StatusClean:
		return "clean"
	case StatusCorrectedSingle:
		return "corrected-single"
	case StatusRecoveredErasure:
		return "recovered-erasure"
	case StatusDoubleError:
		return "double-error-detected"
	case StatusUncorrectable:
		return "uncorrectable"
	}
	return "unknown"
}

// Decode decodes a codeword with no erasure information.
//
//	syn==0, par==0 -> clean
//	par==1         -> single error at bit syn (syn==0 is the overall
//	                  parity bit itself); flip and report corrected
//	syn!=0, par==0 -> double error; detected, returned untouched
func (p Params) Decode(cw uint64) (data uint64, st Status, corrected uint64) {
	syn, par := p.Syndrome(cw)
	switch {
	case syn == 0 && par == 0:
		return p.ExtractData(cw), StatusClean, cw
	case par == 1:
		fixed := cw ^ (1 << uint(syn))
		return p.ExtractData(fixed), StatusCorrectedSingle, fixed
	default:
		return p.ExtractData(cw), StatusDoubleError, cw
	}
}

// maxErasureSolve bounds the subset search; the *guaranteed* erasure
// capability is 3 (see package doc). Larger sets are attempted but may
// report StatusUncorrectable due to ambiguity.
const maxErasureSolve = 8

// DecodeWithErasures decodes a codeword where erased[pos] marks bits whose
// received value is unknown (e.g. flagged by the transport). Resolution:
// erase to zero, then find flip sets among the erased positions whose
// columns reproduce the residual syndrome. A solution using only erasure
// flips is preferred (unique when <=3 erasures and no unknown error);
// otherwise a unique solution with exactly one additional non-erased bit
// flip is accepted (covers 1 erasure + 1 error). Zero or multiple
// candidates yield StatusUncorrectable.
func (p Params) DecodeWithErasures(cw uint64, erased []bool) (data uint64, st Status, corrected uint64) {
	var positions []int
	masked := cw
	for pos := 0; pos < p.TotalBits && pos < len(erased); pos++ {
		if erased[pos] {
			positions = append(positions, pos)
			masked &^= 1 << uint(pos)
		}
	}
	if len(positions) == 0 {
		return p.Decode(cw)
	}
	if len(positions) > maxErasureSolve {
		return p.ExtractData(cw), StatusUncorrectable, cw
	}
	inErasure := make([]bool, p.TotalBits)
	for _, pos := range positions {
		inErasure[pos] = true
	}
	s0, p0 := p.Syndrome(masked)
	m := len(positions)
	var erasureOnly uint64
	erasureOnlyFound := false
	var withError uint64
	withErrorCount := 0
	for sub := 0; sub < (1 << uint(m)); sub++ {
		syn, par := s0, p0
		var flip uint64
		for i := 0; i < m; i++ {
			if sub&(1<<uint(i)) != 0 {
				syn ^= uint64(positions[i]) // position 0 contributes 0
				par ^= 1
				flip |= 1 << uint(positions[i])
			}
		}
		switch {
		case syn == 0 && par == 0:
			// Explained purely by erasure flips. Unique for <=3
			// erasures (a second one would differ by a nonzero
			// codeword supported on <=3 positions; min weight is 4).
			erasureOnly = masked ^ flip
			erasureOnlyFound = true
		case par == 1:
			bit := int(syn) // syn==0 addresses the overall parity bit 0
			if bit < p.TotalBits && !inErasure[bit] {
				cand := masked ^ flip ^ (1 << uint(bit))
				if withErrorCount == 0 || withError != cand {
					withError = cand
					withErrorCount++
				}
			}
		}
	}
	if erasureOnlyFound {
		return p.ExtractData(erasureOnly), StatusRecoveredErasure, erasureOnly
	}
	if withErrorCount == 1 {
		return p.ExtractData(withError), StatusRecoveredErasure, withError
	}
	return p.ExtractData(cw), StatusUncorrectable, cw
}

func popcount(v uint64) int {
	c := 0
	for v != 0 {
		v &= v - 1
		c++
	}
	return c
}
