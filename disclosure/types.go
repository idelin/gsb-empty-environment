package disclosure

// Cell bounds one grid cell: its actual value and the inclusive interval of
// values the cell may take.
type Cell struct {
	Actual int64
	Min    int64
	Max    int64
}

// Table is the protected source grid. Rows and columns each have length at
// most four, every row has the same length, and every cell is non-negative.
type Table struct {
	Cells [][]Cell
}

// Term is one coefficient/cell pair inside a linear constraint. Row and Col
// are zero-based cell coordinates.
type Term struct {
	Row   int
	Col   int
	Coeff int64
}

// Constraint is a linear equality sum(Term[i].Coeff * cell(Term[i].Row,
// Term[i].Col)) == RHS. It models a row total, column total or an optional
// subtotal. GroupID is empty for constraints that are always published;
// otherwise the constraint is published exactly when its group is published.
type Constraint struct {
	ID      string
	GroupID string
	Terms   []Term
	RHS     int64
}

// SuppressionGroup declares that all candidate summaries carrying GroupID are
// released together or hidden together. Cost is charged when the group is
// hidden.
type SuppressionGroup struct {
	ID   string
	Cost int64
}

// SensitiveCell names a cell that must keep at least K distinct feasible
// integer values under every approved publication.
type SensitiveCell struct {
	Row int
	Col int
	K   int
}

// Request is the input of a disclosure review.
type Request struct {
	Table       Table
	Constraints []Constraint
	Groups      []SuppressionGroup
	Sensitive   []SensitiveCell

	// Budget caps the total hiding cost of an approved decision. Zero means no
	// budget limit.
	Budget int64

	// MaxEnumNodes bounds the number of partial assignments the enumerator may
	// explore for one publication set. Zero uses the package default. The
	// enumerator fails closed: hitting the cap rejects the request.
	MaxEnumNodes int64
}

// Witness is a complete grid of cell values that satisfies every published
// constraint and every cell bound, offered as evidence that Value is feasible
// for the sensitive cell.
type Witness struct {
	Value int64
	Cells [][]int64
}

// Exclusion is a re-checkable reason why one value of a sensitive cell is not
// feasible under the published constraints.
type Exclusion struct {
	Value  int64
	Reason string
}

// CellReport is the per-sensitive-cell result for the publication set that was
// selected (or, on rejection, for the set with every group hidden).
type CellReport struct {
	Row               int
	Col               int
	K                 int
	FeasibleValues    []int64
	Witnesses         []Witness
	Exclusions        []Exclusion
	ExclusionsLimited bool
	Protected         bool
}

// Decision is the output of Review.
type Decision struct {
	// Approved is true exactly when a within-budget publication set protects
	// every sensitive cell.
	Approved bool

	HiddenGroupIDs []string
	PublishedIDs   []string
	TotalCost      int64
	Budget         int64

	// RejectedReason is empty for an approved decision. Otherwise it carries a
	// re-checkable explanation, including budget exhaustion and source tables
	// that violate an always-published constraint.
	RejectedReason string

	// InconsistentConstraintID is set when the actual table violates an
	// always-published constraint.
	InconsistentConstraintID string

	Reports []CellReport

	EnumNodes int64
	EnumLimit bool
}

// compiled holds a validated request together with lookup tables used by the
// enumerator.
type compiled struct {
	req        *Request
	rows, cols int
	cellMin    []int64
	cellMax    []int64
	actual     []int64

	groups       []SuppressionGroup // sorted by ID for deterministic search
	groupIndex   map[string]int
	groupCost    []int64
	constraints  []Constraint
	constraintOf []int // group index of each constraint, -1 when always public
	termRows     [][]int
	termCols     [][]int
	termCoeffs   [][]int64
	termRHS      []int64

	sensitive []SensitiveCell
	sensIdx   []int
	enumCap   int64
}

const defaultEnumCap int64 = 2_000_000

// maxExclusionsPerCell keeps rejection payloads bounded; each remaining
// exclusion is still covered by the full enumeration and the limit flag.
const maxExclusionsPerCell = 1024
