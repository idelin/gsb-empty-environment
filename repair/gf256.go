package repair

// gfExp holds powers of the generator 2 in GF(2^8) with modulus 0x11d.
var gfExp [512]byte

// gfLog holds discrete logarithms base 2; gfLog[0] is left as zero.
var gfLog [256]byte

func init() {
	value := 1
	for exponent := 0; exponent < 255; exponent++ {
		gfExp[exponent] = byte(value)
		gfLog[value] = byte(exponent)
		value <<= 1
		if value&0x100 != 0 {
			value ^= 0x11d
		}
	}
	for exponent := 255; exponent < 512; exponent++ {
		gfExp[exponent] = gfExp[exponent-255]
	}
}

// gfMul returns a*b in GF(2^8) modulo x^8+x^4+x^3+x^2+1 (0x11d).
func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// gfMulTable[a][b] precomputes a*b so the search hot path avoids logarithms.
var gfMulTable [256][256]byte

func init() {
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			gfMulTable[a][b] = gfMul(byte(a), byte(b))
		}
	}
}

func mul(a, b byte) byte {
	return gfMulTable[a][b]
}

// gfInv returns the multiplicative inverse of a; it panics for a == 0.
func gfInv(a byte) byte {
	if a == 0 {
		panic("repair: inverse of zero")
	}
	return gfExp[255-int(gfLog[a])]
}

// gfDot returns the inner product of two coefficient rows.
func gfDot(row, vector []byte) byte {
	var acc byte
	for i, coefficient := range row {
		acc ^= mul(coefficient, vector[i])
	}
	return acc
}

// echelon is a row-echelon basis maintained over GF(256). Row slot i, when
// occupied, holds a normalized basis row whose leading (leftmost non-zero)
// entry is at column i. A nil entry means no basis row currently owns that
// pivot column.
type echelon struct {
	rows [][]byte
}

func newEchelon(dim int) *echelon {
	return &echelon{rows: make([][]byte, dim)}
}

func (e *echelon) rank() int {
	rank := 0
	for _, row := range e.rows {
		if row != nil {
			rank++
		}
	}
	return rank
}

// reduce eliminates the existing basis from row. It returns the residual row
// and the column of its leading entry, or -1 when the residual is all zero.
func (e *echelon) reduce(row []byte) ([]byte, int) {
	work := make([]byte, len(row))
	copy(work, row)
	pivot := e.reduceInto(row, work)
	return work, pivot
}

// reduceInto writes the residual of row after elimination into the caller-owned
// buffer work (which must have the same length as row) and returns its leading
// column, or -1 when the residual is zero. It allocates nothing.
func (e *echelon) reduceInto(row, work []byte) int {
	copy(work, row)
	for column, basisRow := range e.rows {
		if basisRow == nil || work[column] == 0 {
			continue
		}
		// Stored basis rows are normalized so their pivot entry is one.
		factor := work[column]
		for j := column; j < len(work); j++ {
			work[j] ^= mul(factor, basisRow[j])
		}
	}
	for column, value := range work {
		if value != 0 {
			return column
		}
	}
	return -1
}

// addRow inserts row into the basis. It reports whether the row increased the
// rank, i.e. whether it was linearly independent of the existing basis.
func (e *echelon) addRow(row []byte) bool {
	work, pivot := e.reduce(row)
	if pivot < 0 {
		return false
	}
	inverse := gfInv(work[pivot])
	for j := pivot; j < len(work); j++ {
		work[j] = mul(work[j], inverse)
	}
	e.rows[pivot] = work
	return true
}

// contains reports whether row belongs to the span of the basis.
func (e *echelon) contains(row []byte) bool {
	_, pivot := e.reduce(row)
	return pivot < 0
}
