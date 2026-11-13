package netsec

import "errors"

// ErrCannotRetract signals that an aggregate cannot invert a removal; the
// caller must rebuild the partition with a full refresh.
var ErrCannotRetract = errors.New("netsec: aggregate cannot retract contribution")

const (
	minSentinel = int64(1<<63 - 1)
	maxSentinel = int64(-1 << 63)
)

// groupAccum holds the monoid state of one group.
type groupAccum struct {
	kinds    []AggKind
	state    []int64
	rowCount int64
	sample   Row
}

func newGroupAccum(kinds []AggKind) *groupAccum {
	g := &groupAccum{kinds: kinds, state: make([]int64, len(kinds))}
	for i, k := range kinds {
		switch k {
		case AggMin:
			g.state[i] = minSentinel
		case AggMax:
			g.state[i] = maxSentinel
		}
	}
	return g
}

// add folds one visible row into the group.
func (g *groupAccum) add(view *ViewDef, row Row) error {
	if g.sample == nil {
		g.sample = row
	}
	g.rowCount++
	for i, out := range view.Projection.Outputs {
		if g.kinds[i] == "" {
			continue
		}
		agg := out.Expr.(AggExpr)
		v, err := evalExpr(agg.Arg, row)
		if err != nil {
			return err
		}
		switch g.kinds[i] {
		case AggSum:
			g.state[i] += v
		case AggCount:
			g.state[i]++
		case AggMin:
			if v < g.state[i] {
				g.state[i] = v
			}
		case AggMax:
			if v > g.state[i] {
				g.state[i] = v
			}
		}
	}
	return nil
}

// retract removes the contribution of one previously visible row. It returns
// ErrCannotRetract for MIN/MAX, forcing a full-refresh fallback.
func (g *groupAccum) retract(view *ViewDef, row Row) error {
	g.rowCount--
	for i, out := range view.Projection.Outputs {
		if g.kinds[i] == "" {
			continue
		}
		agg := out.Expr.(AggExpr)
		v, err := evalExpr(agg.Arg, row)
		if err != nil {
			return err
		}
		switch g.kinds[i] {
		case AggSum:
			g.state[i] -= v
		case AggCount:
			g.state[i]--
		default:
			return ErrCannotRetract
		}
	}
	return nil
}

// finalize renders the group into a materialized result row.
func (g *groupAccum) finalize(view *ViewDef, key GroupKey) (ResultRow, error) {
	values := make(map[string]int64, len(view.Projection.Outputs))
	for i, out := range view.Projection.Outputs {
		if g.kinds[i] != "" {
			v := g.state[i]
			if g.rowCount == 0 && (g.kinds[i] == AggMin || g.kinds[i] == AggMax) {
				v = 0
			}
			values[out.Name] = v
			continue
		}
		v, err := evalExpr(out.Expr, g.sample)
		if err != nil {
			return ResultRow{}, err
		}
		values[out.Name] = v
	}
	return ResultRow{Group: key, Values: values}, nil
}
