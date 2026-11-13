package netsec

// Effect 是一条策略的决定方向。
type Effect int

const (
	// Permit 表示放行策略。
	Permit Effect = iota + 1
	// Forbid 表示禁止策略。Forbid 在组合决定时优先于 Permit。
	Forbid
)

func (e Effect) String() string {
	switch e {
	case Permit:
		return "permit"
	case Forbid:
		return "forbid"
	default:
		return "unknown"
	}
}

// Expr 是 when 条件中的一个表达式节点。
type Expr interface {
	exprNode()
}

// VarExpr 引用请求中的 principal、action 或 resource。
type VarExpr struct {
	Name string // principal | action | resource
}

// LiteralExpr 是字符串、数字或布尔字面量。
type LiteralExpr struct {
	Value any
}

// SetExpr 是集合字面量，例如 ["read", "view"]，作为 in 的右侧。
type SetExpr struct {
	Elements []Expr
}

// AttrExpr 是属性访问，例如 principal.group 或 resource.meta.owner。
type AttrExpr struct {
	Target Expr
	Name   string
}

// HasExpr 对应 has 运算，例如 principal has "group"。
type HasExpr struct {
	Target Expr
	Attr   Expr // 求值结果必须是字符串
}

// InExpr 对应 in 运算，判断左侧值是否出现在右侧集合中。
type InExpr struct {
	Item  Expr
	Scope Expr
}

// BinaryExpr 对应 ==、!=、<、>、&&、||。
type BinaryExpr struct {
	Op       string
	LHS, RHS Expr
}

func (*VarExpr) exprNode()     {}
func (*LiteralExpr) exprNode() {}
func (*SetExpr) exprNode()     {}
func (*AttrExpr) exprNode()    {}
func (*HasExpr) exprNode()     {}
func (*InExpr) exprNode()      {}
func (*BinaryExpr) exprNode()  {}

// Policy 是一条 permit/forbid 策略。
//
// 本题子集里 principal/action/resource 三元组固定出现在策略头部，
// 具体匹配逻辑全部写在 When 条件中；When 为 nil 表示无条件恒真。
type Policy struct {
	ID     string
	Effect Effect
	When   Expr
}

// ParsePolicies 解析 Cedar 子集文本策略，语法见包文档。
// 未显式标注 id 的策略按出现顺序自动命名为 policy-1、policy-2 ……
func ParsePolicies(src string) ([]*Policy, error) {
	p, err := newParser(src)
	if err != nil {
		return nil, err
	}
	return p.parsePolicies()
}
