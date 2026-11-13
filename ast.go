package netsec

import "time"

// MatchOp 表示标签匹配器的运算符。
type MatchOp int

const (
	OpEqual    MatchOp = iota // =
	OpNotEqual                // !=
	OpRegex                   // =~
	OpNotRegex                // !~
)

// Matcher 是向量选择器上的单个标签匹配器。
type Matcher struct {
	Label string
	Op    MatchOp
	Value string
}

// Node 是简化查询树中所有节点共同实现的接口。
type Node interface {
	node()
}

// VectorSelector 表示瞬时向量选择器，例如 http_requests_total{job="api"}。
// Name 为空表示缺省指标名的选择器，例如 {tenant="x"}。
type VectorSelector struct {
	Name     string
	Matchers []Matcher
}

// RangeSelector 表示范围向量选择器，例如 http_requests_total[5m]。
type RangeSelector struct {
	Vector *VectorSelector
	Range  time.Duration
	Offset time.Duration
}

// Subquery 表示子查询，例如 rate(http_requests_total[5m])[1h:1m]。
type Subquery struct {
	Expr   Node
	Range  time.Duration
	Step   time.Duration
	Offset time.Duration
}

// NumberLiteral 表示数字字面量，例如 0.9。
type NumberLiteral struct {
	Value float64
}

// StringLiteral 表示字符串字面量，例如 label_replace 的目标标签参数。
type StringLiteral struct {
	Value string
}

// FunctionCall 表示函数调用，Args 为按顺序排列的参数节点。
type FunctionCall struct {
	Name string
	Args []Node
}

// Aggregation 表示聚合表达式。Without 为 false 时 Grouping 是 by 列表，
// 为 true 时 Grouping 是 without 列表。
type Aggregation struct {
	Op       string
	Expr     Node
	Grouping []string
	Without  bool
}

// BinaryExpr 表示二元运算。On、Ignoring 与 GroupLeft 保存对应的标签列表，
// 列表为空表示该修饰符未出现。
type BinaryExpr struct {
	Op        string
	LHS       Node
	RHS       Node
	On        []string
	Ignoring  []string
	GroupLeft []string
}

func (*VectorSelector) node() {}
func (*RangeSelector) node()  {}
func (*Subquery) node()       {}
func (*NumberLiteral) node()  {}
func (*StringLiteral) node()  {}
func (*FunctionCall) node()   {}
func (*Aggregation) node()    {}
func (*BinaryExpr) node()     {}
