// Package rete 实现增量 RETE 模式匹配算法核心。
//
// 网络分为两层：
//   - Alpha 网络：对单条事实做常量过滤（类型 + 属性常量测试），
//     命中的事实进入 AlphaMemory。
//   - Beta 网络：在 AlphaMemory 与父级 BetaMemory 之间做跨事实 Join，
//     产生 Token 沿网络向下传播，末端 Production 节点把匹配结果
//     物化为冲突集（Agenda）中的待激活条目。
//
// 新增事实时沿网络增量激活；撤销事实时不重新扫描事实库，
// 而是通过负 Token（negToken）沿网络精确级联撤回受影响的 Token
// 以及对应冲突集中的待激活条目。
package rete

import (
	"fmt"
	"sort"
	"strings"
)

// Fact 是工作内存中的一条事实。
type Fact struct {
	ID    int64
	Type  string
	Attrs map[string]any
}

func (f *Fact) String() string {
	keys := make([]string, 0, len(f.Attrs))
	for k := range f.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s#%d(", f.Type, f.ID)
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%s=%v", k, f.Attrs[k])
	}
	sb.WriteString(")")
	return sb.String()
}

// Condition 描述规则中的一个模式（对应一个 Alpha 节点 + 一个 Beta 节点）。
type Condition struct {
	Type   string            // 事实类型
	Consts map[string]any    // 常量过滤：属性 -> 期望常量（Alpha 测试）
	Binds  map[string]string // 变量绑定：属性 -> 变量名（如 "?user"）
}

// ConstTest 是 Alpha 网络上的一次属性常量比较。
type ConstTest struct {
	Attr  string
	Value any
}

// AlphaNode 常量过滤节点，命中测试的事实驻留在其 Memory 中。
type AlphaNode struct {
	Type     string
	Tests    []ConstTest
	Memory   []*Fact
	children []*BetaNode
}

func (a *AlphaNode) matches(f *Fact) bool {
	if f.Type != a.Type {
		return false
	}
	for _, t := range a.Tests {
		if f.Attrs[t.Attr] != t.Value {
			return false
		}
	}
	return true
}

// JoinTest 是 Beta 节点上的跨事实 Join 测试：
// 比较右侧新事实的属性与父 Token 中已绑定变量的值。
type JoinTest struct {
	AlphaAttr string // 右侧（Alpha）事实的属性名
	Var       string // 父 Token 中已绑定的变量名
}

// Token 是 Beta 网络中流动的部分匹配结果。
type Token struct {
	parent   *Token
	fact     *Fact // 根 Token 为 nil
	node     *BetaNode
	bindings map[string]any // 变量名 -> 值
}

// BetaNode 将父级 BetaMemory 的 Token 与 AlphaMemory 的事实做 Join。
type BetaNode struct {
	net        *Network
	parent     *BetaNode // 根节点为 nil
	alpha      *AlphaNode
	joinTests  []JoinTest
	binds      map[string]string // 本节点新绑定的变量：变量名 -> Alpha 属性
	tokens     []*Token          // BetaMemory
	children   []*BetaNode
	production *Production
}

// Production 规则末端节点，匹配成功即在冲突集中产生待激活条目。
type Production struct {
	Name string
	node *BetaNode
}

// Activation 冲突集（Agenda）中的一条待激活条目。
type Activation struct {
	Rule  string
	Token *Token
}

func (a *Activation) String() string {
	keys := make([]string, 0, len(a.Token.bindings))
	for k := range a.Token.bindings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s]", a.Rule)
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%s=%v", k, a.Token.bindings[k])
	}
	return sb.String()
}

// negToken 负 Token：撤销事实时沿 Beta 网络传播的撤回标记，
// 精确命中并级联清除引用该事实/父 Token 的下游 Token。
type negToken struct {
	fact   *Fact  // 右侧（Alpha 侧）被撤销的事实
	parent *Token // 左侧被撤回的父 Token
}

// Network 是一张 RETE 网络。
type Network struct {
	alphas    map[string]*AlphaNode
	alphaList []*AlphaNode // 保证确定性的传播顺序
	betas     []*BetaNode
	root      *BetaNode
	rootToken *Token
	facts     map[int64]*Fact
	factList  []*Fact // 保证确定性的回填顺序
	agenda    []*Activation
	nextID    int64
}

