package netsec

import "math/bits"

// Interval 是带符号 64 位整数上的闭区间 [Lo, Hi]。
//
// Lo > Hi 表示空区间（不可达）。Lo == MinInt64 且 Hi == MaxInt64
// 表示无界顶元素。所有算术在溢出时退化为顶元素，保证可靠性。
type Interval struct {
	Lo int64
	Hi int64
}

// TopInterval 返回无界顶元素。
func TopInterval() Interval { return Interval{Lo: minInt, Hi: maxInt} }

// Point 返回单点区间 [v, v]。
func Point(v int64) Interval { return Interval{Lo: v, Hi: v} }

// Bounded 构造闭区间，若 lo > hi 则返回空区间。
func Bounded(lo, hi int64) Interval {
	if lo > hi {
		return Empty()
	}
	return Interval{Lo: lo, Hi: hi}
}

// Empty 返回空区间（用于不可达状态）。
func Empty() Interval { return Interval{Lo: 1, Hi: 0} }

const (
	minInt = -1 << 63
	maxInt = 1<<63 - 1
)

// IsEmpty 报告区间是否为空。
func (iv Interval) IsEmpty() bool { return iv.Lo > iv.Hi }

// IsTop 报告区间是否覆盖整个 int64 域。
func (iv Interval) IsTop() bool { return iv.Lo == minInt && iv.Hi == maxInt }

// IsSingle 报告区间是否退化为单点。
func (iv Interval) IsSingle() bool { return !iv.IsEmpty() && iv.Lo == iv.Hi }

// Contains 判断 v 是否落在区间内。
func (iv Interval) Contains(v int64) bool {
	return !iv.IsEmpty() && iv.Lo <= v && v <= iv.Hi
}

// SubsetOf 判断 iv 是否为其他的子集（空区间是任意区间的子集）。
func (iv Interval) SubsetOf(other Interval) bool {
	if iv.IsEmpty() {
		return true
	}
	if other.IsEmpty() {
		return false
	}
	return other.Lo <= iv.Lo && iv.Hi <= other.Hi
}

// Hull 返回覆盖 iv 与 other 的最小闭区间（区间包络）。
func (iv Interval) Hull(other Interval) Interval {
	switch {
	case iv.IsEmpty():
		return other
	case other.IsEmpty():
		return iv
	}
	lo := iv.Lo
	if other.Lo < lo {
		lo = other.Lo
	}
	hi := iv.Hi
	if other.Hi > hi {
		hi = other.Hi
	}
	return Interval{Lo: lo, Hi: hi}
}

// Intersect 返回两区间的交集；无交集时返回空区间。
func (iv Interval) Intersect(other Interval) Interval {
	if iv.IsEmpty() || other.IsEmpty() {
		return Empty()
	}
	lo := iv.Lo
	if other.Lo > lo {
		lo = other.Lo
	}
	hi := iv.Hi
	if other.Hi < hi {
		hi = other.Hi
	}
	if lo > hi {
		return Empty()
	}
	return Interval{Lo: lo, Hi: hi}
}

// addChecked 计算带符号加法并报告是否溢出。
func addChecked(a, b int64) (int64, bool) {
	s := a + b
	overflow := (a > 0 && b > maxInt-a) || (a < 0 && b < minInt-b)
	return s, overflow
}

// Shift 将区间整体平移 c；任一端点溢出则退化为顶元素。
func (iv Interval) Shift(c int64) Interval {
	if iv.IsEmpty() {
		return iv
	}
	lo, ofLo := addChecked(iv.Lo, c)
	hi, ofHi := addChecked(iv.Hi, c)
	if ofLo || ofHi {
		return TopInterval()
	}
	return Interval{Lo: lo, Hi: hi}
}

// Add 返回两区间的整数包络（Minkowski 和的可靠近似）。
// 端点加法溢出或端点宽度本身溢出时退化为顶元素。
func (iv Interval) Add(other Interval) Interval {
	if iv.IsEmpty() || other.IsEmpty() {
		return Empty()
	}
	lo, ofLo := addChecked(iv.Lo, other.Lo)
	hi, ofHi := addChecked(iv.Hi, other.Hi)
	if ofLo || ofHi {
		return TopInterval()
	}
	if widthOverflows(iv) || widthOverflows(other) {
		return TopInterval()
	}
	return Interval{Lo: lo, Hi: hi}
}

// widthOverflows 判断区间宽度 Hi-Lo 是否无法用非负 int64 表达。
func widthOverflows(iv Interval) bool {
	if iv.IsEmpty() || iv.IsSingle() {
		return false
	}
	_, borrow := bits.Sub64(uint64(iv.Hi), uint64(iv.Lo), 0)
	return borrow != 0
}
