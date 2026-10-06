package dictagg

// Bitmap is a null bitmap: a set bit means the row is NULL.
type Bitmap struct {
	bits []uint64
	n    int
}

// NewBitmap builds a bitmap of n bits. setBits lists positions whose bit is 1.
func NewBitmap(n int, setBits ...int) *Bitmap {
	b := &Bitmap{bits: make([]uint64, (n+63)/64), n: n}
	for _, i := range setBits {
		b.Set(i)
	}
	return b
}

// BitmapFromValid converts a validity slice (true = valid) into a null bitmap.
func BitmapFromValid(valid []bool) *Bitmap {
	b := &Bitmap{bits: make([]uint64, (len(valid)+63)/64), n: len(valid)}
	for i, v := range valid {
		if !v {
			b.Set(i)
		}
	}
	return b
}

func (b *Bitmap) Set(i int) {
	b.bits[i/64] |= 1 << uint(i%64)
}

// IsNull reports whether row i is NULL. A nil bitmap means "no nulls".
func (b *Bitmap) IsNull(i int) bool {
	if b == nil {
		return false
	}
	return b.bits[i/64]>>(uint(i%64))&1 == 1
}

// Len returns the number of rows covered by the bitmap.
func (b *Bitmap) Len() int {
	if b == nil {
		return 0
	}
	return b.n
}
