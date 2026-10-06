// Package tablegen constructs contingency tables and flat records that
// satisfy two categorical marginal counts plus forbidden (structural-zero)
// combinations. Infeasible specifications yield a Hall-style certificate.
package tablegen

import (
	"errors"
	"fmt"
)

// MaxTotal caps the number of records a Spec may request.
const MaxTotal = 100

// Cell identifies one row/column combination.
type Cell struct {
	Row, Col int
}

// Spec describes the desired contingency table: RowTotals and ColTotals are
// the marginal counts of the two categorical dimensions, and Forbidden marks
// cells that must remain zero.
type Spec struct {
	RowTotals []int
	ColTotals []int
	Forbidden map[Cell]bool
}

// Table is a feasible contingency table; Table[i][j] is the record count
// for row category i and column category j.
type Table [][]int

// Evidence explains why a Spec is infeasible. Rows is a set of row
// categories whose combined Demand exceeds the combined Supply of the
// column categories (Cols) they are allowed to use.
type Evidence struct {
	Reason string
	Rows   []int
	Cols   []int
	Demand int
	Supply int
}

func (e *Evidence) Error() string {
	return fmt.Sprintf("tablegen: infeasible: %s (rows %v demand %d, usable cols %v supply %d)",
		e.Reason, e.Rows, e.Demand, e.Cols, e.Supply)
}

// ErrTotalTooLarge is returned when the requested record count exceeds MaxTotal.
var ErrTotalTooLarge = errors.New("tablegen: total records exceed MaxTotal")

func (s Spec) rowSum() int {
	sum := 0
	for _, v := range s.RowTotals {
		sum += v
	}
	return sum
}

func (s Spec) colSum() int {
	sum := 0
	for _, v := range s.ColTotals {
		sum += v
	}
	return sum
}

func (s Spec) forbiddenAt(i, j int) bool {
	return s.Forbidden[Cell{Row: i, Col: j}]
}

// validate checks structural properties that do not require a flow solve.
func (s Spec) validate() error {
	for i, v := range s.RowTotals {
		if v < 0 {
			return fmt.Errorf("tablegen: negative row total at index %d", i)
		}
	}
	for j, v := range s.ColTotals {
		if v < 0 {
			return fmt.Errorf("tablegen: negative column total at index %d", j)
		}
	}
	for c := range s.Forbidden {
		if c.Row < 0 || c.Row >= len(s.RowTotals) || c.Col < 0 || c.Col >= len(s.ColTotals) {
			return fmt.Errorf("tablegen: forbidden cell %v out of range", c)
		}
	}
	if s.rowSum() != s.colSum() {
		return &Evidence{
			Reason: "row and column marginals sum to different totals",
			Demand: s.rowSum(),
			Supply: s.colSum(),
		}
	}
	if s.rowSum() > MaxTotal {
		return ErrTotalTooLarge
	}
	return nil
}

// Solve returns a contingency table meeting the marginals and structural
// zeros, or an *Evidence (or other error) describing why none exists.
func Solve(s Spec) (Table, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	rows, cols := len(s.RowTotals), len(s.ColTotals)
	total := s.rowSum()

	// Node layout: 0 = source, 1..rows = rows, rows+1..rows+cols = cols, last = sink.
	sink := rows + cols + 1
	f := newFlowNet(sink + 1)
	for i := 0; i < rows; i++ {
		f.addEdge(0, 1+i, s.RowTotals[i])
	}
	for j := 0; j < cols; j++ {
		f.addEdge(1+rows+j, sink, s.ColTotals[j])
	}
	// cellEdge[i][j] indexes the forward edge for cell (i, j), or -1.
	cellEdge := make([][]int, rows)
	for i := range cellEdge {
		cellEdge[i] = make([]int, cols)
		for j := range cellEdge[i] {
			cellEdge[i][j] = -1
		}
	}
	for i := 0; i < rows; i++ {
		for j := 0; j < cols; j++ {
			if !s.forbiddenAt(i, j) {
				cellEdge[i][j] = f.addEdge(1+i, 1+rows+j, total)
			}
		}
	}

	if f.maxFlow(0, sink) != total {
		return nil, s.certificate(f, rows, cols)
	}

	t := make(Table, rows)
	for i := range t {
		t[i] = make([]int, cols)
		for j := range t[i] {
			if e := cellEdge[i][j]; e >= 0 {
				t[i][j] = f.flowOn(e)
			}
		}
	}
	return t, nil
}

// certificate extracts a Hall-style infeasibility witness from the residual
// graph of a non-saturating max flow.
func (s Spec) certificate(f *flowNet, rows, cols int) *Evidence {
	reach := f.reachableFrom(0)
	ev := &Evidence{Reason: "row demand exceeds capacity of usable columns"}
	for i := 0; i < rows; i++ {
		if reach[1+i] {
			ev.Rows = append(ev.Rows, i)
			ev.Demand += s.RowTotals[i]
		}
	}
	for j := 0; j < cols; j++ {
		if reach[1+rows+j] {
			ev.Cols = append(ev.Cols, j)
			ev.Supply += s.ColTotals[j]
		}
	}
	return ev
}
