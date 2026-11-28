package federate

import (
	"fmt"
	"math"
	"strings"
)

// JoinType is a SQL join flavor.
type JoinType string

const (
	InnerJoin JoinType = "inner"
	LeftJoin  JoinType = "left"
	RightJoin JoinType = "right"
	FullJoin  JoinType = "full"
)

func (j JoinType) valid() bool {
	switch j {
	case InnerJoin, LeftJoin, RightJoin, FullJoin:
		return true
	default:
		return false
	}
}

func (j JoinType) preservesLeft() bool {
	return j == LeftJoin || j == FullJoin
}

func (j JoinType) preservesRight() bool {
	return j == RightJoin || j == FullJoin
}

func (j JoinType) outer() bool { return j != InnerJoin }

// JoinSpec describes the optional second table in a query.
type JoinSpec struct {
	Table string
	Type  JoinType
	On    Expr
}

// SelectItem is one projected expression.
type SelectItem struct {
	Expr  Expr
	Alias string
}

func Select(e Expr, alias string) SelectItem {
	return SelectItem{Expr: e, Alias: alias}
}

// OrderItem is one ORDER BY key.
type OrderItem struct {
	Expr       Expr
	Desc       bool
	NullsFirst *bool
}

func Asc(e Expr) OrderItem  { return OrderItem{Expr: e} }
func Desc(e Expr) OrderItem { return OrderItem{Expr: e, Desc: true} }

func (o OrderItem) nullsFirst() bool {
	if o.NullsFirst != nil {
		return *o.NullsFirst
	}
	return o.Desc
}

// resolveAliasExpr maps a simple ORDER BY alias reference to its projection
// expression. Other expressions are returned unchanged.
func resolveAliasExpr(q *Query, e Expr) Expr {
	ref, ok := e.(*ColumnRef)
	if !ok || ref.Table != "" {
		return e
	}
	for _, s := range q.Select {
		if s.Alias != "" && strings.EqualFold(s.Alias, ref.Name) {
			return s.Expr
		}
	}
	return e
}

func resolvedOrderItems(q *Query) []OrderItem {
	out := make([]OrderItem, len(q.OrderBy))
	for i, o := range q.OrderBy {
		out[i] = o
		out[i].Expr = resolveAliasExpr(q, o.Expr)
	}
	return out
}

// Query is the logical query accepted by the planner and both executors.
// Limit < 0 means no LIMIT.
type Query struct {
	From    string
	Join    *JoinSpec
	Where   Expr
	GroupBy []Expr
	Select  []SelectItem
	Having  Expr
	OrderBy []OrderItem
	Limit   int64
	Offset  int64
	Seed    uint64
}

// NewQuery builds a query with a conventional "no limit" sentinel.
func NewQuery(from string) *Query {
	return &Query{From: from, Limit: -1}
}

func (q *Query) validate(cat *Catalog) error {
	if q == nil {
		return fmt.Errorf("nil query")
	}
	if q.From == "" {
		return fmt.Errorf("query must specify FROM")
	}
	if q.Limit < -1 {
		return fmt.Errorf("LIMIT must be >= 0")
	}
	if q.Offset < 0 {
		return fmt.Errorf("OFFSET must be >= 0")
	}
	if q.Limit >= 0 && q.Offset > math.MaxInt64-q.Limit {
		return fmt.Errorf("LIMIT + OFFSET overflows int64")
	}
	if _, ok := cat.Table(q.From); !ok {
		return fmt.Errorf("table %q not found", q.From)
	}
	if q.Join != nil {
		if _, ok := cat.Table(q.Join.Table); !ok {
			return fmt.Errorf("table %q not found", q.Join.Table)
		}
		if !q.Join.Type.valid() {
			return fmt.Errorf("invalid join type %q", q.Join.Type)
		}
		if q.Join.On == nil {
			return fmt.Errorf("join requires an ON predicate")
		}
		if hasAggregate(q.Join.On) {
			return fmt.Errorf("aggregate expression is not allowed in JOIN ON")
		}
	}
	if q.Where != nil && hasAggregate(q.Where) {
		return fmt.Errorf("aggregate expression is not allowed in WHERE")
	}
	if len(q.GroupBy) > 0 {
		for _, e := range q.GroupBy {
			if hasAggregate(e) {
				return fmt.Errorf("aggregate expression is not allowed in GROUP BY")
			}
		}
	}
	all := make(map[string]struct{})
	addAgg := func(e Expr) {
		VisitAggs(e, func(a *AggExpr) { all[aggKey(a)] = struct{}{} })
	}
	for _, s := range q.Select {
		if s.Expr == nil {
			return fmt.Errorf("nil projection expression")
		}
		addAgg(s.Expr)
	}
	addAgg(q.Having)
	for _, o := range q.OrderBy {
		if o.Expr == nil {
			return fmt.Errorf("nil ORDER BY expression")
		}
		addAgg(o.Expr)
	}
	if q.Having != nil && len(all) == 0 && len(q.GroupBy) == 0 {
		return fmt.Errorf("HAVING requires GROUP BY or aggregate projection")
	}
	return nil
}

