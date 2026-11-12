package netsec

// Affine 是寄存器的抽象值：要么是「区域基址 + 常数区间」形式的仿射地址，
// 要么是纯标量整数（可能来源于外部输入）。
//
// 地址来源集合 Origins 记录该值可能由哪些区域基址派生而来：
// 合流时对来源取并集，绝不丢弃来源，从而让每条读取都能追溯到声明区域。
// 纯标量的 Origins 为 nil。
type Affine struct {
	// Scalar 是该值相对各来源基址（或标量情形下相对 0）的偏移闭区间。
	Scalar Interval
	// Origins 的键为区域标识，值为相对该区域基址的偏移区间。
	// 非空表示这是一个仿射地址；nil 表示纯标量。
	Origins map[RegionID]Interval
	// External 标记该标量是否来源于外部输入。
	External bool
	// Checked 标记外部标量是否已经过显式边界检查收窄。
	Checked bool
	// CheckSites 记录使该值收窄的边界检查站点（来源审计用）。
	CheckSites []string
}

// Bottom 是不可达值（空区间、无来源）。
func Bottom() Affine {
	return Affine{Scalar: Empty()}
}

// IsBottom 报告是否为不可达值。
func (a Affine) IsBottom() bool {
	if a.Scalar.IsEmpty() {
		return true
	}
	for _, iv := range a.Origins {
		if iv.IsEmpty() {
			return true
		}
	}
	return false
}

// IsAddress 报告是否为带区域来源的仿射地址。
func (a Affine) IsAddress() bool { return len(a.Origins) > 0 }

// Const 返回纯常数标量。
func Const(v int64) Affine {
	return Affine{Scalar: Point(v)}
}

// RegionBase 返回区域 r 的基址值（来源集合只含 r，偏移为 0）。
func RegionBase(r RegionID) Affine {
	return Affine{
		Scalar:  Point(0),
		Origins: map[RegionID]Interval{r: Point(0)},
	}
}

// InvariantRegion 构造循环不变量用的仿射地址：区域 r 的基址加上偏移区间 iv。
// 与 Narrow（交集收窄）不同，它直接把来源偏移放宽到给定区间，供宿主声明
// 「该寄存器在循环头处可能取的整个偏移范围」。
func InvariantRegion(r RegionID, iv Interval) Affine {
	return Affine{
		Scalar:  iv,
		Origins: map[RegionID]Interval{r: iv},
	}
}

// ExternalTop 返回未经检查的外部输入值（无界标量）。
func ExternalTop() Affine {
	return Affine{Scalar: TopInterval(), External: true}
}

// TopIntervalValue 返回非外部的无界标量顶元素。
func TopIntervalValue() Affine {
	return Affine{Scalar: TopInterval()}
}

// Clone 深拷贝仿射值，避免别名共享来源映射。
func (a Affine) Clone() Affine {
	out := a
	if a.Origins != nil {
		out.Origins = make(map[RegionID]Interval, len(a.Origins))
		for r, iv := range a.Origins {
			out.Origins[r] = iv
		}
	}
	if a.CheckSites != nil {
		out.CheckSites = append([]string(nil), a.CheckSites...)
	}
	return out
}

// AddConst 实现「基址加常数」：整体偏移区间平移 c，来源集合保持不变。
func (a Affine) AddConst(c int64) Affine {
	if a.IsBottom() {
		return a
	}
	out := a.Clone()
	out.Scalar = out.Scalar.Shift(c)
	for r, iv := range out.Origins {
		out.Origins[r] = iv.Shift(c)
	}
	return out
}

// Narrow 用区间分支的结果 iv 收窄该值。
// 标量区间与 iv 取交集；对每个区域来源同样取交集（仿射收窄）。
// 外部值收窄后标记为已检查。checkSite 为分支站点标识。
func (a Affine) Narrow(iv Interval, checkSite string) Affine {
	if a.IsBottom() {
		return a
	}
	out := a.Clone()
	out.Scalar = out.Scalar.Intersect(iv)
	for r, old := range out.Origins {
		out.Origins[r] = old.Intersect(iv)
	}
	if out.External {
		out.Checked = true
		if checkSite != "" {
			out.CheckSites = append(out.CheckSites, checkSite)
		}
	}
	return out
}

// Join 实现合流：来源集合取并集，同源偏移取区间包络，标量取包络。
// 合流只做可靠近似，绝不删除任何来源或检查事实。
func (a Affine) Join(b Affine) Affine {
	switch {
	case a.IsBottom():
		return b.Clone()
	case b.IsBottom():
		return a.Clone()
	}

	out := Affine{
		Scalar:   a.Scalar.Hull(b.Scalar),
		External: a.External || b.External,
		Checked:  a.Checked && b.Checked,
	}

	// 来源集合取并集；共同来源的偏移取包络。
	if a.Origins != nil || b.Origins != nil {
		out.Origins = make(map[RegionID]Interval, len(a.Origins)+len(b.Origins))
		for r, iv := range a.Origins {
			out.Origins[r] = iv
		}
		for r, iv := range b.Origins {
			if cur, ok := out.Origins[r]; ok {
				out.Origins[r] = cur.Hull(iv)
			} else {
				out.Origins[r] = iv
			}
		}
	}

	out.CheckSites = mergeSites(a.CheckSites, b.CheckSites)
	return out
}

// Leq 是偏序：a 是否被 b 容纳（a ⊑ b），用于循环不变量保持性验证。
func (a Affine) Leq(b Affine) bool {
	if a.IsBottom() {
		return true
	}
	if b.IsBottom() || a.External != b.External {
		return false
	}
	// 地址型与纯标量不能互相容纳（来源结构必须一致）。
	if (len(a.Origins) == 0) != (len(b.Origins) == 0) {
		return false
	}
	// 纯标量（无来源）按整体标量区间比较；仿射地址的标量区间是各来源的
	// 包络，真正的包含性由下面逐来源的偏移区间判定。
	if len(a.Origins) == 0 && !a.Scalar.SubsetOf(b.Scalar) {
		return false
	}
	// a 的每个来源都必须在 b 中出现，且其偏移区间被 b 对应区间容纳。
	for r, iv := range a.Origins {
		biv, ok := b.Origins[r]
		if !ok || !iv.SubsetOf(biv) {
			return false
		}
	}
	return true
}

func mergeSites(x, y []string) []string {
	if len(x) == 0 && len(y) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(x)+len(y))
	out := make([]string, 0, len(x)+len(y))
	for _, s := range x {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range y {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
