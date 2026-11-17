package netsec

import "math/big"

func maskBits(width uint8) uint64 {
	if width >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << width) - 1
}

func unsignedBig(bits uint64, width uint8) *big.Int {
	z := new(big.Int).SetUint64(bits)
	if width < 64 {
		z.And(z, new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(width)), big.NewInt(1)))
	}
	return z
}

func signedBig(bits uint64, width uint8) *big.Int {
	z := unsignedBig(bits, width)
	if width > 0 && width <= 64 && z.Bit(int(width-1)) != 0 {
		z.Sub(z, new(big.Int).Lsh(big.NewInt(1), uint(width)))
	}
	return z
}

func signedInt64(bits uint64, width uint8) int64 {
	return signedBig(bits, width).Int64()
}

func checkOverflow(op Op, width uint8, x, y uint64, check OverflowCheck) bool {
	a := unsignedBig(x, width)
	b := unsignedBig(y, width)
	if check.Signed {
		a = signedBig(x, width)
		b = signedBig(y, width)
	}

	r := new(big.Int)
	switch op {
	case OpAdd:
		r.Add(a, b)
	case OpSub:
		r.Sub(a, b)
	case OpMul:
		r.Mul(a, b)
	default:
		return false
	}

	w := check.Width
	if w > 64 {
		return false
	}
	if check.Signed {
		if w == 0 {
			return r.Sign() != 0
		}
		limit := new(big.Int).Lsh(big.NewInt(1), uint(w-1))
		min := new(big.Int).Neg(new(big.Int).Set(limit))
		max := new(big.Int).Sub(limit, big.NewInt(1))
		return r.Cmp(min) < 0 || r.Cmp(max) > 0
	}
	if r.Sign() < 0 || r.BitLen() > int(w) {
		return true
	}
	return false
}
