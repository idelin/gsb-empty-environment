package mvrewrite

import (
	"fmt"
	"sort"
	"strings"
)

// ColRef identifies a column occurrence in a join tree.  Table is the leaf
// alias; when no alias is used it is the physical base-table name.
type ColRef struct {
	Table  string `json:"table"`
	Column string `json:"column"`
}

// C is a concise constructor for ColRef.
func C(table, column string) ColRef { return ColRef{Table: table, Column: column} }

func (c ColRef) String() string { return c.Table + "." + c.Column }

// Key is the stable map key for qualified columns.
func (c ColRef) Key() string { return c.Table + "\x00" + c.Column }

// ColumnSpec declares base-table nullability and key information.
type ColumnSpec struct {
	Name       string     `json:"name"`
	Type       ColumnType `json:"type,omitempty"`
	NotNull    bool       `json:"not_null,omitempty"`
	PrimaryKey bool       `json:"primary_key,omitempty"`
}

// ColumnType selects finite-domain generation.  The zero type behaves as an
// integer-like/any-ordered column for backwards-compatible specs.
type ColumnType string

const (
	ColumnTypeInt    ColumnType = "int"
	ColumnTypeString ColumnType = "string"
)

// TableSpec describes one physical base table.
type TableSpec struct {
	Name    string       `json:"name"`
	Columns []ColumnSpec `json:"columns"`
}

// Catalog contains all base-table declarations shared by a query and views.
type Catalog struct {
	Tables map[string]*TableSpec `json:"tables"`
	// Aliases maps join-tree leaf aliases to physical table names.  Analyze
	// populates it automatically from Scan/ScanAs nodes.
	Aliases map[string]string `json:"aliases,omitempty"`
}

// NewCatalog creates an empty catalog.
func NewCatalog() *Catalog {
	return &Catalog{Tables: map[string]*TableSpec{}, Aliases: map[string]string{}}
}

// Table adds or replaces a table declaration and returns the catalog for
// fluent use.
func (c *Catalog) Table(name string, columns ...ColumnSpec) *Catalog {
	if c.Tables == nil {
		c.Tables = map[string]*TableSpec{}
	}
	t := &TableSpec{Name: name, Columns: columns}
	c.Tables[name] = t
	return c
}

// Col creates an ordinary nullable column declaration.
func Col(name string) ColumnSpec { return ColumnSpec{Name: name} }

// StringCol creates a nullable string column.
func StringCol(name string) ColumnSpec { return ColumnSpec{Name: name, Type: ColumnTypeString} }

// NotNullStringCol creates a NOT NULL string column.
func NotNullStringCol(name string) ColumnSpec {
	return ColumnSpec{Name: name, Type: ColumnTypeString, NotNull: true}
}

// NotNullCol creates a NOT NULL column declaration.
func NotNullCol(name string) ColumnSpec { return ColumnSpec{Name: name, NotNull: true} }

// PKCol creates a primary-key column declaration.  Primary keys imply NOT
// NULL and uniqueness in generated legal instances.
func PKCol(name string) ColumnSpec {
	return ColumnSpec{Name: name, NotNull: true, PrimaryKey: true}
}

func (c *Catalog) table(name string) (*TableSpec, bool) {
	if c == nil || c.Tables == nil {
		return nil, false
	}
	if physical, ok := c.Aliases[name]; ok && physical != "" {
		name = physical
	}
	t, ok := c.Tables[name]
	return t, ok
}

func (c *Catalog) column(table, column string) (ColumnSpec, bool) {
	t, ok := c.table(table)
	if !ok {
		return ColumnSpec{}, false
	}
	for _, col := range t.Columns {
		if col.Name == column {
			return col, true
		}
	}
	return ColumnSpec{}, false
}

// JoinKind is a join-node type.
type JoinKind string

const (
	// ScanNode is a leaf base-table occurrence.
	ScanNode JoinKind = "scan"
	// InnerJoin is an inner join.
	InnerJoin JoinKind = "inner"
	// LeftJoin is a left outer join.  Left is preserved and Right is padded.
	LeftJoin JoinKind = "left"
)

// PredicateOp enumerates supported compensation/WHERE predicates.
type PredicateOp string

const (
	OpIsNull     PredicateOp = "is_null"
	OpNotNull    PredicateOp = "is_not_null"
	OpEq         PredicateOp = "="
	OpNe         PredicateOp = "<>"
	OpLt         PredicateOp = "<"
	OpLe         PredicateOp = "<="
	OpGt         PredicateOp = ">"
	OpGe         PredicateOp = ">="
	OpBetween    PredicateOp = "between"
	OpNotBetween PredicateOp = "not_between"
)

