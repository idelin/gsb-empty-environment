package sharevault

// fieldPrime is the modulus of GF(257). Field elements are the integers
// 0..256 inclusive and are carried in uint16 values.
const fieldPrime = uint32(257)

func feValid(v uint16) bool { return uint32(v) < fieldPrime }

func feAdd(a, b uint16) uint16 {
	return uint16((uint32(a) + uint32(b)) % fieldPrime)
}

func feSub(a, b uint16) uint16 {
	return uint16((uint32(a) + fieldPrime - uint32(b)) % fieldPrime)
}

func feMul(a, b uint16) uint16 {
	return uint16((uint32(a) * uint32(b)) % fieldPrime)
}

func feInv(a uint16) (uint16, bool) {
	if a == 0 {
		return 0, false
	}
	// Fermat's little theorem: a^-1 == a^(p-2) mod p.
	result := uint16(1)
	base := a
	for e := fieldPrime - 2; e > 0; e >>= 1 {
		if e&1 == 1 {
			result = feMul(result, base)
		}
		base = feMul(base, base)
	}
	return result, true
}
