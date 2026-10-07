package resolver

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func v(s string) Version {
	v, err := ParseVersion(s)
	if err != nil {
		panic(err)
	}
	return v
}

func closed(lo, hi string) Range { return Range{v(lo), v(hi)} }

func mustSolve(t *testing.T, m *Manifest, reqs []Requirement) *Lock {
	t.Helper()
	lock, err := Solve(m, reqs)
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	return lock
}

func TestHighestRequestedVersionVector(t *testing.T) {
	// A@2 forces B@1; A@1 allows B@2. The requested vector (A, B) is
	// maximized lexicographically, so A wins over B.
	m := &Manifest{
		Versions: map[string][]Version{
			"A": {v("1.0.0"), v("2.0.0")},
			"B": {v("1.0.0"), v("2.0.0")},
		},
		Deps: map[PkgVersion][]Dependency{
			{"A", v("2.0.0")}: {{"B", closed("1.0.0", "1.0.0")}},
			{"A", v("1.0.0")}: {{"B", closed("1.0.0", "2.0.0")}},
		},
	}
	lock := mustSolve(t, m, []Requirement{
		{"A", AnyRange()},
		{"B", AnyRange()},
	})
	want := map[string]Version{"A": v("2.0.0"), "B": v("1.0.0")}
	if !reflect.DeepEqual(lock.Versions, want) {
		t.Fatalf("got %v, want %v", lock.Versions, want)
	}
}

func TestMinTransitivePackages(t *testing.T) {
	// B@2 has no deps, B@1 drags in C. Only A is requested, so the
	// resolver must prefer B@2 (2 packages total over 3).
	m := &Manifest{
		Versions: map[string][]Version{
			"A": {v("1.0.0")},
			"B": {v("1.0.0"), v("2.0.0")},
			"C": {v("1.0.0")},
		},
		Deps: map[PkgVersion][]Dependency{
			{"A", v("1.0.0")}: {{"B", AnyRange()}},
			{"B", v("1.0.0")}: {{"C", AnyRange()}},
		},
	}
	lock := mustSolve(t, m, []Requirement{{"A", AnyRange()}})
	want := map[string]Version{"A": v("1.0.0"), "B": v("2.0.0")}
	if !reflect.DeepEqual(lock.Versions, want) {
		t.Fatalf("got %v, want %v", lock.Versions, want)
	}
}

func TestConflictUnsat(t *testing.T) {
	m := &Manifest{
		Versions: map[string][]Version{
			"A": {v("1.0.0")},
			"B": {v("1.0.0")},
		},
		Conflicts: [][2]PkgVersion{
			{{"A", v("1.0.0")}, {"B", v("1.0.0")}},
		},
	}
	_, err := Solve(m, []Requirement{{"A", AnyRange()}, {"B", AnyRange()}})
	unsat, ok := err.(*UnsatError)
	if !ok {
		t.Fatalf("expected *UnsatError, got %v", err)
	}
	if len(unsat.Core) != 2 {
		t.Fatalf("core = %v, want both requirements", unsat.Core)
	}
	if !strings.Contains(unsat.Detail, "conflicts") {
		t.Fatalf("detail %q should mention the conflict", unsat.Detail)
	}
}

func TestNoVersionInRange(t *testing.T) {
	m := &Manifest{
		Versions: map[string][]Version{"A": {v("1.0.0")}},
	}
	_, err := Solve(m, []Requirement{{"A", closed("9.0.0", "9.0.0")}})
	unsat, ok := err.(*UnsatError)
	if !ok {
		t.Fatalf("expected *UnsatError, got %v", err)
	}
	if !strings.Contains(unsat.Detail, "no version") {
		t.Fatalf("detail %q should mention missing versions", unsat.Detail)
	}
}

func TestUnsatisfiableDependency(t *testing.T) {
	m := &Manifest{
		Versions: map[string][]Version{
			"A": {v("1.0.0")},
			"B": {v("1.0.0")},
		},
		Deps: map[PkgVersion][]Dependency{
			{"A", v("1.0.0")}: {{"B", closed("2.0.0", "2.0.0")}},
		},
	}
	_, err := Solve(m, []Requirement{{"A", AnyRange()}})
	unsat, ok := err.(*UnsatError)
	if !ok {
		t.Fatalf("expected *UnsatError, got %v", err)
	}
	if len(unsat.Core) != 1 {
		t.Fatalf("core = %v, want just A", unsat.Core)
	}
	if !strings.Contains(unsat.Detail, "requires B") {
		t.Fatalf("detail %q should mention the broken dependency", unsat.Detail)
	}
}

func TestRequestedRangeClash(t *testing.T) {
	// A needs B at 1.x, but B is requested at 2.x. Each is fine alone.
	m := &Manifest{
		Versions: map[string][]Version{
			"A": {v("1.0.0")},
			"B": {v("1.0.0"), v("2.0.0")},
		},
		Deps: map[PkgVersion][]Dependency{
			{"A", v("1.0.0")}: {{"B", closed("1.0.0", "1.0.0")}},
		},
	}
	_, err := Solve(m, []Requirement{
		{"A", AnyRange()},
		{"B", closed("2.0.0", "2.0.0")},
	})
	unsat, ok := err.(*UnsatError)
	if !ok {
		t.Fatalf("expected *UnsatError, got %v", err)
	}
	if len(unsat.Core) != 2 {
		t.Fatalf("core = %v, want both requirements", unsat.Core)
	}
	if !strings.Contains(unsat.Detail, "does not overlap") {
		t.Fatalf("detail %q should mention the range clash", unsat.Detail)
	}
}

// --- Exhaustive cross-check against brute force on small manifests ---