// Predicate is a column predicate.  For range predicates, Value is the low
// bound and High is the high bound.  Comparison predicates use Value.
type Predicate struct {
	Col   ColRef      `json:"col"`
	Op    PredicateOp `json:"op"`
	Value any         `json:"value,omitempty"`
	High  any         `json:"high,omitempty"`
}

// NotNull creates a col IS NOT NULL predicate.
func NotNull(col ColRef) Predicate { return Predicate{Col: col, Op: OpNotNull} }

// IsNull creates a col IS NULL predicate.
func IsNull(col ColRef) Predicate { return Predicate{Col: col, Op: OpIsNull} }

// Eq creates a col = value predicate.
func Eq(col ColRef, value any) Predicate { return Predicate{Col: col, Op: OpEq, Value: value} }

// Gt creates a col > value predicate.
func Gt(col ColRef, value any) Predicate { return Predicate{Col: col, Op: OpGt, Value: value} }

// Ge creates a col >= value predicate.
func Ge(col ColRef, value any) Predicate { return Predicate{Col: col, Op: OpGe, Value: value} }

// Lt creates a col < value predicate.
func Lt(col ColRef, value any) Predicate { return Predicate{Col: col, Op: OpLt, Value: value} }

// Le creates a col <= value predicate.
func Le(col ColRef, value any) Predicate { return Predicate{Col: col, Op: OpLe, Value: value} }

// Between creates an inclusive constant range predicate.
func Between(col ColRef, low, high any) Predicate {
	return Predicate{Col: col, Op: OpBetween, Value: low, High: high}
}

// Equality is a base-table equi-join condition.
type Equality struct {
	Left  ColRef `json:"left"`
	Right ColRef `json:"right"`
}

// On constructs an equality condition.
func On(left, right ColRef) Equality { return Equality{Left: left, Right: right} }

// Node is a node in an acyclic join tree.
//
// For ScanNode, Table is the physical catalog table and Alias is the qualified
// name used by ColRef values.  Alias defaults to Table.
//
// For join nodes, On is evaluated as the join condition.  Where is a
// post-join/null-padding predicate attached to this logical node.  Putting a
// null-rejecting predicate in an inner Right subtree's Where is materially
// different from putting it in the outer query's Where.
type Node struct {
	Kind  JoinKind `json:"kind"`
	Alias string   `json:"alias,omitempty"`
	Table string   `json:"table,omitempty"`

	Left  *Node `json:"left,omitempty"`
	Right *Node `json:"right,omitempty"`

	On    []Equality  `json:"on,omitempty"`
	Where []Predicate `json:"where,omitempty"`
	// NullReject lists explicit IS NOT NULL predicates at this node.
	NullReject []ColRef `json:"null_reject,omitempty"`

	// Select is optional node-level output metadata.  Statement.Select is the
	// projected output set used by rewriting.
	Select []ColRef `json:"select,omitempty"`
}

// Scan constructs a leaf node.
func Scan(aliasOrTable string) *Node {
	return &Node{Kind: ScanNode, Alias: aliasOrTable, Table: aliasOrTable}
}

// ScanAs constructs a physical table scan with a distinct alias.
func ScanAs(table, alias string) *Node {
	return &Node{Kind: ScanNode, Alias: alias, Table: table}
}

// Join constructs an inner or left join node.
func Join(kind JoinKind, left, right *Node, on ...Equality) *Node {
	return &Node{Kind: kind, Left: left, Right: right, On: on}
}

// Inner constructs an inner join.
func Inner(left, right *Node, on ...Equality) *Node {
	return Join(InnerJoin, left, right, on...)
}

// Left constructs a left outer join.
func Left(left, right *Node, on ...Equality) *Node {
	return Join(LeftJoin, left, right, on...)
}

// Query is the original query specification.
type Query struct {
	ID     string   `json:"id,omitempty"`
	Schema *Catalog `json:"-"`
	Root   *Node    `json:"root"`

	// Where contains predicates applied above Root.  Predicates attached to
	// Root.Where are equivalent.
	Where      []Predicate `json:"where,omitempty"`
	NullReject []ColRef    `json:"null_reject,omitempty"`
	Select     []ColRef    `json:"select"`

	// Distinct defaults to true because this package decides set-semantic
	// lossless rewrites.  Set false only to evaluate the raw specification.
	Distinct *bool `json:"distinct,omitempty"`
}

// View is a precomputed materialized outer-join view specification.
type View struct {
	ID         string      `json:"id"`
	Root       *Node       `json:"root"`
	Where      []Predicate `json:"where,omitempty"`
	NullReject []ColRef    `json:"null_reject,omitempty"`
	Select     []ColRef    `json:"select"`
}

func (q *Query) distinct() bool {
	if q == nil || q.Distinct == nil {
		return true
	}
	return *q.Distinct
}

