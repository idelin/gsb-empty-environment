package disclosure

import (
	"slices"
	"strings"
	"testing"
)

// Independent small-domain enumerator.
//
// This brute-force walker deliberately shares no pruning or propagation code
// with the package enumerator. It materializes every assignment of the bounded
// cells (at most 16) and re-evaluates every published equality from scratch,
// so it acts as an independent oracle for the package results.

type oracleResult struct {
	values  map[int]map[int64]struct{}
	witness map[int]map[int64][]int64
	tables  [][]int64
	nodes   int64
}

func oracleEnumerate(c *compiled, published []bool) oracleResult {
	n := c.rows * c.cols
	out := oracleResult{
		values:  make(map[int]map[int64]struct{}),
		witness: make(map[int]map[int64][]int64),
	}
	for _, idx := range c.sensIdx {
		out.values[idx] = make(map[int64]struct{})
		out.witness[idx] = make(map[int64][]int64)
	}

	assignment := make([]int64, n)
	var walk func(pos int)
	walk = func(pos int) {
		if pos == n {
			out.nodes++
			if !oracleSatisfies(c, published, assignment) {
				return
			}
			table := append([]int64(nil), assignment...)
			out.tables = append(out.tables, table)
			for _, idx := range c.sensIdx {
				value := assignment[idx]
				out.values[idx][value] = struct{}{}
				if _, seen := out.witness[idx][value]; !seen {
					out.witness[idx][value] = table
				}
			}
			return
		}
		for value := c.cellMin[pos]; value <= c.cellMax[pos]; value++ {
			assignment[pos] = value
			walk(pos + 1)
			if value == c.cellMax[pos] {
				break
			}
		}
	}
	walk(0)
	return out
}

func oracleSatisfies(c *compiled, published []bool, vals []int64) bool {
	for ci := range c.constraints {
		if !published[ci] {
			continue
		}
		var sum int64
		for j, coeff := range c.termCoeffs[ci] {
			idx := c.termRows[ci][j]*c.cols + c.termCols[ci][j]
			s, ok := mulAdd(sum, coeff, vals[idx])
			if !ok {
				return false
			}
			sum = s
		}
		if sum != c.termRHS[ci] {
			return false
		}
	}
	return true
}

func grid(actual, minVals, maxVals [][]int64) [][]Cell {
	out := make([][]Cell, len(actual))
	for r := range actual {
		out[r] = make([]Cell, len(actual[r]))
		for col := range actual[r] {
			out[r][col] = Cell{Actual: actual[r][col], Min: minVals[r][col], Max: maxVals[r][col]}
		}
	}
	return out
}

func rowTerms(row, cols int) []Term {
	terms := make([]Term, cols)
	for col := 0; col < cols; col++ {
		terms[col] = Term{Row: row, Col: col, Coeff: 1}
	}
	return terms
}

func colTerms(rows, col int) []Term {
	terms := make([]Term, rows)
	for row := 0; row < rows; row++ {
		terms[row] = Term{Row: row, Col: col, Coeff: 1}
	}
	return terms
}

func marginConstraints(rows, cols int, groupID string, rowSum, colSum []int64) []Constraint {
	out := make([]Constraint, 0, rows+cols)
	for row := 0; row < rows; row++ {
		out = append(out, Constraint{
			ID:      "row" + string(rune('0'+row)),
			GroupID: groupID,
			Terms:   rowTerms(row, cols),
			RHS:     rowSum[row],
		})
	}
	for col := 0; col < cols; col++ {
		out = append(out, Constraint{
			ID:      "col" + string(rune('0'+col)),
			GroupID: groupID,
			Terms:   colTerms(rows, col),
			RHS:     colSum[col],
		})
	}
	return out
}

func flatten(grid [][]int64) []int64 {
	out := make([]int64, 0, len(grid)*len(grid[0]))
	for _, row := range grid {
		out = append(out, row...)
	}
	return out
}

func withinBounds(c *compiled, vals []int64) bool {
	for idx, value := range vals {
		if value < c.cellMin[idx] || value > c.cellMax[idx] {
			return false
		}
	}
	return true
}

