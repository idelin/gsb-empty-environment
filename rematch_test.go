package rematch

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// bruteForce enumerates every assignment (each employee unassigned or
// matched to a free allowed position) and returns the optimal assignment
// vector under the lexicographic objectives:
//  1. maximize matched count
//  2. minimize changes (originally assigned employees whose position
//     changed, including becoming unassigned)
//  3. lexicographically smallest vector in employee order, where each entry
//     is the position index and unassigned counts as larger than any index.
func bruteForce(t *testing.T, p Problem) []int {
	t.Helper()
	n, m := len(p.Employees), len(p.Positions)
	posIdx := make(map[string]int, m)
	for j, pos := range p.Positions {
		posIdx[pos] = j
	}
	allowed := make([][]bool, n)
	for i, e := range p.Employees {
		allowed[i] = make([]bool, m)
		for pos, ok := range p.CanWork[e] {
			if ok {
				allowed[i][posIdx[pos]] = true
			}
		}
	}
	orig := make([]int, n)
	for i, e := range p.Employees {
		orig[i] = -1
		if pos, ok := p.Original[e]; ok {
			orig[i] = posIdx[pos]
		}
	}
	stats := func(v []int) (matched, changes int) {
		for i := 0; i < n; i++ {
			if v[i] != -1 {
				matched++
			}
			if orig[i] != -1 && v[i] != orig[i] {
				changes++
			}
		}
		return matched, changes
	}
	better := func(a, b []int) bool {
		ma, ca := stats(a)
		mb, cb := stats(b)
		if ma != mb {
			return ma > mb
		}
		if ca != cb {
			return ca < cb
		}
		for i := 0; i < n; i++ {
			va, vb := a[i], b[i]
			if va == -1 {
				va = m
			}
			if vb == -1 {
				vb = m
			}
			if va != vb {
				return va < vb
			}
		}
		return false
	}
	var best []int
	cur := make([]int, n)
	used := make([]bool, m)
	var rec func(i int)
	rec = func(i int) {
		if i == n {
			v := append([]int(nil), cur...)
			if best == nil || better(v, best) {
				best = v
			}
			return
		}
		cur[i] = -1
		rec(i + 1)
		for j := 0; j < m; j++ {
			if allowed[i][j] && !used[j] {
				used[j] = true
				cur[i] = j
				rec(i + 1)
				used[j] = false
			}
		}
	}
	rec(0)
	return best
}

func solveVector(t *testing.T, p Problem) (Solution, []int) {
	t.Helper()
	sol, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	v := make([]int, len(p.Employees))
	posIdx := make(map[string]int, len(p.Positions))
	for j, pos := range p.Positions {
		posIdx[pos] = j
	}
	for i, e := range p.Employees {
		v[i] = -1
		if pos, ok := sol.Assignment[e]; ok {
			v[i] = posIdx[pos]
		}
	}
	return sol, v
}

func checkAgainstBrute(t *testing.T, p Problem) {
	t.Helper()
	sol, got := solveVector(t, p)
	want := bruteForce(t, p)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("vector mismatch\ngot:  %v\nwant: %v\nproblem: %+v", got, want, p)
	}
	// Cross-check reported fields against the vector.
	matched, changes := 0, 0
	for i, e := range p.Employees {
		_, hadOrig := p.Original[e]
		if got[i] != -1 {
			matched++
		}
		origPos, ok := p.Original[e]
		if ok && (got[i] == -1 || p.Positions[got[i]] != origPos) {
			changes++
		}
		_ = hadOrig
	}
	if sol.Matched != matched {
		t.Fatalf("Matched = %d, want %d", sol.Matched, matched)
	}
	if sol.Changes != changes {
		t.Fatalf("Changes = %d, want %d", sol.Changes, changes)
	}
	// Unmatched / Displaced lists.
	var wantUnmatched, wantDisplaced []string
	for i, e := range p.Employees {
		if got[i] == -1 {
			wantUnmatched = append(wantUnmatched, e)
			if _, ok := p.Original[e]; ok {
				wantDisplaced = append(wantDisplaced, e)
			}
		}
	}
	if !reflect.DeepEqual(sol.Unmatched, wantUnmatched) && !(len(sol.Unmatched) == 0 && len(wantUnmatched) == 0) {
		t.Fatalf("Unmatched = %v, want %v", sol.Unmatched, wantUnmatched)
	}
	if !reflect.DeepEqual(sol.Displaced, wantDisplaced) && !(len(sol.Displaced) == 0 && len(wantDisplaced) == 0) {
		t.Fatalf("Displaced = %v, want %v", sol.Displaced, wantDisplaced)
	}
	checkChains(t, p, sol)
}

