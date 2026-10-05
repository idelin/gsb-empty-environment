package unitinfer

// consistent reports whether the constraint subset is solvable over Z.
func consistent(cs []Constraint, numDims, nvars int) bool {
	a, b, n := buildSystem(cs, numDims, nvars)
	_, _, ok := solveSystem(a, b, n)
	return ok
}

// MinimalConflict returns a subset-minimal inconsistent subset of cs:
// the subset itself has no integer solution, but removing any one of its
// constraints makes it solvable. Computed by deletion-based minimization,
// so it is minimal (not necessarily minimum). Returns nil if cs is solvable.
func MinimalConflict(cs []Constraint, numDims, nvars int) []Constraint {
	if consistent(cs, numDims, nvars) {
		return nil
	}
	core := make([]Constraint, len(cs))
	copy(core, cs)
	i := 0
	for i < len(core) {
		trial := make([]Constraint, 0, len(core)-1)
		trial = append(trial, core[:i]...)
		trial = append(trial, core[i+1:]...)
		if !consistent(trial, numDims, nvars) {
			core = trial // constraint i is redundant for the conflict
		} else {
			i++ // constraint i is needed to keep the conflict
		}
	}
	return core
}
