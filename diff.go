package permdiff

import (
	"fmt"
	"sort"
)

// Config 配置差异检查的有限域与搜索上限。
type Config struct {
	// IntMin/IntMax 是整数属性的有限域（闭区间）。缺省为 [0, 100]。
	IntMin, IntMax int
	// MaxEnum 限制枚举的完整赋值个数，超出后截断并把 Result.Truncated 置真。
	// 缺省为 1 << 20。
	MaxEnum int
}

func (c Config) norm() Config {
	if c.IntMax <= c.IntMin {
		c.IntMin, c.IntMax = 0, 100
	}
	if c.MaxEnum <= 0 {
		c.MaxEnum = 1 << 20
	}
	return c
}

// Result 是差异检查的结果。
type Result struct {
	// Expands 为真表示新规则扩大了授权：存在赋值使新规则确定为允许
	// 而旧规则不是确定为允许（False 或 Unknown）。
	Expands bool `json:"expands"`
	// Witness 是使差异成立的最小属性组合；缺失的键表示该属性无需提供。
	Witness Assignment `json:"witness,omitempty"`
	// NewValue/OldValue 是新旧规则在见证赋值下的三值结果。
	NewValue Tri `json:"newValue,omitempty"`
	OldValue Tri `json:"oldValue,omitempty"`
	// NewTree/OldTree 是新旧规则在见证赋值下的解释树。
	NewTree *Explain `json:"newTree,omitempty"`
	OldTree *Explain `json:"oldTree,omitempty"`
	// Truncated 为真表示枚举空间被截断，结果可能不完备。
	Truncated bool `json:"truncated,omitempty"`
}

// Diff 判断新规则 newC 是否比旧规则 oldC 扩大了授权。
// 扩大当且仅当存在赋值 a 使得 newC(a) = True 且 oldC(a) ≠ True。
// 若扩大，返回使差异成立的最小主体/资源属性组合及两边的解释树。
func Diff(oldC, newC Cond, cfg Config) Result {
	cfg = cfg.norm()
	attrs := newC.Attrs()
	preds := append(oldC.Predicates(), newC.Predicates()...)

	cands := make([][]Value, len(attrs))
	for i, a := range attrs {
		cands[i] = candidates(a, preds, cfg)
	}

	const maxRawWitnesses = 256
	var raw []Assignment
	enumerated := 0
	truncated := false

	var rec func(i int, cur Assignment)
	rec = func(i int, cur Assignment) {
		if truncated || len(raw) >= maxRawWitnesses {
			return
		}
		if i == len(attrs) {
			enumerated++
			if enumerated > cfg.MaxEnum {
				truncated = true
				return
			}
			if isWitness(oldC, newC, cur) {
				raw = append(raw, cur.Clone())
			}
			return
		}
		for _, v := range cands[i] {
			cur[attrs[i]] = v
			rec(i+1, cur)
		}
		delete(cur, attrs[i])
	}
	rec(0, Assignment{})

	var best Assignment
	for _, w := range raw {
		w = minimize(oldC, newC, w)
		if betterWitness(w, best) {
			best = w
		}
	}

	res := Result{Expands: best != nil, Truncated: truncated}
	if best != nil {
		res.Witness = best
		res.NewValue, res.NewTree = newC.Eval(best)
		res.OldValue, res.OldTree = oldC.Eval(best)
	}
	return res
}

// isWitness 判断赋值是否证明扩大：新规则确定为允许，旧规则不确定为允许。
func isWitness(oldC, newC Cond, a Assignment) bool {
	nv, _ := newC.Eval(a)
	if nv != True {
		return false
	}
	ov, _ := oldC.Eval(a)
	return ov != True
}

// minimize 贪心缩小见证：先尝试删除整个属性（退化为缺失/Unknown），
// 再尝试缩减集合属性中的元素，保持见证性质不变。
func minimize(oldC, newC Cond, w Assignment) Assignment {
	w = w.Clone()
	for _, attr := range w.Keys() {
		saved := w[attr]
		delete(w, attr)
		if !isWitness(oldC, newC, w) {
			w[attr] = saved
		}
	}
	for _, attr := range w.Keys() {
		v := w[attr]
		if v.Kind != KindSet {
			continue
		}
		for _, e := range v.Elems() {
			delete(v.Set, e)
			if !isWitness(oldC, newC, w) {
				v.Set[e] = true
			}
		}
		w[attr] = v
	}
	return w
}

