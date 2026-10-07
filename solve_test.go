package resolve

import (
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func v(major int) Version { return Version{Major: major} }

func pkg(name string, versions ...[]Dep) *Package {
	p := &Package{Name: name}
	for i, deps := range versions {
		p.Versions = append(p.Versions, PackageVersion{Version: v(i + 1), Deps: deps})
	}
	return p
}

func manifest(pkgs ...*Package) *Manifest {
	m := &Manifest{Packages: map[string]*Package{}}
	for _, p := range pkgs {
		m.Packages[p.Name] = p
	}
	return m
}

func mustSolve(t *testing.T, m *Manifest, reqs ...string) Result {
	t.Helper()
	r, err := Solve(m, reqs)
	if err != nil {
		t.Fatalf("Solve error: %v", err)
	}
	return r
}

func TestBasicLock(t *testing.T) {
	m := manifest(
		pkg("app", nil, []Dep{{Name: "lib", Min: v(1), Max: v(2)}}),
		pkg("lib", nil, nil),
	)
	r := mustSolve(t, m, "app")
	if !r.OK() {
		t.Fatalf("unexpected unsat: %s", r.Reason)
	}
	want := map[string]Version{"app": v(2), "lib": v(2)}
	if !reflect.DeepEqual(r.Locked, want) {
		t.Fatalf("got %v, want %v", r.Locked, want)
	}
	if got := r.String(); got != "app 2.0.0\nlib 2.0.0\n" {
		t.Fatalf("lock output %q", got)
	}
}

func TestLexicographicRequestVector(t *testing.T) {
	// a@2 conflicts with b@2, so the best vector is (a@2, b@1): a dominates.
	m := manifest(pkg("a", nil, nil), pkg("b", nil, nil))
	m.Conflicts = []Conflict{{PackageA: "a", VersionA: v(2), PackageB: "b", VersionB: v(2)}}
	r := mustSolve(t, m, "a", "b")
	if !r.OK() {
		t.Fatalf("unexpected unsat: %s", r.Reason)
	}
	want := map[string]Version{"a": v(2), "b": v(1)}
	if !reflect.DeepEqual(r.Locked, want) {
		t.Fatalf("got %v, want %v", r.Locked, want)
	}
	// Reversed priority must pick (b@2, a@1).
	r = mustSolve(t, m, "b", "a")
	want = map[string]Version{"a": v(1), "b": v(2)}
	if !reflect.DeepEqual(r.Locked, want) {
		t.Fatalf("reversed: got %v, want %v", r.Locked, want)
	}
}

func TestMinTransitiveCount(t *testing.T) {
	// app@1 depends on lib [1,2]. lib@1 drags in extra; lib@2 does not.
	// The request vector (app@1) is fixed, so the resolver must prefer
	// lib@2 to keep the lock small.
	m := manifest(
		pkg("app", []Dep{{Name: "lib", Min: v(1), Max: v(2)}}),
		pkg("lib", []Dep{{Name: "extra", Min: v(1), Max: v(1)}}, nil),
		pkg("extra", nil),
	)
	r := mustSolve(t, m, "app")
	if !r.OK() {
		t.Fatalf("unexpected unsat: %s", r.Reason)
	}
	want := map[string]Version{"app": v(1), "lib": v(2)}
	if !reflect.DeepEqual(r.Locked, want) {
		t.Fatalf("got %v, want %v", r.Locked, want)
	}
}

func TestConflictUnsat(t *testing.T) {
	m := manifest(
		pkg("app", []Dep{{Name: "x", Min: v(1), Max: v(1)}, {Name: "y", Min: v(1), Max: v(1)}}),
		pkg("x", nil),
		pkg("y", nil),
	)
	m.Conflicts = []Conflict{{PackageA: "x", VersionA: v(1), PackageB: "y", VersionB: v(1)}}
	r := mustSolve(t, m, "app")
	if r.OK() {
		t.Fatalf("expected unsat, got %v", r.Locked)
	}
	if !strings.Contains(r.Reason, "conflicts with") {
		t.Fatalf("reason should mention the conflict, got %q", r.Reason)
	}
}

func TestRangeUnsat(t *testing.T) {
	m := manifest(
		pkg("app", []Dep{{Name: "lib", Min: v(5), Max: v(6)}}),
		pkg("lib", nil, nil),
	)
	r := mustSolve(t, m, "app")
	if r.OK() {
		t.Fatalf("expected unsat, got %v", r.Locked)
	}
	if !strings.Contains(r.Reason, `"lib"`) {
		t.Fatalf("reason should mention lib, got %q", r.Reason)
	}
}

func TestDiamondRangeIntersection(t *testing.T) {
	// app needs left and right; left wants lib<=2, right wants lib>=2.
	m := manifest(
		pkg("app", []Dep{{Name: "left", Min: v(1), Max: v(1)}, {Name: "right", Min: v(1), Max: v(1)}}),
		pkg("left", []Dep{{Name: "lib", Min: v(1), Max: v(2)}}),
		pkg("right", []Dep{{Name: "lib", Min: v(2), Max: v(3)}}),
		pkg("lib", nil, nil, nil),
	)
	r := mustSolve(t, m, "app")
	if !r.OK() {
		t.Fatalf("unexpected unsat: %s", r.Reason)
	}
	if r.Locked["lib"] != v(2) {
		t.Fatalf("lib should resolve to 2.0.0, got %v", r.Locked)
	}
}

func TestUnknownRequest(t *testing.T) {
	r := mustSolve(t, manifest(pkg("a", nil)), "ghost")
	if r.OK() || !strings.Contains(r.Reason, "not in the manifest") {
		t.Fatalf("got %+v", r)
	}
}

func TestInvalidManifest(t *testing.T) {
	m := manifest(pkg("a", []Dep{{Name: "b", Min: v(2), Max: v(1)}}), pkg("b", nil))
	if _, err := Solve(m, []string{"a"}); err == nil {
		t.Fatal("expected validation error for empty range")
	}
}

// --- Exhaustive cross-check against brute force enumeration ---

// feasible reports whether assignment is a complete, valid solution.
func feasible(m *Manifest, requests []string, as map[string]Version) bool {
	for _, r := range requests {
		if _, ok := as[r]; !ok {
			return false
		}
	}
	for name, ver := range as {
		p := m.Packages[name]
		if p == nil || p.version(ver) == nil {
			return false
		}
		for _, d := range p.version(ver).Deps {
			dv, ok := as[d.Name]
			if !ok || dv.Compare(d.Min) < 0 || dv.Compare(d.Max) > 0 {
				return false
			}
		}
	}
	for _, c := range m.Conflicts {
		if a, ok := as[c.PackageA]; ok && a == c.VersionA {
			if b, ok := as[c.PackageB]; ok && b == c.VersionB {
				return false
			}
		}
	}
	return true
}

// better compares two feasible assignments by the solver's objective:
// lexicographic request vector first, then fewer packages.
func better(a, b map[string]Version, requests []string) bool {
	if b == nil {
		return true
	}
	for _, r := range requests {
		if c := a[r].Compare(b[r]); c != 0 {
			return c > 0
		}
	}
	return len(a) < len(b)
}

// bruteForce enumerates every assignment (each package absent or at one of
// its versions) and returns the objective-best feasible one.
func bruteForce(m *Manifest, requests []string) (map[string]Version, bool) {
	names := make([]string, 0, len(m.Packages))
	for name := range m.Packages {
		names = append(names, name)
	}
	sort.Strings(names)

	var best map[string]Version
	cur := map[string]Version{}
	var rec func(i int)
	rec = func(i int) {
		if i == len(names) {
			if feasible(m, requests, cur) && better(cur, best, requests) {
				best = make(map[string]Version, len(cur))
				for k, val := range cur {
					best[k] = val
				}
			}
			return
		}
		rec(i + 1) // package absent
		for _, pv := range m.Packages[names[i]].Versions {
			cur[names[i]] = pv.Version
			rec(i + 1)
			delete(cur, names[i])
		}
	}
	rec(0)
	return best, best != nil
}

// randomManifest builds a small random manifest. Ranges use majors 1..3
// regardless of the target's real versions so infeasible cases occur often.
func randomManifest(rng *rand.Rand) (*Manifest, []string) {
	n := 2 + rng.Intn(4) // 2..5 packages
	names := make([]string, n)
	m := &Manifest{Packages: map[string]*Package{}}
	for i := range names {
		names[i] = string(rune('a' + i))
	}
	for _, name := range names {
		k := 1 + rng.Intn(3) // 1..3 versions
		p := &Package{Name: name}
		for j := 0; j < k; j++ {
			pv := PackageVersion{Version: v(j + 1)}
			for _, other := range names {
				if other == name || rng.Float64() >= 0.35 {
					continue
				}
				lo := 1 + rng.Intn(3)
				hi := lo + rng.Intn(4-lo)
				pv.Deps = append(pv.Deps, Dep{Name: other, Min: v(lo), Max: v(hi)})
			}
			p.Versions = append(p.Versions, pv)
		}
		m.Packages[name] = p
	}
	for i := 0; i < rng.Intn(3); i++ { // 0..2 conflicts
		a := names[rng.Intn(n)]
		b := names[rng.Intn(n)]
		if a == b {
			continue
		}
		va := v(1 + rng.Intn(len(m.Packages[a].Versions)))
		vb := v(1 + rng.Intn(len(m.Packages[b].Versions)))
		m.Conflicts = append(m.Conflicts, Conflict{PackageA: a, VersionA: va, PackageB: b, VersionB: vb})
	}
	// 1..2 distinct requests.
	reqs := []string{names[rng.Intn(n)]}
	if rng.Float64() < 0.5 {
		if r2 := names[rng.Intn(n)]; r2 != reqs[0] {
			reqs = append(reqs, r2)
		}
	}
	return m, reqs
}

func TestExhaustiveCrossCheck(t *testing.T) {
	const seeds = 4000
	unsat := 0
	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		m, reqs := randomManifest(rng)

		got, err := Solve(m, reqs)
		if err != nil {
			t.Fatalf("seed %d: Solve error: %v", seed, err)
		}
		want, ok := bruteForce(m, reqs)

		if !ok {
			unsat++
			if got.OK() {
				t.Fatalf("seed %d: brute force says unsat, solver locked %v (reqs %v)", seed, got.Locked, reqs)
			}
			if got.Reason == "" {
				t.Fatalf("seed %d: unsat result without reason", seed)
			}
			continue
		}
		if !got.OK() {
			t.Fatalf("seed %d: solver says unsat (%s), brute force found %v (reqs %v)", seed, got.Reason, want, reqs)
		}
		if !feasible(m, reqs, got.Locked) {
			t.Fatalf("seed %d: solver lock %v is not feasible (reqs %v)", seed, got.Locked, reqs)
		}
		if !better(got.Locked, want, reqs) && !better(want, got.Locked, reqs) {
			continue // identical objective value, different ties
		}
		if better(want, got.Locked, reqs) {
			t.Fatalf("seed %d: solver lock %v is worse than brute force %v (reqs %v)", seed, got.Locked, want, reqs)
		}
	}
	t.Logf("checked %d random manifests, %d unsatisfiable", seeds, unsat)
}
