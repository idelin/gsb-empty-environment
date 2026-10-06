// Package contab constructs two-dimensional contingency tables (and the
// underlying records) from integer marginal counts over two categorical
// dimensions, honouring forbidden (structural-zero) cell combinations.
//
// Feasibility is decided with a max-flow formulation of the transportation
// problem. When no table exists, Solve returns a Gale-Hoffman style
// certificate: a subset of row categories whose combined count exceeds the
// combined count of every column category they are allowed to use.
package contab

import (
	"errors"
	"fmt"
	"sort"
)

// Cell identifies one combination of the two categorical dimensions.
type Cell struct {
	Row int
	Col int
}

// Spec describes one table-construction problem.
//
// RowCounts[i] is the required number of records whose first dimension
// equals category i; ColCounts[j] the same for the second dimension.
// Forbidden lists combinations that must remain zero (structural zeros).
type Spec struct {
	RowCounts []int
	ColCounts []int
	Forbidden []Cell
}

// Evidence explains why a Spec is infeasible. It is a checkable certificate:
// either the two margins disagree in total, or the listed row subset demands
// more records than its allowed columns can supply.
type Evidence struct {
	Reason string
	// Rows is the violating row subset (nil for a totals mismatch).
	Rows []int
	// Demand is the combined row count of Rows.
	Demand int
	// Supply is the combined column count available to Rows.
	Supply int
}

func (e *Evidence) Error() string {
	if e.Rows == nil {
		return fmt.Sprintf("infeasible: %s (row total %d, column total %d)", e.Reason, e.Demand, e.Supply)
	}
	return fmt.Sprintf("infeasible: %s (rows %v demand %d, allowed columns supply %d)", e.Reason, e.Rows, e.Demand, e.Supply)
}

// Validate reports malformed input (negative counts, out-of-range or
// duplicate forbidden cells). Infeasibility of well-formed input is not an
// error; it is reported through Evidence.
func (s Spec) Validate() error {
	if len(s.RowCounts) == 0 || len(s.ColCounts) == 0 {
		return errors.New("contab: need at least one row and one column category")
	}
	for i, c := range s.RowCounts {
		if c < 0 {
			return fmt.Errorf("contab: negative row count at %d", i)
		}
	}
	for j, c := range s.ColCounts {
		if c < 0 {
			return fmt.Errorf("contab: negative column count at %d", j)
		}
	}
	seen := make(map[Cell]bool, len(s.Forbidden))
	for _, f := range s.Forbidden {
		if f.Row < 0 || f.Row >= len(s.RowCounts) || f.Col < 0 || f.Col >= len(s.ColCounts) {
			return fmt.Errorf("contab: forbidden cell %v out of range", f)
		}
		if seen[f] {
			return fmt.Errorf("contab: duplicate forbidden cell %v", f)
		}
		seen[f] = true
	}
	return nil
}

// Total returns the number of records implied by the row margin.
func (s Spec) Total() int {
	t := 0
	for _, c := range s.RowCounts {
		t += c
	}
	return t
}

// Solve constructs a table meeting both margins with zeros on all forbidden
// cells. On success it returns the table and a nil Evidence. On failure the
// returned Evidence certifies infeasibility.
func Solve(s Spec) (table [][]int, ev *Evidence, err error) {
	if err := s.Validate(); err != nil {
		return nil, nil, err
	}
	rowTotal := s.Total()
	colTotal := 0
	for _, c := range s.ColCounts {
		colTotal += c
	}
	if rowTotal != colTotal {
		return nil, &Evidence{
			Reason: "row and column margins have different totals",
			Demand: rowTotal,
			Supply: colTotal,
		}, nil
	}

	nr, nc := len(s.RowCounts), len(s.ColCounts)
	forbidden := make(map[Cell]bool, len(s.Forbidden))
	for _, f := range s.Forbidden {
		forbidden[f] = true
	}

	// Flow network: source -> rows -> columns -> sink.
	f := newFlow(2 + nr + nc)
	source, sink := 0, 1+nr+nc
	colNode := func(j int) int { return 1 + nr + j }
	for i, c := range s.RowCounts {
		f.addEdge(source, 1+i, c)
	}
	for j, c := range s.ColCounts {
		f.addEdge(colNode(j), sink, c)
	}
	rowEdge := make([][]int, nr) // edge index of the row->col arc, -1 if forbidden
	for i := range rowEdge {
		rowEdge[i] = make([]int, nc)
		for j := 0; j < nc; j++ {
			if forbidden[Cell{i, j}] {
				rowEdge[i][j] = -1
				continue
			}
			rowEdge[i][j] = f.addEdge(1+i, colNode(j), rowTotal)
		}
	}

	if got := f.maxFlow(source, sink); got != rowTotal {
		return nil, shortageEvidence(f, s, source, forbidden), nil
	}

	table = make([][]int, nr)
	for i := range table {
		table[i] = make([]int, nc)
		for j := 0; j < nc; j++ {
			if e := rowEdge[i][j]; e >= 0 {
				table[i][j] = f.edgeFlow(e)
			}
		}
	}
	return table, nil, nil
}

