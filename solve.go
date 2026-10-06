package updater

import "sort"

// Solve finds the optimal lock-file update. Objectives are ordered
// lexicographically:
//
//  1. feasibility — no banned versions and every requirement satisfied,
//     including requirements of newly added dependencies (an old version
//     may never be kept by violating a new dependency);
//  2. minimize changed old-lock packages (new dependencies do not count);
//  3. minimize total package count (new dependencies count here);
//  4. maximize the version vector lexicographically by package name.
//
// Solve returns nil when no feasible update exists.
func (p *Problem) Solve() *Solution {
	s := &solver{prob: p, assigned: map[string]string{}}
	for pkg := range p.OldLock {
		s.pending = append(s.pending, pkg)
	}
	sort.Strings(s.pending)
	s.search()
	if s.best == nil {
		return nil
	}
	return p.newSolution(s.best)
}

func (p *Problem) newSolution(assignment map[string]string) *Solution {
	changes, total := p.objectiveOf(assignment)
	versions := make(map[string]string, len(assignment))
	for pkg, version := range assignment {
		versions[pkg] = version
	}
	return &Solution{Versions: versions, Changes: changes, TotalPackages: total}
}

type solver struct {
	prob     *Problem
	assigned map[string]string
	pending  []string
	best     map[string]string
}

func (s *solver) search() {
	if len(s.pending) == 0 {
		s.record()
		return
	}
	pkg := s.pending[0]
	s.pending = s.pending[1:]

	assignedChanges, assignedTotal := s.partialObjective()
	for _, version := range s.versionOrder(pkg) {
		if s.prob.isBanned(pkg, version) {
			continue
		}
		if !s.consistent(pkg, version) {
			continue
		}
		changes := assignedChanges
		if old, ok := s.prob.OldLock[pkg]; ok && old != version {
			changes++
		}
		// Bound: changes and total only grow as pending packages get
		// assigned, so prune when the best solution is already
		// unreachable on the two primary objectives.
		if s.best != nil {
			bestChanges, bestTotal := s.prob.objectiveOf(s.best)
			if changes > bestChanges {
				continue
			}
			if changes == bestChanges && assignedTotal+1+len(s.pending) > bestTotal {
				continue
			}
		}
		s.assigned[pkg] = version
		added := s.extendPending(pkg, version)
		s.search()
		s.pending = s.pending[:len(s.pending)-added]
		delete(s.assigned, pkg)
	}
	s.pending = append([]string{pkg}, s.pending...)
}

// versionOrder lists candidate versions of pkg in the order most likely
// to reach the optimum early: the locked version first, then the
// remaining versions from highest to lowest.
func (s *solver) versionOrder(pkg string) []string {
	candidates := s.prob.Candidates[pkg]
	order := make([]string, 0, len(candidates))
	old, locked := s.prob.OldLock[pkg]
	if locked {
		for _, v := range candidates {
			if v == old {
				order = append(order, v)
			}
		}
	}
	rest := make([]string, 0, len(candidates))
	for _, v := range candidates {
		if locked && v == old {
			continue
		}
		rest = append(rest, v)
	}
	sort.Slice(rest, func(i, j int) bool {
		return CompareVersions(rest[i], rest[j]) > 0
	})
	return append(order, rest...)
}

// consistent reports whether assigning pkg=version violates any
// requirement between pkg and the already assigned packages, in either
// direction.
func (s *solver) consistent(pkg, version string) bool {
	for _, req := range s.prob.requirementsOf(pkg, version) {
		if depVersion, ok := s.assigned[req.Package]; ok && !req.Allows(depVersion) {
			return false
		}
	}
	for other, otherVersion := range s.assigned {
		for _, req := range s.prob.requirementsOf(other, otherVersion) {
			if req.Package == pkg && !req.Allows(version) {
				return false
			}
		}
	}
	return true
}

// extendPending queues the not-yet-assigned dependencies of
// pkg=version and returns how many were added.
func (s *solver) extendPending(pkg, version string) int {
	added := 0
	for _, req := range s.prob.requirementsOf(pkg, version) {
		if _, ok := s.assigned[req.Package]; ok {
			continue
		}
		if queued(s.pending, req.Package) {
			continue
		}
		s.pending = append(s.pending, req.Package)
		added++
	}
	return added
}

func queued(list []string, pkg string) bool {
	for _, q := range list {
		if q == pkg {
			return true
		}
	}
	return false
}

func (s *solver) partialObjective() (changes, total int) {
	for pkg, version := range s.assigned {
		if old, ok := s.prob.OldLock[pkg]; ok && old != version {
			changes++
		}
	}
	return changes, len(s.assigned)
}

func (s *solver) record() {
	assignment := make(map[string]string, len(s.assigned))
	for pkg, version := range s.assigned {
		assignment[pkg] = version
	}
	if s.best == nil || s.prob.betterSolution(assignment, s.best) {
		s.best = assignment
	}
}
