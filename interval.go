package destplan

// Interval is a half-open logical interval [Lo, Hi) of 64-bit addresses.
// The zero value is the empty interval. The full address space, whose
// exclusive end is 2^64, cannot be represented directly in Hi; build it with
// FullSpace or ShiftedSpan.
type Interval struct {
	Lo uint64
	Hi uint64
}

// Empty reports whether the interval contains no address.
func (i Interval) Empty() bool { return i.Lo == 0 && i.Hi == 0 }

// NewInterval builds [lo, hi) and rejects empty or inverted ranges.
func NewInterval(lo, hi uint64) (Interval, error) {
	if hi <= lo {
		return Interval{}, errf("invalid half-open interval [%d,%d)", lo, hi)
	}
	return Interval{Lo: lo, Hi: hi}, nil
}

// FullSpace returns [0, 2^64).
func FullSpace() Interval { return Interval{Lo: 0, Hi: ^uint64(0)} }

// IsFullSpace reports whether i covers every 64-bit address.
func (i Interval) IsFullSpace() bool { return i.Lo == 0 && i.Hi == ^uint64(0) }

// ShiftedSpan returns [lo, lo+length). length==2^64 with lo==0 yields the
// full space; every other 2^64-length or overflowing span is rejected.
func ShiftedSpan(lo, length uint64) (Interval, error) {
	if length == 0 {
		return Interval{}, errf("zero-length span at %d", lo)
	}
	if length == ^uint64(0) {
		if lo != 0 {
			return Interval{}, errf("span [%d,+2^64) leaves the address space", lo)
		}
		return FullSpace(), nil
	}
	if lo > ^uint64(0)-length {
		return Interval{}, errf("span [%d,+%d) leaves the address space", lo, length)
	}
	hi := lo + length
	return Interval{Lo: lo, Hi: hi}, nil
}

func (i Interval) loEnd() Addr { return Addr64(i.Lo) }

func (i Interval) hiEnd() Addr {
	if i.IsFullSpace() {
		return AddrEnd()
	}
	return Addr64(i.Hi)
}

// Length returns the span width, up to 2^64 for the full space.
func (i Interval) Length() Addr {
	if i.Empty() {
		return Addr{}
	}
	d, _ := addrSub(i.hiEnd(), i.loEnd())
	return d
}

// clampTo restricts the interval to [0, end), dropping parts outside it.
func (i Interval) clampTo(end Addr) (lo, hi Addr) {
	lo = i.loEnd()
	hi = i.hiEnd()
	if addrCmp(lo, end) >= 0 {
		return end, end
	}
	if addrCmp(hi, end) > 0 {
		hi = end
	}
	return lo, hi
}
