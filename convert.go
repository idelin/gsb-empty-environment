package fpcodec

// Flag is a floating-point exception flag raised by Convert.
type Flag uint8

const (
	FlagInexact   Flag = 1 << iota // result not exactly representable
	FlagUnderflow                  // tiny non-zero result rounded in subnormal range
	FlagOverflow                   // magnitude too large, produced Inf
	FlagInvalid                    // signaling NaN input quieted
)

func (f Flag) Has(g Flag) bool { return f&g != 0 }

func (f Flag) String() string {
	if f == 0 {
		return "none"
	}
	s := ""
	add := func(name string) {
		if s != "" {
			s += "|"
		}
		s += name
	}
	if f.Has(FlagInexact) {
		add("inexact")
	}
	if f.Has(FlagUnderflow) {
		add("underflow")
	}
	if f.Has(FlagOverflow) {
		add("overflow")
	}
	if f.Has(FlagInvalid) {
		add("invalid")
	}
	return s
}

// NaNPayloadPolicy selects how a NaN payload is narrowed.
type NaNPayloadPolicy int

const (
	// NaNTruncateMSB keeps the most significant payload bits when the
	// destination mantissa is narrower, and left-shifts (zero-fills low
	// bits) when widening. If truncation would erase the whole payload,
	// the least significant payload bit is set so the value stays NaN.
	NaNTruncateMSB NaNPayloadPolicy = iota
)

// SignalingPolicy selects how signaling NaNs are converted.
type SignalingPolicy int

const (
	// SignalingQuiet sets the quiet bit on signaling NaN inputs and
	// raises FlagInvalid (IEEE-754 default behavior).
	SignalingQuiet SignalingPolicy = iota
)

// SubnormalPolicy selects how subnormal values are converted.
type SubnormalPolicy int

const (
	// SubnormalPreserve converts subnormal inputs and produces subnormal
	// results exactly as IEEE-754 specifies (default).
	SubnormalPreserve SubnormalPolicy = iota
	// SubnormalFlushToZero treats subnormal inputs as signed zero
	// (silently) and flushes would-be subnormal results to signed zero,
	// raising FlagUnderflow|FlagInexact when a non-zero result is lost.
	SubnormalFlushToZero
)

// Policy bundles the explicit conversion policies.
type Policy struct {
	NaNPayload NaNPayloadPolicy
	Signaling  SignalingPolicy
	Subnormal  SubnormalPolicy
}

// DefaultPolicy returns the IEEE-754 default policy:
// MSB-truncating NaN payloads, quieting signaling NaNs, preserving
// subnormals, round-to-nearest-ties-to-even.
func DefaultPolicy() Policy {
	return Policy{
		NaNPayload: NaNTruncateMSB,
		Signaling:  SignalingQuiet,
		Subnormal:  SubnormalPreserve,
	}
}

// Convert reinterprets bits as a `from`-width float and converts it to the
// `to` width with round-to-nearest-ties-to-even, returning the destination
// bit pattern and any raised exception flags.
//
// The conversion is pure integer math: no host floating-point arithmetic is
// involved, so NaN payloads, signaling bits, subnormals and signed zeros are
// never mangled by the hardware. Unsupported widths are rejected before any
// work is done.
func Convert(bits uint64, from, to Width, pol Policy) (uint64, Flag, error) {
	src, err := layoutOf(from)
	if err != nil {
		return 0, 0, err
	}
	dst, err := layoutOf(to)
	if err != nil {
		return 0, 0, err
	}
	u := unpack(src, bits)
	signBit := u.sign << (dst.total() - 1)
	switch u.cls {
	case clsNaN:
		out, f := convertNaN(u, src, dst, pol)
		return out, f, nil
	case clsInf:
		return signBit | dst.expMask()<<dst.mantBits, 0, nil
	case clsZero:
		return signBit, 0, nil
	}
	if pol.Subnormal == SubnormalFlushToZero && u.cls == clsSubnormal {
		return signBit, 0, nil // flushed input, silent by policy
	}
	out, f := convertFinite(u, dst, pol)
	return out, f, nil
}

// convertNaN maps a NaN to the destination format per policy.
func convertNaN(u unpacked, src, dst layout, pol Policy) (uint64, Flag) {
	var flags Flag
	payload := u.payload
	switch {
	case dst.mantBits >= src.mantBits:
		payload <<= dst.mantBits - src.mantBits
	default:
		payload >>= src.mantBits - dst.mantBits
	}
	if u.signaling && pol.Signaling == SignalingQuiet {
		flags |= FlagInvalid
		payload |= uint64(1) << (dst.mantBits - 1)
	}
	if payload == 0 {
		payload = 1 // never let a NaN degenerate into Inf
	}
	signBit := u.sign << (dst.total() - 1)
	return signBit | dst.expMask()<<dst.mantBits | payload, flags
}

// convertFinite rounds a non-zero finite value into the dst layout.
func convertFinite(u unpacked, dst layout, pol Policy) (uint64, Flag) {
	var flags Flag
	signBit := u.sign << (dst.total() - 1)
	m, e := u.mant, u.exp
	precision := int(dst.mantBits) + 1
	msb := bitLen(m) - 1
	unbiased := e + msb // exponent of the most significant bit

	var shift int
	subnormalResult := unbiased < dst.emin()
	if subnormalResult {
		if pol.Subnormal == SubnormalFlushToZero {
			return signBit, FlagUnderflow | FlagInexact
		}
		// Fix the quantum at the subnormal LSB: value = q * 2^(emin-mantBits).
		shift = (dst.emin() - int(dst.mantBits)) - e
	} else {
		shift = msb - (precision - 1)
	}

	q, inexact := roundRNE(m, shift)
	if inexact {
		flags |= FlagInexact
	}

	if subnormalResult {
		if inexact {
			flags |= FlagUnderflow
		}
		if q == 0 {
			return signBit, flags // rounded to signed zero
		}
		if q>>dst.mantBits == 0 {
			return signBit | q, flags // subnormal
		}
		// Rounded up into the smallest normal.
		return signBit | 1<<dst.mantBits, flags
	}

	// Normal path: q may have carried into one extra bit.
	if bitLen(q) > precision {
		q >>= 1
		unbiased++
	}
	if unbiased > dst.emax() {
		// Overflow: RNE yields Inf.
		return signBit | dst.expMask()<<dst.mantBits, flags | FlagOverflow | FlagInexact
	}
	expField := uint64(unbiased + dst.bias)
	return signBit | expField<<dst.mantBits | (q & dst.mantMask()), flags
}
