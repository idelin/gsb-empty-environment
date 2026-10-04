// Package policydiff 实现权限差异检查器：
// 在有限域上比较新旧两条授权规则，判断新规则是否扩大授权，
// 若扩大则返回最小的主体/资源属性组合（反例）以及解释树。
//
// 语义要点：属性可能缺失，缺失产生三值逻辑中的 Unknown，
// 授权决策遵循 deny-by-default —— 只有评估为 True 才放行，
// Unknown 与 False 一律拒绝。因此不能用普通二值逻辑化简。
package policydiff

import "fmt"

// Tri 是三值逻辑（Kleene 强逻辑）的真值。
type Tri int

const (
	False   Tri = iota
	Unknown     // 由缺失属性产生，最终按拒绝处理
	True
)

func (t Tri) String() string {
	switch t {
	case True:
		return "允许(true)"
	case False:
		return "拒绝(false)"
	default:
		return "未知(unknown)"
	}
}

// Kleene 三值逻辑连接词。该逻辑关于“信息量”单调：
// 补充更多属性后，True/False 保持不变，只有 Unknown 可能被消解。
func and3(a, b Tri) Tri {
	if a == False || b == False {
		return False
	}
	if a == Unknown || b == Unknown {
		return Unknown
	}
	return True
}

func or3(a, b Tri) Tri {
	if a == True || b == True {
		return True
	}
	if a == Unknown || b == Unknown {
		return Unknown
	}
	return False
}

func not3(a Tri) Tri {
	switch a {
	case True:
		return False
	case False:
		return True
	default:
		return Unknown
	}
}

// Kind 是属性值的种类。
type Kind int

const (
	IntKind Kind = iota
	LabelKind
	BoolKind
)

// Value 是一个属性值：有限域整数、标签或布尔。
type Value struct {
	Kind  Kind
	Int   int
	Label string
	Bool  bool
}

func Int(v int) Value      { return Value{Kind: IntKind, Int: v} }
func Label(s string) Value { return Value{Kind: LabelKind, Label: s} }
func Bool(b bool) Value    { return Value{Kind: BoolKind, Bool: b} }

func (v Value) String() string {
	switch v.Kind {
	case IntKind:
		return fmt.Sprintf("%d", v.Int)
	case LabelKind:
		return fmt.Sprintf("%q", v.Label)
	default:
		return fmt.Sprintf("%t", v.Bool)
	}
}

// Env 是一次请求携带的属性集合；未出现的属性即“缺失”，评估为 Unknown。
type Env map[string]Value

// ExprKind 是规则表达式节点的种类。
type ExprKind int

const (
	AndExpr ExprKind = iota
	OrExpr
	NotExpr
	IntRangeExpr // 整数范围：Attr ∈ [Lo, Hi]
	LabelInExpr  // 标签集合：Attr ∈ Set
	BoolExpr     // 布尔条件：Attr == Want
)

// Expr 是授权规则表达式树。
type Expr struct {
	Kind ExprKind
	Attr string   // 叶子条件引用的属性名
	Lo   int      // IntRangeExpr 下界（含）
	Hi   int      // IntRangeExpr 上界（含）
	Set  []string // LabelInExpr 标签集合
	Want bool     // BoolExpr 期望值
	Kids []*Expr  // And/Or 的子节点；Not 取 Kids[0]
}

// And 构造合取。
func And(kids ...*Expr) *Expr { return &Expr{Kind: AndExpr, Kids: kids} }

// Or 构造析取。
func Or(kids ...*Expr) *Expr { return &Expr{Kind: OrExpr, Kids: kids} }

// Not 构造否定。
func Not(kid *Expr) *Expr { return &Expr{Kind: NotExpr, Kids: []*Expr{kid}} }

// IntRange 构造整数范围条件 attr ∈ [lo, hi]。
func IntRange(attr string, lo, hi int) *Expr {
	return &Expr{Kind: IntRangeExpr, Attr: attr, Lo: lo, Hi: hi}
}

// LabelIn 构造标签集合条件 attr ∈ set。
func LabelIn(attr string, set ...string) *Expr {
	return &Expr{Kind: LabelInExpr, Attr: attr, Set: set}
}

// BoolCond 构造布尔条件 attr == want。
func BoolCond(attr string, want bool) *Expr {
	return &Expr{Kind: BoolExpr, Attr: attr, Want: want}
}

// Describe 返回表达式的人类可读描述。
func (e *Expr) Describe() string {
	switch e.Kind {
	case AndExpr:
		return "AND"
	case OrExpr:
		return "OR"
	case NotExpr:
		return "NOT"
	case IntRangeExpr:
		return fmt.Sprintf("%s ∈ [%d, %d]", e.Attr, e.Lo, e.Hi)
	case LabelInExpr:
		s := ""
		for i, l := range e.Set {
			if i > 0 {
				s += ", "
			}
			s += fmt.Sprintf("%q", l)
		}
		return fmt.Sprintf("%s ∈ {%s}", e.Attr, s)
	default:
		return fmt.Sprintf("%s == %t", e.Attr, e.Want)
	}
}

// Attrs 收集表达式引用的全部属性名（去重、升序）。
func (e *Expr) Attrs() []string {
	seen := map[string]bool{}
	var walk func(x *Expr)
	walk = func(x *Expr) {
		if x.Attr != "" {
			seen[x.Attr] = true
		}
		for _, k := range x.Kids {
			walk(k)
		}
	}
	walk(e)
	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sortStrings(out)
	return out
}
