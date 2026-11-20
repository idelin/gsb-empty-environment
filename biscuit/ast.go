package biscuit

import (
	"fmt"
	"strconv"
)

// TermKind 标识 Datalog 项的具体类型。
type TermKind int

const (
	// KindVariable 是变量项，名称不包含前导的 $。
	KindVariable TermKind = iota
	KindSymbol            // 裸标识符常量，例如 admin
	KindString            // 双引号字符串常量，例如 "alice"
	KindInteger           // 64 位整数常量
	KindBool              // true / false
)

// Term 表示一个 Datalog 项：变量或常量。
type Term struct {
	Kind TermKind
	Name string // KindVariable / KindSymbol 使用
	Text string // KindString 使用
	Int  int64  // KindInteger 使用
	Bool bool   // KindBool 使用
}

// Variable 构造变量项，name 不带前导 $。
func Variable(name string) Term { return Term{Kind: KindVariable, Name: name} }

// Symbol 构造标识符常量。
func Symbol(name string) Term { return Term{Kind: KindSymbol, Name: name} }

// String 构造字符串常量。
func String(value string) Term { return Term{Kind: KindString, Text: value} }

// Integer 构造整数常量。
func Integer(value int64) Term { return Term{Kind: KindInteger, Int: value} }

// Bool 构造布尔常量。
func Bool(value bool) Term { return Term{Kind: KindBool, Bool: value} }

// IsVariable 报告该项是否为变量。
func (t Term) IsVariable() bool { return t.Kind == KindVariable }

// String 按源语法渲染项。
func (t Term) String() string {
	switch t.Kind {
	case KindVariable:
		return "$" + t.Name
	case KindSymbol:
		return t.Name
	case KindString:
		return strconv.Quote(t.Text)
	case KindInteger:
		return strconv.FormatInt(t.Int, 10)
	case KindBool:
		return strconv.FormatBool(t.Bool)
	default:
		return fmt.Sprintf("Term(kind=%d)", t.Kind)
	}
}

// Fact 是一个基实例谓词 pred(常量…)，参数中不允许出现变量。
type Fact struct {
	Predicate string
	Args      []Term
}

// String 按源语法渲染事实。
func (f Fact) String() string {
	return f.Predicate + "(" + joinTerms(f.Args) + ")"
}

// Rule 是 head :- body 形式的规则，head 与 body 中的谓词可带变量。
type Rule struct {
	Head Fact
	Body []Fact
}

// String 按源语法渲染规则。
func (r Rule) String() string {
	parts := make([]string, len(r.Body))
	for i, atom := range r.Body {
		parts[i] = atom.String()
	}
	return r.Head.String() + " :- " + joinStrings(parts, ", ")
}

// Check 是一个 check if 约束，任一查询能推出事实即视为通过。
type Check struct {
	Queries []Rule
}

// PolicyKind 区分服务端 allow / deny 策略。
type PolicyKind int

const (
	// PolicyAllow 命中即放行。
	PolicyAllow PolicyKind = iota
	// PolicyDeny 命中即拒绝。
	PolicyDeny
)

// Policy 是一条 allow if / deny if 服务端策略，任一查询命中即策略命中。
type Policy struct {
	Kind    PolicyKind
	Queries []Rule
}

// Block 表示一个 Biscuit 块。切片中的第 0 个块为 authority 块，
// 其余为衰减块；ID 字段仅用于标识与错误报告。
type Block struct {
	ID     int
	Facts  []Fact
	Rules  []Rule
	Checks []Check
}

func joinTerms(terms []Term) string {
	parts := make([]string, len(terms))
	for i, term := range terms {
		parts[i] = term.String()
	}
	return joinStrings(parts, ", ")
}

func joinStrings(parts []string, sep string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += sep
		}
		out += part
	}
	return out
}
