package windowframe

import "errors"

// FrameMode is the frame unit used by a window specification.
type FrameMode uint8

const (
	// RowsFrame counts physical rows for frame offsets.
	RowsFrame FrameMode = iota
	// RangeFrame bounds the frame by sort-key value ranges.
	RangeFrame
	// GroupsFrame counts distinct peer groups for frame offsets.
	GroupsFrame
)

// BoundKind identifies the shape of a frame boundary.
type BoundKind uint8

const (
	// BoundCurrentRow is CURRENT ROW.
	BoundCurrentRow BoundKind = iota
	// BoundPreceding is UNBOUNDED PRECEDING or value PRECEDING.
	BoundPreceding
	// BoundFollowing is UNBOUNDED FOLLOWING or value FOLLOWING.
	BoundFollowing
)

// Exclusion is the frame EXCLUDE clause.
type Exclusion uint8

const (
	// ExcludeNoOthers is EXCLUDE NO OTHERS (the default).
	ExcludeNoOthers Exclusion = iota
	// ExcludeCurrentRow removes only the current row.
	ExcludeCurrentRow
	// ExcludeGroup removes the current row and every peer of it.
	ExcludeGroup
	// ExcludeTies removes every peer of the current row except the row itself.
	ExcludeTies
)

// NullsOrder is the NULL placement of an ordering column.
type NullsOrder uint8

const (
	// NullsDefault applies the SQL default: NULLS LAST for ASC, NULLS FIRST
	// for DESC.
	NullsDefault NullsOrder = iota
	// NullsFirst places NULLs before every non-null key.
	NullsFirst
	// NullsLast places NULLs after every non-null key.
	NullsLast
)

// Direction is the sort direction of an ordering column.
type Direction uint8

const (
	// Asc is ascending order.
	Asc Direction = iota
	// Desc is descending order.
	Desc
)

// OrderSpec is one ORDER BY column.
type OrderSpec struct {
	Column    int
	Direction Direction
	Nulls     NullsOrder
}

// FrameBound is one end of a frame. Offset is consulted only for value
// PRECEDING / value FOLLOWING bounds and must be a non-negative number.
type FrameBound struct {
	Kind   BoundKind
	Offset Value
}

// UnboundedPreceding builds the UNBOUNDED PRECEDING boundary.
func UnboundedPreceding() FrameBound {
	return FrameBound{Kind: BoundPreceding}
}

// UnboundedFollowing builds the UNBOUNDED FOLLOWING boundary.
func UnboundedFollowing() FrameBound {
	return FrameBound{Kind: BoundFollowing}
}

// CurrentRow builds the CURRENT ROW boundary.
func CurrentRow() FrameBound {
	return FrameBound{Kind: BoundCurrentRow}
}

// Preceding builds a value PRECEDING boundary.
func Preceding(offset Value) FrameBound {
	return FrameBound{Kind: BoundPreceding, Offset: offset}
}

// Following builds a value FOLLOWING boundary.
func Following(offset Value) FrameBound {
	return FrameBound{Kind: BoundFollowing, Offset: offset}
}

func (b FrameBound) unbounded() bool {
	return b.Kind != BoundCurrentRow && b.Offset.IsNull()
}

// FrameSpec is the frame clause of a window.
type FrameSpec struct {
	Mode      FrameMode
	Start     FrameBound
	End       *FrameBound
	Exclusion Exclusion
}

// WindowSpec fully describes a window. Rows are partitioned by PartitionColumns,
// ordered by Order and framed by Frame. A nil Frame selects the SQL default
// frame for the ordering.
type WindowSpec struct {
	PartitionColumns []int
	Order            []OrderSpec
	Frame            *FrameSpec
}

// FuncKind identifies the window function to evaluate.
type FuncKind uint8

const (
	FuncSum FuncKind = iota
	FuncCount
	FuncAvg
	FuncMin
	FuncMax
	FuncFirstValue
	FuncLastValue
	FuncNthValue
	FuncRowNumber
	FuncRank
	FuncDenseRank
	FuncNtile
	FuncLag
	FuncLead
)

