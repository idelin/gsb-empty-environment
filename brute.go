package updater

import "sort"

// BruteForceSolve independently verifies Solve by full enumeration: it
// enumerates every assignment in which each candidate package is either
// absent or pinned to one of its candidate versions, keeps only fully
// valid assignments, and returns the optimum under the same ordered
// objectives. It is exponential and intended for testing and
// cross-checking on small inputs.
func (p *Problem) BruteForceSolve() *Solution {
	pkgs := make([]string, 0, len(p.Candidates))
	for pkg := range p.Candidates {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)

	var best map[string]string
	assignment := make(map[string]string, len(pkgs))
	var enumerate func(i int)
	enumerate = func(i int) {
		if i == len(pkgs) {
			if p.Validate(assignment) != nil {
				return
			}
			if best == nil || p.betterSolution(assignment, best) {
				snapshot := make(map[string]string, len(assignment))
				for pkg, version := range assignment {
					snapshot[pkg] = version
				}
				best = snapshot
			}
			return
		}
		pkg := pkgs[i]
		// Option 1: the package is absent from the lock file.
		enumerate(i + 1)
		// Option 2: the package is pinned to each candidate version.
		for _, version := range p.Candidates[pkg] {
			assignment[pkg] = version
			enumerate(i + 1)
			delete(assignment, pkg)
		}
	}
	enumerate(0)

	if best == nil {
		return nil
	}
	return p.newSolution(best)
}
