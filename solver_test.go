package netsec

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"testing"
)

func cell(r, c int) Cell {
	return Cell{Row: r, Col: c}
}

func orthogonalGrid(rows, cols, length int) *RoadGraph {
	g := NewRoadGraph()
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			if c+1 < cols {
				_ = g.AddUndirected(cell(r, c), cell(r, c+1), length)
			}
			if r+1 < rows {
				_ = g.AddUndirected(cell(r, c), cell(r+1, c), length)
			}
		}
	}
	return g
}

// TestRelocation3x3 shows that locally allowed candidates can be eliminated by
// global motion continuity: at t=3 three cells are published, but only H lies
// on any complete feasible trajectory (E arrives too early; F cannot reach the
// known final cell once the F-I link is severed).
func TestRelocation3x3(t *testing.T) {
	cells := []Cell{
		cell(0, 0), cell(0, 1), cell(0, 2),
		cell(1, 0), cell(1, 1), cell(1, 2),
		cell(2, 0), cell(2, 1), cell(2, 2),
	}
	A, B, D, E, F, H, I := cells[0], cells[1], cells[3], cells[4], cells[5], cells[7], cells[8]
	g := orthogonalGrid(3, 3, 1)
	// Sever the direct approach to the known final cell from one t=3 candidate.
	g.RemoveUndirected(F, I)

	prob := Problem{
		Allowed: [][]Cell{
			{A},
			{A, B, D},
			{B, D, E},
			{E, F, H},
			{I},
		},
		Graph:          g,
		MaxSpeed:       1,
		MaxSpeedChange: 1,
		TotalLength:    Interval{Min: 4, Max: 4},
	}

	solver, err := NewSolver(prob)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	support, exposed, err := solver.Solve()
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	wantSupport := [][]Cell{
		{A},
		{B, D},
		{E},
		{H},
		{I},
	}
	if !reflect.DeepEqual(support, wantSupport) {
		t.Fatalf("support mismatch:\n got %v\nwant %v", support, wantSupport)
	}

	// The local candidate count is 3 at t=3, but the support size is 1.
	if got := len(prob.Allowed[3]); got != 3 {
		t.Fatalf("sanity: local candidates at t=3 = %d, want 3", got)
	}
	if got := len(support[3]); got != 1 {
		t.Fatalf("support at t=3 = %d, want 1 (candidates E and F are re-identified out)", got)
	}

	wantRanges := []Range{{Start: 0, End: 1}, {Start: 2, End: 5}}
	if !reflect.DeepEqual(exposed, wantRanges) {
		t.Fatalf("unique ranges = %v, want %v", exposed, wantRanges)
	}

	paths, enumErr := EnumerateTrajectories(prob, 0)
	if enumErr != nil {
		t.Fatalf("enumeration: %v", enumErr)
	}
	if len(paths) != 2 {
		t.Fatalf("feasible trajectories = %d, want 2", len(paths))
	}
	enumSupport, enumRanges := SupportFromTrajectories(paths)
	if !reflect.DeepEqual(enumSupport, wantSupport) || !reflect.DeepEqual(enumRanges, wantRanges) {
		t.Fatalf("enumeration cross-check mismatch:\n support %v ranges %v", enumSupport, enumRanges)
	}
}

// TestSpeedChangeCap forces constant speed via MaxSpeedChange == 0 and checks
// that only walks moving at every step satisfy the exact total length.
func TestSpeedChangeCap(t *testing.T) {
	l0 := cell(0, 0)
	r0 := cell(0, 1)
	l1 := cell(1, 0)
	r1 := cell(1, 1)
	all := []Cell{l0, r0, l1, r1}

	prob := Problem{
		Allowed: [][]Cell{
			{l0},
			all,
			all,
			all,
		},
		Graph:          orthogonalGrid(2, 2, 1),
		MaxSpeed:       1,
		MaxSpeedChange: 0,
		TotalLength:    Interval{Min: 3, Max: 3},
	}

	solver, err := NewSolver(prob)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	support, exposed, err := solver.Solve()
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	wantSupport := [][]Cell{
		{l0},
		{r0, l1},
		{l0, r1},
		{r0, l1},
	}
	if !reflect.DeepEqual(support, wantSupport) {
		t.Fatalf("support = %v, want %v", support, wantSupport)
	}
	wantRanges := []Range{{Start: 0, End: 1}}
	if !reflect.DeepEqual(exposed, wantRanges) {
		t.Fatalf("ranges = %v, want %v", exposed, wantRanges)
	}

	paths, enumErr := EnumerateTrajectories(prob, 0)
	if enumErr != nil {
		t.Fatalf("enumeration: %v", enumErr)
	}
	if len(paths) != 8 {
		t.Fatalf("constant-speed walks = %d, want 8", len(paths))
	}
	for _, path := range paths {
		for step := 1; step < len(path); step++ {
			if path[step] == path[step-1] {
				t.Fatalf("path %v contains a stay move, incompatible with exact length 3", path)
			}
		}
	}
	enumSupport, enumRanges := SupportFromTrajectories(paths)
	if !reflect.DeepEqual(enumSupport, wantSupport) || !reflect.DeepEqual(enumRanges, wantRanges) {
		t.Fatalf("enumeration cross-check mismatch: %v %v", enumSupport, enumRanges)
	}
}

