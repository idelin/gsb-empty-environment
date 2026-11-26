package joinenum

import (
	"fmt"
	"sort"
)

// Row is one base-table row keyed by attribute name.
type Row map[string]Value

// Database contains one table per base relation, indexed by relation ID.
// Table row counts need not equal Relation.Cardinality; the latter is the
// optimizer estimate, while the database is used for result verification.
type Database struct {
	Tables [][]Row
}

// NewDatabase returns a Database with tables copied from tables.
func NewDatabase(tables [][]Row) Database {
	out := Database{Tables: make([][]Row, len(tables))}
	for i := range tables {
		out.Tables[i] = append([]Row(nil), tables[i]...)
	}
	return out
}

// Eq returns a null-rejecting equality join predicate between two columns.
func Eq(name string, sel Selectivity, left, right Column) *Predicate {
	refs := []int{left.Rel}
	if right.Rel != left.Rel {
		refs = append(refs, right.Rel)
	}
	return &Predicate{
		Name:        name,
		Refs:        refs,
		Selectivity: sel,
		Match: func(t Tuple) bool {
			a, ok := t[left]
			if !ok || a.Null {
				return false
			}
			b, ok := t[right]
			if !ok || b.Null {
				return false
			}
			return valueEqual(a, b)
		},
	}
}

// MatchPredicate builds a predicate from a custom match function.
func MatchPredicate(name string, sel Selectivity, refs []int, nullPreserving bool, match MatchFunc) *Predicate {
	return &Predicate{
		Name:           name,
		Refs:           append([]int(nil), refs...),
		Selectivity:    sel,
		NullPreserving: nullPreserving,
		Match:          match,
	}
}

func valueEqual(a, b Value) bool {
	if a.Null || b.Null {
		return false
	}
	if a.Str != "" || b.Str != "" {
		return a.Str == b.Str
	}
	return a.Int == b.Int
}

// OriginalPlan materializes the input syntax tree as a Plan, including its
// cost and cardinality under the configured cost model.
func (q *Query) OriginalPlan() (*Plan, error) {
	return q.originalPlanNode(q.root)
}

func (q *Query) originalPlanNode(n *Node) (*Plan, error) {
	if n.IsLeaf() {
		return &Plan{
			Kind:        InnerJoin,
			Leaf:        n.Leaf,
			Relations:   Of(n.Leaf),
			Schema:      Of(n.Leaf),
			Cardinality: q.relations[n.Leaf].Cardinality,
			Encoding:    q.names[n.Leaf],
		}, nil
	}
	left, err := q.originalPlanNode(n.Left)
	if err != nil {
		return nil, err
	}
	right, err := q.originalPlanNode(n.Right)
	if err != nil {
		return nil, err
	}
	id, ok := q.nodeOp[n]
	if !ok {
		return nil, fmt.Errorf("%w: syntax node not indexed", ErrInvalidInput)
	}
	op := q.byID[id]
	outCard, localCost, err := q.model.Join(op.Kind, left.Cardinality, right.Cardinality, op.Pred.Selectivity)
	if err != nil {
		return nil, err
	}
	cost, ok := checkedAdd(left.Cost, right.Cost)
	if !ok {
		return nil, ErrCardinalityOverflow
	}
	cost, ok = checkedAdd(cost, localCost)
	if !ok {
		return nil, ErrCardinalityOverflow
	}
	return &Plan{
		Kind:        op.Kind,
		Leaf:        -1,
		Pred:        op.Pred,
		Left:        left,
		Right:       right,
		Relations:   op.Rels,
		Schema:      op.Schema,
		Cost:        cost,
		Cardinality: outCard,
		Encoding:    string(op.Kind.Token()) + "(" + left.Encoding + "," + right.Encoding + ")",
	}, nil
}

// EvaluateOriginal evaluates the original syntax tree on db.
func (q *Query) EvaluateOriginal(db Database) ([]Tuple, error) {
	p, err := q.OriginalPlan()
	if err != nil {
		return nil, err
	}
	return q.Evaluate(p, db)
}

// Evaluate executes a plan on db.  It implements bag semantics and SQL
// null-extension.  The optimizer's cost estimates are not used.
func (q *Query) Evaluate(p *Plan, db Database) ([]Tuple, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: nil plan", ErrInvalidInput)
	}
	if err := q.validateDB(db); err != nil {
		return nil, err
	}
	return q.eval(p, db)
}

func (q *Query) validateDB(db Database) error {
	if len(db.Tables) != len(q.relations) {
		return fmt.Errorf("%w: database has %d tables, query has %d relations", ErrInvalidInput, len(db.Tables), len(q.relations))
	}
	return nil
}

