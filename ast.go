package outerjoin

import (
	"fmt"
	"sort"
)

type JoinType string

const (
	Inner JoinType = "INNER"
	Left  JoinType = "LEFT"
	Right JoinType = "RIGHT"
	Full  JoinType = "FULL"
)

type DataType int

const (
	TypeAny DataType = iota
	TypeInt
	TypeFloat
	TypeString
	TypeBool
)

type Ref struct {
	Relation string
	Attr     string
}

func (r Ref) String() string {
	return r.Relation + "." + r.Attr
}

type Attr struct {
	Name string
	Type DataType
}

type Schema map[Ref]DataType

func (s Schema) refs() []Ref {
	refs := make([]Ref, 0, len(s))
	for ref := range s {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Relation != refs[j].Relation {
			return refs[i].Relation < refs[j].Relation
		}
		return refs[i].Attr < refs[j].Attr
	})
	return refs
}

func (s Schema) relations() []string {
	seen := map[string]struct{}{}
	var rels []string
	for ref := range s {
		if _, ok := seen[ref.Relation]; !ok {
			seen[ref.Relation] = struct{}{}
			rels = append(rels, ref.Relation)
		}
	}
	sort.Strings(rels)
	return rels
}

func mergeSchema(schemas ...Schema) Schema {
	out := Schema{}
	for _, schema := range schemas {
		for ref, typ := range schema {
			out[ref] = typ
		}
	}
	return out
}

type Node interface {
	node()
	Schema() Schema
	Relations() []string
	Clone() Node
}

type RelationNode struct {
	ID    string
	Attrs []Attr
}

func Relation(id string, attrs ...string) *RelationNode {
	n := &RelationNode{ID: id}
	for _, attr := range attrs {
		n.Attrs = append(n.Attrs, Attr{Name: attr, Type: TypeInt})
	}
	return n
}

func RelationTyped(id string, attrs ...Attr) *RelationNode {
	return &RelationNode{ID: id, Attrs: attrs}
}

func (n *RelationNode) node() {}

func (n *RelationNode) Schema() Schema {
	s := Schema{}
	for _, attr := range n.Attrs {
		s[Ref{Relation: n.ID, Attr: attr.Name}] = attr.Type
	}
	return s
}

func (n *RelationNode) Relations() []string { return []string{n.ID} }

func (n *RelationNode) Clone() Node {
	cp := *n
	cp.Attrs = append([]Attr(nil), n.Attrs...)
	return &cp
}

type JoinNode struct {
	Type  JoinType
	Left  Node
	Right Node
	On    Pred
}

func Join(typ JoinType, left, right Node, on Pred) *JoinNode {
	return &JoinNode{Type: typ, Left: left, Right: right, On: on}
}

func (n *JoinNode) node() {}

func (n *JoinNode) Schema() Schema {
	return mergeSchema(n.Left.Schema(), n.Right.Schema())
}

func (n *JoinNode) Relations() []string {
	return append(append([]string{}, n.Left.Relations()...), n.Right.Relations()...)
}

func (n *JoinNode) Clone() Node {
	if n == nil {
		return (*JoinNode)(nil)
	}
	cp := *n
	cp.Left = n.Left.Clone()
	cp.Right = n.Right.Clone()
	if n.On != nil {
		cp.On = n.On.Clone()
	}
	return &cp
}

type Query struct {
	From  Node
	Where Pred
}

func (q Query) Clone() Query {
	var where Pred
	if q.Where != nil {
		where = q.Where.Clone()
	}
	return Query{From: q.From.Clone(), Where: where}
}

type Tri int

const (
	False   Tri = -1
	Unknown Tri = 0
	True    Tri = 1
)

func (t Tri) String() string {
	switch t {
	case True:
		return "TRUE"
	case False:
		return "FALSE"
	default:
		return "UNKNOWN"
	}
}

type Value any

type Row map[Ref]Value

func NewRow() Row { return Row{} }

func (r Row) Set(relation, attr string, value Value) Row {
	r[Ref{Relation: relation, Attr: attr}] = value
	return r
}