func exclusionValues(report CellReport) []int64 {
	out := make([]int64, 0, len(report.Exclusions))
	for _, exclusion := range report.Exclusions {
		out = append(out, exclusion.Value)
	}
	return out
}

// crossCheckAgainstOracle runs the independent enumerator over the published
// set named in the decision and asserts value sets, gaps and witnesses match.
func crossCheckAgainstOracle(t *testing.T, c *compiled, publishedIDs []string, reports []CellReport) {
	t.Helper()
	published := make([]bool, len(c.constraints))
	known := make(map[string]bool, len(publishedIDs))
	for _, id := range publishedIDs {
		known[id] = true
	}
	for i, con := range c.constraints {
		if con.ID != "" && known[con.ID] {
			published[i] = true
		}
	}
	crossCheckPublished(t, c, published, reports)
}

func crossCheckPublished(t *testing.T, c *compiled, published []bool, reports []CellReport) {
	t.Helper()
	oracle := oracleEnumerate(c, published)

	for _, report := range reports {
		idx := report.Row*c.cols + report.Col
		oracleValues := sortedValues(oracle.values[idx])
		if !slices.Equal(oracleValues, report.FeasibleValues) {
			t.Fatalf("cell (%d,%d): oracle values %v != reported %v", report.Row, report.Col, oracleValues, report.FeasibleValues)
		}
		if len(report.Witnesses) != len(report.FeasibleValues) {
			t.Fatalf("cell (%d,%d): missing witnesses", report.Row, report.Col)
		}
		for _, witness := range report.Witnesses {
			flat := flatten(witness.Cells)
			if flat[idx] != witness.Value || !oracleSatisfies(c, published, flat) || !withinBounds(c, flat) {
				t.Fatalf("witness for %d is not a valid complete table", witness.Value)
			}
			if oracle.witness[idx][witness.Value] == nil {
				t.Fatalf("oracle does not recognize value %d", witness.Value)
			}
		}
		for _, exclusion := range report.Exclusions {
			if _, feasible := oracle.values[idx][exclusion.Value]; feasible {
				t.Fatalf("value %d wrongly excluded at cell (%d,%d)", exclusion.Value, report.Row, report.Col)
			}
			if exclusion.Reason == "" {
				t.Fatalf("exclusion of %d has no re-checkable reason", exclusion.Value)
			}
		}
	}
}

// Scenario 1: two reports that are each marginally safe jointly pin a cell.
func TestTwoMarginallySafeReportsJointlyLeak(t *testing.T) {
	actual := [][]int64{{4, 6}, {6, 4}}
	minVals := [][]int64{{3, 5}, {5, 3}}
	maxVals := [][]int64{{5, 7}, {7, 5}}

	constraints := marginConstraints(2, 2, "margins", []int64{10, 10}, []int64{10, 10})
	constraints = append(constraints, Constraint{
		ID:      "extra",
		GroupID: "extra",
		Terms:   []Term{{0, 0, 1}, {0, 1, 1}, {1, 0, 1}},
		RHS:     16,
	})

	req := &Request{
		Table:       Table{Cells: grid(actual, minVals, maxVals)},
		Constraints: constraints,
		Groups: []SuppressionGroup{
			{ID: "extra", Cost: 5},
			{ID: "margins", Cost: 10},
		},
		Sensitive: []SensitiveCell{{Row: 0, Col: 0, K: 2}},
	}

	c, err := validate(req)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	marginOnly := c.publishedFor(1 << c.groupIndex["margins"])
	extraOnly := c.publishedFor(1 << c.groupIndex["extra"])
	noneHidden := c.publishedFor(0)

	if got := len(oracleEnumerate(c, marginOnly).values[0]); got != 3 {
		t.Fatalf("margins alone: want 3 feasible values, got %d", got)
	}
	if got := len(oracleEnumerate(c, extraOnly).values[0]); got != 3 {
		t.Fatalf("extra alone: want 3 feasible values, got %d", got)
	}
	if got := len(oracleEnumerate(c, noneHidden).values[0]); got != 1 {
		t.Fatalf("joint publication: want 1 feasible value, got %d", got)
	}

	decision, err := Review(req)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !decision.Approved {
		t.Fatalf("want approval after hiding one report, got rejection: %s", decision.RejectedReason)
	}
	if got, want := decision.HiddenGroupIDs, []string{"extra"}; !slices.Equal(got, want) {
		t.Fatalf("hidden groups: want %v (cheapest), got %v", want, got)
	}
	report := decision.Reports[0]
	if !report.Protected || len(report.FeasibleValues) != 3 {
		t.Fatalf("protected report: want 3 feasible values, got %d", len(report.FeasibleValues))
	}
	crossCheckAgainstOracle(t, c, decision.PublishedIDs, decision.Reports)
}

