package seccomp

// MergeFunc combines per-filter return values into the stacked verdict.
type MergeFunc func(results []uint32) uint32

// MinReturns is the literal "numerically smallest 32-bit return wins" rule
// requested by the task.
func MinReturns(results []uint32) uint32 {
	if len(results) == 0 {
		return RetAllow
	}
	m := results[0]
	for _, r := range results[1:] {
		if r < m {
			m = r
		}
	}
	return m
}

// MergeNumeric returns the numerically smallest return value (variadic
// convenience wrapper around MinReturns).
func MergeNumeric(results ...uint32) uint32 { return MinReturns(results) }

// KernelMerge implements the exact Linux seccomp stacking rule:
//
//	if ((cur_ret & SECCOMP_RET_ACTION) < (ret & SECCOMP_RET_ACTION))
//	    ret = cur_ret;
//
// SECCOMP_RET_ACTION (0x7fff0000) ignores the 16-bit DATA and bit 31, so
// KILL_PROCESS and KILL_THREAD share the highest-precedence class; strict
// comparison means the first-seen result wins ties.  results must be given in
// filter evaluation order (kernel: most recently installed first).
func KernelMerge(results []uint32) uint32 {
	ret := RetAllow
	for _, cur := range results {
		if cur&RetAction < ret&RetAction {
			ret = cur
		}
	}
	return ret
}

// MergeKernel is the variadic wrapper around KernelMerge.
func MergeKernel(results ...uint32) uint32 { return KernelMerge(results) }

// WithKernelMerge selects the exact kernel action-mask/first-seen merge
// rule instead of the raw numeric minimum.
func WithKernelMerge() Option {
	return func(c *verifyConfig) {
		c.merge = KernelMerge
		c.mergeLabel = "kernel-action-mask"
	}
}

// allActionClasses lists the legal action bases used to resolve dynamic
// BPF_RET|BPF_A outcomes during stack enumeration.
func allActionClasses() []uint32 {
	return []uint32{
		RetKillProcess, RetKillThread, RetTrap, RetErrno,
		RetUserNotif, RetTrace, RetLog, RetAllow,
	}
}

// StackReport is the result of VerifyStack.
type StackReport struct {
	Valid       bool
	FilterCount int
	Safe        bool
	Complete    bool
	// MergeLabel names the merge rule ("numeric-min" or
	// "kernel-action-mask").
	MergeLabel      string
	Counterexamples []*Counterexample
	Minimal         *Counterexample
}

// StackValidationError identifies an invalid layer.
type StackValidationError struct {
	Index int
	Err   error
}

func (e *StackValidationError) Error() string {
	return "seccomp: stacked filter layer validation failed: " + e.Err.Error()
}

func (e *StackValidationError) Unwrap() error { return e.Err }

type resolvedOutcome struct {
	action uint32
	preds  []Pred
	path   []int
}

type stackLayer struct {
	resolved []resolvedOutcome
}

// VerifyStack checks a stack of layered filters against the policy.  For a
// shared input tuple every filter returns an action; the configured merge
// function (default MinReturns) combines them and the merged verdict must
// not be lax for any policy-matching tuple.
func VerifyStack(filters [][]Instruction, policy *Policy, options ...Option) (*StackReport, error) {
	cfg := defaultConfig()
	for _, o := range options {
		o(&cfg)
	}
	rep := &StackReport{
		Valid:       true,
		FilterCount: len(filters),
		Complete:    true,
		MergeLabel:  cfg.mergeLabel,
	}
	if !cfg.opts.SkipValidation {
		for i, f := range filters {
			if verr := Validate(f); verr != nil {
				rep.Valid = false
				return rep, &StackValidationError{Index: i, Err: verr}
			}
		}
	}
	if err := policy.Validate(); err != nil {
		return rep, err
	}

	var found []*Counterexample
	for _, b := range policy.bindings() {
		seeds, ok := seedDomains(b)
		if !ok {
			continue
		}
		base := seeds[:]
		combinedSeeds := append([]Dom(nil), base...)
		layers := make([]stackLayer, 0, len(filters))
		for _, f := range filters {
			outs, grown, _, trunc := exploreOutcomes(f, base, cfg.opts.PathBudget)
			if trunc {
				rep.Complete = false
			}
			shift := len(combinedSeeds) - len(base)
			for j := range outs {
				outs[j].Preds = remapPreds(outs[j].Preds, shift)
				if outs[j].Dynamic && outs[j].ActionExpr != nil {
					outs[j].ActionExpr = remapExpr(outs[j].ActionExpr, shift)
				}
			}
			combinedSeeds = append(combinedSeeds, grown[len(base):]...)
			layer := stackLayer{}
			for _, oc := range outs {
				if oc.Dynamic {
					for _, ac := range allActionClasses() {
						p := withPreds(oc.Preds, Pred{Kind: PredEQ, L: oc.ActionExpr, R: eConst(ac)})
						layer.resolved = append(layer.resolved, resolvedOutcome{action: ac, preds: p, path: oc.Path})
					}
				} else {
					layer.resolved = append(layer.resolved, resolvedOutcome{action: oc.Action, preds: oc.Preds, path: oc.Path})
				}
			}
			layers = append(layers, layer)
		}

		comboBudget := cfg.opts.PathBudget
		if comboBudget <= 0 {
			comboBudget = DefaultPathBudget
		}
		enumerateStack(filters, layers, 0, nil, nil, b, combinedSeeds, cfg, &found, &rep.Complete, &comboBudget)
	}
	finalizeStack(rep, found)
	return rep, nil
}

