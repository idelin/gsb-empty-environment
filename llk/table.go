package llk

import (
	"fmt"
	"strings"
)

// LLKError 是“文法超界异常”：文法不是 LL(k)，
// 即存在某个非终结符的两个候选产生式在 K 步前瞻下无法区分。
type LLKError struct {
	K           int
	NonTerminal string
	Lookahead   TokenSeq   // 冲突的 K 步前瞻
	AltA, AltB  Production // 冲突的两条候选
}

func (e *LLKError) Error() string {
	la := "ε"
	if len(e.Lookahead) > 0 {
		la = strings.Join(e.Lookahead, " ")
	}
	return fmt.Sprintf("grammar is not LL(%d): non-terminal %s alternatives %q and %q share %d-token lookahead %q",
		e.K, e.NonTerminal, e.AltA.String(), e.AltB.String(), e.K, la)
}

// Table 是 LL(k) 分支预测表：非终结符 -> (K 步前瞻 -> 候选产生式)。
type Table struct {
	K       int
	Start   string
	entries map[string]map[string]*Production
	FirstK  map[string][]TokenSeq // 每个非终结符的 FIRST_k（供调试/展示）
}

// BuildTable 计算 FIRST_k / FOLLOW_k 并构建预测表。
// 候选 A->α 的表项键为 FIRST_k(α) ⊗_k FOLLOW_k(A)（标准 LL(k) 构造）。
// 若文法不是 LL(k)，返回 *LLKError，不构建表。
func BuildTable(g *Grammar, k int) (*Table, error) {
	if k < 1 {
		return nil, fmt.Errorf("llk: K must be >= 1, got %d", k)
	}
	first := computeFirstK(g, k)
	follow := computeFollowK(g, k, first)
	t := &Table{
		K:       k,
		Start:   g.Start,
		entries: map[string]map[string]*Production{},
		FirstK:  map[string][]TokenSeq{},
	}
	for lhs, set := range first {
		t.FirstK[lhs] = set.sorted()
	}
	for lhs, prods := range g.Prods {
		row := map[string]*Production{}
		for i := range prods {
			p := &prods[i]
			rhs := p.RHS
			if len(rhs) == 0 {
				rhs = []Symbol{Eps}
			}
			la := concatK(firstKOfSeq(rhs, first, k), follow[lhs], k)
			for _, seq := range la.sorted() {
				key := seq.key()
				if prev, ok := row[key]; ok {
					return nil, &LLKError{
						K: k, NonTerminal: lhs, Lookahead: seq,
						AltA: *prev, AltB: *p,
					}
				}
				row[key] = p
			}
		}
		t.entries[lhs] = row
	}
	return t, nil
}

// lookup 按前瞻序列查找候选产生式。
func (t *Table) lookup(nt string, la TokenSeq) *Production {
	row, ok := t.entries[nt]
	if !ok {
		return nil
	}
	return row[la.key()]
}

// String 以可读形式打印预测表。
func (t *Table) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "LL(%d) prediction table (start: %s)\n", t.K, t.Start)
	nts := make([]string, 0, len(t.entries))
	for nt := range t.entries {
		nts = append(nts, nt)
	}
	for i := 1; i < len(nts); i++ {
		for j := i; j > 0 && nts[j] < nts[j-1]; j-- {
			nts[j], nts[j-1] = nts[j-1], nts[j]
		}
	}
	for _, nt := range nts {
		fmt.Fprintf(&b, "  %s: FIRST_%d =", nt, t.K)
		for _, seq := range t.FirstK[nt] {
			if len(seq) == 0 {
				b.WriteString(" {ε}")
			} else {
				fmt.Fprintf(&b, " {%s}", strings.Join(seq, " "))
			}
		}
		b.WriteString("\n")
		keys := seqSet{}
		for key := range t.entries[nt] {
			keys[key] = nil
		}
		for key := range keys {
			la := strings.Split(key, "\x00")
			if key == "" {
				la = []string{"ε"}
			}
			fmt.Fprintf(&b, "    [%s] -> %s\n", strings.Join(la, " "), t.entries[nt][key].String())
		}
	}
	return b.String()
}