// DNF is a disjunctive normal form compensation filter.  The zero value is not
// a valid "accept all" representation; use NoFilter.  Semantics:
// OR over Alternatives, where each inner list is AND-ed.
type DNF struct {
	Alternatives [][]Predicate `json:"alternatives"`
}

// NoFilter returns an accept-all DNF.
func NoFilter() DNF { return DNF{Alternatives: [][]Predicate{{}}} }

// All returns a conjunction filter.
func All(preds ...Predicate) DNF { return DNF{Alternatives: [][]Predicate{preds}} }

// AnyOf returns a DNF from conjunctive alternatives.
func AnyOf(alternatives ...[]Predicate) DNF { return DNF{Alternatives: alternatives} }

// Empty reports whether f is nil or accept-all.
func (f DNF) Empty() bool {
	return len(f.Alternatives) == 1 && len(f.Alternatives[0]) == 0
}

// SQL renders a conservative SQL-like Boolean expression.
func (f DNF) SQL() string {
	if len(f.Alternatives) == 0 {
		return "FALSE"
	}
	parts := make([]string, 0, len(f.Alternatives))
	for _, conj := range f.Alternatives {
		if len(conj) == 0 {
			return "TRUE"
		}
		ps := make([]string, 0, len(conj))
		for _, p := range conj {
			ps = append(ps, p.SQL())
		}
		parts = append(parts, "("+strings.Join(ps, " AND ")+")")
	}
	return strings.Join(parts, " OR ")
}

// SQL renders one predicate.
func (p Predicate) SQL() string {
	switch p.Op {
	case OpIsNull:
		return p.Col.String() + " IS NULL"
	case OpNotNull:
		return p.Col.String() + " IS NOT NULL"
	case OpBetween:
		return fmt.Sprintf("%s BETWEEN %v AND %v", p.Col, p.Value, p.High)
	case OpNotBetween:
		return fmt.Sprintf("%s NOT BETWEEN %v AND %v", p.Col, p.Value, p.High)
	default:
		return fmt.Sprintf("%s %s %v", p.Col, p.Op, p.Value)
	}
}

// Compensation is the filter and projection needed on top of a view.
type Compensation struct {
	// Filter is the serializable compensation condition in DNF.
	Filter DNF `json:"filter"`
	// Project is the dedup projection.  It equals Query.Select for identity
	// mappings.
	Project []ColRef `json:"project"`
	// SQL is Filter.SQL retained for convenient display.
	SQL string `json:"sql"`
	// Discriminators lists columns used to distinguish real rows from padded
	// subtrees.
	Discriminators []ColRef `json:"discriminators,omitempty"`
}

// ReasonCode gives a machine-readable explanation for a decision.
type ReasonCode string

const (
	ReasonRewritable           ReasonCode = "rewritable"
	ReasonMissingOutput        ReasonCode = "missing_output_column"
	ReasonMissingDiscriminator ReasonCode = "missing_null_discriminator"
	ReasonViewLosesRows        ReasonCode = "view_loses_query_rows"
	ReasonCompensationMismatch ReasonCode = "compensation_mismatch"
	ReasonInvalidSpec          ReasonCode = "invalid_spec"
	ReasonNoCandidate          ReasonCode = "no_compensation_candidate"
)

// Decision is one view's analysis result.
type Decision struct {
	ViewID                string          `json:"view_id"`
	Rewritable            bool            `json:"rewritable"`
	Compensation          *Compensation   `json:"compensation,omitempty"`
	AttemptedCompensation *Compensation   `json:"attempted_compensation,omitempty"`
	Code                  ReasonCode      `json:"code"`
	Reason                string          `json:"reason"`
	Counterexample        *Counterexample `json:"counterexample,omitempty"`
	RawCounterexample     *Counterexample `json:"raw_counterexample,omitempty"`
	Ambiguity             *AmbiguityProof `json:"ambiguity,omitempty"`

	// ExhaustiveCheck, when non-nil, reports the finite semantic search used
	// to validate or refute the selected compensation.
	ExhaustiveCheck *ExhaustiveReport `json:"exhaustive_check,omitempty"`
}

// Report is the result of analyzing all views.
type Report struct {
	QueryID   string     `json:"query_id"`
	Decisions []Decision `json:"decisions"`
}

// Rewritable returns only successful decisions.
func (r *Report) Rewritable() []Decision {
	if r == nil {
		return nil
	}
	out := make([]Decision, 0, len(r.Decisions))
	for _, d := range r.Decisions {
		if d.Rewritable {
			out = append(out, d)
		}
	}
	return out
}

// IDs returns rewritable view IDs in input order.
func (r *Report) IDs() []string {
	ds := r.Rewritable()
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.ViewID)
	}
	return out
}

func sortedRefs(refs []ColRef) []ColRef {
	out := append([]ColRef(nil), refs...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return out[i].Column < out[j].Column
	})
	return out
}
