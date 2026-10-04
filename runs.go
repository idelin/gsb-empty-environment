package set32

// run 是闭区间 [lo, hi]，两个端点都包含在内。
// 一个规范的 runs 列表要求：按 lo 严格递增，且相邻 run 之间至少空一个值
// （即 next.lo > prev.hi + 1），不允许重叠也不允许相邻。
type run struct {
	lo, hi uint16
}

func runLen(r run) int { return int(r.hi) - int(r.lo) + 1 }

func runsCardinality(rs []run) int {
	n := 0
	for _, r := range rs {
		n += runLen(r)
	}
	return n
}

// runsOr 求两个规范 runs 列表的并集，结果同样规范（不相邻、不重叠）。
func runsOr(a, b []run) []run {
	var out []run
	push := func(lo, hi uint16) {
		if n := len(out); n > 0 && uint32(lo) <= uint32(out[n-1].hi)+1 {
			if hi > out[n-1].hi {
				out[n-1].hi = hi
			}
			return
		}
		out = append(out, run{lo, hi})
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i].lo <= b[j].lo {
			push(a[i].lo, a[i].hi)
			i++
		} else {
			push(b[j].lo, b[j].hi)
			j++
		}
	}
	for ; i < len(a); i++ {
		push(a[i].lo, a[i].hi)
	}
	for ; j < len(b); j++ {
		push(b[j].lo, b[j].hi)
	}
	return out
}

// runsAnd 求交集。输入规范则输出规范：任一输入的空隙都会保留在结果中。
func runsAnd(a, b []run) []run {
	var out []run
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		lo := a[i].lo
		if b[j].lo > lo {
			lo = b[j].lo
		}
		hi := a[i].hi
		if b[j].hi < hi {
			hi = b[j].hi
		}
		if lo <= hi {
			out = append(out, run{lo, hi})
		}
		if a[i].hi < b[j].hi {
			i++
		} else {
			j++
		}
	}
	return out
}

// runsSubtract 求 a \ b。内部用 int 计算避免 uint16 在 65535 处溢出。
func runsSubtract(a, b []run) []run {
	var out []run
	j := 0
	for _, r := range a {
		lo, hi := int(r.lo), int(r.hi)
		for j < len(b) && int(b[j].hi) < lo {
			j++
		}
		cur := lo
		for k := j; k < len(b) && int(b[k].lo) <= hi; k++ {
			if int(b[k].lo) > cur {
				out = append(out, run{uint16(cur), uint16(int(b[k].lo) - 1)})
			}
			if int(b[k].hi) >= hi {
				cur = hi + 1
				break
			}
			cur = int(b[k].hi) + 1
		}
		if cur <= hi {
			out = append(out, run{uint16(cur), uint16(hi)})
		}
	}
	return out
}

// runsXor 求对称差：xor = (a ∪ b) \ (a ∩ b)。
func runsXor(a, b []run) []run {
	return runsSubtract(runsOr(a, b), runsAnd(a, b))
}
