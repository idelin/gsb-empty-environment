package physprop

// Order is a required or provided physical ordering: a list of column
// references, e.g. ["u.id"]. The empty Order means "no requirement".
type Order []string

// Satisfies reports whether the provided ordering satisfies the required
// one. A provided ordering satisfies a requirement when the required
// columns form a prefix of the provided columns.
func Satisfies(provided, required Order) bool {
	if len(required) == 0 {
		return true
	}
	if len(provided) < len(required) {
		return false
	}
	for i := range required {
		if provided[i] != required[i] {
			return false
		}
	}
	return true
}

// LogicalPlan is a node in the logical (relational algebra) plan tree.
type LogicalPlan interface{ logicalNode() }

// Index describes an available index on a table. The index columns define
// the ordering an IndexScan can provide.
type Index struct {
	Name    string
	Columns []string
}

// Scan is a logical table scan. Index is nil when no usable index exists.
type Scan struct {
	Table string
	NRows float64
	Index *Index
}

func (*Scan) logicalNode() {}

// LogicalSort is ORDER BY: it requires its output to be ordered by Keys.
type LogicalSort struct {
	Keys  Order
	Child LogicalPlan
}

func (*LogicalSort) logicalNode() {}

// GroupBy is logical aggregation. A sorted-input (stream) aggregate
// requires its input ordered by Keys; a hash aggregate does not.
type GroupBy struct {
	Keys  []string
	Child LogicalPlan
}

func (*GroupBy) logicalNode() {}

// LogicalMergeJoin joins two inputs on equality keys. It requires the left
// input ordered by LeftKeys and the right input ordered by RightKeys, and
// preserves the left input's ordering on its output.
type LogicalMergeJoin struct {
	LeftKeys  Order
	RightKeys Order
	Left      LogicalPlan
	Right     LogicalPlan
}

func (*LogicalMergeJoin) logicalNode() {}
