package destplan

// Addr is a 128-bit unsigned endpoint. Logical addresses are 64-bit, but the
// exclusive upper bound of the full address space is 2^64, which cannot be
// held in a uint64. All interval arithmetic therefore runs on Addr values so
// that boundary calculations never wrap or lose precision.
type Addr struct {
	Hi uint64
	Lo uint64
}

// Addr64 lifts a 64-bit address into a 128-bit endpoint.
func Addr64(v uint64) Addr { return Addr{Lo: v} }

// AddrEnd returns 2^64, the exclusive end of the logical address space.
func AddrEnd() Addr { return Addr{Hi: 1} }

// InSpace reports whether a lies inside [0, 2^64).
func (a Addr) InSpace() bool { return a.Hi == 0 }

func addrCmp(a, b Addr) int {
	switch {
	case a.Hi < b.Hi:
		return -1
	case a.Hi > b.Hi:
		return 1
	case a.Lo < b.Lo:
		return -1
	case a.Lo > b.Lo:
		return 1
	default:
		return 0
	}
}

func addrEqual(a, b Addr) bool { return a.Hi == b.Hi && a.Lo == b.Lo }

// addrAdd returns a+b and ok=false when the result overflows 128 bits.
func addrAdd(a, b Addr) (r Addr, ok bool) {
	lo := a.Lo + b.Lo
	carry := uint64(0)
	if lo < a.Lo {
		carry = 1
	}
	if a.Hi > ^uint64(0)-b.Hi || (carry == 1 && a.Hi+b.Hi == ^uint64(0)) {
		return Addr{}, false
	}
	hi := a.Hi + b.Hi + carry
	return Addr{Hi: hi, Lo: lo}, true
}

// addrAdd64 returns a+b and ok=false on 128-bit overflow.
func addrAdd64(a Addr, b uint64) (Addr, bool) {
	return addrAdd(a, Addr{Lo: b})
}

// addrSub returns a-b and ok=false on underflow.
func addrSub(a, b Addr) (r Addr, ok bool) {
	if addrCmp(a, b) < 0 {
		return Addr{}, false
	}
	lo := a.Lo - b.Lo
	borrow := uint64(0)
	if a.Lo < b.Lo {
		borrow = 1
	}
	return Addr{Hi: a.Hi - b.Hi - borrow, Lo: lo}, true
}

// addrShift applies a signed delta and reports ok=false when the result
// leaves the 64-bit address space.
func addrShift(a Addr, delta int64) (Addr, bool) {
	if delta == 0 {
		return a, true
	}
	if delta >= 0 {
		r, ok := addrAdd64(a, uint64(delta))
		if !ok || !r.InSpace() {
			return Addr{}, false
		}
		return r, true
	}
	d := uint64(-delta)
	if a.Hi != 0 || a.Lo < d {
		return Addr{}, false
	}
	return Addr{Lo: a.Lo - d}, true
}
