package decisiontable

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Node 是判定树节点。Leaf=true 时 Action 有效；否则按 Dim 维度分支。
type Node struct {
	Leaf     bool
	Action   string
	Dim      int
	Branches []Branch
}

// Branch 是判定树的一条分支：满足 Cond 的输入进入 Next 子树。
type Branch struct {
	Cond Constraint
	Next *Node
}

// BuildTree 把（压缩后的、无冲突的）规则集编译为判定树，用于快速匹配。
func BuildTree(dims []Dimension, rules []Rule) *Node {
	if len(rules) == 0 {
		return &Node{Leaf: true, Action: "<无匹配>"}
	}
	if sameAction(rules) {
		return &Node{Leaf: true, Action: rules[0].Action}
	}
	d := pickDim(dims, rules)
	if d == -1 { // 所有规则均为通配但动作不同：存在未修复的冲突
		return &Node{Leaf: true, Action: rules[0].Action}
	}
	n := &Node{Dim: d}
	switch dims[d].Kind {
	case BoolKind, EnumKind:
		n.Branches = buildDiscreteBranches(dims, rules, d)
	default:
		n.Branches = buildNumberBranches(dims, rules, d)
	}
	return n
}

func sameAction(rules []Rule) bool {
	for _, r := range rules[1:] {
		if r.Action != rules[0].Action {
			return false
		}
	}
	return true
}

