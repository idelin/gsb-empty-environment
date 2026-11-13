package netsec

// KeyBytes is the number of key bytes handled by this package.
const KeyBytes = 4

var (
	sbox              [256]byte
	invSbox           [256]byte
	inverseDifference [256][256][]byte
)

var faultMatrix = [KeyBytes][KeyBytes]byte{
	{2, 3, 1, 1},
	{1, 2, 3, 1},
	{1, 1, 2, 3},
	{3, 1, 1, 2},
}

func init() {
	for x := 0; x < 256; x++ {
		inverse := byte(0)
		if x != 0 {
			inverse = gfPow(byte(x), 254)
		}
		sbox[x] = inverse ^
			rotl8(inverse, 1) ^
			rotl8(inverse, 2) ^
			rotl8(inverse, 3) ^
			rotl8(inverse, 4) ^
			0x63
	}
	for x := 0; x < 256; x++ {
		invSbox[sbox[x]] = byte(x)
	}

	for y := 0; y < 256; y++ {
		for outputDifference := 0; outputDifference < 256; outputDifference++ {
			inputDifference := invSbox[y] ^ invSbox[y^outputDifference]
			inverseDifference[outputDifference][inputDifference] = append(
				inverseDifference[outputDifference][inputDifference],
				byte(y),
			)
		}
	}
}

// SBox applies the S-box defined by the exercise.
func SBox(x byte) byte {
	return sbox[x]
}

// InvSBox is the inverse of SBox.
func InvSBox(x byte) byte {
	return invSbox[x]
}

// GFMultiply multiplies two bytes in GF(256) modulo x^8+x^4+x^3+x+1.
func GFMultiply(a, b byte) byte {
	return gfMul(a, b)
}

func gfPow(base byte, exponent uint) byte {
	result := byte(1)
	for exponent > 0 {
		if exponent&1 != 0 {
			result = gfMul(result, base)
		}
		base = gfMul(base, base)
		exponent >>= 1
	}
	return result
}

func gfMul(a, b byte) byte {
	var product byte
	for b != 0 {
		if b&1 != 0 {
			product ^= a
		}
		highBit := a & 0x80
		a <<= 1
		if highBit != 0 {
			a ^= 0x1b
		}
		b >>= 1
	}
	return product
}

func rotl8(x byte, n uint) byte {
	return x<<n | x>>(8-n)
}
