package netsec

import (
	"errors"
	"strings"
)

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

var (
	errInvalidBech32 = errors.New("invalid bech32 encoding")
	errInvalidField  = errors.New("invalid invoice field")
)

var bech32Index = func() [256]int8 {
	var table [256]int8
	for i := range table {
		table[i] = -1
	}
	for i := 0; i < len(bech32Charset); i++ {
		table[bech32Charset[i]] = int8(i)
	}
	return table
}()

func bech32Polymod(values []byte) uint32 {
	generators := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	var chk uint32 = 1
	for _, value := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(value)
		for i := 0; i < 5; i++ {
			if (top>>uint(i))&1 != 0 {
				chk ^= generators[i]
			}
		}
	}
	return chk
}

func bech32HRPExpand(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]&31)
	}
	return out
}

func bech32Checksum(hrp string, data []byte) []byte {
	values := append(bech32HRPExpand(hrp), data...)
	values = append(values, 0, 0, 0, 0, 0, 0)
	chk := bech32Polymod(values) ^ 1
	out := make([]byte, 6)
	for i := 0; i < 6; i++ {
		out[i] = byte((chk >> uint(5*(5-i))) & 31)
	}
	return out
}

func bech32Encode(hrp string, data []byte) string {
	checksum := bech32Checksum(hrp, data)
	var b strings.Builder
	b.Grow(len(hrp) + 1 + len(data) + len(checksum))
	b.WriteString(hrp)
	b.WriteByte('1')
	for _, value := range append(append([]byte{}, data...), checksum...) {
		b.WriteByte(bech32Charset[value])
	}
	return b.String()
}

func bech32Decode(s string) (string, []byte, error) {
	if len(s) < 8 {
		return "", nil, errInvalidBech32
	}
	if len(s) > 16384 {
		return "", nil, errInvalidBech32
	}

	hasLower, hasUpper := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 33 || c > 126 {
			return "", nil, errInvalidBech32
		}
		if c >= 'a' && c <= 'z' {
			hasLower = true
		}
		if c >= 'A' && c <= 'Z' {
			hasUpper = true
		}
	}
	if hasLower && hasUpper {
		return "", nil, errInvalidBech32
	}
	s = strings.ToLower(s)

	separator := strings.LastIndexByte(s, '1')
	if separator < 1 || separator+7 > len(s) {
		return "", nil, errInvalidBech32
	}
	hrp := s[:separator]
	encoded := s[separator+1:]
	data := make([]byte, len(encoded))
	for i := 0; i < len(encoded); i++ {
		index := bech32Index[encoded[i]]
		if index < 0 {
			return "", nil, errInvalidBech32
		}
		data[i] = byte(index)
	}

	if bech32Polymod(append(bech32HRPExpand(hrp), data...)) != 1 {
		return "", nil, errInvalidBech32
	}
	return hrp, data[:len(data)-6], nil
}

// convert5To8 converts bech32 words to bytes. When pad is false, any leftover
// bits must be zero; BOLT 11 uses that property to reject malleable fields.
func convert5To8(data []byte, pad bool) ([]byte, error) {
	var acc uint16
	var bits uint
	out := make([]byte, 0, len(data)*5/8)
	for _, value := range data {
		if value > 31 {
			return nil, errInvalidField
		}
		acc = acc<<5 | uint16(value)
		bits += 5
		for bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
			acc &= (1 << bits) - 1
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, byte(acc<<(8-bits)))
		}
		return out, nil
	}
	if bits >= 5 || acc != 0 {
		return nil, errInvalidField
	}
	return out, nil
}

func convert8To5(data []byte, pad bool) ([]byte, error) {
	var acc uint16
	var bits uint
	out := make([]byte, 0, (len(data)*8+4)/5)
	for _, value := range data {
		acc = acc<<8 | uint16(value)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out = append(out, byte(acc>>bits)&31)
			acc &= (1 << bits) - 1
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, byte(acc<<(5-bits))&31)
		}
		return out, nil
	}
	if bits > 0 && acc != 0 {
		return nil, errInvalidField
	}
	return out, nil
}
