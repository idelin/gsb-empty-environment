package erasure

// GF(256) arithmetic with the explicit irreducible polynomial
// x^8 + x^4 + x^3 + x^2 + 1 (0x11D). The element 0x02 is a
// generator of the multiplicative group, so log/exp tables are
// built from powers of 2.
const poly = 0x11D

var gfExp [512]byte // exp table, doubled to avoid modular reduction
var gfLog [256]byte // log table, valid for non-zero elements

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[byte(x)] = byte(i)
		x = gfMulSlow(x, 2)
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

// gfMulSlow multiplies in GF(256) via Russian peasant multiplication,
// reducing with the explicit polynomial. Used only to build tables.
func gfMulSlow(a, b int) int {
	r := 0
	for b > 0 {
		if b&1 != 0 {
			r ^= a
		}
		b >>= 1
		a <<= 1
		if a&0x100 != 0 {
			a ^= poly
		}
	}
	return r & 0xFF
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func gfDiv(a, b byte) byte {
	if b == 0 {
		panic("erasure: division by zero in GF(256)")
	}
	if a == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])-int(gfLog[b])+255]
}

func gfInv(a byte) byte {
	if a == 0 {
		panic("erasure: inverse of zero in GF(256)")
	}
	return gfExp[255-int(gfLog[a])]
}
