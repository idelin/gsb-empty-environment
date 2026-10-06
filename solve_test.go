package updater

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func solveAndValidate(t *testing.T, p *Problem) *Solution {
	t.Helper()
	solution := p.Solve()
	if solution == nil {
		t.Fatal("Solve returned nil, want a feasible solution")
	}
	if err := p.Validate(solution.Versions); err != nil {
		t.Fatalf("Solve returned invalid solution: %v", err)
	}
	return solution
}

func TestKeepOldVersionsWhenPossible(t *testing.T) {
	p := &Problem{
		OldLock:    map[string]string{"a": "1.0.0", "b": "2.0.0"},
		Candidates: map[string][]string{"a": {"1.0.0", "1.1.0"}, "b": {"2.0.0", "3.0.0"}},
	}
	solution := solveAndValidate(t, p)
	if solution.Changes != 0 || solution.TotalPackages != 2 {
		t.Fatalf("got changes=%d total=%d, want 0 changes and 2 packages", solution.Changes, solution.TotalPackages)
	}
	if solution.Versions["a"] != "1.0.0" || solution.Versions["b"] != "2.0.0" {
		t.Fatalf("got %v, want old versions kept", solution.Versions)
	}
}

func TestBannedVersionForcesMinimalChange(t *testing.T) {
	p := &Problem{
		OldLock:    map[string]string{"a": "1.0.0", "b": "2.0.0"},
		Candidates: map[string][]string{"a": {"1.0.0", "1.1.0", "1.2.0"}, "b": {"2.0.0"}},
		Banned:     []PackageVersion{{Package: "a", Version: "1.0.0"}},
	}
	solution := solveAndValidate(t, p)
	if solution.Changes != 1 {
		t.Fatalf("got %d changes, want exactly 1", solution.Changes)
	}
	if solution.Versions["a"] != "1.2.0" {
		t.Fatalf("got a=%s, want highest non-banned candidate 1.2.0", solution.Versions["a"])
	}
	if solution.Versions["b"] != "2.0.0" {
		t.Fatalf("got b=%s, want b untouched at 2.0.0", solution.Versions["b"])
	}
}

func TestNewDependencyIsNotAChange(t *testing.T) {
	p := &Problem{
		OldLock:    map[string]string{"app": "1.0.0"},
		Candidates: map[string][]string{"app": {"1.0.0"}, "dep": {"1.0.0"}},
		Constraints: []Constraint{{
			Package: "app", Version: "1.0.0",
			Requires: []Requirement{{Package: "dep"}},
		}},
	}
	solution := solveAndValidate(t, p)
	if solution.Changes != 0 {
		t.Fatalf("got %d changes, want 0: new dependencies are not changes", solution.Changes)
	}
	if solution.TotalPackages != 2 {
		t.Fatalf("got %d total packages, want 2: new dependencies count toward the total", solution.TotalPackages)
	}
}

// The core safety property: an old version may never be kept by
// violating the requirements of a newly added dependency. Here app pulls
// in newpkg, and newpkg requires lib 2.0.0, so lib cannot stay at its
// old 1.0.0 even though minimizing changes would prefer that.
func TestCannotKeepOldVersionByViolatingNewDependency(t *testing.T) {
	p := &Problem{
		OldLock: map[string]string{"app": "1.0.0", "lib": "1.0.0"},
		Candidates: map[string][]string{
			"app":    {"1.0.0"},
			"lib":    {"1.0.0", "2.0.0"},
			"newpkg": {"1.0.0"},
		},
		Constraints: []Constraint{
			{Package: "app", Version: "1.0.0",
				Requires: []Requirement{{Package: "newpkg"}}},
			{Package: "newpkg", Version: "1.0.0",
				Requires: []Requirement{{Package: "lib", Allowed: []string{"2.0.0"}}}},
		},
	}
	solution := solveAndValidate(t, p)
	if solution.Versions["lib"] != "2.0.0" {
		t.Fatalf("got lib=%s, want 2.0.0: newpkg's requirement must be respected", solution.Versions["lib"])
	}
	if solution.Changes != 1 || solution.TotalPackages != 3 {
		t.Fatalf("got changes=%d total=%d, want 1 change and 3 packages", solution.Changes, solution.TotalPackages)
	}
}

