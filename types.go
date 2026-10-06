// Package updater computes optimal lock-file updates under dependency
// constraints, a security blocklist, and lexicographically ordered
// objectives.
package updater

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// PackageVersion identifies one exact version of one package.
type PackageVersion struct {
	Package string `json:"package"`
	Version string `json:"version"`
}

// Requirement constrains the version of a dependency. Allowed lists the
// acceptable versions; an empty Allowed means any candidate version.
type Requirement struct {
	Package string   `json:"package"`
	Allowed []string `json:"allowed"`
}

// Allows reports whether version v satisfies the requirement.
func (r Requirement) Allows(v string) bool {
	if len(r.Allowed) == 0 {
		return true
	}
	for _, a := range r.Allowed {
		if a == v {
			return true
		}
	}
	return false
}

// Constraint lists the requirements of one exact package version.
type Constraint struct {
	Package  string        `json:"package"`
	Version  string        `json:"version"`
	Requires []Requirement `json:"requires"`
}

// Problem describes one lock-file update.
type Problem struct {
	// OldLock maps package name to the currently locked version. Every
	// old-lock package must remain present in the solution.
	OldLock map[string]string `json:"oldLock"`
	// Candidates maps package name to the installable versions.
	Candidates map[string][]string `json:"candidates"`
	// Banned lists versions blocked by the security policy.
	Banned []PackageVersion `json:"banned"`
	// Constraints lists per-version dependency requirements.
	Constraints []Constraint `json:"constraints"`
}

// Solution is the result of an update.
type Solution struct {
	// Versions maps package name to the selected version.
	Versions map[string]string `json:"versions"`
	// Changes counts old-lock packages whose version changed. Newly
	// added dependencies are not changes.
	Changes int `json:"changes"`
	// TotalPackages counts every package in the solution, including
	// newly added dependencies.
	TotalPackages int `json:"totalPackages"`
}

// requirementsOf returns the requirements of one exact package version.
func (p *Problem) requirementsOf(pkg, version string) []Requirement {
	for _, c := range p.Constraints {
		if c.Package == pkg && c.Version == version {
			return c.Requires
		}
	}
	return nil
}

func (p *Problem) isBanned(pkg, version string) bool {
	for _, b := range p.Banned {
		if b.Package == pkg && b.Version == version {
			return true
		}
	}
	return false
}

func (p *Problem) isCandidate(pkg, version string) bool {
	for _, c := range p.Candidates[pkg] {
		if c == version {
			return true
		}
	}
	return false
}

// Validate checks that a complete assignment is a feasible solution:
// every old-lock package is present at a candidate, non-banned version,
// and every requirement of every selected version — including those of
// newly added dependencies — is satisfied inside the assignment.
func (p *Problem) Validate(assignment map[string]string) error {
	for pkg, oldVersion := range p.OldLock {
		if assignment[pkg] == "" {
			return fmt.Errorf("old-lock package %q (locked at %s) is missing", pkg, oldVersion)
		}
	}
	for pkg, version := range assignment {
		if !p.isCandidate(pkg, version) {
			return fmt.Errorf("%s %s is not a candidate version", pkg, version)
		}
		if p.isBanned(pkg, version) {
			return fmt.Errorf("%s %s is banned by the security policy", pkg, version)
		}
	}
	for pkg, version := range assignment {
		for _, req := range p.requirementsOf(pkg, version) {
			depVersion, ok := assignment[req.Package]
			if !ok {
				return fmt.Errorf("%s %s requires %s, which is not selected", pkg, version, req.Package)
			}
			if !req.Allows(depVersion) {
				return fmt.Errorf("%s %s requires %s at one of %v, got %s",
					pkg, version, req.Package, req.Allowed, depVersion)
			}
		}
	}
	return nil
}

// objectiveOf computes the ordered objective values of a solution.
func (p *Problem) objectiveOf(assignment map[string]string) (changes, total int) {
	for pkg, oldVersion := range p.OldLock {
		if assignment[pkg] != oldVersion {
			changes++
		}
	}
	return changes, len(assignment)
}

// betterSolution reports whether candidate a is strictly preferred over
// candidate b under the ordered objectives:
//  1. fewer changed old-lock packages,
//  2. fewer total packages,
//  3. lexicographically larger version vector (packages ordered by name,
//     higher version preferred; a package absent from one solution
//     compares as the empty version).
func (p *Problem) betterSolution(a, b map[string]string) bool {
	aChanges, aTotal := p.objectiveOf(a)
	bChanges, bTotal := p.objectiveOf(b)
	if aChanges != bChanges {
		return aChanges < bChanges
	}
	if aTotal != bTotal {
		return aTotal < bTotal
	}
	names := map[string]bool{}
	for pkg := range a {
		names[pkg] = true
	}
	for pkg := range b {
		names[pkg] = true
	}
	sorted := make([]string, 0, len(names))
	for pkg := range names {
		sorted = append(sorted, pkg)
	}
	sort.Strings(sorted)
	for _, pkg := range sorted {
		av, bv := a[pkg], b[pkg]
		if av == bv {
			continue
		}
		return CompareVersions(av, bv) > 0
	}
	return false
}

// CompareVersions compares dotted numeric versions, ignoring a leading
// "v". Non-numeric segments fall back to string comparison. It returns a
// negative, zero, or positive value for a < b, a == b, a > b.
func CompareVersions(a, b string) int {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var sa, sb string
		if i < len(as) {
			sa = as[i]
		}
		if i < len(bs) {
			sb = bs[i]
		}
		na, errA := strconv.Atoi(sa)
		nb, errB := strconv.Atoi(sb)
		if errA == nil && errB == nil {
			if na != nb {
				return na - nb
			}
			continue
		}
		if sa != sb {
			return strings.Compare(sa, sb)
		}
	}
	return 0
}
