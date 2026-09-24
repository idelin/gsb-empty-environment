// Package secded implements an extended Hamming SECDED code
// (Single Error Correction, Double Error Detection) from scratch,
// together with an interleaved block format for burst-error and
// erasure handling, and a self-describing container format.
//
// Bit-position mapping (1-indexed positions inside one codeword):
//
//	positions 1..N-1 : classic Hamming(2^M - 1) code.
//	                   positions 1,2,4,...,2^(M-1) are Hamming parity bits;
//	                   all other positions carry data bits in increasing order.
//	position  N      : overall (extended) parity bit, N = 2^M.
//
// Codeword length N = 2^M, data bits K = 2^M - M - 1, minimum distance 4.
// In a uint64 codeword, bit (p-1) holds the value of position p.
//
// Decoding uses syndrome s (XOR of the position numbers of all set bits in
// the Hamming part) and overall parity p:
//
//	s==0, p==0 : no (detectable) error
//	s!=0, p==1 : single error at position s          -> corrected
//	s==0, p==1 : single error at the overall parity bit (position N)
//	s!=0, p==0 : double error detected               -> NOT correctable
//
// Capability limits (documented, not hidden):
//   - 3 bit errors may alias to a single-error syndrome and be MISCORRECTED.
//     A corrected block is therefore "most likely", never "certainly" right.
//   - 4 bit errors may produce syndrome 0 / even parity and be UNDETECTABLE.
//   - Erasures (known positions): at most 3 per codeword are recoverable,
//     and 2*t+e <= 3 must hold for t unknown errors plus e erasures.
package secded

import "errors"

// Status is the outcome of decoding one codeword.
type Status int

const (
	// StatusClean: syndrome and overall parity are both zero.
	StatusClean Status = iota
	// StatusCorrected: a single data/parity bit was flipped back.
	// NOTE: this is a maximum-likelihood correction, not a proof;
	// 3 or more errors can alias to it (see package doc).
	StatusCorrected
	// StatusParityOnly: the error was confined to the overall parity
	// bit (position N); data bits needed no change.
	StatusParityOnly
	// StatusDouble: a double (even-weight) error was detected and is
	// NOT correctable. The decoder never guesses in this case.
	StatusDouble
)

func (s Status) String() string {
	switch s {
	case StatusClean:
		return "clean"
	case StatusCorrected:
		return "corrected-single"
	case StatusParityOnly:
		return "parity-bit-only"
	case StatusDouble:
		return "double-error-uncorrectable"
	}
	return "unknown"
}

// Code describes one extended Hamming SECDED code family member.
type Code struct {
	M int // number of Hamming parity bits
	N int // codeword length in bits, N = 1<<M
	K int // data bits per codeword, K = N - M - 1

	dataPos []int // dataPos[i] = 1-indexed codeword position of data bit i
}

// New builds the code with m Hamming parity bits (2 <= m <= 6, so a
// codeword always fits in a uint64).
func New(m int) (*Code, error) {
	if m < 2 || m > 6 {
		return nil, errors.New("secded: m must be in [2,6]")
	}
	n := 1 << uint(m)
	c := &Code{M: m, N: n, K: n - m - 1}
	for p := 1; p <= n-1; p++ {
		if p&(p-1) != 0 { // not a power of two -> data position
			c.dataPos = append(c.dataPos, p)
		}
	}
	return c, nil
}

// DataPositions returns a copy of the 1-indexed data-bit position map:
// result[i] is the codeword position carrying data bit i.
func (c *Code) DataPositions() []int { return append([]int(nil), c.dataPos...) }

// Encode packs the low K bits of data into a codeword.
func (c *Code) Encode(data uint64) uint64 {
	var cw uint64
	for i := 0; i < c.K; i++ {
		if data>>uint(i)&1 == 1 {
			cw |= 1 << uint(c.dataPos[i]-1)
		}
	}
	// Hamming parity bits: parity bit at position 2^j covers all
	// positions (including itself) whose position number has bit j set.
	for j := 0; j < c.M; j++ {
		pj := 1 << uint(j)
		var par uint64
		for p := 1; p <= c.N-1; p++ {
			if p&pj != 0 {
				par ^= (cw >> uint(p-1)) & 1
			}
		}
		if par == 1 {
			cw |= 1 << uint(pj-1)
		}
	}
	// Overall parity bit at position N makes the whole codeword even.
	var par uint64
	for p := 1; p <= c.N-1; p++ {
		par ^= (cw >> uint(p-1)) & 1
	}
	if par == 1 {
		cw |= 1 << uint(c.N-1)
	}
	return cw
}