// NewNetwork 创建一张空网络，含一个持有空 Token 的根节点。
func NewNetwork() *Network {
	n := &Network{
		alphas: make(map[string]*AlphaNode),
		facts:  make(map[int64]*Fact),
	}
	n.root = &BetaNode{net: n}
	n.rootToken = &Token{node: n.root, bindings: map[string]any{}}
	n.root.tokens = []*Token{n.rootToken}
	n.agenda = nil
	return n
}

// alphaKey 让相同类型 + 相同常量测试的条件共享同一个 Alpha 节点。
func alphaKey(c Condition) string {
	keys := make([]string, 0, len(c.Consts))
	for k := range c.Consts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(c.Type)
	for _, k := range keys {
		fmt.Fprintf(&sb, "|%s=%v", k, c.Consts[k])
	}
	return sb.String()
}

func (n *Network) alphaFor(c Condition) *AlphaNode {
	key := alphaKey(c)
	if a, ok := n.alphas[key]; ok {
		return a
	}
	a := &AlphaNode{Type: c.Type}
	keys := make([]string, 0, len(c.Consts))
	for k := range c.Consts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a.Tests = append(a.Tests, ConstTest{Attr: k, Value: c.Consts[k]})
	}
	// 规则后于事实加入时，用存量事实一次性回填 AlphaMemory。
	for _, f := range n.factList {
		if a.matches(f) {
			a.Memory = append(a.Memory, f)
		}
	}
	n.alphas[key] = a
	n.alphaList = append(n.alphaList, a)
	return a
}

// AddRule 按条件顺序构建（或复用）Alpha/Beta 节点，并在末端挂接规则。
// 已存在的事实会立即沿新建子网增量传播（seed）。
func (n *Network) AddRule(name string, conds ...Condition) *Production {
	parent := n.root
	bound := map[string]bool{}
	for _, c := range conds {
		alpha := n.alphaFor(c)
		var joins []JoinTest
		binds := map[string]string{}
		attrs := make([]string, 0, len(c.Binds))
		for attr := range c.Binds {
			attrs = append(attrs, attr)
		}
		sort.Strings(attrs)
		for _, attr := range attrs {
			v := c.Binds[attr]
			if bound[v] {
				joins = append(joins, JoinTest{AlphaAttr: attr, Var: v})
			} else {
				binds[v] = attr
				bound[v] = true
			}
		}
		node := &BetaNode{
			net:       n,
			parent:    parent,
			alpha:     alpha,
			joinTests: joins,
			binds:     binds,
		}
		parent.children = append(parent.children, node)
		alpha.children = append(alpha.children, node)
		n.betas = append(n.betas, node)
		// 用父级已有 Token 做左侧激活，从 AlphaMemory 中增量补齐匹配。
		for _, t := range parent.tokens {
			node.leftActivate(t)
		}
		parent = node
	}
	p := &Production{Name: name, node: parent}
	parent.production = p
	for _, t := range parent.tokens {
		n.addActivation(p, t)
	}
	return p
}

// AddFact 向工作内存插入事实，并沿 Alpha 网络增量传播。
func (n *Network) AddFact(typ string, attrs map[string]any) *Fact {
	n.nextID++
	f := &Fact{ID: n.nextID, Type: typ, Attrs: attrs}
	n.facts[f.ID] = f
	n.factList = append(n.factList, f)
	for _, a := range n.alphaList {
		if a.matches(f) {
			a.Memory = append(a.Memory, f)
			for _, b := range a.children {
				b.rightActivate(f)
			}
		}
	}
	return f
}