// checkChains verifies that the adjustment chains are internally consistent
// and transform the original assignment into the solution assignment.
func checkChains(t *testing.T, p Problem, sol Solution) {
	t.Helper()
	cur := map[string]string{}
	for e, pos := range p.Original {
		cur[e] = pos
	}
	seenEmp := map[string]bool{}
	for ci, chain := range sol.Chains {
		if len(chain) == 0 {
			t.Fatalf("chain %d is empty", ci)
		}
		for ki, st := range chain {
			if seenEmp[st.Employee] {
				t.Fatalf("chain %d: employee %q appears in two chains/steps", ci, st.Employee)
			}
			seenEmp[st.Employee] = true
			if cur[st.Employee] != st.From {
				t.Fatalf("chain %d step %d: From=%q, but current position of %q is %q",
					ci, ki, st.From, st.Employee, cur[st.Employee])
			}
			if ki > 0 {
				prev := chain[ki-1]
				if st.From != prev.To && st.To != prev.From {
					t.Fatalf("chain %d step %d: no shared position with previous step (%v -> %v)",
						ci, ki, prev, st)
				}
			}
			if st.From == st.To {
				t.Fatalf("chain %d step %d: no-op step for %q", ci, ki, st.Employee)
			}
			if st.To == "" {
				delete(cur, st.Employee)
			} else {
				cur[st.Employee] = st.To
			}
		}
	}
	// After applying all chains, cur must equal the solution assignment.
	if len(cur) != len(sol.Assignment) {
		t.Fatalf("chains yield %d assignments, solution has %d", len(cur), len(sol.Assignment))
	}
	for e, pos := range sol.Assignment {
		if cur[e] != pos {
			t.Fatalf("chains yield %q -> %q, solution has %q", e, cur[e], pos)
		}
	}
	// Every employee with a changed assignment must appear in some chain.
	for _, e := range p.Employees {
		origPos, had := p.Original[e]
		newPos, has := sol.Assignment[e]
		changed := (had && !has) || (!had && has) || (had && has && origPos != newPos)
		if changed != seenEmp[e] {
			t.Fatalf("employee %q changed=%v but in-chain=%v", e, changed, seenEmp[e])
		}
	}
}

func TestRandomizedAgainstBruteForce(t *testing.T) {
	for seed := int64(0); seed < 3000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(6)
		m := 1 + rng.Intn(6)
		edgeProb := 0.2 + 0.6*rng.Float64()
		p := Problem{
			CanWork:  map[string]map[string]bool{},
			Original: map[string]string{},
		}
		for i := 0; i < n; i++ {
			p.Employees = append(p.Employees, fmt.Sprintf("e%d", i))
		}
		for j := 0; j < m; j++ {
			p.Positions = append(p.Positions, fmt.Sprintf("p%d", j))
		}
		for _, e := range p.Employees {
			for _, pos := range p.Positions {
				if rng.Float64() < edgeProb {
					if p.CanWork[e] == nil {
						p.CanWork[e] = map[string]bool{}
					}
					p.CanWork[e][pos] = true
				}
			}
		}
		// Random original assignment: injective, may use now-invalid edges.
		free := append([]string(nil), p.Positions...)
		rng.Shuffle(len(free), func(a, b int) { free[a], free[b] = free[b], free[a] })
		for _, e := range p.Employees {
			if len(free) == 0 || rng.Float64() < 0.3 {
				continue
			}
			p.Original[e] = free[len(free)-1]
			free = free[:len(free)-1]
		}
		checkAgainstBrute(t, p)
	}
}

func TestInvalidatedEdgeForcesSwap(t *testing.T) {
	p := Problem{
		Employees: []string{"a", "b"},
		Positions: []string{"p", "q"},
		CanWork: map[string]map[string]bool{
			"a": {"q": true},
			"b": {"p": true, "q": true},
		},
		Original: map[string]string{"a": "p", "b": "q"},
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "q", "b": "p"}
	if !reflect.DeepEqual(sol.Assignment, want) {
		t.Fatalf("Assignment = %v, want %v", sol.Assignment, want)
	}
	if sol.Matched != 2 || sol.Changes != 2 {
		t.Fatalf("Matched=%d Changes=%d, want 2/2", sol.Matched, sol.Changes)
	}
	if len(sol.Chains) != 1 || len(sol.Chains[0]) != 2 {
		t.Fatalf("Chains = %+v, want one chain of two steps", sol.Chains)
	}
}