func enumerateStack(
	filters [][]Instruction,
	layers []stackLayer,
	depth int,
	chosen []resolvedOutcome,
	actions []uint32,
	b binding,
	seeds []Dom,
	cfg verifyConfig,
	found *[]*Counterexample,
	complete *bool,
	budget *int,
) {
	if depth == len(layers) {
		if *budget <= 0 {
			*complete = false
			return
		}
		*budget--
		merged := cfg.merge(actions)
		if !cfg.opts.isLax(merged) {
			return
		}
		var preds []Pred
		for _, c := range chosen {
			preds = append(preds, c.preds...)
		}
		vals, ok, budgetHit := solveMinimal(preds, seeds, cfg.opts.SolverBudget)
		if budgetHit {
			*complete = false
		}
		if budgetHit || !ok || vals == nil {
			return
		}
		t := tupleFromVals(vals, b)
		layerActions := make([]uint32, 0, len(filters))
		for _, f := range filters {
			got, err := RunTuple(f, t)
			if err != nil {
				return
			}
			layerActions = append(layerActions, got)
		}
		actualMerged := cfg.merge(layerActions)
		if !cfg.opts.isLax(actualMerged) {
			return
		}
		if b.rule != nil && !b.rule.Matches(t) {
			return
		}
		var path []int
		if len(chosen) > 0 {
			path = append([]int(nil), chosen[0].path...)
		}
		*found = append(*found, &Counterexample{
			Tuple:        t,
			RuleName:     b.rule.Name,
			RuleIndex:    b.ruleIndex,
			Path:         path,
			LayerActions: layerActions,
			MergedAction: actualMerged,
		})
		return
	}
	if *budget <= 0 {
		*complete = false
		return
	}
	for _, ro := range layers[depth].resolved {
		enumerateStack(filters, layers, depth+1,
			append(chosen, ro), append(actions, ro.action),
			b, seeds, cfg, found, complete, budget)
	}
}

func finalizeStack(rep *StackReport, found []*Counterexample) {
	sortCex(found)
	var uniq []*Counterexample
	for _, c := range found {
		if len(uniq) > 0 && tuplesEqual(uniq[len(uniq)-1].Tuple, c.Tuple) {
			continue
		}
		uniq = append(uniq, c)
	}
	rep.Counterexamples = uniq
	rep.Safe = len(uniq) == 0
	if len(uniq) > 0 {
		rep.Minimal = uniq[0]
	}
}

func sortCex(found []*Counterexample) {
	for i := 1; i < len(found); i++ {
		for j := i; j > 0 && found[j].Tuple.Less(found[j-1].Tuple); j-- {
			found[j], found[j-1] = found[j-1], found[j]
		}
	}
}

// remapExpr shifts opaque variable ids (>=16) by shift.
func remapExpr(e *Expr, shift int) *Expr {
	if e == nil || shift == 0 {
		return e
	}
	if e.op == EVar && e.vid >= 16 {
		return eVar(e.vid + shift)
	}
	out := &Expr{op: e.op, vid: e.vid, c: e.c}
	if e.l != nil {
		out.l = remapExpr(e.l, shift)
	}
	if e.r != nil {
		out.r = remapExpr(e.r, shift)
	}
	return out
}

func remapPreds(ps []Pred, shift int) []Pred {
	if shift == 0 {
		return ps
	}
	out := make([]Pred, len(ps))
	for i, p := range ps {
		out[i] = Pred{Kind: p.Kind, L: remapExpr(p.L, shift), R: remapExpr(p.R, shift)}
	}
	return out
}
