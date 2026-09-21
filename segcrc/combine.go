package segcrc

import "errors"

// gf2Dim is the dimension of the CRC state space: 32 bits.
const gf2Dim = 32

// gf2MatrixTimes evaluates the GF(2) linear operator mat at vector vec:
// for every set bit k of vec, XOR row mat[k] into the result.
// (Port of zlib's gf2_matrix_times.)
func gf2MatrixTimes(mat *[gf2Dim]uint32, vec uint32) uint32 {
	var sum uint32
	for n := 0; n < gf2Dim; n++ {
		if vec&1 != 0 {
			sum ^= mat[n]
		}
		vec >>= 1
	}
	return sum
}

// gf2MatrixSquare sets square = mat composed with mat (over GF(2)).
// (Port of zlib's gf2_matrix_square.)
func gf2MatrixSquare(square, mat *[gf2Dim]uint32) {
	for n := 0; n < gf2Dim; n++ {
		square[n] = gf2MatrixTimes(mat, mat[n])
	}
}

// shiftByZeros advances a finalized CRC-32 digest through len2 zero *bytes*.
// It is a faithful port of zlib's crc32_combine_ operator loop: the first
// two squares build the 2-bit and 4-bit operators and the do/while doubles
// the span on each matrix squaring.
func shiftByZeros(crc uint32, len2 int64) uint32 {
	// Degenerate case; callers already exclude negatives.
	if len2 <= 0 {
		return crc
	}

	var even, odd [gf2Dim]uint32 // even- / odd-power-of-two zeros operators

	// Operator for one zero bit goes into odd.
	odd[0] = Poly // reflected CRC-32 polynomial
	row := uint32(1)
	for n := 1; n < gf2Dim; n++ {
		odd[n] = row
		row <<= 1
	}

	gf2MatrixSquare(&even, &odd) // operator for two zero bits
	gf2MatrixSquare(&odd, &even) // operator for four zero bits

	// Apply len2 zero bits to crc. Faithful do/while port of zlib: the first
	// square turns the one-bit operator into the one-byte (8-bit) operator.
	for {
		gf2MatrixSquare(&even, &odd)
		if len2&1 != 0 {
			crc = gf2MatrixTimes(&even, crc)
		}
		len2 >>= 1
		if len2 == 0 {
			break
		}

		gf2MatrixSquare(&odd, &even)
		if len2&1 != 0 {
			crc = gf2MatrixTimes(&odd, crc)
		}
		len2 >>= 1
		if len2 == 0 {
			break
		}
	}
	return crc
}

// Combine returns the Summary of A||B from the summaries of A and B alone:
// no bytes of either segment are read.
//
// It is the port of zlib's crc32_combine. The empty summary is the identity:
// Combine(Empty, b) == b and Combine(a, Empty) == a.
func Combine(a, b Summary) (Summary, error) {
	if a.Length < 0 || b.Length < 0 {
		return Summary{}, ErrNegativeLen
	}
	total, ok := addLen(a.Length, b.Length)
	if !ok {
		return Summary{}, ErrLenOverflow
	}
	switch {
	case a.IsEmpty():
		return b, nil
	case b.IsEmpty():
		return a, nil
	}
	return Summary{CRC: shiftByZeros(a.CRC, b.Length) ^ b.CRC, Length: total}, nil
}

// Compose combines an ordered list of segment summaries; it is the fold of
// Combine. Arbitrary chunking yields the same result as one checksum over the
// whole input.
func Compose(parts ...Summary) (Summary, error) {
	acc := Empty
	for _, p := range parts {
		next, err := Combine(acc, p)
		if err != nil {
			return Summary{}, err
		}
		acc = next
	}
	return acc, nil
}

func addLen(a, b int64) (int64, bool) {
	if a > MaxLen-b {
		return 0, false
	}
	return a + b, true
}

// Segment is one named, independently stored piece of a logical byte stream.
type Segment struct {
	Name    string
	Summary Summary
}

// Joiner assembles named segments in a fixed order. A segment can be replaced
// (e.g. after its bytes changed) and the overall summary recomputed without
// touching the bytes of any unchanged segment.
type Joiner struct {
	order []string
	segs  map[string]Summary
}

// NewJoiner creates a Joiner over names in the given concatenation order.
// Duplicate names are rejected.
func NewJoiner(names ...string) (*Joiner, error) {
	j := &Joiner{order: append([]string(nil), names...), segs: map[string]Summary{}}
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			return nil, errors.New("segcrc: duplicate segment name: " + n)
		}
		seen[n] = true
	}
	return j, nil
}

// Set stores (or replaces) one segment's summary. A name not in the order is
// rejected.
func (j *Joiner) Set(name string, s Summary) error {
	if _, ok := j.indexOf(name); !ok {
		return errors.New("segcrc: unknown segment name: " + name)
	}
	j.segs[name] = s
	return nil
}

// Replace is Set; use it at call sites that semantically replace a subsegment.
func (j *Joiner) Replace(name string, s Summary) error { return j.Set(name, s) }

func (j *Joiner) indexOf(name string) (int, bool) {
	for i, n := range j.order {
		if n == name {
			return i, true
		}
	}
	return 0, false
}

// Missing reports ordered names whose summary has not been supplied yet.
func (j *Joiner) Missing() []string {
	var m []string
	for _, n := range j.order {
		if _, ok := j.segs[n]; !ok {
			m = append(m, n)
		}
	}
	return m
}

// Build returns the summary of the full concatenated stream, or
// ErrMissingData naming the first absent segment.
func (j *Joiner) Build() (Summary, error) {
	acc := Empty
	for _, n := range j.order {
		s, ok := j.segs[n]
		if !ok {
			return Summary{}, missingError(n)
		}
		next, err := Combine(acc, s)
		if err != nil {
			return Summary{}, err
		}
		acc = next
	}
	return acc, nil
}

// Verify builds the overall summary and checks it against want. Same digest at
// a different total length fails with ErrLenMismatch.
func (j *Joiner) Verify(want Summary) error {
	got, err := j.Build()
	if err != nil {
		return err
	}
	if got.CRC == want.CRC && got.Length != want.Length {
		return ErrLenMismatch
	}
	if got != want {
		return ErrCRCMismatch
	}
	return nil
}