func (k FuncKind) String() string {
	switch k {
	case FuncSum:
		return "SUM"
	case FuncCount:
		return "COUNT"
	case FuncAvg:
		return "AVG"
	case FuncMin:
		return "MIN"
	case FuncMax:
		return "MAX"
	case FuncFirstValue:
		return "FIRST_VALUE"
	case FuncLastValue:
		return "LAST_VALUE"
	case FuncNthValue:
		return "NTH_VALUE"
	case FuncRowNumber:
		return "ROW_NUMBER"
	case FuncRank:
		return "RANK"
	case FuncDenseRank:
		return "DENSE_RANK"
	case FuncNtile:
		return "NTILE"
	case FuncLag:
		return "LAG"
	case FuncLead:
		return "LEAD"
	default:
		return "UNKNOWN"
	}
}

// FuncExpr is one window function invocation.
//
// Column is the value column for aggregates, FIRST_VALUE, LAST_VALUE and
// NTH_VALUE, or the target column for LAG/LEAD. The CountAll flag selects
// COUNT(*). N is the N of NTH_VALUE / NTILE and Offset the LAG/LEAD distance.
// Default is the LAG/LEAD out-of-window default (NULL when unset).
type FuncExpr struct {
	Func        FuncKind
	Column      int
	CountAll    bool
	N           int
	Offset      int
	Default     Value
	HasDefault  bool
	IgnoreNulls bool
}

var (
	errColumnIndex      = errors.New("windowframe: column index out of range")
	errNegativeOffset   = errors.New("windowframe: frame offset must not be negative")
	errNonIntegerOffset = errors.New("windowframe: ROWS/GROUPS offsets must be integers")
	errRangeNeedsOrder  = errors.New("windowframe: RANGE with a value or CURRENT ROW bound requires ORDER BY")
	errGroupsNeedsOrder = errors.New("windowframe: GROUPS requires ORDER BY")
	errRangeSingleOrder = errors.New("windowframe: RANGE value offsets require exactly one ORDER BY column")
	errRangeOffsetType  = errors.New("windowframe: RANGE offsets must be numeric")
	errNtileN           = errors.New("windowframe: NTILE argument must be at least 1")
	errNthN             = errors.New("windowframe: NTH_VALUE N must be at least 1")
)

// Validate checks the specification against the number of input columns. It
// mirrors the static frame-rule checks a SQL front end would perform.
func (w WindowSpec) Validate(numColumns int) error {
	validCol := func(c int) bool { return c >= 0 && c < numColumns }
	for _, c := range w.PartitionColumns {
		if !validCol(c) {
			return errColumnIndex
		}
	}
	for _, o := range w.Order {
		if !validCol(o.Column) {
			return errColumnIndex
		}
	}
	if w.Frame == nil {
		return nil
	}
	f := w.Frame
	if f.Mode == GroupsFrame && len(w.Order) == 0 {
		return errGroupsNeedsOrder
	}
	if err := f.Start.validate(f.Mode, len(w.Order), true); err != nil {
		return err
	}
	if f.End != nil {
		if err := f.End.validate(f.Mode, len(w.Order), false); err != nil {
			return err
		}
	}
	return nil
}

func (b FrameBound) validate(mode FrameMode, orderColumns int, start bool) error {
	if mode == RangeFrame && orderColumns == 0 {
		// Without ORDER BY, RANGE may only span the whole partition.
		if !(b.unbounded()) {
			return errRangeNeedsOrder
		}
		return nil
	}
	if b.Kind == BoundCurrentRow || b.Offset.IsNull() {
		return nil
	}
	num, ok := b.Offset.AsNumber()
	if !ok {
		return errRangeOffsetType
	}
	if num < 0 {
		return errNegativeOffset
	}
	switch mode {
	case RowsFrame, GroupsFrame:
		if b.Offset.kind != KindInt {
			return errNonIntegerOffset
		}
	case RangeFrame:
		if orderColumns != 1 {
			return errRangeSingleOrder
		}
	}
	_ = start
	return nil
}

// ValidateFunc checks a function expression against the number of columns.
func (e FuncExpr) Validate(numColumns int) error {
	switch e.Func {
	case FuncNtile:
		if e.N < 1 {
			return errNtileN
		}
	case FuncNthValue:
		if e.N < 1 {
			return errNthN
		}
	}
	if e.Func == FuncRowNumber || e.Func == FuncRank || e.Func == FuncDenseRank || e.Func == FuncNtile {
		return nil
	}
	if e.Func == FuncCount && e.CountAll {
		return nil
	}
	if e.Column < 0 || e.Column >= numColumns {
		return errColumnIndex
	}
	return nil
}