// Scenario 2: subtotal constraints carve a gap in the middle of an interval.
func TestSubtotalsCreateGap(t *testing.T) {
	actual := [][]int64{
		{0, 0, 0},
		{3, 0, 3},
		{0, 3, 0},
	}
	minVals := [][]int64{
		{0, 0, 0},
		{0, 0, 0},
		{0, 0, 0},
	}
	maxVals := [][]int64{
		{3, 3, 3},
		{4, 3, 3},
		{4, 3, 3},
	}

	req := &Request{
		Table: Table{Cells: grid(actual, minVals, maxVals)},
		Constraints: []Constraint{
			// Always published: this non-totally-unimodular subtotal creates
			// the {0, 2} gap and cannot be switched off by any group choice.
			{ID: "weighted", Terms: []Term{{0, 0, 3}, {1, 0, 2}}, RHS: 6},
			// Hidable subtotal. Even when it is hidden, the gap above remains.
			{ID: "extra", GroupID: "subtotals", Terms: []Term{{0, 1, 1}, {0, 2, 1}}, RHS: 0},
		},
		Groups:    []SuppressionGroup{{ID: "subtotals", Cost: 5}},
		Sensitive: []SensitiveCell{{Row: 0, Col: 0, K: 3}},
	}

	c, err := validate(req)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	oracle := oracleEnumerate(c, c.publishedFor(0))
	if got := sortedValues(oracle.values[c.sensIdx[0]]); !slices.Equal(got, []int64{0, 2}) {
		t.Fatalf("oracle feasible values: want [0 2] with a gap, got %v", got)
	}

	decision, err := Review(req)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if decision.Approved {
		t.Fatal("want rejection when the gap leaves fewer than K values")
	}
	report := decision.Reports[0]
	if !slices.Equal(report.FeasibleValues, []int64{0, 2}) {
		t.Fatalf("feasible values: want [0 2], got %v", report.FeasibleValues)
	}
	if !slices.Contains(exclusionValues(report), 1) {
		t.Fatalf("value 1 must be reported as excluded, got %+v", report.Exclusions)
	}
	for _, exclusion := range report.Exclusions {
		if exclusion.Value == 1 {
			// The gap is a genuinely non-convex hole: no single bound interval
			// proves it, so the reason must be an independently re-runnable
			// exhaustive proof rather than an empty string.
			if !strings.Contains(exclusion.Reason, "exhaustive enumeration") {
				t.Fatalf("gap exclusion must carry an exhaustive-search reason, got: %s", exclusion.Reason)
			}
		}
	}
	crossCheckAgainstOracle(t, c, decision.PublishedIDs, decision.Reports)

	req.Sensitive[0].K = 2
	okDecision, err := Review(req)
	if err != nil {
		t.Fatalf("review k=2: %v", err)
	}
	if !okDecision.Approved {
		t.Fatalf("want approval for K=2, got: %s", okDecision.RejectedReason)
	}
	if len(okDecision.HiddenGroupIDs) != 0 {
		t.Fatalf("want no hidden groups, got %v", okDecision.HiddenGroupIDs)
	}
}