// RetractFact 撤销事实：从命中的 AlphaMemory 中移除，并向 Beta 网络
// 注入负 Token，精确级联撤回相关 Token 与冲突集条目，不做全量重扫。
func (n *Network) RetractFact(id int64) bool {
	f, ok := n.facts[id]
	if !ok {
		return false
	}
	delete(n.facts, id)
	for i, m := range n.factList {
		if m == f {
			n.factList = append(n.factList[:i], n.factList[i+1:]...)
			break
		}
	}
	for _, a := range n.alphaList {
		idx := -1
		for i, m := range a.Memory {
			if m == f {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		a.Memory = append(a.Memory[:idx], a.Memory[idx+1:]...)
		for _, b := range a.children {
			b.propagateNegRight(negToken{fact: f})
		}
	}
	return true
}

func (b *BetaNode) passJoins(t *Token, f *Fact) bool {
	for _, j := range b.joinTests {
		if f.Attrs[j.AlphaAttr] != t.bindings[j.Var] {
			return false
		}
	}
	return true
}

// leftActivate 父 Token 到达：与 AlphaMemory 中所有事实尝试 Join。
func (b *BetaNode) leftActivate(t *Token) {
	for _, f := range b.alpha.Memory {
		if b.passJoins(t, f) {
			b.makeToken(t, f)
		}
	}
}

// rightActivate 新事实到达：与父级 BetaMemory 中所有 Token 尝试 Join。
func (b *BetaNode) rightActivate(f *Fact) {
	for _, t := range b.parent.tokens {
		if b.passJoins(t, f) {
			b.makeToken(t, f)
		}
	}
}

func (b *BetaNode) makeToken(parent *Token, f *Fact) {
	bindings := make(map[string]any, len(parent.bindings)+len(b.binds))
	for k, v := range parent.bindings {
		bindings[k] = v
	}
	for v, attr := range b.binds {
		bindings[v] = f.Attrs[attr]
	}
	tok := &Token{parent: parent, fact: f, node: b, bindings: bindings}
	b.tokens = append(b.tokens, tok)
	if b.production != nil {
		b.net.addActivation(b.production, tok)
	}
	for _, c := range b.children {
		c.leftActivate(tok)
	}
}

// propagateNegRight 处理 Alpha 侧注入的负 Token：
// 移除 BetaMemory 中引用该事实的 Token，并向下级联。
func (b *BetaNode) propagateNegRight(nt negToken) {
	var kept, removed []*Token
	for _, t := range b.tokens {
		if t.fact == nt.fact {
			removed = append(removed, t)
		} else {
			kept = append(kept, t)
		}
	}
	if len(removed) == 0 {
		return
	}
	b.tokens = kept
	for _, t := range removed {
		b.cascadeRetract(t)
	}
}

// propagateNegLeft 处理左侧（父 Token 被撤回）级联下来的负 Token。
func (b *BetaNode) propagateNegLeft(nt negToken) {
	var kept, removed []*Token
	for _, t := range b.tokens {
		if t.parent == nt.parent {
			removed = append(removed, t)
		} else {
			kept = append(kept, t)
		}
	}
	if len(removed) == 0 {
		return
	}
	b.tokens = kept
	for _, t := range removed {
		b.cascadeRetract(t)
	}
}

// cascadeRetract 撤回一个 Token：摘除其冲突集条目，并向子节点传播负 Token。
func (b *BetaNode) cascadeRetract(t *Token) {
	if b.production != nil {
		b.net.removeActivation(b.production, t)
	}
	for _, c := range b.children {
		c.propagateNegLeft(negToken{parent: t})
	}
}

func (n *Network) addActivation(p *Production, t *Token) {
	n.agenda = append(n.agenda, &Activation{Rule: p.Name, Token: t})
}

func (n *Network) removeActivation(p *Production, t *Token) {
	for i, a := range n.agenda {
		if a.Token == t && a.Rule == p.Name {
			n.agenda = append(n.agenda[:i], n.agenda[i+1:]...)
			return
		}
	}
}

// Agenda 返回当前冲突集的快照。
func (n *Network) Agenda() []*Activation {
	out := make([]*Activation, len(n.agenda))
	copy(out, n.agenda)
	return out
}

// TokenCount 返回所有 Beta 节点（不含根）当前驻留的 Token 总数，
// 主要用于测试观察负 Token 级联是否清理干净。
func (n *Network) TokenCount() int {
	total := 0
	for _, b := range n.betas {
		total += len(b.tokens)
	}
	return total
}