// Syndrome returns the Hamming syndrome s (the XOR of the 1-indexed
// positions of all set bits in positions 1..N-1) and the overall
// parity p over all N positions.
func (c *Code) Syndrome(cw uint64) (s, p int) {
	for pos := 1; pos <= c.N-1; pos++ {
		if cw>>uint(pos-1)&1 == 1 {
			s ^= pos
			p ^= 1
		}
	}
	if cw>>uint(c.N-1)&1 == 1 {
		p ^= 1
	}
	return s, p
}

// Decode examines a (possibly corrupted) codeword.
//
// It returns the extracted data bits, the status, and the 1-indexed
// position that was corrected (-1 if none). For StatusDouble the
// returned data is zero and MUST NOT be used: the decoder never
// presents a guess as if it were repaired data.
func (c *Code) Decode(cw uint64) (data uint64, st Status, correctedPos int) {
	s, p := c.Syndrome(cw)
	switch {
	case s == 0 && p == 0:
		return c.extract(cw), StatusClean, -1
	case s != 0 && p == 1:
		cw ^= 1 << uint(s-1)
		return c.extract(cw), StatusCorrected, s
	case s == 0 && p == 1:
		// Error confined to the overall parity bit; data untouched.
		return c.extract(cw), StatusParityOnly, c.N
	default: // s != 0 && p == 0
		return 0, StatusDouble, -1
	}
}

// extract reads the K data bits out of a codeword.
func (c *Code) extract(cw uint64) uint64 {
	var data uint64
	for i := 0; i < c.K; i++ {
		if cw>>uint(c.dataPos[i]-1)&1 == 1 {
			data |= 1 << uint(i)
		}
	}
	return data
}

// Erasure decoding -------------------------------------------------------

var (
	// ErrTooManyErasures: more than d_min-1 = 3 erasures in one codeword
	// are beyond the code's guaranteed capability and are refused.
	ErrTooManyErasures = errors.New("secded: more than 3 erasures in one codeword")
	// ErrErasureAmbiguous: no unique minimum-error assignment exists
	// (extra unknown errors on top of the erasures, or an ambiguous
	// pattern). The decoder refuses to guess.
	ErrErasureAmbiguous = errors.New("secded: erasure pattern not uniquely decodable")
)

// DecodeWithErasures decodes a codeword where the bits at the given
// 1-indexed positions are unknown (erased, e.g. marked by a transport
// layer). The values of those bits in cw are ignored.
//
// At most 3 erasures are accepted. All 2^e assignments are tried and
// the assignment needing the fewest additional corrections wins; it
// must be unique, otherwise ErrErasureAmbiguous is returned. This
// brute-force search is exact for d_min = 4: it succeeds precisely
// when 2*t + e <= 3 (t unknown errors, e erasures).
func (c *Code) DecodeWithErasures(cw uint64, erased []int) (data uint64, st Status, err error) {
	if len(erased) > 3 {
		return 0, 0, ErrTooManyErasures
	}
	seen := make(map[int]bool, len(erased))
	mask := ^uint64(0)
	for _, p := range erased {
		if p < 1 || p > c.N || seen[p] {
			return 0, 0, errors.New("secded: invalid erasure position")
		}
		seen[p] = true
		mask &^= 1 << uint(p-1)
	}
	base := cw & mask

	bestCorr, bestCount := -1, 0
	var bestData uint64
	var bestStatus Status
	for a := 0; a < 1<<uint(len(erased)); a++ {
		w := base
		for i, p := range erased {
			if a>>uint(i)&1 == 1 {
				w |= 1 << uint(p-1)
			}
		}
		d, st, _ := c.Decode(w)
		var corr int
		switch st {
		case StatusClean:
			corr = 0
		case StatusCorrected, StatusParityOnly:
			corr = 1
		default: // StatusDouble: assignment inconsistent with <=1 error
			continue
		}
		if bestCorr == -1 || corr < bestCorr {
			bestCorr, bestCount, bestData, bestStatus = corr, 1, d, st
		} else if corr == bestCorr {
			bestCount++
		}
	}
	if bestCorr == -1 || bestCount > 1 {
		return 0, 0, ErrErasureAmbiguous
	}
	return bestData, bestStatus, nil
}