// Scenario 3: the actual table violates an always-published constraint.
func TestActualTableViolatesPublicConstraint(t *testing.T) {
	actual := [][]int64{{4, 6}, {6, 4}}
	minVals := [][]int64{{3, 5}, {5, 3}}
	maxVals := [][]int64{{5, 7}, {7, 5}}

	req := &Request{
		Table: Table{Cells: grid(actual, minVals, maxVals)},
		Constraints: []Constraint{
			{ID: "bad-row", Terms: rowTerms(0, 2), RHS: 11},
		},
		Sensitive: []SensitiveCell{{Row: 0, Col: 0, K: 2}},
	}
	decision, err := Review(req)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if decision.Approved {
		t.Fatal("want rejection for inconsistent actual table")
	}
	if decision.InconsistentConstraintID != "bad-row" {
		t.Fatalf("want bad-row flagged, got %q", decision.InconsistentConstraintID)
	}
	if decision.RejectedReason == "" {
		t.Fatal("want a re-checkable rejection reason")
	}
}

// Scenario 4: hiding the last group still leaks the protected cell.
func TestHidingLastGroupStillLeaks(t *testing.T) {
	actual := [][]int64{{2, 2}, {2, 2}}
	minVals := [][]int64{{2, 2}, {2, 2}}
	maxVals := [][]int64{{2, 2}, {2, 2}}

	req := &Request{
		Table: Table{Cells: grid(actual, minVals, maxVals)},
		Constraints: []Constraint{
			{ID: "r0", GroupID: "only", Terms: rowTerms(0, 2), RHS: 4},
			{ID: "c0", GroupID: "only", Terms: colTerms(2, 0), RHS: 4},
		},
		Groups:    []SuppressionGroup{{ID: "only", Cost: 1}},
		Sensitive: []SensitiveCell{{Row: 0, Col: 0, K: 2}},
	}
	decision, err := Review(req)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if decision.Approved {
		t.Fatal("want rejection: tight bounds pin the cell even after hiding the only group")
	}
	report := decision.Reports[0]
	if report.Protected || len(report.FeasibleValues) != 1 {
		t.Fatalf("want exactly one pinned value, got %v", report.FeasibleValues)
	}
	if report.FeasibleValues[0] != 2 {
		t.Fatalf("want pinned value 2, got %d", report.FeasibleValues[0])
	}
	if decision.RejectedReason == "" {
		t.Fatal("want a re-checkable rejection reason")
	}

	c, err := validate(req)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	oracle := oracleEnumerate(c, c.publishedFor(1))
	if len(oracle.values[0]) != 1 {
		t.Fatalf("oracle: want one feasible table, got %d values", len(oracle.values[0]))
	}
	crossCheckAgainstOracle(t, c, decision.PublishedIDs, decision.Reports)
}

// Variant of scenario 4 with a wider bound: hiding every group still leaves
// only one feasible value, and every other bound value gets an exclusion.
func TestHidingLastGroupLeaksWithExclusions(t *testing.T) {
	actual := [][]int64{{2, 2}, {2, 2}}
	minVals := [][]int64{{1, 2}, {2, 2}}
	maxVals := [][]int64{{3, 2}, {2, 2}}

	req := &Request{
		Table: Table{Cells: grid(actual, minVals, maxVals)},
		Constraints: []Constraint{
			// Always public: survives group hiding and keeps the cell pinned.
			{ID: "c0", Terms: colTerms(2, 0), RHS: 4},
			{ID: "r0", GroupID: "only", Terms: rowTerms(0, 2), RHS: 4},
		},
		Groups:    []SuppressionGroup{{ID: "only", Cost: 1}},
		Sensitive: []SensitiveCell{{Row: 0, Col: 0, K: 2}},
	}
	decision, err := Review(req)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if decision.Approved {
		t.Fatal("want rejection when hiding the only group still pins the cell")
	}
	report := decision.Reports[0]
	if !slices.Equal(report.FeasibleValues, []int64{2}) {
		t.Fatalf("want pinned value [2], got %v", report.FeasibleValues)
	}
	if !slices.Equal(exclusionValues(report), []int64{1, 3}) {
		t.Fatalf("want exclusions [1 3], got %v", exclusionValues(report))
	}
	c, err := validate(req)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	crossCheckPublished(t, c, c.publishedFor(1), decision.Reports)
}

