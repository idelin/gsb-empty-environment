package netsec

import "fmt"

// MaxVariables 是精化组件支持的布尔程序变量数上限。
// 具体状态空间为 2^n，n=12 时最多 4096 个取值，可直接枚举。
const MaxVariables = 12

// Expr 是布尔程序上的表达式，仅包含变量、常量与与/或/非。
type Expr interface {
	fmt.Stringer
	eval(Valuation) bool
}

// Valuation 是对全部布尔变量的一次具体取值，第 i 位对应变量 i。
type Valuation uint32

// Var 表示对某个变量的引用，N 为变量在 Program.Vars 中的下标。
type Var struct{ N int }

// Not 表示逻辑非。
type Not struct{ Inner Expr }

// And 表示逻辑与。
type And struct{ L, R Expr }

// Or 表示逻辑或。
type Or struct{ L, R Expr }

// Const 表示布尔常量。
type Const bool

// V 构造变量引用。
func V(n int) Expr { return Var{N: n} }

// NOT 构造逻辑非。
func NOT(e Expr) Expr { return Not{Inner: e} }

// AND 构造逻辑与。
func AND(a, b Expr) Expr { return And{L: a, R: b} }

// OR 构造逻辑或。
func OR(a, b Expr) Expr { return Or{L: a, R: b} }

// TRUE 与 FALSE 是布尔常量表达式。
const (
	TRUE  = Const(true)
	FALSE = Const(false)
)

func (v Var) eval(s Valuation) bool { return s&(1<<uint(v.N)) != 0 }
func (n Not) eval(s Valuation) bool { return !n.Inner.eval(s) }
func (a And) eval(s Valuation) bool { return a.L.eval(s) && a.R.eval(s) }
func (o Or) eval(s Valuation) bool  { return o.L.eval(s) || o.R.eval(s) }
func (c Const) eval(Valuation) bool { return bool(c) }

func (v Var) String() string { return fmt.Sprintf("v%d", v.N) }
func (n Not) String() string { return "!(" + n.Inner.String() + ")" }
func (a And) String() string { return "(" + a.L.String() + " & " + a.R.String() + ")" }
func (o Or) String() string  { return "(" + o.L.String() + " | " + o.R.String() + ")" }
func (c Const) String() string {
	if bool(c) {
		return "true"
	}
	return "false"
}

// Stmt 是控制流图节点上承载的语句。
type Stmt interface {
	fmt.Stringer
}

// Assign 把变量 N 赋值为表达式 RHS（确定性赋值）。
type Assign struct {
	N   int
	RHS Expr
}

// Havoc 表示对变量 N 的非确定性赋值（抽象建模产生的自由输入）。
type Havoc struct{ N int }

// Assert 断言 Cond 成立；失败时控制流进入错误汇聚节点 ErrNode。
type Assert struct{ Cond Expr }

func (a Assign) String() string { return fmt.Sprintf("v%d := %s", a.N, a.RHS) }
func (h Havoc) String() string  { return fmt.Sprintf("havoc v%d", h.N) }
func (a Assert) String() string { return "assert " + a.Cond.String() }

// ErrNode 是错误汇聚哨兵节点的固定下标。
const ErrNode = -1

// Node 是控制流图中的一个节点。
// Stmt 为 nil 表示纯连接节点；Next 给出后继下标；ErrTarget 为 true 的
// Assert 节点在断言失败时跳转到 ErrNode。
type Node struct {
	Stmt      Stmt
	Next      []int
	ErrTarget bool
}

// Program 是一个有限布尔程序：具名布尔变量、控制流图、入口节点与入口取值。
type Program struct {
	Vars  []string
	Nodes []Node
	Entry int
	// Init 给出入口节点上允许的具体取值集合；为 nil 时表示全部取值。
	Init []Valuation
}

// Validate 检查程序结构，包括变量数上限、变量引用与跳转下标合法性。
func (p *Program) Validate() error {
	n := len(p.Vars)
	if n == 0 {
		return fmt.Errorf("refine: program has no variables")
	}
	if n > MaxVariables {
		return fmt.Errorf("refine: program has %d variables, limit is %d", n, MaxVariables)
	}
	if p.Entry < 0 || p.Entry >= len(p.Nodes) {
		return fmt.Errorf("refine: entry node %d out of range", p.Entry)
	}
	for i, node := range p.Nodes {
		for _, t := range node.Next {
			if t < 0 || t >= len(p.Nodes) {
				return fmt.Errorf("refine: node %d jumps to out-of-range node %d", i, t)
			}
		}
	}
	for _, s := range p.Init {
		if s >= Valuation(1<<uint(n)) {
			return fmt.Errorf("refine: initial valuation %d exceeds %d-variable state space", s, n)
		}
	}
	return p.walkExprs(func(e Expr) error {
		if v, ok := e.(Var); ok && (v.N < 0 || v.N >= n) {
			return fmt.Errorf("refine: variable index %d out of range (have %d vars)", v.N, n)
		}
		return nil
	})
}

func (p *Program) walkExprs(fn func(Expr) error) error {
	var walk func(Expr) error
	walk = func(e Expr) error {
		if err := fn(e); err != nil {
			return err
		}
		switch t := e.(type) {
		case Not:
			return walk(t.Inner)
		case And:
			if err := walk(t.L); err != nil {
				return err
			}
			return walk(t.R)
		case Or:
			if err := walk(t.L); err != nil {
				return err
			}
			return walk(t.R)
		}
		return nil
	}
	for _, node := range p.Nodes {
		switch s := node.Stmt.(type) {
		case Assign:
			if s.N < 0 || s.N >= len(p.Vars) {
				return fmt.Errorf("refine: assignment to variable %d out of range", s.N)
			}
			if err := walk(s.RHS); err != nil {
				return err
			}
		case Havoc:
			if s.N < 0 || s.N >= len(p.Vars) {
				return fmt.Errorf("refine: havoc on variable %d out of range", s.N)
			}
		case Assert:
			if err := walk(s.Cond); err != nil {
				return err
			}
		case nil:
		default:
			return fmt.Errorf("refine: unknown statement type %T", node.Stmt)
		}
	}
	return nil
}

// universe 返回该程序全部具体取值（至多 2^MaxVariables 个）。
func (p *Program) universe() []Valuation {
	return enumerate(1 << uint(len(p.Vars)))
}

func enumerate(size int) []Valuation {
	out := make([]Valuation, 0, size)
	for i := 0; i < size; i++ {
		out = append(out, Valuation(i))
	}
	return out
}

// concreteSuccs 计算单条具体取值经过节点后的后继取值与是否进入错误节点。
func concreteSuccs(node Node, s Valuation) (next []Valuation, fails bool) {
	switch st := node.Stmt.(type) {
	case nil:
		return []Valuation{s}, false
	case Assign:
		ns := s
		mask := Valuation(1) << uint(st.N)
		if st.RHS.eval(s) {
			ns |= mask
		} else {
			ns &^= mask
		}
		return []Valuation{ns}, false
	case Havoc:
		mask := Valuation(1) << uint(st.N)
		return []Valuation{s &^ mask, s | mask}, false
	case Assert:
		if st.Cond.eval(s) {
			return []Valuation{s}, false
		}
		return []Valuation{s}, true
	default:
		panic("refine: unreachable statement type")
	}
}
