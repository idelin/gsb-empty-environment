// Package rete 实现一个增量 RETE 模式匹配网络。
//
// 网络由两层构成：
//   - Alpha 网络：对单条事实做常量条件过滤，命中的事实进入 Alpha 记忆。
//   - Beta 网络：JoinNode 将左侧 Beta 记忆中的 Token 与右侧 Alpha 记忆中的
//     事实做跨事实 Join，逐级产出更长的 Token，末端 Token 激活规则进入冲突集。
//
// 新增事实沿网络增量传播（right/left activate），撤销事实时以该事实为根的
// Token 子树被精确级联删除（负 Token 语义），并同步清除冲突集中的待激活条目，
// 全程不需要重新扫描事实库。
package rete

import (
	"fmt"
	"reflect"
)

// Fact 是事实库中的一条工作记忆元素（WME）。
type Fact struct {
	ID     int64
	Type   string
	Fields map[string]interface{}
}

func (f *Fact) String() string {
	return fmt.Sprintf("%s#%d%v", f.Type, f.ID, f.Fields)
}

// Condition 是 Alpha 层的常量条件，例如 {Field:"age", Op:">=", Value:18}。
type Condition struct {
	Field string
	Op    string // "==" "!=" "<" "<=" ">" ">="
	Value interface{}
}

// Test 判断事实是否满足该常量条件。
func (c Condition) Test(f *Fact) bool {
	v, ok := f.Fields[c.Field]
	if !ok {
		return false
	}
	switch c.Op {
	case "==":
		return valuesEqual(v, c.Value)
	case "!=":
		return !valuesEqual(v, c.Value)
	case "<", "<=", ">", ">=":
		cmp, ok := compareOrdered(v, c.Value)
		if !ok {
			return false
		}
		switch c.Op {
		case "<":
			return cmp < 0
		case "<=":
			return cmp <= 0
		case ">":
			return cmp > 0
		default:
			return cmp >= 0
		}
	}
	return false
}

// JoinTest 描述 Beta 层 Join：Token 中第 LeftIndex 个事实（0 起，按模式顺序）
// 的 LeftField 字段必须等于新事实的 RightField 字段。
type JoinTest struct {
	LeftIndex  int
	LeftField  string
	RightField string
}

// Pattern 是规则中的一个模式：某类型事实 + 常量条件 + 与左侧事实的 Join 约束。
type Pattern struct {
	Type      string
	Conds     []Condition
	JoinTests []JoinTest
}

// Rule 是一条产生式规则，所有模式依次 Join 全部命中即激活。
type Rule struct {
	Name     string
	Patterns []Pattern
}

// AlphaNode 常量过滤节点，命中事实缓存在 memory 中。
type AlphaNode struct {
	factType string
	conds    []Condition
	memory   map[int64]*Fact
	children []*JoinNode
}

func (a *AlphaNode) matches(f *Fact) bool {
	for _, c := range a.conds {
		if !c.Test(f) {
			return false
		}
	}
	return true
}

// BetaMemory 存放 Join 产出的 Token。
type BetaMemory struct {
	tokens     map[*Token]struct{}
	joins      []*JoinNode
	production *Production
}

// JoinNode 左接 BetaMemory、右接 AlphaNode，输出到 out。
type JoinNode struct {
	left  *BetaMemory
	right *AlphaNode
	tests []JoinTest
	out   *BetaMemory
}

// Production 规则末端节点。
type Production struct {
	rule Rule
}

// Token 是一次部分/完整匹配：父 Token + 本层事实，children 支撑级联撤回。
type Token struct {
	parent     *Token
	fact       *Fact // 根哑 Token 为 nil
	memory     *BetaMemory
	children   map[*Token]struct{}
	activation *Activation
}

