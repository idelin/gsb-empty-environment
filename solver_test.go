package main

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// --- brute-force reference solver -----------------------------------------

// bruteForce enumerates every total assignment (each known package either
// unassigned or one of its candidates) and returns the lexicographically
// minimal feasible objective plus one assignment attaining it.
func bruteForce(reg *Registry, lock map[string]LockEntry, roots map[string]string) (objective, map[string]*Candidate, bool) {
	rootCons := map[string]Constraint{}
	for n, r := range roots {
		c, err := ParseConstraint(r)
		if err != nil {
			return objective{}, nil, false
		}
		rootCons[n] = c
	}
	namesSet := map[string]bool{}
	for n := range reg.byName {
		namesSet[n] = true
	}
	for n := range roots {
		namesSet[n] = true
	}
	var names []string
	for n := range namesSet {
		names = append(names, n)
	}
	sort.Strings(names)

	// options[i] = nil (unassigned) followed by each candidate.
	options := make([][]*Candidate, len(names))
	for i, n := range names {
		options[i] = append([]*Candidate{nil}, reg.byName[n]...)
	}

	s := &solver{reg: reg, lock: lock}
	best := objective{}
	var bestAssign map[string]*Candidate
	found := false

	assign := make([]*Candidate, len(names))
	var visit func(i int)
	visit = func(i int) {
		if i == len(names) {
			cur := map[string]*Candidate{}
			for j, c := range assign {
				if c != nil {
					cur[names[j]] = c
				}
			}
			if !validResolution(cur, rootCons) {
				return
			}
			obj := s.objectiveOf(cur)
			if !found || lessObjective(obj, best) {
				found, best = true, obj
				bestAssign = cur
			}
			return
		}
		for _, c := range options[i] {
			assign[i] = c
			visit(i + 1)
		}
	}
	visit(0)
	return best, bestAssign, found
}