// With changes tied, fewer total packages beats a higher version vector:
// adding a dependency is only worthwhile when it buys feasibility or
// fewer changes.
func TestTotalPackagesOutranksVersionVector(t *testing.T) {
	p := &Problem{
		OldLock:    map[string]string{"app": "1.0.0"},
		Candidates: map[string][]string{"app": {"1.0.0", "2.0.0", "3.0.0"}, "dep": {"1.0.0"}},
		Banned:     []PackageVersion{{Package: "app", Version: "1.0.0"}},
		Constraints: []Constraint{{
			Package: "app", Version: "3.0.0",
			Requires: []Requirement{{Package: "dep"}},
		}},
	}
	solution := solveAndValidate(t, p)
	if solution.Versions["app"] != "2.0.0" || solution.TotalPackages != 1 {
		t.Fatalf("got %v (total %d), want app=2.0.0 alone: fewer packages beats higher version",
			solution.Versions, solution.TotalPackages)
	}
}

// With changes and total tied, the version vector is maximized
// lexicographically by package name: a's version is maximized first.
func TestVersionVectorMaximizedLexicographically(t *testing.T) {
	p := &Problem{
		OldLock: map[string]string{"a": "1.0.0", "b": "1.0.0"},
		Candidates: map[string][]string{
			"a": {"1.0.0", "1.5.0", "2.0.0"},
			"b": {"1.0.0", "1.5.0", "2.0.0"},
		},
		Banned: []PackageVersion{
			{Package: "a", Version: "1.0.0"},
			{Package: "b", Version: "1.0.0"},
		},
		Constraints: []Constraint{{
			Package: "a", Version: "2.0.0",
			Requires: []Requirement{{Package: "b", Allowed: []string{"1.5.0"}}},
		}},
	}
	solution := solveAndValidate(t, p)
	want := map[string]string{"a": "2.0.0", "b": "1.5.0"}
	if !reflect.DeepEqual(solution.Versions, want) {
		t.Fatalf("got %v, want %v", solution.Versions, want)
	}
}

func TestInfeasibleWhenOldVersionBannedAndIrreplaceable(t *testing.T) {
	p := &Problem{
		OldLock:    map[string]string{"a": "1.0.0"},
		Candidates: map[string][]string{"a": {"1.0.0"}},
		Banned:     []PackageVersion{{Package: "a", Version: "1.0.0"}},
	}
	if solution := p.Solve(); solution != nil {
		t.Fatalf("got %v, want nil: no feasible update exists", solution)
	}
	if solution := p.BruteForceSolve(); solution != nil {
		t.Fatalf("brute force got %v, want nil", solution)
	}
}

// Full-enumeration cross-check: on randomized small instances, Solve
// must return exactly the optimum found by enumerating every valid
// assignment.
func TestSolveMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 500; trial++ {
		problem := randomProblem(rng)
		got := problem.Solve()
		want := problem.BruteForceSolve()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: Solve=%+v, BruteForce=%+v\nproblem: %+v", trial, got, want, problem)
		}
	}
}

func randomProblem(rng *rand.Rand) *Problem {
	numPkgs := 2 + rng.Intn(4)
	names := make([]string, numPkgs)
	candidates := make(map[string][]string, numPkgs)
	for i := range names {
		names[i] = fmt.Sprintf("p%d", i)
		numVersions := 1 + rng.Intn(3)
		versions := make([]string, numVersions)
		for j := range versions {
			versions[j] = fmt.Sprintf("%d.0.0", j+1)
		}
		candidates[names[i]] = versions
	}

	oldLock := map[string]string{}
	for _, name := range names {
		if rng.Intn(2) == 0 {
			versions := candidates[name]
			oldLock[name] = versions[rng.Intn(len(versions))]
		}
	}
	if len(oldLock) == 0 {
		oldLock[names[0]] = candidates[names[0]][0]
	}

	var banned []PackageVersion
	for _, name := range names {
		for _, version := range candidates[name] {
			if rng.Intn(6) == 0 {
				banned = append(banned, PackageVersion{Package: name, Version: version})
			}
		}
	}

	var constraints []Constraint
	for _, name := range names {
		for _, version := range candidates[name] {
			if rng.Intn(2) != 0 {
				continue
			}
			var requires []Requirement
			for _, dep := range names {
				if dep == name || rng.Intn(2) == 0 {
					continue
				}
				var allowed []string
				for _, v := range candidates[dep] {
					if rng.Intn(2) == 0 {
						allowed = append(allowed, v)
					}
				}
				requires = append(requires, Requirement{Package: dep, Allowed: allowed})
			}
			if len(requires) > 0 {
				constraints = append(constraints, Constraint{
					Package: name, Version: version, Requires: requires,
				})
			}
		}
	}

	return &Problem{
		OldLock:     oldLock,
		Candidates:  candidates,
		Banned:      banned,
		Constraints: constraints,
	}
}
