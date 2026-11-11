package netsec

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// naiveSat is an independent reference solver: it enumerates all
// 2^NumVars assignments and evaluates clauses directly.
func naiveSat(numVars int, clauses []Clause) bool {
	for mask := 0; mask < 1<<numVars; mask++ {
		a := Assignment{Values: make([]bool, numVars)}
		for v := 0; v < numVars; v++ {
			a.Values[v] = mask>>v&1 == 1
		}
		if a.Satisfies(clauses) {
			return true
		}
	}
	return false
}

// bruteCore enumerates every subset of groups and returns the canonical
// minimum-cardinality conflict core: smallest size, ties broken by the
// lexicographic order of the sorted ID list.
func bruteCore(in Input) (core []string, satisfiable, backgroundUnsat bool) {
	if !naiveSat(in.NumVars, in.Background) {
		return []string{}, false, true
	}
	ids := make([]string, len(in.Groups))
	byID := make(map[string][]Clause, len(in.Groups))
	for i, g := range in.Groups {
		ids[i] = g.ID
		byID[g.ID] = g.Clauses
	}
	sort.Strings(ids)

	var best []string
	for mask := 0; mask < 1<<len(ids); mask++ {
		var sub []string
		clauses := append([]Clause(nil), in.Background...)
		for i, id := range ids {
			if mask>>i&1 == 1 {
				sub = append(sub, id)
				clauses = append(clauses, byID[id]...)
			}
		}
		if naiveSat(in.NumVars, clauses) {
			continue
		}
		if best == nil || len(sub) < len(best) || (len(sub) == len(best) && lessStrings(sub, best)) {
			best = sub
		}
	}
	if best == nil {
		return nil, true, false
	}
	return best, false, false
}

func lessStrings(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// checkWitnesses verifies that every witness satisfies the background
// and the core with its own group removed.
func checkWitnesses(t *testing.T, in Input, res Result) {
	t.Helper()
	if len(res.Witnesses) != len(res.Core) {
		t.Fatalf("got %d witnesses for core of size %d", len(res.Witnesses), len(res.Core))
	}
	inCore := make(map[string]bool, len(res.Core))
	for _, id := range res.Core {
		inCore[id] = true
	}
	for _, drop := range res.Core {
		w, ok := res.Witnesses[drop]
		if !ok {
			t.Fatalf("missing witness for group %q", drop)
		}
		if len(w.Values) != in.NumVars {
			t.Fatalf("witness for %q assigns %d vars, want %d", drop, len(w.Values), in.NumVars)
		}
		if !w.Satisfies(in.Background) {
			t.Fatalf("witness for %q violates background", drop)
		}
		for _, g := range in.Groups {
			if g.ID == drop || !inCore[g.ID] {
				continue
			}
			if !w.Satisfies(g.Clauses) {
				t.Fatalf("witness for %q violates core group %q", drop, g.ID)
			}
		}
	}
}

// TestExhaustiveRandom cross-checks MinConflictCore against full subset
// enumeration on randomized instances, including duplicate groups and
// empty clauses.
func TestExhaustiveRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1252))
	for trial := 0; trial < 2000; trial++ {
		numVars := rng.Intn(5) // 0..4 variables
		lit := func() Literal {
			return Literal{Var: rng.Intn(numVars), Neg: rng.Intn(2) == 0}
		}
		clause := func() Clause {
			if numVars == 0 {
				return nil // no valid literal exists; empty clause only
			}
			n := rng.Intn(4) // 0..3 literals, empty allowed
			c := make(Clause, n)
			for i := range c {
				c[i] = lit()
			}
			return c
		}
		clauses := func() []Clause {
			n := rng.Intn(4)
			cs := make([]Clause, n)
			for i := range cs {
				cs[i] = clause()
			}
			return cs
		}

		in := Input{NumVars: numVars, Background: clauses()}
		numGroups := rng.Intn(7) // 0..6 groups keeps 2^n enumeration cheap
		for i := 0; i < numGroups; i++ {
			g := Group{ID: fmt.Sprintf("g%02d", i)}
			if i > 0 && rng.Intn(4) == 0 {
				// Duplicate an earlier group's clauses under a new ID.
				g.Clauses = in.Groups[rng.Intn(i)].Clauses
			} else {
				g.Clauses = clauses()
			}
			in.Groups = append(in.Groups, g)
		}

		res, err := MinConflictCore(in, Budget{})
		if err != nil {
			t.Fatalf("trial %d: unexpected error: %v", trial, err)
		}
		wantCore, wantSat, wantBgUnsat := bruteCore(in)

		if res.BackgroundUnsat != wantBgUnsat {
			t.Fatalf("trial %d: BackgroundUnsat=%v, want %v", trial, res.BackgroundUnsat, wantBgUnsat)
		}
		if res.Satisfiable != wantSat {
			t.Fatalf("trial %d: Satisfiable=%v, want %v", trial, res.Satisfiable, wantSat)
		}
		if !res.Optimal {
			t.Fatalf("trial %d: unlimited budget must be optimal", trial)
		}
		if !reflect.DeepEqual(res.Core, wantCore) {
			t.Fatalf("trial %d: core %v, want %v\ninput: %+v", trial, res.Core, wantCore, in)
		}
		if res.Core != nil {
			clauses := append([]Clause(nil), in.Background...)
			byID := make(map[string][]Clause)
			for _, g := range in.Groups {
				byID[g.ID] = g.Clauses
			}
			for _, id := range res.Core {
				clauses = append(clauses, byID[id]...)
			}
			if naiveSat(in.NumVars, clauses) {
				t.Fatalf("trial %d: reported core %v is satisfiable", trial, res.Core)
			}
			checkWitnesses(t, in, res)
		}
	}
}