func TestInfeasible(t *testing.T) {
	g := orthogonalGrid(3, 3, 1)
	prob := Problem{
		Allowed: [][]Cell{
			{cell(0, 0)},
			{cell(0, 1), cell(1, 0)},
			{cell(1, 1)},
		},
		Graph:          g,
		MaxSpeed:       1,
		MaxSpeedChange: 1,
		TotalLength:    Interval{Min: 9, Max: 9},
	}
	solver, err := NewSolver(prob)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	if _, _, err := solver.Solve(); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("err = %v, want ErrInfeasible", err)
	}
	paths, enumErr := EnumerateTrajectories(prob, 0)
	if enumErr != nil {
		t.Fatalf("enumeration: %v", enumErr)
	}
	if len(paths) != 0 {
		t.Fatalf("enumeration returned %d paths for an infeasible problem", len(paths))
	}
}

func TestSingleTimeStep(t *testing.T) {
	g := orthogonalGrid(2, 2, 1)
	prob := Problem{
		Allowed:        [][]Cell{{cell(0, 0), cell(1, 1)}},
		Graph:          g,
		MaxSpeed:       1,
		MaxSpeedChange: 0,
		TotalLength:    Interval{Min: 0, Max: 0},
	}
	solver, err := NewSolver(prob)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	support, ranges, err := solver.Solve()
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	want := [][]Cell{{cell(0, 0), cell(1, 1)}}
	if !reflect.DeepEqual(support, want) || len(ranges) != 0 {
		t.Fatalf("support=%v ranges=%v, want %v / none", support, ranges, want)
	}
}

func TestBudgetedAdvance(t *testing.T) {
	g := orthogonalGrid(3, 3, 1)
	prob := Problem{
		Allowed: [][]Cell{
			{cell(0, 0)},
			{cell(0, 1), cell(1, 0)},
			{cell(0, 2), cell(1, 1), cell(2, 0)},
			{cell(2, 1), cell(1, 2)},
			{cell(2, 2)},
		},
		Graph:          g,
		MaxSpeed:       1,
		MaxSpeedChange: 1,
		TotalLength:    Interval{Min: 4, Max: 4},
	}

	full, err := NewSolver(prob)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	wantSupport, wantRanges, err := full.Solve()
	if err != nil {
		t.Fatalf("reference Solve: %v", err)
	}

	stepwise, err := NewSolver(prob)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	if pending, done := stepwise.Advance(0); done || !reflect.DeepEqual(pending, []Range{{Start: 1, End: 5}}) {
		t.Fatalf("zero-budget Advance = (%v, %v), want ([1,5), false)", pending, done)
	}
	if pending := stepwise.Pending(); !reflect.DeepEqual(pending, []Range{{Start: 1, End: 5}}) {
		t.Fatalf("Pending = %v, want [1,5)", pending)
	}

	totalUnits := 0
	for {
		pending, done := stepwise.Advance(1)
		totalUnits++
		if done {
			if pending != nil {
				t.Fatalf("pending must be nil when done, got %v", pending)
			}
			break
		}
		if len(pending) != 1 || pending[0].Start < 0 || pending[0].Start >= pending[0].End ||
			pending[0].End > 5 {
			t.Fatalf("malformed pending range: %v", pending)
		}
		if totalUnits > 100000 {
			t.Fatal("solver did not converge under unit budgets")
		}
	}
	if !reflect.DeepEqual(stepwise.Supports(), wantSupport) {
		t.Fatalf("budgeted support mismatch:\n got %v\nwant %v", stepwise.Supports(), wantSupport)
	}
	if !reflect.DeepEqual(stepwise.UniqueRanges(), wantRanges) {
		t.Fatalf("budgeted ranges = %v, want %v", stepwise.UniqueRanges(), wantRanges)
	}
	if stepwise.Err() != nil {
		t.Fatalf("unexpected error: %v", stepwise.Err())
	}
}

