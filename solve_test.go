package updater

import (
	"math/rand"
	"testing"
)

func v(major, minor, patch int) Version { return Version{major, minor, patch} }

func maxV(major, minor, patch int) *Version {
	mv := v(major, minor, patch)
	return &mv
}

// assertOptimal checks that Solve and BruteForce agree and that the
// solution matches the expected selection and objective values.
func assertOptimal(t *testing.T, p *Problem, want map[string]Version, changes, total int) {
	t.Helper()
	for name, solver := range map[string]func(*Problem) *Solution{"solve": Solve, "brute": BruteForce} {
		sol := solver(p)
		if sol == nil {
			t.Fatalf("%s: no solution, want %v", name, want)
		}
		if !p.Check(sol) {
			t.Fatalf("%s: infeasible solution %v", name, sol.Selected)
		}
		if sol.Changes != changes || sol.Total != total {
			t.Fatalf("%s: objective (changes=%d,total=%d), want (%d,%d)",
				name, sol.Changes, sol.Total, changes, total)
		}
		for pkg, wv := range want {
			if sol.Selected[pkg] != wv {
				t.Fatalf("%s: %s=%s, want %s (full: %v)", name, pkg, sol.Selected[pkg], wv, sol.Selected)
			}
		}
		if len(sol.Selected) != len(want) {
			t.Fatalf("%s: selected %v, want exactly %v", name, sol.Selected, want)
		}
	}
}

// The security denylist is the hardest constraint: a banned locked version
// must be replaced even though replacing it counts as a change.
func TestDenylistForcesChange(t *testing.T) {
	p := &Problem{
		Locked:     map[string]Version{"a": v(1, 0, 0)},
		Candidates: map[string][]Version{"a": {v(1, 0, 0), v(1, 1, 0), v(2, 0, 0)}},
		Banned:     map[string][]Version{"a": {v(1, 0, 0), v(2, 0, 0)}},
	}
	assertOptimal(t, p, map[string]Version{"a": v(1, 1, 0)}, 1, 1)
}

// With no constraints at all, every locked package keeps its old version:
// zero changes dominates any version upgrade.
func TestNoConstraintKeepsEverything(t *testing.T) {
	p := &Problem{
		Locked:     map[string]Version{"a": v(1, 0, 0), "b": v(2, 0, 0)},
		Candidates: map[string][]Version{"a": {v(1, 0, 0), v(3, 0, 0)}, "b": {v(2, 0, 0), v(4, 0, 0)}},
	}
	assertOptimal(t, p, map[string]Version{"a": v(1, 0, 0), "b": v(2, 0, 0)}, 0, 2)
}

// Among solutions with the same minimal change count, the version vector
// is maximized: a must move off a banned version, so it jumps to the
// highest allowed candidate while b stays put.
func TestMaximizeVectorAmongMinimalChanges(t *testing.T) {
	p := &Problem{
		Locked:     map[string]Version{"a": v(1, 0, 0), "b": v(1, 0, 0)},
		Candidates: map[string][]Version{"a": {v(1, 0, 0), v(2, 0, 0), v(3, 0, 0)}, "b": {v(1, 0, 0), v(2, 0, 0)}},
		Banned:     map[string][]Version{"a": {v(1, 0, 0)}},
	}
	assertOptimal(t, p, map[string]Version{"a": v(3, 0, 0), "b": v(1, 0, 0)}, 1, 2)
}

// Core guarantee: the solver may not keep an old locked version when a new
// dependency requires a newer one. lib@1.0.0 is banned, lib@2.0.0 requires
// app >= 2.0.0, so app must move even though app@1.0.0 itself is allowed.
func TestCannotKeepOldVersionViolatingNewDependency(t *testing.T) {
	p := &Problem{
		Locked: map[string]Version{"app": v(1, 0, 0), "lib": v(1, 0, 0)},
		Candidates: map[string][]Version{
			"app": {v(1, 0, 0), v(2, 0, 0)},
			"lib": {v(1, 0, 0), v(2, 0, 0)},
		},
		Banned: map[string][]Version{"lib": {v(1, 0, 0)}},
		Deps: map[string]map[Version][]Requirement{
			"lib": {v(2, 0, 0): {{Pkg: "app", Min: v(2, 0, 0)}}},
		},
	}
	assertOptimal(t, p, map[string]Version{"app": v(2, 0, 0), "lib": v(2, 0, 0)}, 2, 2)

	// Full enumeration must confirm that no feasible solution keeps
	// app@1.0.0 at all.
	names := p.Candidates["app"]
	_ = names
	keptOld := false
	for _, libV := range p.Candidates["lib"] {
		sol, ok := p.buildSolution(map[string]Version{"app": v(1, 0, 0), "lib": libV})
		if ok && sol != nil {
			keptOld = true
		}
	}
	if keptOld {
		t.Fatal("enumeration found a feasible solution keeping app@1.0.0")
	}
}

// New dependencies do not count as changes but do count toward the total
// package count: adding x costs nothing in the primary objective, yet the
// total is reported as a secondary objective.
func TestNewDependencyCountsInTotalNotChanges(t *testing.T) {
	p := &Problem{
		Locked:     map[string]Version{"a": v(1, 0, 0)},
		Candidates: map[string][]Version{"a": {v(1, 0, 0), v(2, 0, 0)}, "x": {v(1, 0, 0)}},
		Banned:     map[string][]Version{"a": {v(1, 0, 0)}},
		Deps: map[string]map[Version][]Requirement{
			"a": {v(2, 0, 0): {{Pkg: "x", Min: v(1, 0, 0)}}},
		},
	}
	assertOptimal(t, p, map[string]Version{"a": v(2, 0, 0), "x": v(1, 0, 0)}, 1, 2)
}

