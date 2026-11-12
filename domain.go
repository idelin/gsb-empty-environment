package netsec

// Interval 是闭区间 [Lo, Hi]，表示数值属性被泛化后的取值范围，
// 或背景知识中对某数值属性的已知区间。
type Interval struct {
	Lo, Hi float64
}

// Contains 报告 v 是否落在区间内（含端点）。
func (iv Interval) Contains(v float64) bool {
	return iv.Lo <= v && v <= iv.Hi
}

// Intersects 报告两个区间是否存在公共点。
func (iv Interval) Intersects(o Interval) bool {
	return iv.Lo <= o.Hi && o.Lo <= iv.Hi
}

// Domain 表示单个属性的取值可能集：
// 匿名记录中它是泛化/缺失后的可能原值集合，
// 外部观察中它是背景知识给出的约束。
// 三者恰有一个生效：Any（缺失，匹配一切）、Num（数值区间）、Cats（类别集合）。
type Domain struct {
	Any  bool
	Num  *Interval
	Cats map[string]bool
}

// Missing 返回缺失/被抑制属性的域，与任何约束兼容。
func Missing() Domain { return Domain{Any: true} }

// Exact 返回仅含单个数值的域。
func Exact(v float64) Domain { return Range(v, v) }

// Range 返回数值区间 [lo, hi] 的域；lo > hi 时 panic。
func Range(lo, hi float64) Domain {
	if lo > hi {
		panic("netsec: Range 下界大于上界")
	}
	return Domain{Num: &Interval{Lo: lo, Hi: hi}}
}

// Categories 返回类别集合的域；空集合视为缺失。
func Categories(vals ...string) Domain {
	if len(vals) == 0 {
		return Missing()
	}
	cats := make(map[string]bool, len(vals))
	for _, v := range vals {
		cats[v] = true
	}
	return Domain{Cats: cats}
}

// Intersects 报告两个域是否存在公共可能值。
// 任一方缺失即兼容；类型不一致（数值 vs 类别）不兼容。
func (d Domain) Intersects(o Domain) bool {
	if d.Any || o.Any {
		return true
	}
	if d.Num != nil && o.Num != nil {
		return d.Num.Intersects(*o.Num)
	}
	if d.Cats != nil && o.Cats != nil {
		for v := range d.Cats {
			if o.Cats[v] {
				return true
			}
		}
		return false
	}
	return false
}