func (r Row) Clone() Row {
	out := make(Row, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}

type Database map[string][]Row

type CmpOp string

const (
	Eq CmpOp = "="
	Ne CmpOp = "<>"
	Lt CmpOp = "<"
	Le CmpOp = "<="
	Gt CmpOp = ">"
	Ge CmpOp = ">="
)

type Pred interface {
	pred()
	Clone() Pred
}

type Expr interface {
	expr()
	Clone() Expr
}

type BoolLiteral struct {
	Value Tri
}

func Bool(value bool) *BoolLiteral {
	if value {
		return &BoolLiteral{Value: True}
	}
	return &BoolLiteral{Value: False}
}

func UnknownLit() *BoolLiteral { return &BoolLiteral{Value: Unknown} }

func (p *BoolLiteral) pred() {}
func (p *BoolLiteral) Clone() Pred {
	return &BoolLiteral{Value: p.Value}
}

type NotPred struct {
	Inner Pred
}

func Not(p Pred) *NotPred { return &NotPred{Inner: p} }
func (p *NotPred) pred()  {}
func (p *NotPred) Clone() Pred {
	return &NotPred{Inner: p.Inner.Clone()}
}

type AndPred struct {
	Terms []Pred
}

func And(terms ...Pred) *AndPred { return &AndPred{Terms: terms} }
func (p *AndPred) pred()         {}
func (p *AndPred) Clone() Pred {
	cp := &AndPred{Terms: make([]Pred, len(p.Terms))}
	for i, term := range p.Terms {
		cp.Terms[i] = term.Clone()
	}
	return cp
}

type OrPred struct {
	Terms []Pred
}

func Or(terms ...Pred) *OrPred { return &OrPred{Terms: terms} }
func (p *OrPred) pred()        {}
func (p *OrPred) Clone() Pred {
	cp := &OrPred{Terms: make([]Pred, len(p.Terms))}
	for i, term := range p.Terms {
		cp.Terms[i] = term.Clone()
	}
	return cp
}

type Comparison struct {
	Op    CmpOp
	Left  Expr
	Right Expr
}

func Cmp(op CmpOp, left, right Expr) *Comparison {
	return &Comparison{Op: op, Left: left, Right: right}
}
func EqPred(left, right Expr) *Comparison { return Cmp(Eq, left, right) }
func NePred(left, right Expr) *Comparison { return Cmp(Ne, left, right) }
func LtPred(left, right Expr) *Comparison { return Cmp(Lt, left, right) }
func LePred(left, right Expr) *Comparison { return Cmp(Le, left, right) }
func GtPred(left, right Expr) *Comparison { return Cmp(Gt, left, right) }
func GePred(left, right Expr) *Comparison { return Cmp(Ge, left, right) }

func (p *Comparison) pred() {}
func (p *Comparison) Clone() Pred {
	return &Comparison{Op: p.Op, Left: p.Left.Clone(), Right: p.Right.Clone()}
}

type NullTest struct {
	Operand Expr
	Negated bool
}

func IsNull(operand Expr) *NullTest    { return &NullTest{Operand: operand} }
func IsNotNull(operand Expr) *NullTest { return &NullTest{Operand: operand, Negated: true} }
func (p *NullTest) pred()              {}
func (p *NullTest) Clone() Pred {
	return &NullTest{Operand: p.Operand.Clone(), Negated: p.Negated}
}

type InPred struct {
	Operand Expr
	Items   []Expr
	Negated bool
}

func In(operand Expr, items ...Expr) *InPred {
	return &InPred{Operand: operand, Items: items}
}

func NotIn(operand Expr, items ...Expr) *InPred {
	return &InPred{Operand: operand, Items: items, Negated: true}
}

func (p *InPred) pred() {}
func (p *InPred) Clone() Pred {
	cp := &InPred{Operand: p.Operand.Clone(), Negated: p.Negated, Items: make([]Expr, len(p.Items))}
	for i, item := range p.Items {
		cp.Items[i] = item.Clone()
	}
	return cp
}

type BooleanPredicate struct {
	Expr Expr
}

func AsPredicate(expr Expr) *BooleanPredicate { return &BooleanPredicate{Expr: expr} }
func BoolColumn(relation, attr string) *BooleanPredicate {
	return AsPredicate(Col(relation, attr))
}

func (p *BooleanPredicate) pred() {}
func (p *BooleanPredicate) Clone() Pred {
	return &BooleanPredicate{Expr: p.Expr.Clone()}
}

type ColumnExpr struct {
	Ref Ref
}

func Col(relation, attr string) *ColumnExpr {
	return &ColumnExpr{Ref: Ref{Relation: relation, Attr: attr}}
}

func (e *ColumnExpr) expr() {}
func (e *ColumnExpr) Clone() Expr {
	return &ColumnExpr{Ref: e.Ref}
}

type LiteralExpr struct {
	Value Value
}

func Lit(value Value) *LiteralExpr { return &LiteralExpr{Value: value} }
func NullLit() *LiteralExpr        { return &LiteralExpr{Value: nil} }

func (e *LiteralExpr) expr() {}
func (e *LiteralExpr) Clone() Expr {
	return &LiteralExpr{Value: e.Value}
}

type CoalesceExpr struct {
	Exprs []Expr
}

func Coalesce(exprs ...Expr) *CoalesceExpr { return &CoalesceExpr{Exprs: exprs} }
func (e *CoalesceExpr) expr()              {}
func (e *CoalesceExpr) Clone() Expr {
	cp := &CoalesceExpr{Exprs: make([]Expr, len(e.Exprs))}
	for i, expr := range e.Exprs {
		cp.Exprs[i] = expr.Clone()
	}
	return cp
}

type CaseBranch struct {
	When Pred
	Then Expr
}

func When(condition Pred, then Expr) CaseBranch {
	return CaseBranch{When: condition, Then: then}
}

type CaseExpr struct {
	Branches []CaseBranch
	Else     Expr
}

func Case(branches ...CaseBranch) *CaseExpr { return &CaseExpr{Branches: branches} }
func (e *CaseExpr) expr()                   {}
func (e *CaseExpr) Clone() Expr {
	cp := &CaseExpr{Branches: make([]CaseBranch, len(e.Branches))}
	for i, branch := range e.Branches {
		cp.Branches[i] = CaseBranch{When: branch.When.Clone(), Then: branch.Then.Clone()}
	}
	if e.Else != nil {
		cp.Else = e.Else.Clone()
	}
	return cp
}

func (e *CaseExpr) WithElse(elseExpr Expr) *CaseExpr {
	e.Else = elseExpr
	return e
}

type EvaluationError struct {
	Reason string
}

func (e *EvaluationError) Error() string { return "outerjoin: " + e.Reason }

func errorf(format string, args ...any) error {
	return &EvaluationError{Reason: fmt.Sprintf(format, args...)}
}
