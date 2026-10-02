// Package window implements vectorized window-function accumulators
// (SUM / AVG / RANK) over columnar, partition-ordered data.
package window

import "fmt"

// Mode selects the window frame semantics.
type Mode int

const (
	// Rows frames the window by physical row offsets.
	Rows Mode = iota
	// Range frames the window by logical offsets on the ORDER BY key.
	Range
)

// BoundKind identifies the kind of a frame boundary.
type BoundKind int

const (
	UnboundedPreceding BoundKind = iota
	Preceding
	CurrentRow
	Following
	UnboundedFollowing
)

// Bound is one side of a window frame. Offset is only meaningful for
// Preceding and Following: it is a row count under Rows mode and an
// ORDER BY key distance under Range mode.
type Bound struct {
	Kind   BoundKind
	Offset float64
}

// Frame describes a window frame such as
// "ROWS BETWEEN 10 PRECEDING AND CURRENT ROW".
type Frame struct {
	Mode  Mode
	Start Bound
	End   Bound
}

// boundOrder ranks boundary kinds so frame validity can be checked.
func boundOrder(k BoundKind) int {
	switch k {
	case UnboundedPreceding:
		return 0
	case Preceding:
		return 1
	case CurrentRow:
		return 2
	case Following:
		return 3
	case UnboundedFollowing:
		return 4
	}
	return -1
}

// validate checks the frame for SQL-like sanity.
func (f Frame) validate() error {
	if f.Mode != Rows && f.Mode != Range {
		return fmt.Errorf("window: unknown frame mode %d", f.Mode)
	}
	for _, b := range []Bound{f.Start, f.End} {
		if b.Kind < UnboundedPreceding || b.Kind > UnboundedFollowing {
			return fmt.Errorf("window: unknown bound kind %d", b.Kind)
		}
		if (b.Kind == Preceding || b.Kind == Following) && b.Offset < 0 {
			return fmt.Errorf("window: negative frame offset %v", b.Offset)
		}
	}
	if f.Start.Kind == UnboundedFollowing {
		return fmt.Errorf("window: frame start cannot be UNBOUNDED FOLLOWING")
	}
	if f.End.Kind == UnboundedPreceding {
		return fmt.Errorf("window: frame end cannot be UNBOUNDED PRECEDING")
	}
	if boundOrder(f.Start.Kind) > boundOrder(f.End.Kind) {
		return fmt.Errorf("window: frame start sorts after frame end")
	}
	if f.Start.Kind == f.End.Kind {
		switch f.Start.Kind {
		case Preceding:
			if f.Start.Offset < f.End.Offset {
				return fmt.Errorf("window: PRECEDING start offset %v is nearer than end offset %v",
					f.Start.Offset, f.End.Offset)
			}
		case Following:
			if f.Start.Offset > f.End.Offset {
				return fmt.Errorf("window: FOLLOWING start offset %v is beyond end offset %v",
					f.Start.Offset, f.End.Offset)
			}
		}
	}
	return nil
}