func TestInvalidProblems(t *testing.T) {
	g := orthogonalGrid(2, 2, 1)
	cases := map[string]Problem{
		"nil graph": {
			Allowed:     [][]Cell{{cell(0, 0)}},
			TotalLength: Interval{Min: 0, Max: 0},
		},
		"no time steps": {
			Graph:       g,
			TotalLength: Interval{Min: 0, Max: 0},
		},
		"empty allowed set": {
			Allowed:     [][]Cell{{cell(0, 0)}, {}},
			Graph:       g,
			TotalLength: Interval{Min: 0, Max: 0},
		},
		"bad interval": {
			Allowed:     [][]Cell{{cell(0, 0)}},
			Graph:       g,
			TotalLength: Interval{Min: 3, Max: 1},
		},
		"negative change bound": {
			Allowed:        [][]Cell{{cell(0, 0)}},
			Graph:          g,
			MaxSpeedChange: -1,
			TotalLength:    Interval{Min: 0, Max: 0},
		},
	}
	for name, prob := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSolver(prob); !errors.Is(err, ErrInvalidProblem) {
				t.Fatalf("err = %v, want ErrInvalidProblem", err)
			}
			if _, err := EnumerateTrajectories(prob, 0); !errors.Is(err, ErrInvalidProblem) {
				t.Fatalf("enumeration err = %v, want ErrInvalidProblem", err)
			}
		})
	}
	if err := g.AddEdge(cell(0, 0), cell(0, 1), -1); !errors.Is(err, ErrInvalidProblem) {
		t.Fatalf("AddEdge err = %v, want ErrInvalidProblem", err)
	}
}

func TestRandomSmallGrids(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 99))
	for iter := 0; iter < 200; iter++ {
		rows := 1 + rng.IntN(3)
		cols := 1 + rng.IntN(3)
		timeCount := 1 + rng.IntN(5)
		g := NewRoadGraph()
		allCells := make([]Cell, 0, rows*cols)
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				allCells = append(allCells, cell(r, c))
			}
		}
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				if c+1 < cols && rng.IntN(2) == 0 {
					_ = g.AddUndirected(cell(r, c), cell(r, c+1), 1)
				}
				if r+1 < rows && rng.IntN(2) == 0 {
					_ = g.AddUndirected(cell(r, c), cell(r+1, c), 1)
				}
				if r+1 < rows && c+1 < cols && rng.IntN(3) == 0 {
					_ = g.AddUndirected(cell(r, c), cell(r+1, c+1), 2)
				}
			}
		}
		// Guarantee at least the stay move remains usable everywhere.
		allowed := make([][]Cell, timeCount)
		for step := range allowed {
			k := 1 + rng.IntN(len(allCells))
			perm := rng.Perm(len(allCells))[:k]
			chosen := make([]Cell, k)
			for i, idx := range perm {
				chosen[i] = allCells[idx]
			}
			allowed[step] = chosen
		}
		maxSpeed := rng.IntN(3) // 0 disables the cap
		maxChange := rng.IntN(3)
		hi := timeCount * 2
		lo := rng.IntN(hi + 1)
		prob := Problem{
			Allowed:        allowed,
			Graph:          g,
			MaxSpeed:       maxSpeed,
			MaxSpeedChange: maxChange,
			TotalLength:    Interval{Min: lo, Max: hi},
		}

		solver, err := NewSolver(prob)
		if err != nil {
			t.Fatalf("iter %d: NewSolver: %v", iter, err)
		}
		dpSupport, dpRanges, dpErr := solver.Solve()

		paths, enumErr := EnumerateTrajectories(prob, 10000)
		if errors.Is(enumErr, ErrEnumerationLimit) {
			continue
		}
		if enumErr != nil {
			t.Fatalf("iter %d: enumeration: %v", iter, enumErr)
		}
		if len(paths) == 0 {
			if !errors.Is(dpErr, ErrInfeasible) {
				t.Fatalf("iter %d: enumeration found no paths but DP err = %v", iter, dpErr)
			}
			continue
		}
		if dpErr != nil {
			t.Fatalf("iter %d: DP reported %v but enumeration found %d paths", iter, dpErr, len(paths))
		}
		enumSupport, enumRanges := SupportFromTrajectories(paths)
		if !reflect.DeepEqual(dpSupport, enumSupport) {
			t.Fatalf("iter %d support mismatch:\n dp   %v\nenum %v", iter, dpSupport, enumSupport)
		}
		if !reflect.DeepEqual(dpRanges, enumRanges) {
			t.Fatalf("iter %d ranges mismatch: dp %v enum %v", iter, dpRanges, enumRanges)
		}
		// Local allowed-set sizes must never be reported as the support size.
		for step := range allowed {
			if len(dpSupport[step]) > len(allowed[step]) {
				t.Fatalf("iter %d: support larger than allowed set at t=%d", iter, step)
			}
		}
	}
}