// validResolution checks full consistency: roots satisfied, all dependency
// constraints satisfied, and the assigned set is exactly the root closure.
func validResolution(assign map[string]*Candidate, roots map[string]Constraint) bool {
	for n, con := range roots {
		c := assign[n]
		if c == nil || !con.Allows(c.parsed) {
			return false
		}
	}
	for n, c := range assign {
		_ = n
		for _, d := range c.deps {
			dep := assign[d.name]
			if dep == nil || !d.con.Allows(dep.parsed) {
				return false
			}
		}
	}
	// Exactly the reachable closure: everything assigned must be reachable
	// from the roots.
	reach := map[string]bool{}
	var queue []string
	for n := range roots {
		if assign[n] != nil && !reach[n] {
			reach[n] = true
			queue = append(queue, n)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range assign[cur].deps {
			if !reach[d.name] {
				reach[d.name] = true
				queue = append(queue, d.name)
			}
		}
	}
	for n := range assign {
		if !reach[n] {
			return false
		}
	}
	return true
}

// --- helpers ---------------------------------------------------------------

func mustRegistry(t *testing.T, cands []Candidate) *Registry {
	t.Helper()
	r, err := NewRegistry(cands)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r
}

func mustSolve(t *testing.T, reg *Registry, lock map[string]LockEntry, roots map[string]string) (map[string]*Candidate, objective) {
	t.Helper()
	res, obj, ok := Solve(reg, lock, roots)
	if !ok {
		t.Fatalf("expected a feasible resolution, got none")
	}
	rootCons := map[string]Constraint{}
	for n, r := range roots {
		c, _ := ParseConstraint(r)
		rootCons[n] = c
	}
	if !validResolution(res, rootCons) {
		t.Fatalf("solver returned an invalid resolution: %v", res)
	}
	s := &solver{lock: lock}
	if got := s.objectiveOf(res); got != obj {
		t.Fatalf("reported objective %v != actual %v", obj, got)
	}
	return res, obj
}

// --- targeted scenarios ----------------------------------------------------

func TestKeepsLockedVersionsWhenPossible(t *testing.T) {
	reg := mustRegistry(t, []Candidate{
		{Name: "a", Version: "1.0.0", Source: "reg", Size: 10, Deps: map[string]string{"b": ">=1.0.0"}},
		{Name: "a", Version: "2.0.0", Source: "reg", Size: 10},
		{Name: "b", Version: "1.0.0", Source: "reg", Size: 5},
		{Name: "b", Version: "2.0.0", Source: "reg", Size: 1},
	})
	lock := map[string]LockEntry{"a": {Version: "1.0.0", Source: "reg"}, "b": {Version: "1.0.0", Source: "reg"}}
	res, obj := mustSolve(t, reg, lock, map[string]string{"a": "*"})
	if obj.changes != 0 || obj.bytes != 0 {
		t.Fatalf("expected zero-change zero-download plan, got %+v", obj)
	}
	if res["b"].Version != "1.0.0" {
		t.Fatalf("b should stay at 1.0.0 even though 2.0.0 is smaller, got %s", res["b"].Version)
	}
}

func TestForcedChangeCascadeAndGC(t *testing.T) {
	reg := mustRegistry(t, []Candidate{
		{Name: "a", Version: "1.0.0", Source: "reg", Size: 10, Deps: map[string]string{"b": "=1.0.0"}},
		{Name: "a", Version: "2.0.0", Source: "reg", Size: 10, Deps: map[string]string{"b": ">=2.0.0"}},
		{Name: "b", Version: "1.0.0", Source: "reg", Size: 5, Deps: map[string]string{"c": "*"}},
		{Name: "b", Version: "2.0.0", Source: "reg", Size: 6},
		{Name: "c", Version: "1.0.0", Source: "reg", Size: 7},
	})
	lock := map[string]LockEntry{
		"a": {Version: "1.0.0", Source: "reg"},
		"b": {Version: "1.0.0", Source: "reg"},
		"c": {Version: "1.0.0", Source: "reg"},
	}
	roots := map[string]string{"a": ">=2.0.0"}
	res, obj := mustSolve(t, reg, lock, roots)
	if obj.changes != 2 {
		t.Fatalf("expected exactly 2 forced changes (a and b), got %d", obj.changes)
	}
	if res["b"].Version != "2.0.0" {
		t.Fatalf("b must follow a to 2.0.0, got %s", res["b"].Version)
	}
	if _, ok := res["c"]; ok {
		t.Fatalf("c is no longer reachable and must be garbage-collected")
	}
	plan := Explain(reg, lock, roots, res, obj)
	var bChange, cChange *Change
	for i := range plan.Changes {
		switch plan.Changes[i].Name {
		case "b":
			bChange = &plan.Changes[i]
		case "c":
			cChange = &plan.Changes[i]
		}
	}
	if bChange == nil || len(bChange.Reasons) == 0 {
		t.Fatalf("missing reason for forced change of b")
	}
	want := "locked 1.0.0 violates constraint \">=2.0.0\" from a@2.0.0 (reg)"
	if bChange.Reasons[0] != want {
		t.Fatalf("reason mismatch:\n got %q\nwant %q", bChange.Reasons[0], want)
	}
	if len(bChange.Path) != 2 || bChange.Path[0] != "a@2.0.0 (reg) [root]" {
		t.Fatalf("unexpected reason path for b: %v", bChange.Path)
	}
	if cChange == nil || cChange.Kind != "removed" {
		t.Fatalf("c must be reported as removed")
	}
}

func TestDependencyCycle(t *testing.T) {
	reg := mustRegistry(t, []Candidate{
		{Name: "a", Version: "1.0.0", Source: "reg", Size: 3, Deps: map[string]string{"b": ">=1.0.0"}},
		{Name: "a", Version: "2.0.0", Source: "reg", Size: 3, Deps: map[string]string{"b": ">=2.0.0"}},
		{Name: "b", Version: "1.0.0", Source: "reg", Size: 4, Deps: map[string]string{"a": ">=1.0.0"}},
		{Name: "b", Version: "2.0.0", Source: "reg", Size: 4, Deps: map[string]string{"a": ">=2.0.0"}},
	})
	lock := map[string]LockEntry{
		"a": {Version: "1.0.0", Source: "reg"},
		"b": {Version: "1.0.0", Source: "reg"},
	}
	res, obj := mustSolve(t, reg, lock, map[string]string{"a": ">=2.0.0"})
	if obj.changes != 2 || res["a"].Version != "2.0.0" || res["b"].Version != "2.0.0" {
		t.Fatalf("cycle must force both to 2.0.0, got a=%s b=%s obj=%+v",
			res["a"].Version, res["b"].Version, obj)
	}
}

func TestSameVersionDifferentSource(t *testing.T) {
	reg := mustRegistry(t, []Candidate{
		{Name: "x", Version: "1.0.0", Source: "git", Size: 100},
		{Name: "x", Version: "1.0.0", Source: "reg", Size: 10},
		{Name: "x", Version: "2.0.0", Source: "reg", Size: 10},
	})
	// Locked on the expensive git source: keeping the pin wins over the
	// cheaper registry copy because preserved pins dominate download bytes.
	lock := map[string]LockEntry{"x": {Version: "1.0.0", Source: "git"}}
	res, obj := mustSolve(t, reg, lock, map[string]string{"x": ">=1.0.0"})
	if obj.changes != 0 || obj.bytes != 0 || res["x"].Source != "git" {
		t.Fatalf("must keep locked git pin, got %+v obj=%+v", res["x"], obj)
	}
	// Fresh package (no lock entry): the cheaper source is downloaded.
	res, obj = mustSolve(t, reg, map[string]LockEntry{}, map[string]string{"x": "=1.0.0"})
	if obj.bytes != 10 || res["x"].Source != "reg" {
		t.Fatalf("must pick cheaper source for new downloads, got %+v obj=%+v", res["x"], obj)
	}
}

func TestUnsatisfiable(t *testing.T) {
	reg := mustRegistry(t, []Candidate{
		{Name: "a", Version: "1.0.0", Source: "reg", Size: 1},
	})
	if _, _, ok := Solve(reg, nil, map[string]string{"a": ">=2.0.0"}); ok {
		t.Fatalf("expected infeasible")
	}
}

// --- exhaustive optimality check on random small instances -----------------

func TestExhaustiveOptimality(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	constraints := []string{"*", ">=1.0.0", "<2.0.0", "=1.0.0", ">=1.0.0, <2.0.0", "=2.0.0"}
	sources := []string{"reg", "git"}
	names := []string{"a", "b", "c", "d"}

	for trial := 0; trial < 300; trial++ {
		var cands []Candidate
		for _, n := range names {
			nv := 1 + rng.Intn(2) // 1-2 versions per package
			for v := 1; v <= nv; v++ {
				srcs := []string{"reg"}
				if rng.Intn(3) == 0 { // sometimes a second source, same version
					srcs = sources[:1+rng.Intn(2)]
				}
				for _, src := range srcs {
					deps := map[string]string{}
					for _, d := range names {
						if d != n && rng.Intn(3) == 0 { // may create cycles
							deps[d] = constraints[rng.Intn(len(constraints))]
						}
					}
					cands = append(cands, Candidate{
						Name:    n,
						Version: vString(v),
						Source:  src,
						Size:    int64(1 + rng.Intn(50)),
						Deps:    deps,
					})
				}
			}
		}
		reg, err := NewRegistry(cands)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		lock := map[string]LockEntry{}
		for _, c := range cands {
			if rng.Intn(2) == 0 {
				lock[c.Name] = LockEntry{Version: c.Version, Source: c.Source}
			}
		}
		roots := map[string]string{}
		for _, n := range names {
			if rng.Intn(2) == 0 {
				roots[n] = constraints[rng.Intn(len(constraints))]
			}
		}
		if len(roots) == 0 {
			roots["a"] = "*"
		}

		gotRes, gotObj, gotOK := Solve(reg, lock, roots)
		wantObj, _, wantOK := bruteForce(reg, lock, roots)
		if gotOK != wantOK {
			t.Fatalf("trial %d: feasibility mismatch solver=%v brute=%v\nregistry=%+v\nlock=%+v\nroots=%+v",
				trial, gotOK, wantOK, cands, lock, roots)
		}
		if !gotOK {
			continue
		}
		if gotObj != wantObj {
			t.Fatalf("trial %d: solver objective %+v, brute-force optimum %+v\nregistry=%+v\nlock=%+v\nroots=%+v",
				trial, gotObj, wantObj, cands, lock, roots)
		}
		rootCons := map[string]Constraint{}
		for n, r := range roots {
			c, _ := ParseConstraint(r)
			rootCons[n] = c
		}
		if !validResolution(gotRes, rootCons) {
			t.Fatalf("trial %d: solver returned invalid resolution", trial)
		}
		if !reflect.DeepEqual(gotObj, sObjectiveOf(lock, gotRes)) {
			t.Fatalf("trial %d: objective misreported", trial)
		}
	}
}

func sObjectiveOf(lock map[string]LockEntry, res map[string]*Candidate) objective {
	s := &solver{lock: lock}
	return s.objectiveOf(res)
}

func vString(v int) string {
	return string(rune('0'+v)) + ".0.0"
}
