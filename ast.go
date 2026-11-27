package scim

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Operator 是 RFC 7644 §4.1.1 定义的属性比较操作符。
type Operator string

const (
	OpEq Operator = "eq"
	OpNe Operator = "ne"
	OpCo Operator = "co"
	OpSw Operator = "sw"
	OpEw Operator = "ew"
	OpPr Operator = "pr"
	OpGt Operator = "gt"
	OpGe Operator = "ge"
	OpLt Operator = "lt"
	OpLe Operator = "le"
)

// IsBinary 报告操作符是否需要右侧值。
func (op Operator) IsBinary() bool {
	switch op {
	case OpEq, OpNe, OpCo, OpSw, OpEw, OpGt, OpGe, OpLt, OpLe:
		return true
	default:
		return false
	}
}

// LogicalOp 是 and / or 逻辑连接符。
type LogicalOp string

const (
	LogicalAnd LogicalOp = "and"
	LogicalOr  LogicalOp = "or"
)

// LiteralKind 标识过滤字面量的 JSON 类型。
type LiteralKind int

const (
	LitString LiteralKind = iota
	LitNumber
	LitBool
	LitNull
)

// Literal 是比较表达式右侧的 JSON 标量值。
type Literal struct {
	Kind  LiteralKind
	Value any
	Raw   string
}

// StringLiteral 构造字符串字面量（调用方输入永远不会被拼进过滤语法，
// 而是作为 AST 数据保存并在序列化时正确转义）。
func StringLiteral(v string) *Literal {
	raw, _ := json.Marshal(v)
	return &Literal{Kind: LitString, Value: v, Raw: string(raw)}
}

// QuoteValue 把任意字符串安全编码为 SCIM/JSON 字符串字面量（含两侧引号）。
// 宿主若必须自行拼装片段，也应只通过该函数输出值，杜绝引号逃逸。
func QuoteValue(v string) string {
	return StringLiteral(v).String()
}

func boolLiteral(v bool) *Literal {
	return &Literal{Kind: LitBool, Value: v, Raw: strconv.FormatBool(v)}
}

func nullLiteral() *Literal {
	return &Literal{Kind: LitNull, Value: nil, Raw: "null"}
}

func numberLiteral(raw string) (*Literal, error) {
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, err
	}
	return &Literal{Kind: LitNumber, Value: f, Raw: raw}, nil
}

// String 返回可直接嵌入 SCIM filter 的安全表示。
func (l *Literal) String() string {
	if l == nil {
		return "null"
	}
	if l.Raw != "" {
		return l.Raw
	}
	switch l.Kind {
	case LitString:
		b, _ := json.Marshal(l.Value)
		return string(b)
	case LitBool:
		return strconv.FormatBool(l.Value.(bool))
	default:
		return "null"
	}
}

// AttrPath 表示 SCIM 属性路径，可带 schema URN 前缀与子属性。
// 例如 name.familyName、emails.value、
// urn:ietf:params:scim:schemas:core:2.0:User:userName。
type AttrPath struct {
	// Raw 是词法层面的原始路径文本。
	Raw string
	// Schema 是最后一个冒号之前的 URN 前缀（没有则为空）。
	Schema string
	// Parts 是 URN 属性名之后以 '.' 分隔的属性/子属性段。
	Parts []string
}

// ParseAttrPath 解析属性路径并校验每一段符合 RFC 7643 ATTRNAME 规则。
func ParseAttrPath(raw string) (AttrPath, error) {
	ap := AttrPath{Raw: raw}
	rest := raw
	if idx := strings.LastIndex(rest, ":"); idx >= 0 {
		ap.Schema = rest[:idx]
		rest = rest[idx+1:]
	}
	if rest == "" {
		return ap, &LexError{Pos: 0, Msg: "empty attribute name in path " + strconv.Quote(raw)}
	}
	ap.Parts = strings.Split(rest, ".")
	for _, part := range ap.Parts {
		if !validAttrName(part) {
			return ap, &LexError{Pos: 0, Msg: "invalid attribute name segment " + strconv.Quote(part)}
		}
	}
	return ap, nil
}

