package federate

import (
	"math"
	"strings"
)

// Volatility mirrors PostgreSQL's function volatility classification.
type Volatility uint8

const (
	Immutable Volatility = iota
	Stable
	Volatile
)

func (v Volatility) String() string {
	switch v {
	case Immutable:
		return "IMMUTABLE"
	case Stable:
		return "STABLE"
	case Volatile:
		return "VOLATILE"
	default:
		return "UNKNOWN"
	}
}

func maxVolatility(a, b Volatility) Volatility {
	if a > b {
		return a
	}
	return b
}

// EvalEnv is the evaluation environment for an expression. Token is a stable
// identity for the current row, allowing deterministic simulation of volatile
// functions between the reference and distributed executions.
type EvalEnv struct {
	Row   Row
	Aggs  map[string]Value
	Token uint64
	Seed  uint64
	Now   int64
}

// Expr is the common expression interface. Aggregate expressions are only
// valid in projections, HAVING and ORDER BY of grouped queries.
type Expr interface {
	exprNode()
	String() string
	Volatility() Volatility
	Eval(*EvalEnv) Value
}

type ColumnRef struct {
	Table string
	Name  string
}

func (ColumnRef) exprNode() {}
func (c *ColumnRef) String() string {
	if c.Table != "" {
		return c.Table + "." + c.Name
	}
	return c.Name
}
func (c *ColumnRef) Volatility() Volatility { return Immutable }
func (c *ColumnRef) Eval(env *EvalEnv) Value {
	if env == nil {
		return Null()
	}
	return env.Row.Value(c.Table, c.Name)
}

func Col(name string) *ColumnRef { return &ColumnRef{Name: name} }
func ColOf(table, name string) *ColumnRef {
	return &ColumnRef{Table: table, Name: name}
}

type LiteralExpr struct {
	Value Value
}

func (LiteralExpr) exprNode() {}
func (l *LiteralExpr) String() string {
	return l.Value.String()
}
func (l *LiteralExpr) Volatility() Volatility { return Immutable }
func (l *LiteralExpr) Eval(*EvalEnv) Value    { return l.Value }

func Const(v Value) *LiteralExpr { return &LiteralExpr{Value: v} }

type BinaryExpr struct {
	Op    string
	Left  Expr
	Right Expr
}

func (BinaryExpr) exprNode() {}
func (b *BinaryExpr) String() string {
	return "(" + b.Left.String() + " " + b.Op + " " + b.Right.String() + ")"
}
func (b *BinaryExpr) Volatility() Volatility {
	return maxVolatility(b.Left.Volatility(), b.Right.Volatility())
}
func (b *BinaryExpr) Eval(env *EvalEnv) Value {
	l, r := b.Left.Eval(env), b.Right.Eval(env)
	switch strings.ToUpper(b.Op) {
	case "AND":
		return sqlAnd(l, r)
	case "OR":
		return sqlOr(l, r)
	case "=":
		if l.IsNull() || r.IsNull() {
			return Null()
		}
		c, ok := Compare(l, r)
		if !ok {
			return Null()
		}
		return Bool(c == 0)
	case "<>", "!=":
		if l.IsNull() || r.IsNull() {
			return Null()
		}
		c, ok := Compare(l, r)
		if !ok {
			return Null()
		}
		return Bool(c != 0)
	case "<":
		return compareResult(l, r, func(c int) bool { return c < 0 })
	case "<=":
		return compareResult(l, r, func(c int) bool { return c <= 0 })
	case ">":
		return compareResult(l, r, func(c int) bool { return c > 0 })
	case ">=":
		return compareResult(l, r, func(c int) bool { return c >= 0 })
	case "+":
		return addValues(l, r)
	case "-":
		return subValues(l, r)
	case "*":
		return mulValues(l, r)
	case "/":
		return divValues(l, r)
	default:
		return Null()
	}
}

func compareResult(l, r Value, pred func(int) bool) Value {
	if l.IsNull() || r.IsNull() {
		return Null()
	}
	c, ok := Compare(l, r)
	if !ok {
		return Null()
	}
	return Bool(pred(c))
}

