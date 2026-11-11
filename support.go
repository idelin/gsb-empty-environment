package netsec

// abstractState 是当前谓词集合下的抽象立方（谓词真值赋值）。
// 第 i 位对值与谓词 i 编码：has 的第 i 位为 0 表示该谓词取 *（不关心），
// 为 1 时由 val 的第 i 位给出具体真假。谓词数不超过 24，uint32 足够。
type abstractState struct {
	has uint32
	val uint32
}

func (a abstractState) setBit(i int, b bool) abstractState {
	a.has |= 1 << uint(i)
	if b {
		a.val |= 1 << uint(i)
	} else {
		a.val &^= 1 << uint(i)
	}
	return a
}

// contains 判断具体取值 s 是否落在立方内。
func (a abstractState) contains(s Valuation, preds []Expr) bool {
	for i, pred := range preds {
		if a.has&(1<<uint(i)) == 0 {
			continue
		}
		want := a.val&(1<<uint(i)) != 0
		if pred.eval(s) != want {
			return false
		}
	}
	return true
}

// valSet 是具体取值集合，用 map 模拟位向量（最多 4096 个元素）。
type valSet map[Valuation]struct{}

func (s valSet) add(v Valuation) { s[v] = struct{}{} }
func (s valSet) contains(v Valuation) bool {
	_, ok := s[v]
	return ok
}

func (s valSet) copy() valSet {
	out := make(valSet, len(s))
	for v := range s {
		out.add(v)
	}
	return out
}

func (s valSet) slice() []Valuation {
	out := make([]Valuation, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	return out
}

// dedupPredicates 按规范字符串形式去除等价重复谓词。
func dedupPredicates(preds []Expr) []Expr {
	seen := make(map[string]struct{}, len(preds))
	out := make([]Expr, 0, len(preds))
	for _, p := range preds {
		key := p.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	return out
}
