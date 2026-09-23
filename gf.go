package erasure

// GF(256) arithmetic with the explicit irreducible polynomial
// p(x) = x^8 + x^4 + x^3 + x^2 + 1, i.e. 0x11d, generator 2.
const gfPolynomial = 0x11d

var gfExp [512]byte // anti-log table (doubled to avoid modular reduction)
var gfLog [256]byte // log table

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[byte(x)] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= gfPolynomial
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
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
	return gfExp[(int(gfLog[a])-int(gfLog[b])+255)%255]
}

func gfInv(a byte) byte {
	if a == 0 {
		panic("erasure: inverse of zero in GF(256)")
	}
	return gfExp[255-gfLog[a]]
}

// gfPow returns a^n in GF(256); n must be >= 0.
func gfPow(a byte, n int) byte {
	if n == 0 {
		return 1
	}
	if a == 0 {
		return 0
	}
	return gfExp[(int(gfLog[a])*n)%255]
}
