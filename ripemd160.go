package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"math/bits"
)

var ripemdLeftMessage = [5][16]int{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{7, 4, 13, 1, 10, 6, 15, 3, 12, 0, 9, 5, 2, 14, 11, 8},
	{3, 10, 14, 4, 9, 15, 8, 1, 2, 7, 0, 6, 13, 11, 5, 12},
	{1, 9, 11, 10, 0, 8, 12, 4, 13, 3, 7, 15, 14, 5, 6, 2},
	{4, 0, 5, 9, 7, 12, 2, 10, 14, 1, 3, 8, 11, 6, 15, 13},
}

var ripemdRightMessage = [5][16]int{
	{5, 14, 7, 0, 9, 2, 11, 4, 13, 6, 15, 8, 1, 10, 3, 12},
	{6, 11, 3, 7, 0, 13, 5, 10, 14, 15, 8, 12, 4, 9, 1, 2},
	{15, 5, 1, 3, 7, 14, 6, 9, 11, 8, 12, 2, 10, 0, 4, 13},
	{8, 6, 4, 1, 3, 11, 15, 0, 5, 12, 2, 13, 9, 7, 10, 14},
	{12, 15, 10, 4, 1, 5, 8, 7, 6, 2, 13, 14, 0, 3, 9, 11},
}

var ripemdLeftShift = [5][16]int{
	{11, 14, 15, 12, 5, 8, 7, 9, 11, 13, 14, 15, 6, 7, 9, 8},
	{7, 6, 8, 13, 11, 9, 7, 15, 7, 12, 15, 9, 11, 7, 13, 12},
	{11, 13, 6, 7, 14, 9, 13, 15, 14, 8, 13, 6, 5, 12, 7, 5},
	{11, 12, 14, 15, 14, 15, 9, 8, 9, 14, 5, 6, 8, 6, 5, 12},
	{9, 15, 5, 11, 6, 8, 13, 12, 5, 12, 13, 14, 11, 8, 5, 6},
}

var ripemdRightShift = [5][16]int{
	{8, 9, 9, 11, 13, 15, 15, 5, 7, 7, 8, 11, 14, 14, 12, 6},
	{9, 13, 15, 7, 12, 8, 9, 11, 7, 7, 12, 7, 6, 15, 13, 11},
	{9, 7, 15, 11, 8, 6, 6, 14, 12, 13, 5, 14, 13, 13, 7, 5},
	{15, 5, 8, 11, 14, 14, 6, 14, 6, 9, 12, 9, 12, 5, 15, 8},
	{8, 5, 12, 9, 12, 5, 14, 6, 8, 13, 6, 5, 15, 13, 11, 11},
}

var ripemdLeftConstant = [5]uint32{
	0x00000000, 0x5a827999, 0x6ed9eba1, 0x8f1bbcdc, 0xa953fd4e,
}

var ripemdRightConstant = [5]uint32{
	0x50a28be6, 0x5c4dd124, 0x6d703ef3, 0x7a6d76e9, 0x00000000,
}

func ripemd160Sum(data []byte) [20]byte {
	h := [5]uint32{0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476, 0xc3d2e1f0}

	padded := make([]byte, 0, len(data)+72)
	padded = append(padded, data...)
	padded = append(padded, 0x80)
	for len(padded)%64 != 56 {
		padded = append(padded, 0)
	}
	var lengthBytes [8]byte
	binary.LittleEndian.PutUint64(lengthBytes[:], uint64(len(data))*8)
	padded = append(padded, lengthBytes[:]...)

	for offset := 0; offset < len(padded); offset += 64 {
		var x [16]uint32
		for i := range x {
			x[i] = binary.LittleEndian.Uint32(padded[offset+i*4 : offset+i*4+4])
		}

		al, bl, cl, dl, el := h[0], h[1], h[2], h[3], h[4]
		ar, br, cr, dr, er := h[0], h[1], h[2], h[3], h[4]

		for round := 0; round < 5; round++ {
			for step := 0; step < 16; step++ {
				t := al + ripemdFunction(round, bl, cl, dl) + x[ripemdLeftMessage[round][step]] + ripemdLeftConstant[round]
				t = bits.RotateLeft32(t, ripemdLeftShift[round][step]) + el
				al, el, dl, cl, bl = el, dl, bits.RotateLeft32(cl, 10), bl, t

				t = ar + ripemdFunction(4-round, br, cr, dr) + x[ripemdRightMessage[round][step]] + ripemdRightConstant[round]
				t = bits.RotateLeft32(t, ripemdRightShift[round][step]) + er
				ar, er, dr, cr, br = er, dr, bits.RotateLeft32(cr, 10), br, t
			}
		}

		t := h[1] + cl + dr
		h[1] = h[2] + dl + er
		h[2] = h[3] + el + ar
		h[3] = h[4] + al + br
		h[4] = h[0] + bl + cr
		h[0] = t
	}

	var digest [20]byte
	for i := 0; i < 5; i++ {
		binary.LittleEndian.PutUint32(digest[i*4:i*4+4], h[i])
	}
	return digest
}

func ripemdFunction(round int, x, y, z uint32) uint32 {
	switch round {
	case 0:
		return x ^ y ^ z
	case 1:
		return (x & y) | (^x & z)
	case 2:
		return (x | ^y) ^ z
	case 3:
		return (x & z) | (y & ^z)
	default:
		return x ^ (y | ^z)
	}
}

func hash160(data []byte) [20]byte {
	sha := sha256.Sum256(data)
	return ripemd160Sum(sha[:])
}
