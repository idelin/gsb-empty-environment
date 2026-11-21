package admission

import "reflect"

// DiffReport explains a divergence between the incremental engine and the
// independent interpreter.
type DiffReport struct {
	// FirstLeaf is the first vantage path where the engines disagree.
	FirstLeaf Path
	// EngineLeaf is the incremental engine leaf, nil when missing.
	EngineLeaf *EffectiveLeaf
	// InterpreterLeaf is the interpreter leaf, nil when missing.
	InterpreterLeaf *EffectiveLeaf
	// Reason summarizes the difference.
	Reason string
}

// CrossCheckApply admits a batch through the incremental engine and, in
// parallel conceptually, replays it with the independent whole-tree
// interpreter. It fails with ErrInterpreterMismatch whenever the two
// engines disagree on acceptance, error kind, or any per-leaf value and
// provenance closure. On success the returned tree is the incremental
// engine result and leaves are the interpreter's leaf set (identical to
// the engine's).
func CrossCheckApply(tree *Tree, policy *Policy, ops []Op) (*Tree, []EffectiveLeaf, *DiffReport, error) {
	next, engineErr := tree.Apply(policy, ops)

	interp := newInterpreter(policy)
	interp.seedFromTree(tree.root)
	interpErr, interpLeavesRaw := interp.replay(ops)
	if false && interpErr != nil {
		println("DEBUG interp rejected:", interpErr.Error())
	}

	if !isNilError(engineErr) || !isNilError(interpErr) {
		if !sameFailure(engineErr, interpErr) {
			return nil, nil, &DiffReport{
					Reason: failureReason(engineErr, interpErr),
				}, newError(ErrInterpreterMismatch, -1, nil,
					"engines disagree on batch acceptance: %s", failureReason(engineErr, interpErr))
		}
		return nil, nil, nil, engineErr
	}

	engineLeaves, err := next.EffectiveLeaves(policy)
	if err != nil {
		return nil, nil, nil, err
	}
	interpLeaves := toEffectiveLeaves(interpLeavesRaw)

	if report := diffLeaves(engineLeaves, interpLeaves); report != nil {
		return nil, nil, report, newError(ErrInterpreterMismatch, -1, report.FirstLeaf,
			"engines disagree at %v: %s", []string(report.FirstLeaf), report.Reason)
	}
	return next, engineLeaves, nil, nil
}

func sameFailure(a, b error) bool {
	if isNilError(a) != isNilError(b) {
		return false
	}
	if isNilError(a) {
		return true
	}
	ea, oka := a.(*Error)
	eb, okb := b.(*Error)
	if !oka || !okb {
		return a.Error() == b.Error()
	}
	return ea.Kind == eb.Kind
}

func isNilError(err error) bool {
	if err == nil {
		return true
	}
	v := reflect.ValueOf(err)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

func failureReason(engineErr, interpErr error) string {
	switch {
	case isNilError(engineErr) && !isNilError(interpErr):
		return "engine accepted but interpreter rejected: " + interpErr.Error()
	case !isNilError(engineErr) && isNilError(interpErr):
		return "interpreter accepted but engine rejected: " + engineErr.Error()
	case !isNilError(engineErr) && !isNilError(interpErr):
		return "engine=" + engineErr.Error() + " | interpreter=" + interpErr.Error()
	default:
		return "unexpected leaf difference"
	}
}

func diffLeaves(engine, interp []EffectiveLeaf) *DiffReport {
	if len(engine) != len(interp) {
		path := Path{}
		if len(interp) > 0 {
			path = interp[min(len(engine), len(interp)-1)].Path
		} else if len(engine) > 0 {
			path = engine[0].Path
		}
		return &DiffReport{
			FirstLeaf:       path,
			Reason:          "leaf count differs",
			EngineLeaf:      pickLeaf(engine, path),
			InterpreterLeaf: pickLeaf(interp, path),
		}
	}
	for i := range engine {
		if !leafEqual(engine[i], interp[i]) {
			return &DiffReport{
				FirstLeaf:       clonePath(engine[i].Path),
				Reason:          "value or provenance closure differs",
				EngineLeaf:      leafPtr(engine[i]),
				InterpreterLeaf: leafPtr(interp[i]),
			}
		}
	}
	return nil
}

func pickLeaf(leaves []EffectiveLeaf, path Path) *EffectiveLeaf {
	for i := range leaves {
		if pathEqual(leaves[i].Path, path) {
			return leafPtr(leaves[i])
		}
	}
	return nil
}

func leafPtr(leaf EffectiveLeaf) *EffectiveLeaf {
	cp := leaf
	return &cp
}

// MergeBatches decides whether two mutually independent batches may be
// merged into a combined admission decision. Both orders must be legal,
// and the resulting trees must be identical leaf by leaf including the
// terminal source and full reference closure. Identical literals backed by
// different authorization evidence therefore fail the merge even though
// the values match.
//
// Each order is cross-checked against the independent interpreter.
func MergeBatches(tree *Tree, policy *Policy, first, second []Op) (*Tree, []EffectiveLeaf, error) {
	ab, leavesAB, _, err := CrossCheckApply(tree, policy, append(append([]Op(nil), first...), second...))
	if err != nil {
		return nil, nil, err
	}

	_, leavesBA, _, err := CrossCheckApply(tree, policy, append(append([]Op(nil), second...), first...))
	if err != nil {
		return nil, nil, newError(ErrMergeConflict, -1, nil,
			"reverse ordering is not legal: %s", err.Error())
	}

	if !leavesEqual(leavesAB, leavesBA) {
		path := firstConflict(leavesAB, leavesBA)
		return nil, nil, newError(ErrMergeConflict, -1, path,
			"batch orders produce different values or source evidence at %v",
			[]string(path))
	}
	return ab, leavesAB, nil
}

func firstConflict(a, b []EffectiveLeaf) Path {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if !leafEqual(a[i], b[i]) {
			return clonePath(a[i].Path)
		}
	}
	if len(a) > n {
		return clonePath(a[n].Path)
	}
	if len(b) > n {
		return clonePath(b[n].Path)
	}
	return nil
}
