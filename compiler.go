package netsec

import "sort"

// 规则节点类型。
const (
	opFact = "fact"
	opAnd  = "and"
	opOr   = "or"
	opNot  = "not"
)

// Rule 是编译后的三值规则 DAG 节点。规则只能由 Compiler 构造，
// 结构相同的子式共享同一个 Rule 实例。
type Rule struct {
	op       string
	factID   string
	children []*Rule
	key      string
	compiler *Compiler
}

// Op 返回节点类型："fact"、"and"、"or" 或 "not"。
func (r *Rule) Op() string { return r.op }

// FactID 返回原子事实节点的 ID，非原子节点返回空串。
func (r *Rule) FactID() string { return r.factID }

// Children 返回复合节点的子节点，原子节点返回 nil。
func (r *Rule) Children() []*Rule { return r.children }

// MaxFacts 是单个 Compiler 可声明的不同原子事实上限。
const MaxFacts = 20

// Compiler 构造并持有规则 DAG，按结构归并等价子式。
type Compiler struct {
	nodes map[string]*Rule
	facts map[string]*Rule
}

// NewCompiler 创建空编译器。
func NewCompiler() *Compiler {
	return &Compiler{
		nodes: make(map[string]*Rule),
		facts: make(map[string]*Rule),
	}
}

// Fact 返回 ID 为 id 的原子事实节点；同一 ID 始终返回同一节点。
// 不同事实超过 MaxFacts 个时 panic。
func (c *Compiler) Fact(id string) *Rule {
	if id == "" {
		panic("netsec: fact id must not be empty")
	}
	if r, ok := c.facts[id]; ok {
		return r
	}
	if len(c.facts) >= MaxFacts {
		panic("netsec: more than 20 distinct facts")
	}
	r := &Rule{
		op:       opFact,
		factID:   id,
		key:      factKey(id),
		compiler: c,
	}
	c.facts[id] = r
	c.nodes[r.key] = r
	return r
}

// And 编译合取节点，children 至少一个且必须来自同一 Compiler，否则 panic。
func (c *Compiler) And(children ...*Rule) *Rule {
	return c.combine(opAnd, children)
}

// Or 编译析取节点，children 至少一个且必须来自同一 Compiler，否则 panic。
func (c *Compiler) Or(children ...*Rule) *Rule {
	return c.combine(opOr, children)
}

// Not 编译否定节点，child 必须来自同一 Compiler，否则 panic。
func (c *Compiler) Not(child *Rule) *Rule {
	c.checkOwned(child)
	key := "N:" + child.key
	if r, ok := c.nodes[key]; ok {
		return r
	}
	r := &Rule{
		op:       opNot,
		children: []*Rule{child},
		key:      key,
		compiler: c,
	}
	c.nodes[key] = r
	return r
}

// Facts 返回已声明的全部原子事实 ID，按字典序排列。
func (c *Compiler) Facts() []string {
	ids := make([]string, 0, len(c.facts))
	for id := range c.facts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// FactCount 返回已声明的不同原子事实数量。
func (c *Compiler) FactCount() int { return len(c.facts) }

func (c *Compiler) combine(op string, children []*Rule) *Rule {
	if len(children) == 0 {
		panic("netsec: " + op + " requires at least one child")
	}
	unique := make([]*Rule, 0, len(children))
	seen := make(map[string]bool, len(children))
	for _, child := range children {
		c.checkOwned(child)
		if seen[child.key] {
			continue
		}
		seen[child.key] = true
		unique = append(unique, child)
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i].key < unique[j].key })

	key := opPrefix(op)
	for _, child := range unique {
		key += "(" + child.key + ")"
	}
	r := &Rule{
		op:       op,
		children: unique,
		key:      key,
		compiler: c,
	}
	// 首次写入式归并：等价结构（例如 AND 去重后与既有单/双子节点同键）
	// 必须共享同一节点，维持“同构即同节点”的不变量。
	if existing, ok := c.nodes[key]; ok {
		return existing
	}
	c.nodes[key] = r
	return r
}

func (c *Compiler) checkOwned(child *Rule) {
	if child == nil {
		panic("netsec: nil rule child")
	}
	if child.compiler != c {
		panic("netsec: rule child belongs to a different Compiler")
	}
}

func opPrefix(op string) string {
	switch op {
	case opAnd:
		return "A:"
	case opOr:
		return "O:"
	default:
		return "?"
	}
}

// factKey 用长度前缀保证与复合节点键不会碰撞。
func factKey(id string) string {
	return "F:" + itoa(len(id)) + ":" + id
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
