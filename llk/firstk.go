package llk

import "strings"

// TokenSeq 是一个 Token 序列（长度 <= K），用作 FIRST_k 集合元素与预测表键。
type TokenSeq []string

func (s TokenSeq) key() string { return strings.Join(s, "\x00") }

// seqSet 是 TokenSeq 的集合。
type seqSet map[string]TokenSeq

func (s seqSet) add(seq TokenSeq) bool {
	k := seq.key()
	if _, ok := s[k]; ok {
		return false
	}
	cp := make(TokenSeq, len(seq))
	copy(cp, seq)
	s[k] = cp
	return true
}

func (s seqSet) sorted() []TokenSeq {
	out := make([]TokenSeq, 0, len(s))
	for _, seq := range s {
		out = append(out, seq)
	}
	// 简单插入排序，保证输出确定性。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].key() < out[j-1].key(); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// concatK 计算两个 FIRST_k 集合的 K 截断连接：{ xy | x∈A, y∈B, |xy|<=K }。
func concatK(a, b seqSet, k int) seqSet {
	out := seqSet{}
	if len(a) == 0 {
		a = seqSet{"": {}}
	}
	if len(b) == 0 {
		b = seqSet{"": {}}
	}
	for _, x := range a {
		for _, y := range b {
			xy := make(TokenSeq, 0, k)
			xy = append(xy, x...)
			xy = append(xy, y...)
			if len(xy) > k {
				xy = xy[:k]
			}
			out.add(xy)
		}
	}
	return out
}

// firstKOfSeq 在给定的非终结符 FIRST_k 表下，计算符号串的 FIRST_k。
func firstKOfSeq(symbols []Symbol, first map[string]seqSet, k int) seqSet {
	result := seqSet{"": {}} // 空串的 FIRST_k = {ε}
	for _, sym := range symbols {
		var symFirst seqSet
		switch {
		case sym.Epsilon:
			symFirst = seqSet{"": {}}
		case sym.Term:
			symFirst = seqSet{}
			symFirst.add(TokenSeq{sym.Name})
		default:
			symFirst = first[sym.Name]
		}
		result = concatK(result, symFirst, k)
	}
	return result
}

// computeFirstK 对文法中所有非终结符做不动点迭代，求 FIRST_k。
func computeFirstK(g *Grammar, k int) map[string]seqSet {
	first := map[string]seqSet{}
	for lhs := range g.Prods {
		first[lhs] = seqSet{}
	}
	for changed := true; changed; {
		changed = false
		for lhs, prods := range g.Prods {
			for _, p := range prods {
				rhs := p.RHS
				if len(rhs) == 0 {
					rhs = []Symbol{Eps}
				}
				for _, seq := range firstKOfSeq(rhs, first, k).sorted() {
					if first[lhs].add(seq) {
						changed = true
					}
				}
			}
		}
	}
	return first
}

// computeFollowK 对文法中所有非终结符做不动点迭代，求 FOLLOW_k。
// 开始符号的 FOLLOW_k 含空串（输入结束）。
func computeFollowK(g *Grammar, k int, first map[string]seqSet) map[string]seqSet {
	follow := map[string]seqSet{}
	for lhs := range g.Prods {
		follow[lhs] = seqSet{}
	}
	follow[g.Start] = seqSet{"": {}}
	for changed := true; changed; {
		changed = false
		for _, prods := range g.Prods {
			for _, p := range prods {
				for i, sym := range p.RHS {
					if sym.Term {
						continue
					}
					// FOLLOW_k(sym) += FIRST_k(剩余串) ⊗_k FOLLOW_k(p.LHS)
					beta := firstKOfSeq(p.RHS[i+1:], first, k)
					for _, seq := range concatK(beta, follow[p.LHS], k).sorted() {
						if follow[sym.Name].add(seq) {
							changed = true
						}
					}
				}
			}
		}
	}
	return follow
}