func sqlAnd(a, b Value) Value {
	at, an := truth(a)
	bt, bn := truth(b)
	if (!an || !at) || (!bn || !bt) {
		if (an && !at) || (bn && !bt) {
			return Bool(false)
		}
		return Null()
	}
	return Bool(true)
}

func sqlOr(a, b Value) Value {
	at, an := truth(a)
	bt, bn := truth(b)
	if (an && at) || (bn && bt) {
		return Bool(true)
	}
	if !an || !bn {
		return Null()
	}
	return Bool(false)
}

func subValues(a, b Value) Value {
	if a.IsNull() || b.IsNull() {
		return Null()
	}
	if a.Type == TypeFloat || b.Type == TypeFloat {
		return Float(a.asFloat() - b.asFloat())
	}
	if a.Type == TypeInt && b.Type == TypeInt {
		return Int(a.Int - b.Int)
	}
	return Null()
}

func mulValues(a, b Value) Value {
	if a.IsNull() || b.IsNull() {
		return Null()
	}
	if a.Type == TypeFloat || b.Type == TypeFloat {
		return Float(a.asFloat() * b.asFloat())
	}
	if a.Type == TypeInt && b.Type == TypeInt {
		return Int(a.Int * b.Int)
	}
	return Null()
}

type UnaryExpr struct {
	Op   string
	Expr Expr
}

func (UnaryExpr) exprNode() {}
func (u *UnaryExpr) String() string {
	return "(" + u.Op + " " + u.Expr.String() + ")"
}
func (u *UnaryExpr) Volatility() Volatility { return u.Expr.Volatility() }
func (u *UnaryExpr) Eval(env *EvalEnv) Value {
	v := u.Expr.Eval(env)
	switch strings.ToUpper(u.Op) {
	case "NOT":
		if v.IsNull() {
			return Null()
		}
		if v.Type != TypeBool {
			return Null()
		}
		return Bool(!v.Bool)
	case "IS NULL":
		return Bool(v.IsNull())
	case "IS NOT NULL":
		return Bool(!v.IsNull())
	case "-":
		if v.IsNull() {
			return Null()
		}
		if v.Type == TypeInt {
			return Int(-v.Int)
		}
		if v.Type == TypeFloat {
			return Float(-v.Float)
		}
		return Null()
	default:
		return Null()
	}
}

type CollateExpr struct {
	Expr      Expr
	Collation string
}

func (CollateExpr) exprNode() {}
func (c *CollateExpr) String() string {
	return c.Expr.String() + " COLLATE " + c.Collation
}
func (c *CollateExpr) Volatility() Volatility { return c.Expr.Volatility() }
func (c *CollateExpr) Eval(env *EvalEnv) Value {
	v := c.Expr.Eval(env)
	if v.Type == TypeString {
		v.Collation = c.Collation
	}
	return v
}

type FuncExpr struct {
	Name           string
	Args           []Expr
	VolatilityKind Volatility
}

func (FuncExpr) exprNode() {}
func (f *FuncExpr) String() string {
	args := make([]string, len(f.Args))
	for i := range f.Args {
		args[i] = f.Args[i].String()
	}
	return f.Name + "(" + strings.Join(args, ", ") + ")"
}
func (f *FuncExpr) Volatility() Volatility {
	v := f.VolatilityKind
	for _, a := range f.Args {
		v = maxVolatility(v, a.Volatility())
	}
	return v
}
func (f *FuncExpr) Eval(env *EvalEnv) Value {
	if env == nil {
		env = &EvalEnv{}
	}
	switch strings.ToLower(f.Name) {
	case "random":
		return Float(float64(splitmix64(env.Seed^env.Token^exprSalt(f))) / float64(math.MaxUint64))
	case "now":
		return Int(env.Now)
	case "abs":
		if len(f.Args) != 1 {
			return Null()
		}
		v := f.Args[0].Eval(env)
		if v.Type == TypeInt {
			if v.Int < 0 {
				return Int(-v.Int)
			}
			return v
		}
		if v.Type == TypeFloat {
			return Float(math.Abs(v.Float))
		}
		return Null()
	case "lower", "upper":
		if len(f.Args) != 1 {
			return Null()
		}
		v := f.Args[0].Eval(env)
		if v.Type != TypeString {
			return Null()
		}
		if strings.EqualFold(f.Name, "lower") {
			v.Str = strings.ToLower(v.Str)
		} else {
			v.Str = strings.ToUpper(v.Str)
		}
		return v
	case "length":
		if len(f.Args) != 1 {
			return Null()
		}
		v := f.Args[0].Eval(env)
		if v.Type != TypeString {
			return Null()
		}
		return Int(int64(len(v.Str)))
	case "coalesce":
		for _, a := range f.Args {
			v := a.Eval(env)
			if !v.IsNull() {
				return v
			}
		}
		return Null()
	case "greatest", "least":
		if len(f.Args) == 0 {
			return Null()
		}
		best := f.Args[0].Eval(env)
		for _, a := range f.Args[1:] {
			v := a.Eval(env)
			c, ok := Compare(v, best)
			if !ok {
				continue
			}
			if (strings.EqualFold(f.Name, "greatest") && c > 0) ||
				(strings.EqualFold(f.Name, "least") && c < 0) {
				best = v
			}
		}
		return best
	default:
		return Null()
	}
}