// betterWitness 比较两个见证的最小性：
// 先比属性个数，再比集合总大小，再比整数和，最后按字典序保证确定性。
func betterWitness(a, b Assignment) bool {
	if a == nil {
		return false
	}
	if b == nil {
		return true
	}
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	if sa, sb := setSize(a), setSize(b); sa != sb {
		return sa < sb
	}
	if ia, ib := intSum(a), intSum(b); ia != ib {
		return ia < ib
	}
	return fmt.Sprintf("%v", a) < fmt.Sprintf("%v", b)
}

func setSize(a Assignment) int {
	n := 0
	for _, v := range a {
		if v.Kind == KindSet {
			n += len(v.Set)
		}
	}
	return n
}

func intSum(a Assignment) int {
	n := 0
	for _, v := range a {
		if v.Kind == KindInt {
			n += v.Int
		}
	}
	return n
}

// candidates 为属性生成候选值：整数取谓词区间端点及其相邻点，
// 集合取谓词字面集合、元素全集的小组合及增减变体，布尔取真假。
func candidates(attr string, preds []Predicate, cfg Config) []Value {
	ints := map[int]bool{cfg.IntMin: true, cfg.IntMax: true}
	hasInt, hasSet, hasBool := false, false, false
	var literals []map[string]bool
	universe := map[string]bool{}

	addInt := func(v int) {
		if v >= cfg.IntMin && v <= cfg.IntMax {
			ints[v] = true
		}
	}
	addLiteral := func(s map[string]bool) {
		literals = append(literals, s)
		for e := range s {
			universe[e] = true
		}
	}

	for _, p := range preds {
		if p.Attr() != attr {
			continue
		}
		switch q := p.(type) {
		case IntRange:
			hasInt = true
			addInt(q.Min)
			addInt(q.Max)
			addInt(q.Min - 1)
			addInt(q.Max + 1)
		case BoolIs:
			hasBool = true
		case SetSubsetOf:
			hasSet = true
			addLiteral(q.Set)
		case SetContainsAll:
			hasSet = true
			addLiteral(q.Set)
		case SetContains:
			hasSet = true
			addLiteral(map[string]bool{q.Elem: true})
		case SetIntersects:
			hasSet = true
			addLiteral(q.Set)
		}
	}

	var out []Value
	if hasInt {
		sorted := make([]int, 0, len(ints))
		for v := range ints {
			sorted = append(sorted, v)
		}
		sort.Ints(sorted)
		for _, v := range sorted {
			out = append(out, IntValue(v))
		}
	}
	if hasBool {
		out = append(out, BoolValue(false), BoolValue(true))
	}
	if hasSet {
		for _, s := range setCandidates(literals, universe) {
			out = append(out, Value{Kind: KindSet, Set: s})
		}
	}
	return out
}

// setCandidates 以确定顺序生成集合候选：空集、全集元素的单例与配对、
// 各字面集合及其增减一个元素的变体。上限 128 个，超出即截断。
func setCandidates(literals []map[string]bool, universe map[string]bool) []map[string]bool {
	const cap = 128
	seen := map[string]bool{}
	var out []map[string]bool
	add := func(s map[string]bool) {
		if len(out) >= cap {
			return
		}
		k := setKey(s)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, s)
	}

	elems := make([]string, 0, len(universe))
	for e := range universe {
		elems = append(elems, e)
	}
	sort.Strings(elems)

	add(map[string]bool{})
	for _, e := range elems {
		add(map[string]bool{e: true})
	}
	for i := 0; i < len(elems); i++ {
		for j := i + 1; j < len(elems); j++ {
			add(map[string]bool{elems[i]: true, elems[j]: true})
		}
	}
	for _, lit := range literals {
		add(cloneSet(lit))
		for _, e := range elems {
			if lit[e] {
				minus := cloneSet(lit)
				delete(minus, e)
				add(minus)
			} else {
				plus := cloneSet(lit)
				plus[e] = true
				add(plus)
			}
		}
	}
	return out
}

func cloneSet(s map[string]bool) map[string]bool {
	out := make(map[string]bool, len(s))
	for e := range s {
		out[e] = true
	}
	return out
}

func setKey(s map[string]bool) string {
	elems := make([]string, 0, len(s))
	for e := range s {
		elems = append(elems, e)
	}
	sort.Strings(elems)
	k := ""
	for _, e := range elems {
		k += e + "\x00"
	}
	return k
}
