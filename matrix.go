package secded

// This file builds the parity-check matrix H independently of the
// encoder/decoder logic in secded.go. Tests use it to cross-verify
// syndromes and codeword validity through a separate code path, so a
// bug in Syndrome() cannot silently agree with itself.
//
// H has M+1 rows and N columns (column index = position-1):
//
//	row 0      : all ones (overall parity check)
//	rows 1..M  : column of position p (1..N-1) is the M-bit binary
//	             representation of p, bit j in row j+1;
//	             column of position N is all-zero in these rows.

// ParityCheckMatrix returns H as M+1 bitmasks; bit (p-1) of row r is
// H[r][p-1].
func (c *Code) ParityCheckMatrix() []uint64 {
	h := make([]uint64, c.M+1)
	for p := 1; p <= c.N; p++ {
		h[0] |= 1 << uint(p-1) // overall parity row covers everything
		if p == c.N {
			continue // extended bit: zero column in the Hamming rows
		}
		for j := 0; j < c.M; j++ {
			if p>>uint(j)&1 == 1 {
				h[j+1] |= 1 << uint(p-1)
			}
		}
	}
	return h
}

// HSyndrome computes H * cw^T over GF(2) and returns the resulting
// M+1 syndrome bits (bit 0 = overall parity check). It deliberately
// shares no code with Syndrome().
func (c *Code) HSyndrome(cw uint64) []int {
	h := c.ParityCheckMatrix()
	out := make([]int, c.M+1)
	for r, row := range h {
		v := row & cw
		// parity of v
		v ^= v >> 32
		v ^= v >> 16
		v ^= v >> 8
		v ^= v >> 4
		v ^= v >> 2
		v ^= v >> 1
		out[r] = int(v & 1)
	}
	return out
}