func exprSalt(f Expr) uint64 {
	var h uint64 = 1469598103934665603
	s := f.String()
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

var builtinVolatility = map[string]Volatility{
	"random":   Volatile,
	"now":      Stable,
	"abs":      Immutable,
	"lower":    Immutable,
	"upper":    Immutable,
	"length":   Immutable,
	"coalesce": Immutable,
	"greatest": Immutable,
	"least":    Immutable,
}

// Func builds a function expression. Unknown functions are conservatively
// classified VOLATILE.
func Func(name string, args ...Expr) *FuncExpr {
	v, ok := builtinVolatility[strings.ToLower(name)]
	if !ok {
		v = Volatile
	}
	return &FuncExpr{Name: name, Args: args, VolatilityKind: v}
}

// WithVolatility overrides a function's volatility classification.
func (f *FuncExpr) WithVolatility(v Volatility) *FuncExpr {
	f.VolatilityKind = v
	return f
}

func Collate(e Expr, name string) *CollateExpr {
	return &CollateExpr{Expr: e, Collation: name}
}

// AggKind enumerates supported aggregate functions.
type AggKind string

const (
	AggCount AggKind = "count"
	AggSum   AggKind = "sum"
	AggAvg   AggKind = "avg"
	AggMin   AggKind = "min"
	AggMax   AggKind = "max"
)

type AggExpr struct {
	Kind     AggKind
	Arg      Expr
	Distinct bool
}

func (AggExpr) exprNode() {}
func (a *AggExpr) String() string {
	arg := "*"
	if a.Arg != nil {
		arg = a.Arg.String()
	}
	if a.Distinct {
		arg = "DISTINCT " + arg
	}
	return string(a.Kind) + "(" + arg + ")"
}
func (a *AggExpr) Volatility() Volatility {
	if a.Arg == nil {
		return Immutable
	}
	return a.Arg.Volatility()
}
func (a *AggExpr) Eval(env *EvalEnv) Value {
	if env == nil || env.Aggs == nil {
		return Null()
	}
	if v, ok := env.Aggs[aggKey(a)]; ok {
		return v
	}
	return Null()
}

func AggKey(a *AggExpr) string { return aggKey(a) }

func aggKey(a *AggExpr) string {
	arg := "*"
	if a.Arg != nil {
		arg = a.Arg.String()
	}
	if a.Distinct {
		return string(a.Kind) + ":distinct:" + arg
	}
	return string(a.Kind) + ":" + arg
}

func Count(arg Expr) *AggExpr { return &AggExpr{Kind: AggCount, Arg: arg} }
func CountDistinct(arg Expr) *AggExpr {
	return &AggExpr{Kind: AggCount, Arg: arg, Distinct: true}
}
func Sum(arg Expr) *AggExpr { return &AggExpr{Kind: AggSum, Arg: arg} }
func SumDistinct(arg Expr) *AggExpr {
	return &AggExpr{Kind: AggSum, Arg: arg, Distinct: true}
}
func Avg(arg Expr) *AggExpr { return &AggExpr{Kind: AggAvg, Arg: arg} }
func Min(arg Expr) *AggExpr { return &AggExpr{Kind: AggMin, Arg: arg} }
func Max(arg Expr) *AggExpr { return &AggExpr{Kind: AggMax, Arg: arg} }

func Eq(l, r Expr) *BinaryExpr  { return &BinaryExpr{Op: "=", Left: l, Right: r} }
func Ne(l, r Expr) *BinaryExpr  { return &BinaryExpr{Op: "<>", Left: l, Right: r} }
func Lt(l, r Expr) *BinaryExpr  { return &BinaryExpr{Op: "<", Left: l, Right: r} }
func Le(l, r Expr) *BinaryExpr  { return &BinaryExpr{Op: "<=", Left: l, Right: r} }
func Gt(l, r Expr) *BinaryExpr  { return &BinaryExpr{Op: ">", Left: l, Right: r} }
func Ge(l, r Expr) *BinaryExpr  { return &BinaryExpr{Op: ">=", Left: l, Right: r} }
func Add(l, r Expr) *BinaryExpr { return &BinaryExpr{Op: "+", Left: l, Right: r} }
func Sub(l, r Expr) *BinaryExpr { return &BinaryExpr{Op: "-", Left: l, Right: r} }
func Mul(l, r Expr) *BinaryExpr { return &BinaryExpr{Op: "*", Left: l, Right: r} }
func Div(l, r Expr) *BinaryExpr { return &BinaryExpr{Op: "/", Left: l, Right: r} }
func Not(e Expr) *UnaryExpr     { return &UnaryExpr{Op: "NOT", Expr: e} }
func IsNull(e Expr) *UnaryExpr  { return &UnaryExpr{Op: "IS NULL", Expr: e} }
func IsNotNull(e Expr) *UnaryExpr {
	return &UnaryExpr{Op: "IS NOT NULL", Expr: e}
}

// And builds a conjunction. Empty input is TRUE.
func And(exprs ...Expr) Expr {
	return combine("AND", exprs)
}

// Or builds a disjunction. Empty input is FALSE.
func Or(exprs ...Expr) Expr {
	return combine("OR", exprs)
}

func combine(op string, exprs []Expr) Expr {
	filtered := make([]Expr, 0, len(exprs))
	for _, e := range exprs {
		if e != nil {
			filtered = append(filtered, e)
		}
	}
	if len(filtered) == 0 {
		return Const(Bool(op == "AND"))
	}
	out := filtered[0]
	for _, e := range filtered[1:] {
		out = &BinaryExpr{Op: op, Left: out, Right: e}
	}
	return out
}

// SplitConjuncts flattens top-level AND nodes.
func SplitConjuncts(e Expr) []Expr {
	if e == nil {
		return nil
	}
	if b, ok := e.(*BinaryExpr); ok && strings.EqualFold(b.Op, "AND") {
		return append(SplitConjuncts(b.Left), SplitConjuncts(b.Right)...)
	}
	return []Expr{e}
}

// ColumnRefs returns all column references in an expression.
func ColumnRefs(e Expr) []*ColumnRef {
	var out []*ColumnRef
	var walk func(Expr)
	walk = func(x Expr) {
		switch n := x.(type) {
		case *ColumnRef:
			out = append(out, n)
		case *BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *UnaryExpr:
			walk(n.Expr)
		case *FuncExpr:
			for _, a := range n.Args {
				walk(a)
			}
		case *CollateExpr:
			walk(n.Expr)
		case *AggExpr:
			if n.Arg != nil {
				walk(n.Arg)
			}
		}
	}
	walk(e)
	return out
}

// VisitAggs visits every aggregate node, including duplicates.
func VisitAggs(e Expr, fn func(*AggExpr)) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *AggExpr:
		fn(n)
	case *BinaryExpr:
		VisitAggs(n.Left, fn)
		VisitAggs(n.Right, fn)
	case *UnaryExpr:
		VisitAggs(n.Expr, fn)
	case *FuncExpr:
		for _, a := range n.Args {
			VisitAggs(a, fn)
		}
	case *CollateExpr:
		VisitAggs(n.Expr, fn)
	}
}

func hasAggregate(e Expr) bool {
	found := false
	VisitAggs(e, func(*AggExpr) { found = true })
	return found
}

func containsVolatile(e Expr) bool {
	return e != nil && e.Volatility() == Volatile
}

func parseBool(v Value) bool {
	return v.Type == TypeBool && v.Bool
}