func TestDuplicateGroupsTieBreak(t *testing.T) {
	// Both groups force x0=true and x0=false respectively in pairs; each
	// pair {a,b} and {a,c} conflicts, tie broken to the smaller ID list.
	in := Input{
		NumVars: 1,
		Groups: []Group{
			{ID: "c", Clauses: []Clause{{Neg(0)}}},
			{ID: "b", Clauses: []Clause{{Neg(0)}}},
			{ID: "a", Clauses: []Clause{{Pos(0)}}},
		},
	}
	res, err := MinConflictCore(in, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(res.Core, want) {
		t.Fatalf("core %v, want %v", res.Core, want)
	}
	checkWitnesses(t, in, res)
}

func TestEmptyClauseGroup(t *testing.T) {
	// A group containing an empty clause conflicts with the background
	// on its own.
	in := Input{
		NumVars: 2,
		Groups: []Group{
			{ID: "keep", Clauses: []Clause{{Pos(0), Pos(1)}}},
			{ID: "bad", Clauses: []Clause{{Pos(0)}, {}}},
		},
	}
	res, err := MinConflictCore(in, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"bad"}; !reflect.DeepEqual(res.Core, want) {
		t.Fatalf("core %v, want %v", res.Core, want)
	}
	checkWitnesses(t, in, res)
}

func TestBackgroundUnsat(t *testing.T) {
	in := Input{
		NumVars:    1,
		Background: []Clause{{Pos(0)}, {Neg(0)}},
		Groups:     []Group{{ID: "g", Clauses: []Clause{{Pos(0)}}}},
	}
	res, err := MinConflictCore(in, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.BackgroundUnsat || !res.Optimal || len(res.Core) != 0 {
		t.Fatalf("got %+v, want empty optimal core with BackgroundUnsat", res)
	}
}

func TestBackgroundEmptyClause(t *testing.T) {
	in := Input{NumVars: 0, Background: []Clause{{}}}
	res, err := MinConflictCore(in, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.BackgroundUnsat {
		t.Fatalf("empty background clause must be unsatisfiable: %+v", res)
	}
}

func TestSatisfiable(t *testing.T) {
	in := Input{
		NumVars: 2,
		Groups: []Group{
			{ID: "a", Clauses: []Clause{{Pos(0), Neg(1)}}},
			{ID: "b", Clauses: []Clause{{Neg(0), Pos(1)}}},
		},
	}
	res, err := MinConflictCore(in, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Satisfiable || !res.Optimal || res.Core != nil {
		t.Fatalf("got %+v, want optimal satisfiable result", res)
	}
}

func TestBudgetExhausted(t *testing.T) {
	in := Input{
		NumVars: 2,
		Groups: []Group{
			{ID: "a", Clauses: []Clause{{Pos(0)}}},
			{ID: "b", Clauses: []Clause{{Pos(1)}}},
			{ID: "c", Clauses: []Clause{{Neg(0)}, {Neg(1)}}},
		},
	}
	// The minimum core {a,c} or {b,c} needs size 2; a budget of one
	// check cannot prove any core.
	res, err := MinConflictCore(in, Budget{MaxChecks: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Optimal {
		t.Fatal("budget-exhausted result must be marked not optimal")
	}
	if res.Core != nil {
		t.Fatalf("no core can be proven within one check, got %v", res.Core)
	}
	if res.Satisfiable {
		t.Fatal("satisfiability was not established")
	}

	// With enough budget the search completes optimally.
	full, err := MinConflictCore(in, Budget{MaxChecks: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !full.Optimal {
		t.Fatal("sufficient budget must prove optimality")
	}
	if want := []string{"a", "c"}; !reflect.DeepEqual(full.Core, want) {
		t.Fatalf("core %v, want %v", full.Core, want)
	}
	checkWitnesses(t, in, full)
}

func TestValidation(t *testing.T) {
	cases := []Input{
		{NumVars: MaxVars + 1},
		{NumVars: -1},
		{NumVars: 1, Groups: []Group{{ID: ""}}},
		{NumVars: 1, Groups: []Group{{ID: "x"}, {ID: "x"}}},
		{NumVars: 1, Background: []Clause{{Pos(1)}}},
		{NumVars: 1, Groups: []Group{{ID: "g", Clauses: []Clause{{Pos(-1)}}}}},
	}
	for i, in := range cases {
		if _, err := MinConflictCore(in, Budget{}); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}
