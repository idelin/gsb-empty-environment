package physprop

import (
	"fmt"
	"math"
	"strings"
)

// PhysicalPlan is a node in the physical (executable) plan tree. Every
// node reports the ordering it guarantees on its output rows, so that a
// parent can decide whether a required physical property is already
// satisfied or must be enforced with a Sort operator.
type PhysicalPlan interface {
	// Ordering is the physical order this operator guarantees on its
	// output (nil/empty means unordered).
	Ordering() Order
	Cost() float64
	Rows() float64
	Children() []PhysicalPlan
	fmt.Stringer
}

// SeqScan is a full table scan. It provides no ordering.
type SeqScan struct {
	Table string
	NRows float64
}

func NewSeqScan(s *Scan) *SeqScan { return &SeqScan{Table: s.Table, NRows: s.NRows} }

func (s *SeqScan) Ordering() Order          { return nil }
func (s *SeqScan) Cost() float64            { return s.NRows }
func (s *SeqScan) Rows() float64            { return s.NRows }
func (s *SeqScan) Children() []PhysicalPlan { return nil }
func (s *SeqScan) String() string           { return fmt.Sprintf("SeqScan(%s)", s.Table) }

// IndexScan scans a table through an index and therefore provides the
// index's column ordering for free.
type IndexScan struct {
	Table string
	Index Index
	NRows float64
}

func NewIndexScan(s *Scan) *IndexScan {
	return &IndexScan{Table: s.Table, Index: *s.Index, NRows: s.NRows}
}

func (s *IndexScan) Ordering() Order          { return Order(s.Index.Columns) }
func (s *IndexScan) Cost() float64            { return s.NRows*0.05 + 1 }
func (s *IndexScan) Rows() float64            { return s.NRows }
func (s *IndexScan) Children() []PhysicalPlan { return nil }
func (s *IndexScan) String() string {
	return fmt.Sprintf("IndexScan(%s using %s)", s.Table, s.Index.Name)
}

// Sort is the physical enforcer for the ordering property. It is inserted
// only when no cheaper plan already delivers the required order.
type Sort struct {
	Keys  Order
	Child PhysicalPlan
}

func NewSort(keys Order, child PhysicalPlan) *Sort { return &Sort{Keys: keys, Child: child} }

func (s *Sort) Ordering() Order          { return s.Keys }
func (s *Sort) Rows() float64            { return s.Child.Rows() }
func (s *Sort) Children() []PhysicalPlan { return []PhysicalPlan{s.Child} }
func (s *Sort) Cost() float64 {
	// External sort: O(n log n) with a hefty constant.
	return s.Child.Cost() + s.Child.Rows()*math.Log2(s.Child.Rows()+1)*2
}
func (s *Sort) String() string {
	return fmt.Sprintf("Sort(%s)", strings.Join(s.Keys, ", "))
}

// HashAggregate destroys any input ordering.
type HashAggregate struct {
	Keys  []string
	Child PhysicalPlan
}

func NewHashAggregate(keys []string, child PhysicalPlan) *HashAggregate {
	return &HashAggregate{Keys: keys, Child: child}
}

func (a *HashAggregate) Ordering() Order          { return nil }
func (a *HashAggregate) Cost() float64            { return a.Child.Cost() + a.Child.Rows() }
func (a *HashAggregate) Rows() float64            { return math.Max(a.Child.Rows()/10, 1) }
func (a *HashAggregate) Children() []PhysicalPlan { return []PhysicalPlan{a.Child} }
func (a *HashAggregate) String() string {
	return fmt.Sprintf("HashAggregate(keys: %s)", strings.Join(a.Keys, ", "))
}

// StreamAggregate aggregates a pre-sorted input and preserves the
// grouping-key ordering on its output.
type StreamAggregate struct {
	Keys  []string
	Child PhysicalPlan
}

func NewStreamAggregate(keys []string, child PhysicalPlan) *StreamAggregate {
	return &StreamAggregate{Keys: keys, Child: child}
}

func (a *StreamAggregate) Ordering() Order          { return Order(a.Keys) }
func (a *StreamAggregate) Cost() float64            { return a.Child.Cost() + a.Child.Rows()*0.1 }
func (a *StreamAggregate) Rows() float64            { return math.Max(a.Child.Rows()/10, 1) }
func (a *StreamAggregate) Children() []PhysicalPlan { return []PhysicalPlan{a.Child} }
func (a *StreamAggregate) String() string {
	return fmt.Sprintf("StreamAggregate(keys: %s)", strings.Join(a.Keys, ", "))
}

// MergeJoin requires both inputs ordered on the join keys and preserves
// the left (outer) input's ordering on its output.
type MergeJoin struct {
	LeftKeys  Order
	RightKeys Order
	Left      PhysicalPlan
	Right     PhysicalPlan
}

func NewMergeJoin(leftKeys, rightKeys Order, left, right PhysicalPlan) *MergeJoin {
	return &MergeJoin{LeftKeys: leftKeys, RightKeys: rightKeys, Left: left, Right: right}
}

func (j *MergeJoin) Ordering() Order          { return j.LeftKeys }
func (j *MergeJoin) Rows() float64            { return math.Max(j.Left.Rows(), j.Right.Rows()) }
func (j *MergeJoin) Children() []PhysicalPlan { return []PhysicalPlan{j.Left, j.Right} }
func (j *MergeJoin) Cost() float64 {
	return j.Left.Cost() + j.Right.Cost() + (j.Left.Rows()+j.Right.Rows())*0.1
}
func (j *MergeJoin) String() string {
	return fmt.Sprintf("MergeJoin(%s = %s)",
		strings.Join(j.LeftKeys, ", "), strings.Join(j.RightKeys, ", "))
}

// Format renders a physical plan as an indented tree annotated with the
// ordering each operator provides.
func Format(p PhysicalPlan) string {
	var b strings.Builder
	format(&b, p, 0)
	return b.String()
}

func format(b *strings.Builder, p PhysicalPlan, depth int) {
	b.WriteString(strings.Repeat("  ", depth))
	b.WriteString(p.String())
	if ord := p.Ordering(); len(ord) > 0 {
		fmt.Fprintf(b, "  [order: %s]", strings.Join(ord, ", "))
	}
	fmt.Fprintf(b, "  (cost=%.1f rows=%.0f)\n", p.Cost(), p.Rows())
	for _, c := range p.Children() {
		format(b, c, depth+1)
	}
}

// ContainsSort reports whether any Sort enforcer exists in the plan tree.
func ContainsSort(p PhysicalPlan) bool {
	if _, ok := p.(*Sort); ok {
		return true
	}
	for _, c := range p.Children() {
		if ContainsSort(c) {
			return true
		}
	}
	return false
}
