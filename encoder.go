package prefixcode

import "fmt"

// Encode encodes symbols with the given canonical codes, packing bits
// most-significant-bit first. The final byte is padded with zero bits.
func Encode(symbols []byte, codes []Code) ([]byte, error) {
	var out []byte
	var buf uint64
	nbits := 0
	for _, s := range symbols {
		c := codes[s]
		if c.Len == 0 {
			return nil, fmt.Errorf("%v: symbol %d", ErrNoCode, s)
		}
		buf = buf<<uint(c.Len) | uint64(c.Bits)
		nbits += int(c.Len)
		for nbits >= 8 {
			out = append(out, byte(buf>>uint(nbits-8)))
			nbits -= 8
			buf &= mask64(nbits)
		}
	}
	if nbits > 0 {
		out = append(out, byte(buf<<uint(8-nbits)))
	}
	return out, nil
}