func TestBudgetExhaustionRejects(t *testing.T) {
	actual := [][]int64{{4, 6}, {6, 4}}
	minVals := [][]int64{{3, 5}, {5, 3}}
	maxVals := [][]int64{{5, 7}, {7, 5}}
	constraints := marginConstraints(2, 2, "margins", []int64{10, 10}, []int64{10, 10})
	constraints = append(constraints, Constraint{
		ID:      "extra",
		GroupID: "extra",
		Terms:   []Term{{0, 0, 1}, {0, 1, 1}, {1, 0, 1}},
		RHS:     16,
	})

	req := &Request{
		Table:       Table{Cells: grid(actual, minVals, maxVals)},
		Constraints: constraints,
		Groups: []SuppressionGroup{
			{ID: "extra", Cost: 5},
			{ID: "margins", Cost: 10},
		},
		Sensitive: []SensitiveCell{{Row: 0, Col: 0, K: 2}},
		Budget:    4,
	}
	decision, err := Review(req)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if decision.Approved {
		t.Fatal("want rejection when every protecting choice exceeds the budget")
	}
	if decision.RejectedReason == "" {
		t.Fatal("want a budget rejection reason")
	}
}

func TestEqualCostTieBreaksOnGroupIDSequence(t *testing.T) {
	actual := [][]int64{{4, 6}, {6, 4}}
	minVals := [][]int64{{3, 5}, {5, 3}}
	maxVals := [][]int64{{5, 7}, {7, 5}}
	constraints := marginConstraints(2, 2, "margins", []int64{10, 10}, []int64{10, 10})
	constraints = append(constraints, Constraint{
		ID:      "extra",
		GroupID: "extra",
		Terms:   []Term{{0, 0, 1}, {0, 1, 1}, {1, 0, 1}},
		RHS:     16,
	})

	req := &Request{
		Table:       Table{Cells: grid(actual, minVals, maxVals)},
		Constraints: constraints,
		Groups: []SuppressionGroup{
			{ID: "margins", Cost: 7},
			{ID: "extra", Cost: 7},
		},
		Sensitive: []SensitiveCell{{Row: 0, Col: 0, K: 2}},
	}
	decision, err := Review(req)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !decision.Approved {
		t.Fatalf("want approval, got: %s", decision.RejectedReason)
	}
	// Both single-group choices cost 7; ["extra"] is lexicographically smaller
	// than ["margins"].
	if got, want := decision.HiddenGroupIDs, []string{"extra"}; !slices.Equal(got, want) {
		t.Fatalf("tie-break: want %v, got %v", want, got)
	}
}

func TestEnumLimitFailsClosed(t *testing.T) {
	actual := [][]int64{{1, 1}, {1, 1}}
	minVals := [][]int64{{0, 0}, {0, 0}}
	maxVals := [][]int64{{1, 1}, {1, 1}}
	req := &Request{
		Table:        Table{Cells: grid(actual, minVals, maxVals)},
		Constraints:  []Constraint{{ID: "r", Terms: rowTerms(0, 2), RHS: 2}},
		Sensitive:    []SensitiveCell{{Row: 0, Col: 0, K: 2}},
		MaxEnumNodes: 1,
	}
	decision, err := Review(req)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if decision.Approved || !decision.EnumLimit {
		t.Fatalf("want fail-closed limit decision, got approved=%v limit=%v", decision.Approved, decision.EnumLimit)
	}
}

