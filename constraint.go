package decisiontable

import (
	"sort"
	"strconv"
	"strings"
)

// Kind 是条件维度（列）的类型。
type Kind int

const (
	BoolKind   Kind = iota // 布尔维度，如 VIP=true/false
	NumberKind             // 数值维度，用区间约束，如 金额>500
	EnumKind               // 枚举维度，用取值集合约束，如 渠道∈{online}
)

// Interval 是数值区间，支持开闭端点与正负无穷。
type Interval struct {
	Lo, Hi         float64
	LoOpen, HiOpen bool // true 表示开区间端点
	LoInf, HiInf   bool // true 表示该端为无穷（无界）
}

// contains 判断 v 是否落在区间内。
func (iv Interval) contains(v float64) bool {
	if !iv.LoInf {
		if v < iv.Lo || (v == iv.Lo && iv.LoOpen) {
			return false
		}
	}
	if !iv.HiInf {
		if v > iv.Hi || (v == iv.Hi && iv.HiOpen) {
			return false
		}
	}
	return true
}

// valid 判断区间是否非空。
func (iv Interval) valid() bool {
	if iv.LoInf || iv.HiInf {
		return true
	}
	if iv.Lo < iv.Hi {
		return true
	}
	return iv.Lo == iv.Hi && !iv.LoOpen && !iv.HiOpen // 退化为单点
}

// intersect 返回两区间的交集；ok=false 表示不相交。
func (a Interval) intersect(b Interval) (Interval, bool) {
	var r Interval
	switch {
	case a.LoInf && b.LoInf:
		r.LoInf = true
	case a.LoInf:
		r.Lo, r.LoOpen = b.Lo, b.LoOpen
	case b.LoInf:
		r.Lo, r.LoOpen = a.Lo, a.LoOpen
	case a.Lo > b.Lo:
		r.Lo, r.LoOpen = a.Lo, a.LoOpen
	case b.Lo > a.Lo:
		r.Lo, r.LoOpen = b.Lo, b.LoOpen
	default:
		r.Lo, r.LoOpen = a.Lo, a.LoOpen || b.LoOpen
	}
	switch {
	case a.HiInf && b.HiInf:
		r.HiInf = true
	case a.HiInf:
		r.Hi, r.HiOpen = b.Hi, b.HiOpen
	case b.HiInf:
		r.Hi, r.HiOpen = a.Hi, a.HiOpen
	case a.Hi < b.Hi:
		r.Hi, r.HiOpen = a.Hi, a.HiOpen
	case b.Hi < a.Hi:
		r.Hi, r.HiOpen = b.Hi, b.HiOpen
	default:
		r.Hi, r.HiOpen = a.Hi, a.HiOpen || b.HiOpen
	}
	return r, r.valid()
}

// union 返回包住两区间的最小区间（调用前需保证可合并）。
func (a Interval) union(b Interval) Interval {
	var r Interval
	switch {
	case a.LoInf || b.LoInf:
		r.LoInf = true
	case a.Lo < b.Lo:
		r.Lo, r.LoOpen = a.Lo, a.LoOpen
	case b.Lo < a.Lo:
		r.Lo, r.LoOpen = b.Lo, b.LoOpen
	default:
		r.Lo, r.LoOpen = a.Lo, a.LoOpen && b.LoOpen
	}
	switch {
	case a.HiInf || b.HiInf:
		r.HiInf = true
	case a.Hi > b.Hi:
		r.Hi, r.HiOpen = a.Hi, a.HiOpen
	case b.Hi > a.Hi:
		r.Hi, r.HiOpen = b.Hi, b.HiOpen
	default:
		r.Hi, r.HiOpen = a.Hi, a.HiOpen && b.HiOpen
	}
	return r
}

// merge 尝试把两个区间合并为一个连续区间（相交或端点相接）；ok=false 表示中间有空隙。
func (a Interval) merge(b Interval) (Interval, bool) {
	if _, ok := a.intersect(b); ok {
		return a.union(b), true
	}
	if !a.HiInf && !b.LoInf && a.Hi == b.Lo && !(a.HiOpen && b.LoOpen) {
		return a.union(b), true
	}
	if !b.HiInf && !a.LoInf && b.Hi == a.Lo && !(b.HiOpen && a.LoOpen) {
		return a.union(b), true
	}
	return Interval{}, false
}

func (iv Interval) equal(o Interval) bool { return iv == o }

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// human 返回区间的紧凑可读形式，如 "≤100"、">500"、"∈(100, 500]"。
func (iv Interval) human() string {
	switch {
	case iv.LoInf && iv.HiInf:
		return "=*"
	case iv.LoInf:
		if iv.HiOpen {
			return "<" + formatFloat(iv.Hi)
		}
		return "≤" + formatFloat(iv.Hi)
	case iv.HiInf:
		if iv.LoOpen {
			return ">" + formatFloat(iv.Lo)
		}
		return "≥" + formatFloat(iv.Lo)
	case iv.Lo == iv.Hi:
		return "=" + formatFloat(iv.Lo)
	default:
		lo, hi := "[", "]"
		if iv.LoOpen {
			lo = "("
		}
		if iv.HiOpen {
			hi = ")"
		}
		return "∈" + lo + formatFloat(iv.Lo) + ", " + formatFloat(iv.Hi) + hi
	}
}

// Constraint 是单个维度上的约束。Any=true 表示通配（匹配任意值）。
type Constraint struct {
	Any   bool
	Num   Interval        // NumberKind 使用
	Bools map[bool]bool   // BoolKind 使用：允许的取值集合
	Strs  map[string]bool // EnumKind 使用：允许的取值集合
}

// Any 构造通配约束。
func Any() Constraint { return Constraint{Any: true} }

