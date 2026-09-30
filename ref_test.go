package fpcodec

// Independent exact-integer reference implementation of format conversion,
// written with math/big arbitrary-precision arithmetic. It shares no code
// with the optimized uint64 engine in convert.go, and never touches host
// floating point, so NaN payloads and subnormals cannot be mangled.

import "math/big"

func refConvert(bits uint64, from, to Width, pol Policy) (uint64, Flag) {
	sl := mustLayout(from)
	dl := mustLayout(to)
	sign := bits >> (sl.total() - 1) & 1
	signBit := sign << (dl.total() - 1)
	expF := (bits >> sl.mantBits) & sl.expMask()
	mantF := bits & sl.mantMask()

	if expF == sl.expMask() {
		if mantF == 0 { // Inf
			return signBit | dl.expMask()<<dl.mantBits, 0
		}
		// NaN: MSB-truncating payload policy, quiet signaling NaNs.
		payload := mantF
		if dl.mantBits >= sl.mantBits {
			payload <<= dl.mantBits - sl.mantBits
		} else {
			payload >>= sl.mantBits - dl.mantBits
		}
		var flags Flag
		if mantF&(uint64(1)<<(sl.mantBits-1)) == 0 {
			flags |= FlagInvalid
			payload |= uint64(1) << (dl.mantBits - 1)
		}
		if payload == 0 {
			payload = 1
		}
		return signBit | dl.expMask()<<dl.mantBits | payload, flags
	}
	if expF == 0 && mantF == 0 {
		return signBit, 0 // zero keeps its sign
	}
	if expF == 0 && pol.Subnormal == SubnormalFlushToZero {
		return signBit, 0
	}

	// Exact value: M * 2^E.
	var M *big.Int
	var E int
	if expF == 0 {
		M = new(big.Int).SetUint64(mantF)
		E = sl.emin() - int(sl.mantBits)
	} else {
		M = new(big.Int).SetUint64(mantF | uint64(1)<<sl.mantBits)
		E = int(expF) - sl.bias - int(sl.mantBits)
	}

	p := int(dl.mantBits) + 1
	unbiased := E + M.BitLen() - 1
	subnormalResult := unbiased < dl.emin()
	if subnormalResult && pol.Subnormal == SubnormalFlushToZero {
		return signBit, FlagUnderflow | FlagInexact
	}
	var qexp int // exponent of the destination LSB quantum
	if subnormalResult {
		qexp = dl.emin() - int(dl.mantBits)
	} else {
		qexp = unbiased - (p - 1)
	}

	// n = round_RNE(M * 2^(E-qexp)) in arbitrary precision.
	var n *big.Int
	inexact := false
	if E >= qexp {
		n = new(big.Int).Lsh(M, uint(E-qexp))
	} else {
		s := uint(qexp - E)
		n = new(big.Int).Rsh(new(big.Int).Set(M), s)
		lowMask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), s), big.NewInt(1))
		rem := new(big.Int).And(M, lowMask)
		half := new(big.Int).Lsh(big.NewInt(1), s-1)
		switch c := rem.Cmp(half); {
		case c > 0:
			n.Add(n, big.NewInt(1))
		case c == 0 && n.Bit(0) == 1:
			n.Add(n, big.NewInt(1))
		}
		inexact = rem.Sign() != 0
	}

	var flags Flag
	if inexact {
		flags |= FlagInexact
	}
	if subnormalResult {
		if inexact {
			flags |= FlagUnderflow
		}
		if n.Sign() == 0 {
			return signBit, flags
		}
		if n.BitLen() <= int(dl.mantBits) {
			return signBit | n.Uint64(), flags
		}
		return signBit | uint64(1)<<dl.mantBits, flags // rounded up to min normal
	}
	if n.BitLen() > p { // carry out of the top bit
		n.Rsh(n, 1)
		qexp++
	}
	if qexp+(p-1) > dl.emax() {
		return signBit | dl.expMask()<<dl.mantBits, flags | FlagOverflow | FlagInexact
	}
	expField := uint64(qexp + (p - 1) + dl.bias)
	return signBit | expField<<dl.mantBits | (n.Uint64() & dl.mantMask()), flags
}