// A request without any suppression groups only has one publication set.
func TestNoGroupsApproveAndReject(t *testing.T) {
	actual := [][]int64{{1, 0}}
	minVals := [][]int64{{0, 0}}
	maxVals := [][]int64{{1, 1}}

	okReq := &Request{
		Table:       Table{Cells: grid(actual, minVals, maxVals)},
		Constraints: []Constraint{{ID: "r0", Terms: rowTerms(0, 2), RHS: 1}},
		Sensitive:   []SensitiveCell{{Row: 0, Col: 0, K: 2}},
	}
	okDecision, err := Review(okReq)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !okDecision.Approved {
		t.Fatalf("want approval, got: %s", okDecision.RejectedReason)
	}
	if len(okDecision.HiddenGroupIDs) != 0 || okDecision.TotalCost != 0 {
		t.Fatalf("want zero-cost empty hidden set, got %v cost %d", okDecision.HiddenGroupIDs, okDecision.TotalCost)
	}

	badReq := &Request{
		Table:       Table{Cells: grid(actual, minVals, maxVals)},
		Constraints: []Constraint{{ID: "r0", Terms: rowTerms(0, 2), RHS: 1}},
		Sensitive:   []SensitiveCell{{Row: 0, Col: 0, K: 3}},
	}
	badDecision, err := Review(badReq)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if badDecision.Approved {
		t.Fatal("want rejection when the only publication set cannot reach K")
	}
}

func TestMalformedRequestsRejected(t *testing.T) {
	if _, err := Review(nil); err == nil {
		t.Fatal("nil request must error")
	}
	actual := [][]int64{{1, 1}, {1, 1}}
	minVals := [][]int64{{0, 0}, {0, 0}}
	maxVals := [][]int64{{1, 1}, {1, 1}}
	base := func() *Request {
		return &Request{
			Table:       Table{Cells: grid(actual, minVals, maxVals)},
			Constraints: []Constraint{{ID: "r0", Terms: rowTerms(0, 2), RHS: 2}},
			Sensitive:   []SensitiveCell{{Row: 0, Col: 0, K: 2}},
		}
	}

	negBudget := base()
	negBudget.Budget = -1
	if _, err := Review(negBudget); err == nil {
		t.Fatal("negative budget must error")
	}

	badK := base()
	badK.Sensitive[0].K = 0
	if _, err := Review(badK); err == nil {
		t.Fatal("K=0 must error")
	}

	unknownGroup := base()
	unknownGroup.Constraints[0].GroupID = "ghost"
	if _, err := Review(unknownGroup); err == nil {
		t.Fatal("unknown group reference must error")
	}

	bigTable := base()
	bigTable.Table.Cells = make([][]Cell, 5)
	for r := range bigTable.Table.Cells {
		bigTable.Table.Cells[r] = []Cell{{}, {}}
	}
	if _, err := Review(bigTable); err == nil {
		t.Fatal("5-row table must error")
	}
}

// Negative coefficients model differences and must reverse the bound
// contribution during propagation.
func TestNegativeCoefficientConstraint(t *testing.T) {
	actual := [][]int64{{5, 2}}
	minVals := [][]int64{{0, 0}}
	maxVals := [][]int64{{9, 9}}
	req := &Request{
		Table: Table{Cells: grid(actual, minVals, maxVals)},
		// x00 - x01 == 3 with both cells in [0,9].
		Constraints: []Constraint{
			{Terms: []Term{{0, 0, 1}, {0, 1, -1}}, RHS: 3},
		},
		Sensitive: []SensitiveCell{{Row: 0, Col: 0, K: 2}},
	}
	decision, err := Review(req)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !decision.Approved {
		t.Fatalf("want approval, got: %s", decision.RejectedReason)
	}
	report := decision.Reports[0]
	// x00 = x01 + 3 with x01 in [0,9], so x00 ranges over [3,9].
	if got, want := report.FeasibleValues, []int64{3, 4, 5, 6, 7, 8, 9}; !slices.Equal(got, want) {
		t.Fatalf("feasible values: want %v, got %v", want, got)
	}
	if got := exclusionValues(report); !slices.Equal(got, []int64{0, 1, 2}) {
		t.Fatalf("exclusions: want [0 1 2], got %v", got)
	}
	if !strings.Contains(report.Exclusions[0].Reason, "RHS") {
		t.Fatalf("propagation exclusion must cite the constraint RHS, got: %s", report.Exclusions[0].Reason)
	}
	c, err := validate(req)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	crossCheckPublished(t, c, c.publishedFor(0), decision.Reports)
}
