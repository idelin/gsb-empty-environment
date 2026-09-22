package bitlayout

import "fmt"

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// checksumSum implements the "sum" checksum: the covered stream bits are
// grouped into bytes in stream order (the first bit is the most significant
// bit of the first byte, a final partial byte is zero-padded on the right)
// and the bytes are summed modulo 2^bits.
func checksumSum(r *bitReader, from, to uint64, bits int) uint64 {
	var sum uint64
	mod := mask64(bits)
	for pos := from; pos < to; {
		n := to - pos
		k := 8
		if n < 8 {
			k = int(n)
		}
		b, ok := r.get(pos, k)
		if !ok {
			return 0 // cannot happen: range was already encoded/decoded
		}
		if k < 8 {
			b <<= uint(8 - k)
		}
		sum = (sum + b) & mod
		pos += uint64(k)
	}
	return sum
}