// Facts 按模式顺序返回 Token 覆盖的全部事实。
func (t *Token) Facts() []*Fact {
	var rev []*Fact
	for cur := t; cur != nil && cur.fact != nil; cur = cur.parent {
		rev = append(rev, cur.fact)
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// Activation 是冲突集（议程）中的一条待激活条目。
type Activation struct {
	Rule  string
	Token *Token
}

func (a *Activation) String() string {
	return fmt.Sprintf("Activation(%s, facts=%v)", a.Rule, a.Token.Facts())
}

// Network 是 RETE 网络本体。
type Network struct {
	root       *BetaMemory
	alphas     []*AlphaNode
	byType     map[string][]*AlphaNode
	facts      map[int64]*Fact
	factTokens map[int64][]*Token // 事实 ID -> 直接以该事实创建的 Token
	agenda     []*Activation
	agendaIdx  map[*Token]*Activation
	nextID     int64
}

// NewNetwork 创建空网络，根 Beta 记忆内含一个哑 Token 作为 Join 起点。
func NewNetwork() *Network {
	n := &Network{
		byType:     make(map[string][]*AlphaNode),
		facts:      make(map[int64]*Fact),
		factTokens: make(map[int64][]*Token),
		agendaIdx:  make(map[*Token]*Activation),
	}
	n.root = &BetaMemory{tokens: make(map[*Token]struct{})}
	dummy := &Token{memory: n.root, children: make(map[*Token]struct{})}
	n.root.tokens[dummy] = struct{}{}
	return n
}

// AddRule 把规则编译进网络：每个模式对应一个 Alpha 节点（相同条件复用）
// 和一个 Join 节点，末端挂 Production。
func (n *Network) AddRule(rule Rule) {
	mem := n.root
	for i, p := range rule.Patterns {
		alpha := n.alphaFor(p.Type, p.Conds)
		jn := &JoinNode{
			left:  mem,
			right: alpha,
			tests: p.JoinTests,
			out:   &BetaMemory{tokens: make(map[*Token]struct{})},
		}
		mem.joins = append(mem.joins, jn)
		alpha.children = append(alpha.children, jn)
		mem = jn.out
		_ = i
	}
	mem.production = &Production{rule: rule}
}

func (n *Network) alphaFor(factType string, conds []Condition) *AlphaNode {
	for _, a := range n.alphas {
		if a.factType == factType && reflect.DeepEqual(a.conds, conds) {
			return a
		}
	}
	a := &AlphaNode{factType: factType, conds: conds, memory: make(map[int64]*Fact)}
	n.alphas = append(n.alphas, a)
	n.byType[factType] = append(n.byType[factType], a)
	return a
}

// Assert 新增事实并沿网络增量传播，返回带 ID 的事实句柄。
func (n *Network) Assert(factType string, fields map[string]interface{}) *Fact {
	n.nextID++
	f := &Fact{ID: n.nextID, Type: factType, Fields: fields}
	n.facts[f.ID] = f
	for _, an := range n.byType[factType] {
		if an.matches(f) {
			an.memory[f.ID] = f
			for _, jn := range an.children {
				n.rightActivate(jn, f)
			}
		}
	}
	return f
}

// Retract 撤销事实：从 Alpha 记忆移除后，精确级联删除所有依赖该事实的
// Token 子树（负 Token 语义），并同步清除冲突集中的对应待激活条目。
func (n *Network) Retract(id int64) bool {
	f, ok := n.facts[id]
	if !ok {
		return false
	}
	delete(n.facts, id)
	for _, an := range n.byType[f.Type] {
		delete(an.memory, id)
	}
	// 只触碰以该事实为根的 Token，其余 Token 原地保留，不做全量重扫。
	roots := append([]*Token(nil), n.factTokens[id]...)
	for _, t := range roots {
		n.deleteToken(t)
	}
	delete(n.factTokens, id)
	return true
}

// deleteToken 级联删除 Token 及其全部后代。
func (n *Network) deleteToken(t *Token) {
	children := make([]*Token, 0, len(t.children))
	for c := range t.children {
		children = append(children, c)
	}
	for _, c := range children {
		n.deleteToken(c)
	}
	if t.activation != nil {
		n.removeActivation(t.activation)
		t.activation = nil
	}
	delete(t.memory.tokens, t)
	if t.parent != nil {
		delete(t.parent.children, t)
	}
	if t.fact != nil {
		lst := n.factTokens[t.fact.ID]
		for i, x := range lst {
			if x == t {
				lst = append(lst[:i], lst[i+1:]...)
				break
			}
		}
		if len(lst) == 0 {
			delete(n.factTokens, t.fact.ID)
		} else {
			n.factTokens[t.fact.ID] = lst
		}
	}
}

// rightActivate：新事实进入 Alpha 记忆后，与左侧 Beta 记忆中的 Token 做 Join。
func (n *Network) rightActivate(jn *JoinNode, f *Fact) {
	for tok := range jn.left.tokens {
		if joinPass(jn.tests, tok, f) {
			n.newToken(tok, f, jn.out)
		}
	}
}

// leftActivate：新 Token 进入 Beta 记忆后，与右侧 Alpha 记忆中的事实做 Join。
func (n *Network) leftActivate(jn *JoinNode, tok *Token) {
	for _, f := range jn.right.memory {
		if joinPass(jn.tests, tok, f) {
			n.newToken(tok, f, jn.out)
		}
	}
}

// newToken 在 mem 中建立 Token，并继续向下游传播；若 mem 挂有规则则激活。
func (n *Network) newToken(parent *Token, fact *Fact, mem *BetaMemory) *Token {
	t := &Token{
		parent:   parent,
		fact:     fact,
		memory:   mem,
		children: make(map[*Token]struct{}),
	}
	if parent != nil {
		parent.children[t] = struct{}{}
	}
	mem.tokens[t] = struct{}{}
	if fact != nil {
		n.factTokens[fact.ID] = append(n.factTokens[fact.ID], t)
	}
	if mem.production != nil {
		act := &Activation{Rule: mem.production.rule.Name, Token: t}
		t.activation = act
		n.agenda = append(n.agenda, act)
		n.agendaIdx[t] = act
	}
	for _, jn := range mem.joins {
		n.leftActivate(jn, t)
	}
	return t
}

func (n *Network) removeActivation(act *Activation) {
	delete(n.agendaIdx, act.Token)
	for i, a := range n.agenda {
		if a == act {
			n.agenda = append(n.agenda[:i], n.agenda[i+1:]...)
			return
		}
	}
}

// ConflictSet 返回当前冲突集（待激活条目）的副本。
func (n *Network) ConflictSet() []*Activation {
	return append([]*Activation(nil), n.agenda...)
}

func joinPass(tests []JoinTest, tok *Token, f *Fact) bool {
	if len(tests) == 0 {
		return true
	}
	facts := tok.Facts()
	for _, jt := range tests {
		if jt.LeftIndex < 0 || jt.LeftIndex >= len(facts) {
			return false
		}
		lv, ok := facts[jt.LeftIndex].Fields[jt.LeftField]
		if !ok {
			return false
		}
		rv, ok := f.Fields[jt.RightField]
		if !ok {
			return false
		}
		if !valuesEqual(lv, rv) {
			return false
		}
	}
	return true
}

func valuesEqual(a, b interface{}) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	fa, oka := toFloat(a)
	fb, okb := toFloat(b)
	return oka && okb && fa == fb
}

func compareOrdered(a, b interface{}) (int, bool) {
	if fa, oka := toFloat(a); oka {
		if fb, okb := toFloat(b); okb {
			switch {
			case fa < fb:
				return -1, true
			case fa > fb:
				return 1, true
			default:
				return 0, true
			}
		}
	}
	sa, oka := a.(string)
	sb, okb := b.(string)
	if oka && okb {
		switch {
		case sa < sb:
			return -1, true
		case sa > sb:
			return 1, true
		default:
			return 0, true
		}
	}
	return 0, false
}

func toFloat(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}
