package netsec

import "errors"

// Label 是最多 16 个秘密来源的集合,第 i 位表示来源 i。
type Label uint16

const MaxSources = 16

func LabelOf(src int) Label {
	if src < 0 || src >= MaxSources {
		panic("source id out of range")
	}
	return 1 << uint(src)
}

func (l Label) Empty() bool      { return l == 0 }
func (l Label) Has(src int) bool { return l&LabelOf(src) != 0 }

// value 携带运行时标签:data 为显式值依赖,pc 为条件控制依赖。
// 两者分离是为了让降密只能清除显式依赖,无法洗掉外围分支的控制依赖。
type value struct {
	n    int64
	data Label
	pc   Label
}

func (v value) full() Label { return v.data | v.pc }

// Expr 是表达式 AST。攻击者可任意构造 AST,但无法伪造 Registry 签发的授权。
type Expr interface{ exprNode() }

type Const struct{ V int64 }
type Source struct{ ID int } // 读取秘密来源 ID(0..15)
type Add struct{ L, R Expr }
type If struct{ Cond, Then, Else Expr }
type Let struct {
	Name string
	Val  Expr
	Body Expr
}
type Var struct{ Name string }

// Output 公开输出一个值;仅当值的完整标签为空时才允许。
type Output struct{ Val Expr }

// Declassify 是唯一降密操作。Auth 必须由 Registry 针对当前请求与
// NodeID 签发且只能使用一次;只能移除 Auth.Allowed 允许的来源,
// 且只作用于显式数据依赖,不影响控制依赖。
type Declassify struct {
	NodeID string
	Auth   *Authorization
	Val    Expr
}

func (Const) exprNode()      {}
func (Source) exprNode()     {}
func (Add) exprNode()        {}
func (If) exprNode()         {}
func (Let) exprNode()        {}
func (Var) exprNode()        {}
func (Output) exprNode()     {}
func (Declassify) exprNode() {}

var (
	ErrNonPublicOutput = errors.New("ifc: output label is not empty")
	ErrUndefinedVar    = errors.New("ifc: undefined variable")
	ErrBadSource       = errors.New("ifc: source id out of range")
)

// Evaluator 在单个请求上下文中执行表达式程序。
type Evaluator struct {
	Reg       *Registry
	Audit     *AuditLog
	RequestID string

	// SourceValues 是各秘密来源的当前值;来源本身秘密,
	// 读取后其标签随值一起传播。
	SourceValues [MaxSources]int64

	env      map[string]value
	out      []int64
	consumed []*Authorization // 本次求值已消费的授权,失败时回滚
}

func (e *Evaluator) Run(prog []Expr) ([]int64, error) {
	e.env = make(map[string]value)
	e.out = nil
	e.consumed = nil
	for _, ex := range prog {
		if _, err := e.eval(ex, 0); err != nil {
			// 失败回滚:不产生部分公开输出,已消费授权全部归还。
			e.Reg.Restore(e.consumed)
			e.Audit.Log(Event{Kind: EvRollback, Request: e.RequestID, Detail: err.Error()})
			return nil, err
		}
	}
	out := e.out
	e.out = nil
	return out, nil
}

func (e *Evaluator) eval(ex Expr, pc Label) (value, error) {
	switch n := ex.(type) {
	case Const:
		return value{n: n.V, pc: pc}, nil
	case Source:
		if n.ID < 0 || n.ID >= MaxSources {
			return value{}, ErrBadSource
		}
		return value{n: e.SourceValues[n.ID], data: LabelOf(n.ID), pc: pc}, nil
	case Add:
		l, err := e.eval(n.L, pc)
		if err != nil {
			return value{}, err
		}
		r, err := e.eval(n.R, pc)
		if err != nil {
			return value{}, err
		}
		return value{n: l.n + r.n, data: l.data | r.data, pc: l.pc | r.pc}, nil
	case If:
		c, err := e.eval(n.Cond, pc)
		if err != nil {
			return value{}, err
		}
		// 条件标签并入分支内部的控制依赖。
		inner := pc | c.full()
		if c.n != 0 {
			return e.eval(n.Then, inner)
		}
		return e.eval(n.Else, inner)
	case Let:
		v, err := e.eval(n.Val, pc)
		if err != nil {
			return value{}, err
		}
		old, ok := e.env[n.Name]
		e.env[n.Name] = v
		res, err := e.eval(n.Body, pc)
		if ok {
			e.env[n.Name] = old
		} else {
			delete(e.env, n.Name)
		}
		return res, err
	case Var:
		v, ok := e.env[n.Name]
		if !ok {
			return value{}, ErrUndefinedVar
		}
		v.pc |= pc
		return v, nil
	case Declassify:
		v, err := e.eval(n.Val, pc)
		if err != nil {
			return value{}, err
		}
		if err := e.Reg.Consume(n.Auth, e.RequestID, n.NodeID); err != nil {
			return value{}, err
		}
		e.consumed = append(e.consumed, n.Auth)
		e.Audit.Log(Event{Kind: EvDeclassify, Request: e.RequestID, Node: n.NodeID, Removed: v.data & n.Auth.Allowed})
		// 只清除显式数据依赖中被授权的来源;pc 控制依赖原样保留。
		v.data &^= n.Auth.Allowed
		return v, nil
	case Output:
		v, err := e.eval(n.Val, pc)
		if err != nil {
			return value{}, err
		}
		if !v.full().Empty() {
			return value{}, ErrNonPublicOutput
		}
		e.out = append(e.out, v.n)
		e.Audit.Log(Event{Kind: EvOutput, Request: e.RequestID})
		return v, nil
	default:
		return value{}, errors.New("ifc: unknown expression")
	}
}