func TestNewlyAssignedIsNotAChange(t *testing.T) {
	p := Problem{
		Employees: []string{"a", "b"},
		Positions: []string{"p", "q"},
		CanWork: map[string]map[string]bool{
			"a": {"p": true},
			"b": {"p": true, "q": true},
		},
		Original: map[string]string{"a": "p"}, // b was unassigned
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "p", "b": "q"}
	if !reflect.DeepEqual(sol.Assignment, want) {
		t.Fatalf("Assignment = %v, want %v", sol.Assignment, want)
	}
	if sol.Changes != 0 {
		t.Fatalf("Changes = %d, want 0 (b was unassigned before)", sol.Changes)
	}
}

func TestLexicographicTieBreak(t *testing.T) {
	p := Problem{
		Employees: []string{"a", "b"},
		Positions: []string{"p", "q"},
		CanWork: map[string]map[string]bool{
			"a": {"p": true, "q": true},
			"b": {"p": true, "q": true},
		},
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "p", "b": "q"}
	if !reflect.DeepEqual(sol.Assignment, want) {
		t.Fatalf("Assignment = %v, want %v (lexicographically smallest)", sol.Assignment, want)
	}
}

func TestUnassignedCountsAsLargest(t *testing.T) {
	p := Problem{
		Employees: []string{"a", "b"},
		Positions: []string{"p"},
		CanWork: map[string]map[string]bool{
			"a": {"p": true},
			"b": {"p": true},
		},
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "p"}
	if !reflect.DeepEqual(sol.Assignment, want) {
		t.Fatalf("Assignment = %v, want %v", sol.Assignment, want)
	}
	if !reflect.DeepEqual(sol.Unmatched, []string{"b"}) {
		t.Fatalf("Unmatched = %v, want [b]", sol.Unmatched)
	}
	if len(sol.Displaced) != 0 {
		t.Fatalf("Displaced = %v, want empty", sol.Displaced)
	}
}

func TestDisplacedEmployee(t *testing.T) {
	p := Problem{
		Employees: []string{"a"},
		Positions: []string{"p"},
		CanWork:   map[string]map[string]bool{}, // edge a->p invalidated
		Original:  map[string]string{"a": "p"},
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(sol.Assignment) != 0 {
		t.Fatalf("Assignment = %v, want empty", sol.Assignment)
	}
	if !reflect.DeepEqual(sol.Unmatched, []string{"a"}) {
		t.Fatalf("Unmatched = %v, want [a]", sol.Unmatched)
	}
	if !reflect.DeepEqual(sol.Displaced, []string{"a"}) {
		t.Fatalf("Displaced = %v, want [a]", sol.Displaced)
	}
	if sol.Changes != 1 {
		t.Fatalf("Changes = %d, want 1", sol.Changes)
	}
	if len(sol.Chains) != 1 || len(sol.Chains[0]) != 1 ||
		sol.Chains[0][0] != (ChainStep{Employee: "a", From: "p", To: ""}) {
		t.Fatalf("Chains = %+v", sol.Chains)
	}
}

func TestKeepOriginalWhenPossible(t *testing.T) {
	p := Problem{
		Employees: []string{"a", "b"},
		Positions: []string{"p", "q"},
		CanWork: map[string]map[string]bool{
			"a": {"p": true, "q": true},
			"b": {"q": true},
		},
		Original: map[string]string{"a": "p", "b": "q"},
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if sol.Changes != 0 || len(sol.Chains) != 0 {
		t.Fatalf("Changes=%d Chains=%v, want 0 changes and no chains", sol.Changes, sol.Chains)
	}
	if !reflect.DeepEqual(sol.Assignment, p.Original) {
		t.Fatalf("Assignment = %v, want original kept", sol.Assignment)
	}
}

func TestValidationErrors(t *testing.T) {
	base := Problem{
		Employees: []string{"a"},
		Positions: []string{"p"},
		CanWork:   map[string]map[string]bool{"a": {"p": true}},
	}
	cases := []Problem{
		{Employees: []string{"a", "a"}, Positions: []string{"p"}},
		{Employees: []string{"a"}, Positions: []string{"p", "p"}},
		{Employees: []string{"a"}, Positions: []string{"p"}, CanWork: map[string]map[string]bool{"x": {"p": true}}},
		{Employees: []string{"a"}, Positions: []string{"p"}, CanWork: map[string]map[string]bool{"a": {"x": true}}},
		{Employees: []string{"a"}, Positions: []string{"p"}, Original: map[string]string{"x": "p"}},
		{Employees: []string{"a"}, Positions: []string{"p"}, Original: map[string]string{"a": "x"}},
		{Employees: []string{"a", "b"}, Positions: []string{"p"}, Original: map[string]string{"a": "p", "b": "p"}},
	}
	for i, c := range cases {
		if c.CanWork == nil {
			c.CanWork = base.CanWork
		}
		if _, err := Solve(c); err == nil {
			t.Fatalf("case %d: expected error, got nil", i)
		}
	}
}