func validAttrName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && ((r >= '0' && r <= '9') || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return true
}

// Root 返回首段属性名（如 emails.type 的 emails）。
func (p AttrPath) Root() string {
	if len(p.Parts) == 0 {
		return ""
	}
	return p.Parts[0]
}

// Leaf 返回最后一段属性名。
func (p AttrPath) Leaf() string {
	if n := len(p.Parts); n > 0 {
		return p.Parts[n-1]
	}
	return ""
}

// Canonical 返回小写、点分的规范短路径。
func (p AttrPath) Canonical() string {
	parts := make([]string, len(p.Parts))
	for i, v := range p.Parts {
		parts[i] = strings.ToLower(v)
	}
	return strings.Join(parts, ".")
}

// FullCanonical 返回含小写 schema URN 前缀的完整规范路径。
func (p AttrPath) FullCanonical() string {
	if p.Schema != "" {
		return strings.ToLower(p.Schema) + ":" + p.Canonical()
	}
	return p.Canonical()
}

// String 还原为 SCIM 路径文本。
func (p AttrPath) String() string {
	if p.Raw != "" {
		return p.Raw
	}
	if p.Schema != "" {
		return p.Schema + ":" + strings.Join(p.Parts, ".")
	}
	return strings.Join(p.Parts, ".")
}

// Expr 是 SCIM filter AST 节点。
type Expr interface {
	exprNode()
	// Depth 返回完整二叉 AST 的最大深度（叶子为 1）。
	Depth() int
	// String 输出合法的 SCIM filter 文本；逻辑表达式始终带括号，
	// 因此不存在优先级歧义，也可安全用于日志/下游查询。
	String() string
}

// AttrExpr 是属性比较或 pr 存在性表达式。
type AttrExpr struct {
	Path  AttrPath
	Op    Operator
	Value *Literal // 仅 Op != OpPr 时非空
}

func (*AttrExpr) exprNode() {}

func (e *AttrExpr) Depth() int { return 1 }

func (e *AttrExpr) String() string {
	if e.Op == OpPr {
		return e.Path.String() + " pr"
	}
	return e.Path.String() + " " + string(e.Op) + " " + e.Value.String()
}

// NotExpr 是逻辑非。
type NotExpr struct {
	Inner Expr
}

func (*NotExpr) exprNode() {}

func (e *NotExpr) Depth() int { return 1 + e.Inner.Depth() }

func (e *NotExpr) String() string { return "not(" + e.Inner.String() + ")" }

// LogicalExpr 是 and / or 二元逻辑表达式。
type LogicalExpr struct {
	Op    LogicalOp
	Left  Expr
	Right Expr
}

func (*LogicalExpr) exprNode() {}

func (e *LogicalExpr) Depth() int {
	d := e.Left.Depth()
	if rd := e.Right.Depth(); rd > d {
		d = rd
	}
	return 1 + d
}

func (e *LogicalExpr) String() string {
	return "(" + e.Left.String() + " " + string(e.Op) + " " + e.Right.String() + ")"
}

// ValuePathExpr 是 attr[subFilter] 值路径。
type ValuePathExpr struct {
	Path   AttrPath
	Filter Expr
}

func (*ValuePathExpr) exprNode() {}

func (e *ValuePathExpr) Depth() int { return 1 + e.Filter.Depth() }

func (e *ValuePathExpr) String() string {
	return e.Path.String() + "[" + e.Filter.String() + "]"
}

// Walk 对 AST 做深度优先遍历。visit 对每个节点都会被调用（含逻辑节点本身）。
func Walk(e Expr, visit func(Expr)) {
	if e == nil {
		return
	}
	visit(e)
	switch v := e.(type) {
	case *NotExpr:
		Walk(v.Inner, visit)
	case *LogicalExpr:
		Walk(v.Left, visit)
		Walk(v.Right, visit)
	case *ValuePathExpr:
		Walk(v.Filter, visit)
	case *AttrExpr:
		// leaf
	}
}

// CountNodes 返回 AST 节点总数。
func CountNodes(e Expr) int {
	n := 0
	Walk(e, func(Expr) { n++ })
	return n
}