// validAssignment checks every constraint of the problem.
func validAssignment(m *Manifest, reqs []Requirement, assign map[string]Version) bool {
	for _, r := range reqs {
		ver, ok := assign[r.Pkg]
		if !ok || !r.Range.Contains(ver) {
			return false
		}
	}
	for pkg, ver := range assign {
		for _, d := range m.Deps[PkgVersion{pkg, ver}] {
			dv, ok := assign[d.Pkg]
			if !ok || !d.Range.Contains(dv) {
				return false
			}
		}
	}
	for _, c := range m.Conflicts {
		v0, ok0 := assign[c[0].Pkg]
		v1, ok1 := assign[c[1].Pkg]
		if ok0 && ok1 && v0 == c[0].Ver && v1 == c[1].Ver {
			return false
		}
	}
	return true
}

// objective is (requested version vector, package count); the vector is
// maximized lexicographically first, then the count minimized.
type objective struct {
	vec   []Version
	count int
}

func better(a, b objective) bool {
	for i := range a.vec {
		if c := a.vec[i].Compare(b.vec[i]); c != 0 {
			return c > 0
		}
	}
	return a.count < b.count
}

// bruteForce enumerates every assignment (each package absent or at one
// version) and returns the optimal objective.
func bruteForce(m *Manifest, reqs []Requirement) (objective, bool) {
	var pkgs []string
	for pkg := range m.Versions {
		pkgs = append(pkgs, pkg)
	}
	assign := make(map[string]Version)
	var best objective
	found := false
	var rec func(i int)
	rec = func(i int) {
		if i == len(pkgs) {
			if !validAssignment(m, reqs, assign) {
				return
			}
			obj := objective{count: len(assign)}
			for _, r := range reqs {
				obj.vec = append(obj.vec, assign[r.Pkg])
			}
			if !found || better(obj, best) {
				best, found = obj, true
			}
			return
		}
		rec(i + 1) // package not installed
		for _, ver := range m.Versions[pkgs[i]] {
			assign[pkgs[i]] = ver
			rec(i + 1)
			delete(assign, pkgs[i])
		}
	}
	rec(0)
	return best, found
}

func randomManifest(rng *rand.Rand) *Manifest {
	n := 3 + rng.Intn(3) // 3..5 packages
	names := []string{"a", "b", "c", "d", "e"}[:n]
	m := &Manifest{
		Versions: make(map[string][]Version),
		Deps:     make(map[PkgVersion][]Dependency),
	}
	for _, name := range names {
		k := 1 + rng.Intn(3) // 1..3 versions
		for i := 1; i <= k; i++ {
			m.Versions[name] = append(m.Versions[name], Version{i, 0, 0})
		}
	}
	randRange := func(pkg string) Range {
		vs := m.Versions[pkg]
		lo, hi := rng.Intn(len(vs)), rng.Intn(len(vs))
		if lo > hi {
			lo, hi = hi, lo
		}
		return Range{vs[lo], vs[hi]}
	}
	for _, name := range names {
		for _, ver := range m.Versions[name] {
			var deps []Dependency
			for _, other := range names {
				if other != name && rng.Float64() < 0.3 {
					deps = append(deps, Dependency{other, randRange(other)})
				}
			}
			if deps != nil {
				m.Deps[PkgVersion{name, ver}] = deps
			}
		}
	}
	for i := 0; i < rng.Intn(4); i++ {
		p1 := names[rng.Intn(n)]
		p2 := names[rng.Intn(n)]
		if p1 == p2 {
			continue
		}
		v1 := m.Versions[p1][rng.Intn(len(m.Versions[p1]))]
		v2 := m.Versions[p2][rng.Intn(len(m.Versions[p2]))]
		m.Conflicts = append(m.Conflicts, [2]PkgVersion{{p1, v1}, {p2, v2}})
	}
	return m
}

func randomReqs(rng *rand.Rand, m *Manifest) []Requirement {
	var names []string
	for pkg := range m.Versions {
		names = append(names, pkg)
	}
	rng.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	var reqs []Requirement
	for _, pkg := range names[:1+rng.Intn(2)] {
		vs := m.Versions[pkg]
		lo, hi := rng.Intn(len(vs)), rng.Intn(len(vs))
		if lo > hi {
			lo, hi = hi, lo
		}
		reqs = append(reqs, Requirement{pkg, Range{vs[lo], vs[hi]}})
	}
	return reqs
}

func TestAgainstBruteForce(t *testing.T) {
	for seed := int64(0); seed < 500; seed++ {
		rng := rand.New(rand.NewSource(seed))
		m := randomManifest(rng)
		reqs := randomReqs(rng, m)

		lock, err := Solve(m, reqs)
		best, found := bruteForce(m, reqs)

		if !found {
			if err == nil {
				t.Fatalf("seed %d: solver found %v but brute force proves unsat", seed, lock)
			}
			if _, ok := err.(*UnsatError); !ok {
				t.Fatalf("seed %d: expected *UnsatError, got %T", seed, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("seed %d: solver reports unsat (%v) but brute force found objective %+v", seed, err, best)
		}
		if !validAssignment(m, reqs, lock.Versions) {
			t.Fatalf("seed %d: solver lock %v violates constraints", seed, lock)
		}
		got := objective{count: len(lock.Versions)}
		for _, r := range reqs {
			got.vec = append(got.vec, lock.Versions[r.Pkg])
		}
		if !reflect.DeepEqual(got.vec, best.vec) || got.count != best.count {
			t.Fatalf("seed %d: solver objective %+v != brute force optimum %+v", seed, got, best)
		}
	}
}