// shortageEvidence derives a Gale-Hoffman certificate from the residual
// graph: the rows still reachable from the source demand more than every
// column they may use can supply.
func shortageEvidence(f *flow, s Spec, source int, forbidden map[Cell]bool) *Evidence {
	nr, nc := len(s.RowCounts), len(s.ColCounts)
	reach := f.reachable(source)
	rows := []int{}
	demand := 0
	for i := 0; i < nr; i++ {
		if reach[1+i] {
			rows = append(rows, i)
			demand += s.RowCounts[i]
		}
	}
	supply := 0
	for j := 0; j < nc; j++ {
		usable := false
		for _, i := range rows {
			if !forbidden[Cell{i, j}] {
				usable = true
				break
			}
		}
		if usable {
			supply += s.ColCounts[j]
		}
	}
	return &Evidence{
		Reason: "row subset exceeds the capacity of its allowed columns",
		Rows:   rows,
		Demand: demand,
		Supply: supply,
	}
}

// Verify checks that table meets both margins and keeps forbidden cells at
// zero. It is the row/column conservation check behind Generate.
func Verify(s Spec, table [][]int) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if len(table) != len(s.RowCounts) {
		return fmt.Errorf("contab: table has %d rows, want %d", len(table), len(s.RowCounts))
	}
	forbidden := make(map[Cell]bool, len(s.Forbidden))
	for _, f := range s.Forbidden {
		forbidden[f] = true
	}
	for i, row := range table {
		if len(row) != len(s.ColCounts) {
			return fmt.Errorf("contab: table row %d has %d columns, want %d", i, len(row), len(s.ColCounts))
		}
		sum := 0
		for j, v := range row {
			if v < 0 {
				return fmt.Errorf("contab: negative entry at (%d,%d)", i, j)
			}
			if v > 0 && forbidden[Cell{i, j}] {
				return fmt.Errorf("contab: forbidden cell (%d,%d) holds %d", i, j, v)
			}
			sum += v
		}
		if sum != s.RowCounts[i] {
			return fmt.Errorf("contab: row %d sums to %d, want %d", i, sum, s.RowCounts[i])
		}
	}
	for j := range s.ColCounts {
		sum := 0
		for i := range table {
			sum += table[i][j]
		}
		if sum != s.ColCounts[j] {
			return fmt.Errorf("contab: column %d sums to %d, want %d", j, sum, s.ColCounts[j])
		}
	}
	return nil
}

// Record is one generated observation: the two category indices it belongs to.
type Record struct {
	Row int
	Col int
}

// Generate solves spec and expands the table into exactly spec.Total()
// records, each record counting once toward its row and its column quota.
// If shuffle is true the record order is randomised with rng (a nil rng uses
// a fixed seed); shuffling never changes the per-cell quotas. It returns a
// nil slice and the Evidence when spec is infeasible.
func Generate(spec Spec, shuffle bool, rng randSource) ([]Record, *Evidence, error) {
	table, ev, err := Solve(spec)
	if err != nil || ev != nil {
		return nil, ev, err
	}
	recs := make([]Record, 0, spec.Total())
	for i, row := range table {
		for j, n := range row {
			for k := 0; k < n; k++ {
				recs = append(recs, Record{Row: i, Col: j})
			}
		}
	}
	if shuffle {
		shuffleRecords(recs, rng)
	}
	return recs, nil, nil
}

// Quotas tallies records back into a table, the inverse of Generate's
// expansion. Comparing Quotas before and after a shuffle shows order is the
// only thing that changed.
func Quotas(spec Spec, recs []Record) ([][]int, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	table := make([][]int, len(spec.RowCounts))
	for i := range table {
		table[i] = make([]int, len(spec.ColCounts))
	}
	for k, r := range recs {
		if r.Row < 0 || r.Row >= len(spec.RowCounts) || r.Col < 0 || r.Col >= len(spec.ColCounts) {
			return nil, fmt.Errorf("contab: record %d out of range: %v", k, r)
		}
		table[r.Row][r.Col]++
	}
	return table, nil
}

// SortedEvidenceRows returns a copy of the evidence row subset in ascending
// order, handy for deterministic comparisons in tests and logs.
func (e *Evidence) SortedEvidenceRows() []int {
	if e.Rows == nil {
		return nil
	}
	out := append([]int(nil), e.Rows...)
	sort.Ints(out)
	return out
}
