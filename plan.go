package phyprop

import (
	"fmt"
	"strings"
)

// Node is a physical plan operator. Each operator declares the physical
// properties it requires from each child, and the properties it delivers
// on its own output (assuming its requirements are met).
type Node interface {
	fmt.Stringer
	// Children returns the node's inputs, in order.
	Children() []Node
	// SetChildren replaces the node's inputs (used by the enforcer).
	SetChildren(children []Node)
	// RequiredProps returns the order required from each child, parallel
	// to Children().
	RequiredProps() []Order
	// DeliveredProps returns the order this node guarantees on its output.
	DeliveredProps() Order
}

// node is embedded by concrete operators to provide child plumbing.
type node struct {
	children []Node
}

func (n *node) Children() []Node       { return n.children }
func (n *node) SetChildren(c []Node)   { n.children = c }
func (n *node) RequiredProps() []Order { return make([]Order, len(n.children)) }
func (n *node) DeliveredProps() Order  { return nil }

// --- Scans ---

// SeqScan reads a table with no guaranteed order.
type SeqScan struct {
	node
	Table string
}

func NewSeqScan(table string) *SeqScan { return &SeqScan{Table: table} }
func (s *SeqScan) String() string      { return fmt.Sprintf("SeqScan(%s)", s.Table) }

// IndexScan reads a table through an index, delivering rows in index order.
type IndexScan struct {
	node
	Table     string
	IndexCols Order
}

func NewIndexScan(table string, cols ...OrderColumn) *IndexScan {
	return &IndexScan{Table: table, IndexCols: Order(cols)}
}
func (s *IndexScan) String() string {
	return fmt.Sprintf("IndexScan(%s, index(%s))", s.Table, s.IndexCols.String())
}
func (s *IndexScan) DeliveredProps() Order { return s.IndexCols }

// --- Enforcer ---

// Sort is the physical enforcer inserted when a child's delivered order
// does not satisfy a requirement.
type Sort struct {
	node
	Required Order
}

func NewSort(required Order, child Node) *Sort {
	s := &Sort{Required: required}
	s.children = []Node{child}
	return s
}
func (s *Sort) String() string        { return fmt.Sprintf("Sort(%s)", s.Required.String()) }
func (s *Sort) DeliveredProps() Order { return s.Required }

// --- Aggregates ---

// StreamAgg requires its input sorted on the grouping columns and
// preserves that order on its output.
type StreamAgg struct {
	node
	GroupBy Order
}

func NewStreamAgg(groupBy Order, child Node) *StreamAgg {
	a := &StreamAgg{GroupBy: groupBy}
	a.children = []Node{child}
	return a
}
func (a *StreamAgg) String() string         { return fmt.Sprintf("StreamAgg(group by %s)", a.GroupBy.String()) }
func (a *StreamAgg) RequiredProps() []Order { return []Order{a.GroupBy} }
func (a *StreamAgg) DeliveredProps() Order  { return a.GroupBy }

// HashAgg requires no order and destroys any input order.
type HashAgg struct {
	node
	GroupBy []string
}

func NewHashAgg(groupBy []string, child Node) *HashAgg {
	a := &HashAgg{GroupBy: groupBy}
	a.children = []Node{child}
	return a
}
func (a *HashAgg) String() string {
	return fmt.Sprintf("HashAgg(group by %s)", strings.Join(a.GroupBy, ", "))
}

// --- Joins ---

// MergeJoin requires both inputs sorted on their join keys and preserves
// the left input's order on its output.
type MergeJoin struct {
	node
	LeftKeys  Order
	RightKeys Order
}

func NewMergeJoin(leftKeys, rightKeys Order, left, right Node) *MergeJoin {
	j := &MergeJoin{LeftKeys: leftKeys, RightKeys: rightKeys}
	j.children = []Node{left, right}
	return j
}
func (j *MergeJoin) String() string {
	return fmt.Sprintf("MergeJoin(%s = %s)", j.LeftKeys.String(), j.RightKeys.String())
}
func (j *MergeJoin) RequiredProps() []Order { return []Order{j.LeftKeys, j.RightKeys} }
func (j *MergeJoin) DeliveredProps() Order  { return j.LeftKeys }

// HashJoin requires no order and delivers none.
type HashJoin struct {
	node
	Cond string
}

func NewHashJoin(cond string, left, right Node) *HashJoin {
	j := &HashJoin{Cond: cond}
	j.children = []Node{left, right}
	return j
}
func (j *HashJoin) String() string { return fmt.Sprintf("HashJoin(%s)", j.Cond) }

// --- Pass-through operators ---

// Project passes its child's order through unchanged.
type Project struct {
	node
	Exprs []string
}

func NewProject(exprs []string, child Node) *Project {
	p := &Project{Exprs: exprs}
	p.children = []Node{child}
	return p
}
func (p *Project) String() string        { return fmt.Sprintf("Project(%s)", strings.Join(p.Exprs, ", ")) }
func (p *Project) DeliveredProps() Order { return p.children[0].DeliveredProps() }

// Limit passes its child's order through unchanged.
type Limit struct {
	node
	N int
}

func NewLimit(n int, child Node) *Limit {
	l := &Limit{N: n}
	l.children = []Node{child}
	return l
}
func (l *Limit) String() string        { return fmt.Sprintf("Limit(%d)", l.N) }
func (l *Limit) DeliveredProps() Order { return l.children[0].DeliveredProps() }