func (q *Query) eval(p *Plan, db Database) ([]Tuple, error) {
	if p.IsLeaf() {
		if p.Leaf < 0 || p.Leaf >= len(q.relations) {
			return nil, fmt.Errorf("%w: plan leaf %d out of range", ErrInvalidInput, p.Leaf)
		}
		rel := q.relations[p.Leaf]
		out := make([]Tuple, 0, len(db.Tables[p.Leaf]))
		for _, row := range db.Tables[p.Leaf] {
			t := make(Tuple, len(rel.Attributes))
			for _, attr := range rel.Attributes {
				v, ok := row[attr]
				if !ok {
					v = NullValue()
				}
				t[Column{Rel: p.Leaf, Attr: attr}] = v
			}
			out = append(out, t)
		}
		return out, nil
	}
	left, err := q.eval(p.Left, db)
	if err != nil {
		return nil, err
	}
	right, err := q.eval(p.Right, db)
	if err != nil {
		return nil, err
	}
	if p.Pred == nil || p.Pred.Match == nil {
		return nil, fmt.Errorf("%w: plan predicate %q has no Match function", ErrInvalidInput, predicateName(p.Pred))
	}
	switch p.Kind {
	case InnerJoin:
		return q.evalInner(left, right, p.Pred)
	case LeftOuterJoin:
		return q.evalLeft(left, right, p, p.Pred)
	case SemiJoin:
		return q.evalSemi(left, right, p.Pred), nil
	case AntiJoin:
		return q.evalAnti(left, right, p.Pred), nil
	default:
		return nil, fmt.Errorf("%w: unknown plan operator %d", ErrInvalidInput, p.Kind)
	}
}

func predicateName(p *Predicate) string {
	if p == nil {
		return "<nil>"
	}
	return p.Name
}

func combine(left, right Tuple) Tuple {
	out := make(Tuple, len(left)+len(right))
	for k, v := range left {
		out[k] = v
	}
	for k, v := range right {
		out[k] = v
	}
	return out
}

func (q *Query) evalInner(left, right []Tuple, pred *Predicate) ([]Tuple, error) {
	out := make([]Tuple, 0, len(left)*minInt(len(right), 64))
	for _, l := range left {
		for _, r := range right {
			t := combine(l, r)
			if pred.Match(t) {
				out = append(out, t)
			}
		}
	}
	return out, nil
}

func (q *Query) evalLeft(left, right []Tuple, p *Plan, pred *Predicate) ([]Tuple, error) {
	out := make([]Tuple, 0, len(left))
	nullRight := q.nullTuple(p.Right.Schema)
	for _, l := range left {
		matched := false
		for _, r := range right {
			t := combine(l, r)
			if pred.Match(t) {
				out = append(out, t)
				matched = true
			}
		}
		if !matched {
			out = append(out, combine(l, nullRight))
		}
	}
	return out, nil
}

func (q *Query) evalSemi(left, right []Tuple, pred *Predicate) []Tuple {
	out := make([]Tuple, 0, len(left))
	for _, l := range left {
		found := false
		for _, r := range right {
			if pred.Match(combine(l, r)) {
				found = true
				break
			}
		}
		if found {
			out = append(out, l)
		}
	}
	return out
}

func (q *Query) evalAnti(left, right []Tuple, pred *Predicate) []Tuple {
	out := make([]Tuple, 0, len(left))
	for _, l := range left {
		found := false
		for _, r := range right {
			if pred.Match(combine(l, r)) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, l)
		}
	}
	return out
}

func (q *Query) nullTuple(schema RelationSet) Tuple {
	out := make(Tuple)
	for _, r := range schema.Elements() {
		for _, attr := range q.relations[r].Attributes {
			out[Column{Rel: r, Attr: attr}] = NullValue()
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// CanonicalRows returns a stable textual representation of a result bag,
// suitable for equality assertions in tests and diagnostics.
func CanonicalRows(rows []Tuple) []string {
	out := make([]string, 0, len(rows))
	for _, t := range rows {
		keys := make([]Column, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].Rel != keys[j].Rel {
				return keys[i].Rel < keys[j].Rel
			}
			return keys[i].Attr < keys[j].Attr
		})
		s := ""
		for _, k := range keys {
			v := t[k]
			if v.Null {
				s += fmt.Sprintf("(%d,%s)=NULL;", k.Rel, k.Attr)
			} else if v.Str != "" {
				s += fmt.Sprintf("(%d,%s)=%q;", k.Rel, k.Attr, v.Str)
			} else {
				s += fmt.Sprintf("(%d,%s)=%d;", k.Rel, k.Attr, v.Int)
			}
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
