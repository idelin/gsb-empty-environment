package csp

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// bruteForce enumerates the full cartesian product of the given initial
// domains and returns every assignment satisfying all constraints. This
// is the ground truth the library is checked against.
func bruteForce(p *Problem, initDoms []map[int]struct{}) []map[string]int {
	vars := p.Vars()
	vals := make([][]int, len(vars))
	for i, v := range vars {
		vals[i] = sortedValues(initDoms[i])
		_ = v
	}
	var sols []map[string]int
	assign := make(map[*Var]int, len(vars))
	var rec func(i int)
	rec = func(i int) {
		if i == len(vars) {
			ok := true
			for _, c := range p.Constraints() {
				if !c.Holds(assign) {
					ok = false
					break
				}
			}
			if ok {
				sol := make(map[string]int, len(vars))
				for _, v := range vars {
					sol[v.Name] = assign[v]
				}
				sols = append(sols, sol)
			}
			return
		}
		for _, val := range vals[i] {
			assign[vars[i]] = val
			rec(i + 1)
		}
	}
	rec(0)
	return sols
}

func solKey(sol map[string]int) string {
	keys := make([]string, 0, len(sol))
	for k := range sol {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%d", k, sol[k])
	}
	return strings.Join(parts, ",")
}

func solSet(sols []map[string]int) map[string]bool {
	set := make(map[string]bool, len(sols))
	for _, s := range sols {
		set[solKey(s)] = true
	}
	return set
}

// randomProblem builds a small random problem from the seed. Domains are
// kept tiny so brute force stays cheap.
func randomProblem(r *rand.Rand) *Problem {
	p := New()
	n := 2 + r.Intn(3) // 2..4 variables
	vars := make([]*Var, n)
	for i := 0; i < n; i++ {
		lo := -2 + r.Intn(3) // -2..0
		hi := lo + r.Intn(4) // lo..lo+3
		vars[i] = p.AddVar(fmt.Sprintf("v%d", i), lo, hi)
	}
	nc := 1 + r.Intn(5) // 1..5 constraints
	for i := 0; i < nc; i++ {
		switch r.Intn(3) {
		case 0:
			a, b := r.Intn(n), r.Intn(n)
			if a == b {
				b = (b + 1) % n
			}
			p.Add(&Equality{X: vars[a], Y: vars[b], C: r.Intn(5) - 2})
		case 1:
			a, b := r.Intn(n), r.Intn(n)
			if a == b {
				b = (b + 1) % n
			}
			p.Add(&DiffBound{X: vars[a], Y: vars[b], C: r.Intn(5) - 2})
		case 2:
			k := 2 + r.Intn(n-1) // subset size 2..n
			perm := r.Perm(n)[:k]
			sub := make([]*Var, k)
			for j, idx := range perm {
				sub[j] = vars[idx]
			}
			p.Add(&AllDifferent{Vars: sub})
		}
	}
	return p
}

// TestConservativePropagation verifies propagation never deletes a value
// that appears in some real (brute-force) solution.
func TestConservativePropagation(t *testing.T) {
	for seed := int64(0); seed < 500; seed++ {
		r := rand.New(rand.NewSource(seed))
		p := randomProblem(r)
		initDoms := p.Snapshot()
		want := bruteForce(p, initDoms)

		p.Propagate() // result irrelevant; domains must stay conservative

		vars := p.Vars()
		for _, sol := range want {
			for _, v := range vars {
				if _, ok := v.Dom[sol[v.Name]]; !ok {
					t.Fatalf("seed %d: propagation removed solution value %s=%d (domain now %v)",
						seed, v.Name, sol[v.Name], sortedValues(v.Dom))
				}
			}
		}
	}
}

// TestSolverMatchesBruteForce verifies the backtracking solver returns
// exactly the set of brute-force solutions: complete and sound.
func TestSolverMatchesBruteForce(t *testing.T) {
	for seed := int64(0); seed < 500; seed++ {
		r := rand.New(rand.NewSource(seed))
		p := randomProblem(r)
		initDoms := p.Snapshot()
		want := solSet(bruteForce(p, initDoms))

		got := solSet(p.Solve(0))

		if len(got) != len(want) {
			t.Fatalf("seed %d: solver found %d solutions, brute force found %d", seed, len(got), len(want))
		}
		for k := range want {
			if !got[k] {
				t.Fatalf("seed %d: solver missed solution %s", seed, k)
			}
		}
	}
}

// TestRollbackRestoresDomains verifies Solve leaves every domain exactly
// as it was before the call, including after unsatisfiable searches.
func TestRollbackRestoresDomains(t *testing.T) {
	for seed := int64(0); seed < 500; seed++ {
		r := rand.New(rand.NewSource(seed))
		p := randomProblem(r)
		before := p.Snapshot()

		p.Solve(0)

		after := p.Snapshot()
		for i, v := range p.Vars() {
			if !reflect.DeepEqual(before[i], after[i]) {
				t.Fatalf("seed %d: domain of %s not restored: before %v after %v",
					seed, v.Name, sortedValues(before[i]), sortedValues(after[i]))
			}
		}
	}
}

// TestPigeonholeUnsatOnlyBySearch shows propagation alone cannot prove
// unsatisfiability: 3 variables over {1,2} with all-different survives
// propagation, and only complete backtracking confirms no solution.
func TestPigeonholeUnsatOnlyBySearch(t *testing.T) {
	p := New()
	a := p.AddVar("a", 1, 2)
	b := p.AddVar("b", 1, 2)
	c := p.AddVar("c", 1, 2)
	p.Add(&AllDifferent{Vars: []*Var{a, b, c}})

	if !p.Propagate() {
		t.Fatal("propagation unexpectedly detected the pigeonhole contradiction")
	}
	if sols := p.Solve(0); len(sols) != 0 {
		t.Fatalf("expected no solutions, got %v", sols)
	}
}

// TestPropagateDetectsWipeout shows propagation does report definite
// unsatisfiability when a domain empties.
func TestPropagateDetectsWipeout(t *testing.T) {
	p := New()
	x := p.AddVar("x", 0, 2)
	y := p.AddVar("y", 0, 2)
	p.Add(&Equality{X: x, Y: y, C: 5}) // x = y + 5 impossible on [0,2]

	if p.Propagate() {
		t.Fatal("expected propagation to empty a domain")
	}
	if sols := p.Solve(0); len(sols) != 0 {
		t.Fatalf("expected no solutions, got %v", sols)
	}
}
