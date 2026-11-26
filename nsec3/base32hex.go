package nsec3

// RFC 4648 section 7 "base32 extended hex alphabet". NSEC3 hashed owner
// names use this alphabet without "=" padding (RFC 5155 section 3.1).
const base32HexAlphabet = "0123456789abcdefghijklmnopqrstuv"

var base32HexDecode [256]byte

func init() {
	for i := range base32HexDecode {
		base32HexDecode[i] = 0xFF
	}
	for i, c := range []byte(base32HexAlphabet) {
		base32HexDecode[c] = byte(i)
		// Decoders must accept the upper-case form too (RFC 5155 owner
		// names are case-insensitive).
		if c >= 'a' {
			base32HexDecode[c-'a'+'A'] = byte(i)
		}
	}
}

// Base32HexEncode encodes data using the unpadded extended hex base32
// alphabet ("0".."9","a".."v").
func Base32HexEncode(data []byte) string {
	var buf [8]byte
	out := make([]byte, 0, (len(data)*8+4)/5)
	for len(data) > 0 {
		var quanta []byte
		if len(data) >= 5 {
			quanta = data[:5]
			data = data[5:]
		} else {
			quanta = data
			data = nil
		}
		var acc uint64
		bits := len(quanta) * 8
		for _, b := range quanta {
			acc = (acc << 8) | uint64(b)
		}
		// Left-align inside a 40-bit block so the padding zero bits sit
		// at the right end.
		acc <<= 40 - bits
		symbols := (bits + 4) / 5
		for i := 0; i < symbols; i++ {
			idx := (acc >> (35 - 5*i)) & 0x1F
			buf[i] = base32HexAlphabet[idx]
		}
		out = append(out, buf[:symbols]...)
	}
	return string(out)
}

// Base32HexDecode decodes an unpadded (padding is tolerated) extended hex
// base32 string. Both lower- and upper-case letters are accepted. Any
// character outside the alphabet, or non-zero trailing padding bits, is
// rejected.
func Base32HexDecode(s string) ([]byte, error) {
	var acc uint32
	nbits := uint(0)
	out := make([]byte, 0, (len(s)*5+7)/8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '=' { // tolerate padded input even though NSEC3 omits it
			continue
		}
		v := base32HexDecode[c]
		if v == 0xFF {
			return nil, ErrBadBase32Hex
		}
		acc = (acc << 5) | uint32(v)
		nbits += 5
		if nbits >= 8 {
			nbits -= 8
			out = append(out, byte(acc>>nbits))
			acc &= (1 << nbits) - 1
		}
	}
	if nbits > 0 {
		// Leftover bits must be zero (RFC 4648 section 3.5).
		if acc&((1<<nbits)-1) != 0 {
			return nil, ErrBadBase32Hex
		}
	}
	return out, nil
}