func queryHasAggregate(q *Query) bool {
	for _, s := range q.Select {
		if hasAggregate(s.Expr) {
			return true
		}
	}
	if hasAggregate(q.Having) {
		return true
	}
	for _, o := range q.OrderBy {
		if hasAggregate(o.Expr) {
			return true
		}
	}
	return false
}

func (q *Query) aggregates() []*AggExpr {
	seen := make(map[string]struct{})
	var out []*AggExpr
	add := func(e Expr) {
		VisitAggs(e, func(a *AggExpr) {
			k := aggKey(a)
			if _, ok := seen[k]; ok {
				return
			}
			seen[k] = struct{}{}
			out = append(out, a)
		})
	}
	for _, s := range q.Select {
		add(s.Expr)
	}
	add(q.Having)
	for _, o := range q.OrderBy {
		add(o.Expr)
	}
	return out
}

// Result is a fully materialised query result.
type Result struct {
	Columns []string
	Rows    [][]Value
}

// Stats records physical work for the distributed execution.
type Stats struct {
	ShardRowsScanned int
	RowsTransferred  int
	PartialGroups    int
	CoordinatorRows  int
}

func (s *Stats) add(o Stats) {
	s.ShardRowsScanned += o.ShardRowsScanned
	s.RowsTransferred += o.RowsTransferred
	s.PartialGroups += o.PartialGroups
	s.CoordinatorRows += o.CoordinatorRows
}

// EqualResults compares columns and every value in order.
func EqualResults(a, b *Result) bool {
	if a == nil || b == nil || len(a.Columns) != len(b.Columns) || len(a.Rows) != len(b.Rows) {
		return false
	}
	for i := range a.Columns {
		if a.Columns[i] != b.Columns[i] {
			return false
		}
	}
	for i := range a.Rows {
		if len(a.Rows[i]) != len(b.Rows[i]) {
			return false
		}
		for j := range a.Rows[i] {
			if !Equal(a.Rows[i][j], b.Rows[i][j]) {
				if !(a.Rows[i][j].IsNull() && b.Rows[i][j].IsNull()) {
					return false
				}
			}
		}
	}
	return true
}

// DiffResults returns a human-readable first mismatch.
func DiffResults(expected, actual *Result) string {
	if expected == nil || actual == nil {
		return "nil result"
	}
	if strings.Join(expected.Columns, ",") != strings.Join(actual.Columns, ",") {
		return fmt.Sprintf("columns differ: %v vs %v", expected.Columns, actual.Columns)
	}
	n := len(expected.Rows)
	if len(actual.Rows) < n {
		n = len(actual.Rows)
	}
	for i := 0; i < n; i++ {
		if len(expected.Rows[i]) != len(actual.Rows[i]) {
			return fmt.Sprintf("row %d width differs", i)
		}
		for j := range expected.Rows[i] {
			a, b := expected.Rows[i][j], actual.Rows[i][j]
			if Equal(a, b) || (a.IsNull() && b.IsNull()) {
				continue
			}
			return fmt.Sprintf("row %d col %d differs: expected %s, got %s", i, j, a.String(), b.String())
		}
	}
	if len(expected.Rows) != len(actual.Rows) {
		return fmt.Sprintf("row count differs: expected %d, got %d", len(expected.Rows), len(actual.Rows))
	}
	return ""
}