// When the change count and the locked version vector tie, fewer total
// packages win: x@2.0.0 is chosen over x@1.0.0 because x@1.0.0 would pull
// in an extra transitive dependency y.
func TestFewerTotalPackagesBreaksTies(t *testing.T) {
	p := &Problem{
		Locked:     map[string]Version{"a": v(1, 0, 0)},
		Candidates: map[string][]Version{"a": {v(1, 0, 0), v(2, 0, 0)}, "x": {v(1, 0, 0), v(2, 0, 0)}, "y": {v(1, 0, 0)}},
		Banned:     map[string][]Version{"a": {v(1, 0, 0)}},
		Deps: map[string]map[Version][]Requirement{
			"a": {v(2, 0, 0): {{Pkg: "x", Min: v(1, 0, 0)}}},
			"x": {v(1, 0, 0): {{Pkg: "y", Min: v(1, 0, 0)}}},
		},
	}
	assertOptimal(t, p, map[string]Version{"a": v(2, 0, 0), "x": v(2, 0, 0)}, 1, 2)
}

// A locked package whose pinned version is no longer among the candidates
// must change, and the change is counted.
func TestMissingLockedVersionForcesChange(t *testing.T) {
	p := &Problem{
		Locked:     map[string]Version{"a": v(0, 9, 0)},
		Candidates: map[string][]Version{"a": {v(1, 0, 0), v(1, 1, 0)}},
	}
	assertOptimal(t, p, map[string]Version{"a": v(1, 1, 0)}, 1, 1)
}

// Unsatisfiable requirements yield no solution from both solvers.
func TestInfeasible(t *testing.T) {
	p := &Problem{
		Locked:     map[string]Version{"a": v(1, 0, 0)},
		Candidates: map[string][]Version{"a": {v(1, 0, 0)}, "b": {v(1, 0, 0)}},
		Deps: map[string]map[Version][]Requirement{
			"a": {v(1, 0, 0): {{Pkg: "b", Min: v(2, 0, 0)}}},
		},
	}
	if sol := Solve(p); sol != nil {
		t.Fatalf("Solve returned %v, want nil", sol.Selected)
	}
	if sol := BruteForce(p); sol != nil {
		t.Fatalf("BruteForce returned %v, want nil", sol.Selected)
	}
}

// TestRandomizedAgainstBruteForce cross-checks Solve against full
// enumeration on many small random problems, including random denylists,
// random dependency edges, and locked versions missing from candidates.
func TestRandomizedAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(20261008))
	allVersions := []Version{v(1, 0, 0), v(1, 1, 0), v(2, 0, 0)}
	pkgNames := []string{"a", "b", "c", "d", "e"}

	for iter := 0; iter < 500; iter++ {
		n := 2 + r.Intn(4) // 2..5 packages in the universe
		pkgs := pkgNames[:n]

		p := &Problem{
			Locked:     map[string]Version{},
			Candidates: map[string][]Version{},
			Banned:     map[string][]Version{},
			Deps:       map[string]map[Version][]Requirement{},
		}
		for _, pkg := range pkgs {
			// 1..3 distinct candidates per package.
			perm := r.Perm(len(allVersions))
			k := 1 + r.Intn(len(allVersions))
			for _, idx := range perm[:k] {
				p.Candidates[pkg] = append(p.Candidates[pkg], allVersions[idx])
			}
			// Random denylist entries.
			for _, ver := range allVersions {
				if r.Intn(4) == 0 {
					p.Banned[pkg] = append(p.Banned[pkg], ver)
				}
			}
			// Random requirements per selectable version.
			for _, ver := range p.Candidates[pkg] {
				for _, target := range pkgs {
					if target == pkg || r.Intn(3) != 0 {
						continue
					}
					req := Requirement{Pkg: target, Min: allVersions[r.Intn(len(allVersions))]}
					if r.Intn(2) == 0 {
						req.Max = maxV(2, 0, 0)
					}
					if p.Deps[pkg] == nil {
						p.Deps[pkg] = map[Version][]Requirement{}
					}
					p.Deps[pkg][ver] = append(p.Deps[pkg][ver], req)
				}
			}
		}
		// Lock a random non-empty subset; the pinned version may be absent
		// from the candidates.
		for _, pkg := range pkgs {
			if r.Intn(2) == 0 || len(p.Locked) == 0 {
				p.Locked[pkg] = allVersions[r.Intn(len(allVersions))]
			}
		}

		got := Solve(p)
		want := BruteForce(p)
		if want == nil {
			if got != nil {
				t.Fatalf("iter %d: Solve found %v, BruteForce found nothing", iter, got.Selected)
			}
			continue
		}
		if got == nil {
			t.Fatalf("iter %d: Solve found nothing, BruteForce found %v", iter, want.Selected)
		}
		if !p.Check(got) {
			t.Fatalf("iter %d: Solve returned infeasible %v", iter, got.Selected)
		}
		if !equivalent(got, want) {
			t.Fatalf("iter %d: Solve (changes=%d,total=%d,sel=%v) != BruteForce (changes=%d,total=%d,sel=%v)",
				iter, got.Changes, got.Total, got.Selected, want.Changes, want.Total, want.Selected)
		}
	}
}