// pickDim 选择非通配规则数最多的维度（并列取靠前者），加速收敛。
func pickDim(dims []Dimension, rules []Rule) int {
	best, bestScore := -1, 0
	for d := range dims {
		score := 0
		for _, r := range rules {
			if !r.Conds[d].Any {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = d, score
		}
	}
	return best
}

// narrow 返回"维度 d 取值确定后"的子规则集：保留覆盖该值的规则并把维度 d 置为通配。
func narrow(dims []Dimension, rules []Rule, d int, match func(Constraint) bool) []Rule {
	var sub []Rule
	for _, r := range rules {
		if r.Conds[d].Any || match(r.Conds[d]) {
			nr := Rule{ID: r.ID, Action: r.Action, Conds: make([]Constraint, len(dims))}
			copy(nr.Conds, r.Conds)
			nr.Conds[d] = Constraint{Any: true}
			sub = append(sub, nr)
		}
	}
	return sub
}

func buildDiscreteBranches(dims []Dimension, rules []Rule, d int) []Branch {
	kind := dims[d].Kind
	var branches []Branch
	if kind == BoolKind {
		for _, v := range []bool{false, true} {
			sub := narrow(dims, rules, d, func(c Constraint) bool { return c.Bools[v] })
			if len(sub) == 0 {
				continue
			}
			branches = append(branches, Branch{Cond: Bool(v), Next: BuildTree(dims, sub)})
		}
		return branches
	}
	// EnumKind：按出现的取值建分支，另加通配默认分支覆盖未列举取值
	vals := map[string]bool{}
	for _, r := range rules {
		for v := range r.Conds[d].Strs {
			vals[v] = true
		}
	}
	ordered := make([]string, 0, len(vals))
	for v := range vals {
		ordered = append(ordered, v)
	}
	sort.Strings(ordered)
	for _, v := range ordered {
		sub := narrow(dims, rules, d, func(c Constraint) bool { return c.Strs[v] })
		branches = append(branches, Branch{Cond: Eq(v), Next: BuildTree(dims, sub)})
	}
	var anyRules []Rule
	for _, r := range rules {
		if r.Conds[d].Any {
			anyRules = append(anyRules, r)
		}
	}
	if len(anyRules) > 0 {
		branches = append(branches, Branch{Cond: Any(), Next: BuildTree(dims, anyRules)})
	}
	return branches
}

// numSeg 是数值轴上的基本段（点或开区间），sample 用于判定覆盖规则集。
type numSeg struct {
	lo, hi         float64
	loInf, hiInf   bool
	loOpen, hiOpen bool
	sample         float64
}

func buildNumberBranches(dims []Dimension, rules []Rule, d int) []Branch {
	bset := map[float64]bool{}
	for _, r := range rules {
		iv := r.Conds[d].Num
		if r.Conds[d].Any {
			continue
		}
		if !iv.LoInf {
			bset[iv.Lo] = true
		}
		if !iv.HiInf {
			bset[iv.Hi] = true
		}
	}
	bounds := make([]float64, 0, len(bset))
	for b := range bset {
		bounds = append(bounds, b)
	}
	sort.Float64s(bounds)

	var segs []numSeg
	segs = append(segs, numSeg{loInf: true, hi: bounds[0], hiOpen: true, loOpen: true, sample: bounds[0] - 1})
	for i, b := range bounds {
		segs = append(segs, numSeg{lo: b, hi: b, sample: b}) // 边界点段
		if i+1 < len(bounds) {
			next := bounds[i+1]
			segs = append(segs, numSeg{lo: b, hi: next, loOpen: true, hiOpen: true, sample: (b + next) / 2})
		}
	}
	last := bounds[len(bounds)-1]
	segs = append(segs, numSeg{lo: last, hiInf: true, loOpen: true, hiOpen: true, sample: last + 1})

	type group struct {
		key   string
		segLo numSeg
		segHi numSeg
		sub   []Rule
	}
	var groups []group
	for _, s := range segs {
		sub := narrow(dims, rules, d, func(c Constraint) bool { return c.Num.contains(s.sample) })
		if len(sub) == 0 {
			continue // 无规则覆盖的空隙
		}
		key := ruleSetKey(sub)
		if n := len(groups); n > 0 && groups[n-1].key == key {
			groups[n-1].segHi = s // 相邻段覆盖集相同，合并为一条分支
			continue
		}
		groups = append(groups, group{key: key, segLo: s, segHi: s, sub: sub})
	}

	var branches []Branch
	for _, g := range groups {
		iv := Interval{
			Lo: g.segLo.lo, LoInf: g.segLo.loInf, LoOpen: g.segLo.loOpen,
			Hi: g.segHi.hi, HiInf: g.segHi.hiInf, HiOpen: g.segHi.hiOpen,
		}
		branches = append(branches, Branch{Cond: Constraint{Num: iv}, Next: BuildTree(dims, g.sub)})
	}
	return branches
}

func ruleSetKey(rules []Rule) string {
	ids := make([]string, len(rules))
	for i, r := range rules {
		ids[i] = strconv.Itoa(r.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// Evaluate 沿判定树匹配输入，返回动作。
func (n *Node) Evaluate(dims []Dimension, in Input) (string, bool) {
	if n == nil {
		return "", false
	}
	if n.Leaf {
		return n.Action, true
	}
	d := n.Dim
	v := in[dims[d].Name]
	for _, b := range n.Branches {
		if b.Cond.matchValue(dims[d].Kind, v) {
			return b.Next.Evaluate(dims, in)
		}
	}
	return "", false
}

// String 以缩进树形文本输出判定树。
func (n *Node) String(dims []Dimension) string {
	var sb strings.Builder
	writeNode(&sb, dims, n, "", true)
	return sb.String()
}

func writeNode(sb *strings.Builder, dims []Dimension, n *Node, prefix string, root bool) {
	if root {
		if n.Leaf {
			fmt.Fprintf(sb, "=> %s\n", n.Action)
			return
		}
		fmt.Fprintf(sb, "按 %s 分支:\n", dims[n.Dim].Name)
	}
	for i, b := range n.Branches {
		last := i == len(n.Branches)-1
		tee, fill := "├── ", "│   "
		if last {
			tee, fill = "└── ", "    "
		}
		label := b.Cond.String(dims[n.Dim])
		if b.Next.Leaf {
			fmt.Fprintf(sb, "%s%s%s => %s\n", prefix, tee, label, b.Next.Action)
		} else {
			fmt.Fprintf(sb, "%s%s%s, 按 %s 分支:\n", prefix, tee, label, dims[b.Next.Dim].Name)
			writeNode(sb, dims, b.Next, prefix+fill, false)
		}
	}
}

// LeafCount 统计叶子数（用于验证压缩效果）。
func (n *Node) LeafCount() int {
	if n == nil {
		return 0
	}
	if n.Leaf {
		return 1
	}
	total := 0
	for _, b := range n.Branches {
		total += b.Next.LeafCount()
	}
	return total
}