// Bool 构造布尔等值约束。
func Bool(v bool) Constraint { return Constraint{Bools: map[bool]bool{v: true}} }

// Eq 构造枚举等值约束。
func Eq(s string) Constraint { return Constraint{Strs: map[string]bool{s: true}} }

// OneOf 构造枚举集合约束。
func OneOf(ss ...string) Constraint {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return Constraint{Strs: m}
}

// Range 构造数值区间约束 [lo, hi]（可通过 loOpen/hiOpen 改为开端点）。
func Range(lo, hi float64, loOpen, hiOpen bool) Constraint {
	return Constraint{Num: Interval{Lo: lo, Hi: hi, LoOpen: loOpen, HiOpen: hiOpen}}
}

// Between 构造左开右闭区间 (lo, hi]，业务上表示 "lo < x ≤ hi"。
func Between(lo, hi float64) Constraint { return Range(lo, hi, true, false) }

// Gt 构造 (v, +∞)。
func Gt(v float64) Constraint { return Constraint{Num: Interval{Lo: v, LoOpen: true, HiInf: true}} }

// Ge 构造 [v, +∞)。
func Ge(v float64) Constraint { return Constraint{Num: Interval{Lo: v, HiInf: true}} }

// Lt 构造 (-∞, v)。
func Lt(v float64) Constraint { return Constraint{Num: Interval{Hi: v, HiOpen: true, LoInf: true}} }

// Le 构造 (-∞, v]。
func Le(v float64) Constraint { return Constraint{Num: Interval{Hi: v, LoInf: true}} }

// intersect 求两个约束的交集；ok=false 表示不相交（两规则在该维度上不重叠）。
func (c Constraint) intersect(o Constraint, k Kind) (Constraint, bool) {
	if c.Any {
		return o, true
	}
	if o.Any {
		return c, true
	}
	switch k {
	case BoolKind:
		r := map[bool]bool{}
		for v := range c.Bools {
			if o.Bools[v] {
				r[v] = true
			}
		}
		if len(r) == 0 {
			return Constraint{}, false
		}
		return Constraint{Bools: r}, true
	case EnumKind:
		r := map[string]bool{}
		for v := range c.Strs {
			if o.Strs[v] {
				r[v] = true
			}
		}
		if len(r) == 0 {
			return Constraint{}, false
		}
		return Constraint{Strs: r}, true
	default: // NumberKind
		iv, ok := c.Num.intersect(o.Num)
		if !ok {
			return Constraint{}, false
		}
		return Constraint{Num: iv}, true
	}
}

// merge 尝试把两个约束合并为一个约束（用于规则压缩）。
// 通配吸收一切；布尔/枚举取并集；数值区间要求连续（相交或相接）。
func (c Constraint) merge(o Constraint, k Kind) (Constraint, bool) {
	if c.Any || o.Any {
		return Constraint{Any: true}, true
	}
	switch k {
	case BoolKind:
		r := map[bool]bool{}
		for v := range c.Bools {
			r[v] = true
		}
		for v := range o.Bools {
			r[v] = true
		}
		if len(r) == 2 { // {true,false} 等价于通配
			return Constraint{Any: true}, true
		}
		return Constraint{Bools: r}, true
	case EnumKind:
		r := map[string]bool{}
		for v := range c.Strs {
			r[v] = true
		}
		for v := range o.Strs {
			r[v] = true
		}
		return Constraint{Strs: r}, true
	default: // NumberKind
		iv, ok := c.Num.merge(o.Num)
		if !ok {
			return Constraint{}, false
		}
		if iv.LoInf && iv.HiInf { // 合并后覆盖全轴，等价于通配
			return Constraint{Any: true}, true
		}
		return Constraint{Num: iv}, true
	}
}

func (c Constraint) equal(o Constraint, k Kind) bool {
	if c.Any || o.Any {
		return c.Any == o.Any
	}
	switch k {
	case BoolKind:
		if len(c.Bools) != len(o.Bools) {
			return false
		}
		for v := range c.Bools {
			if !o.Bools[v] {
				return false
			}
		}
		return true
	case EnumKind:
		if len(c.Strs) != len(o.Strs) {
			return false
		}
		for v := range c.Strs {
			if !o.Strs[v] {
				return false
			}
		}
		return true
	default:
		return c.Num.equal(o.Num)
	}
}

// String 返回 "维度名+约束" 的可读形式，如 "VIP=true"、"金额>500"。
func (c Constraint) String(d Dimension) string {
	if c.Any {
		return d.Name + "=*"
	}
	switch d.Kind {
	case BoolKind:
		if c.Bools[true] {
			return d.Name + "=true"
		}
		return d.Name + "=false"
	case EnumKind:
		vals := make([]string, 0, len(c.Strs))
		for v := range c.Strs {
			vals = append(vals, v)
		}
		sort.Strings(vals)
		if len(vals) == 1 {
			return d.Name + "=" + vals[0]
		}
		return d.Name + "∈{" + strings.Join(vals, ",") + "}"
	default:
		return d.Name + c.Num.human()
	}
}

// matchValue 判断具体输入值是否满足约束。
func (c Constraint) matchValue(k Kind, v any) bool {
	if c.Any {
		return true
	}
	switch k {
	case BoolKind:
		b, ok := v.(bool)
		return ok && c.Bools[b]
	case EnumKind:
		s, ok := v.(string)
		return ok && c.Strs[s]
	default:
		var f float64
		switch n := v.(type) {
		case float64:
			f = n
		case float32:
			f = float64(n)
		case int:
			f = float64(n)
		case int64:
			f = float64(n)
		default:
			return false
		}
		return c.Num.contains(f)
	}
}
