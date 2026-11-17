package netsec

import "fmt"

// Status is the result of a rewrite check.
type Status uint8

const (
	// StatusVerified means the two graphs were proved observationally
	// equivalent for every input byte assignment.
	StatusVerified Status = iota + 1
	// StatusCounterexample means a concrete input exposes a difference.
	StatusCounterexample
	// StatusUnknown means the search budget was exhausted.
	StatusUnknown
)

const (
	Equivalent          = StatusVerified
	NotEquivalent       = StatusCounterexample
	Unknown             = StatusUnknown
	StatusEquivalent    = StatusVerified
	StatusNotEquivalent = StatusCounterexample
)

func (s Status) String() string {
	switch s {
	case StatusVerified:
		return "verified"
	case StatusCounterexample:
		return "counterexample"
	case StatusUnknown:
		return "unknown"
	default:
		return fmt.Sprintf("status(%d)", uint8(s))
	}
}

// Budget bounds the symbolic search. Non-positive fields use the defaults in
// DefaultBudget. Budget exhaustion is reported as StatusUnknown, never as a
// proof of equivalence.
type Budget struct {
	MaxPaths        int
	MaxPathPairs    int
	MaxSATNodes     int
	MaxSATConflicts int
}

// DefaultBudget is intentionally generous for small compiler peepholes while
// still making adversarial graphs return unknown in bounded time.
var DefaultBudget = Budget{
	MaxPaths:        4096,
	MaxPathPairs:    65536,
	MaxSATNodes:     500000,
	MaxSATConflicts: 100000,
}

// Options configures Verify.
type Options struct {
	Budget Budget
}

// VerifyOption is implemented by Budget and Options.
type VerifyOption interface {
	applyVerify(*Options)
}

func (b Budget) applyVerify(o *Options) {
	o.Budget = b
}

func (o Options) applyVerify(dst *Options) {
	*dst = o
}

// Counterexample is a concrete input on which the two graphs differ.
type Counterexample struct {
	Input []byte
	Old   Outcome
	New   Outcome
}

// Result is the outcome of Verify.
type Result struct {
	Status         Status
	Counterexample *Counterexample
	Reason         string
	PathsOld       int
	PathsNew       int
	PathPairs      int
}

// Verified reports whether the rewrite was proved equivalent.
func (r Result) Verified() bool {
	return r.Status == StatusVerified
}

// Verify checks that old and new have identical observable behavior for all
// assignments to their input bytes. It enumerates control-flow paths, asserts
// each branch's path condition in bit-vector CNF, and asks a SAT solver for a
// normal-return mismatch or a different error source.
//
// A found SAT model is replayed with the independent concrete interpreter
// before it is returned. If the search cannot finish within budget, the result
// is StatusUnknown.
func Verify(old, new *Graph, opts ...VerifyOption) Result {
	cfg := Options{Budget: DefaultBudget}
	for _, opt := range opts {
		if opt != nil {
			opt.applyVerify(&cfg)
		}
	}
	cfg.Budget = normalizedBudget(cfg.Budget)

	if err := old.Validate(); err != nil {
		return Result{Status: StatusUnknown, Reason: "old graph: " + err.Error()}
	}
	if err := new.Validate(); err != nil {
		return Result{Status: StatusUnknown, Reason: "new graph: " + err.Error()}
	}
	if old.InputCount != new.InputCount {
		return Result{
			Status: StatusUnknown,
			Reason: fmt.Sprintf("input count mismatch: old=%d new=%d", old.InputCount, new.InputCount),
		}
	}

	interner := newExprInterner()
	oldPaths, oldExhausted, err := enumerateSymbolicPaths(old, cfg.Budget.MaxPaths, interner)
	if err != nil {
		return Result{Status: StatusUnknown, Reason: "old symbolic search: " + err.Error()}
	}
	if oldExhausted {
		return Result{
			Status:   StatusUnknown,
			Reason:   "path budget exhausted while enumerating old graph",
			PathsOld: len(oldPaths),
		}
	}
	newPaths, newExhausted, err := enumerateSymbolicPaths(new, cfg.Budget.MaxPaths, interner)
	if err != nil {
		return Result{Status: StatusUnknown, Reason: "new symbolic search: " + err.Error()}
	}
	if newExhausted {
		return Result{
			Status:   StatusUnknown,
			Reason:   "path budget exhausted while enumerating new graph",
			PathsOld: len(oldPaths),
			PathsNew: len(newPaths),
		}
	}

	res := Result{
		Status:   StatusVerified,
		PathsOld: len(oldPaths),
		PathsNew: len(newPaths),
	}
	for _, po := range oldPaths {
		for _, pn := range newPaths {
			res.PathPairs++
			if cfg.Budget.MaxPathPairs > 0 && res.PathPairs > cfg.Budget.MaxPathPairs {
				res.Status = StatusUnknown
				res.Reason = "path-pair budget exhausted"
				return res
			}

			// Two errors with the same provenance are equivalent on every
			// input; the satisfiability of the path conditions is irrelevant.
			if po.outcome.kind == OutcomeError &&
				pn.outcome.kind == OutcomeError &&
				po.outcome.source == pn.outcome.source {
				continue
			}

			cnf := newCNF()
			ctx := newCompileContext(cnf)
			if err := assertPathConditions(ctx, po.conditions); err != nil {
				res.Status = StatusUnknown
				res.Reason = "old path: " + err.Error()
				return res
			}
			if err := assertPathConditions(ctx, pn.conditions); err != nil {
				res.Status = StatusUnknown
				res.Reason = "new path: " + err.Error()
				return res
			}
			mismatch, err := compileMismatch(ctx, po.outcome, pn.outcome)
			if err != nil {
				res.Status = StatusUnknown
				res.Reason = "symbolic comparison: " + err.Error()
				return res
			}
			cnf.assert(mismatch)

			sat, model, solverExhausted := solveCNF(cnf, cfg.Budget.MaxSATNodes, cfg.Budget.MaxSATConflicts)
			if solverExhausted {
				res.Status = StatusUnknown
				res.Reason = "SAT budget exhausted"
				return res
			}
			if !sat {
				continue
			}

			input := decodeInput(ctx, old.InputCount, model)
			oldOut, err := interpretValidated(old, input)
			if err != nil {
				res.Status = StatusUnknown
				res.Reason = "counterexample replay failed on old graph: " + err.Error()
				return res
			}
			newOut, err := interpretValidated(new, input)
			if err != nil {
				res.Status = StatusUnknown
				res.Reason = "counterexample replay failed on new graph: " + err.Error()
				return res
			}
			if oldOut.Equal(newOut) {
				res.Status = StatusUnknown
				res.Reason = "SAT model did not replay as a concrete mismatch"
				return res
			}
			res.Status = StatusCounterexample
			res.Counterexample = &Counterexample{
				Input: append([]byte(nil), input...),
				Old:   oldOut,
				New:   newOut,
			}
			return res
		}
	}
	return res
}

// VerifyRewrite is an alias for Verify.
func VerifyRewrite(old, new *Graph, opts ...VerifyOption) Result {
	return Verify(old, new, opts...)
}

// VerifyWithBudget is a convenience wrapper for the common case.
func VerifyWithBudget(old, new *Graph, budget Budget) Result {
	return Verify(old, new, budget)
}

func normalizedBudget(b Budget) Budget {
	d := DefaultBudget
	if b.MaxPaths <= 0 {
		b.MaxPaths = d.MaxPaths
	}
	if b.MaxPathPairs <= 0 {
		b.MaxPathPairs = d.MaxPathPairs
	}
	if b.MaxSATNodes <= 0 {
		b.MaxSATNodes = d.MaxSATNodes
	}
	if b.MaxSATConflicts <= 0 {
		b.MaxSATConflicts = d.MaxSATConflicts
	}
	return b
}

func assertPathConditions(ctx *compileContext, conds []pathCondition) error {
	for _, cond := range conds {
		bits, err := ctx.bits(cond.expr)
		if err != nil {
			return err
		}
		l := nonZero(ctx, bits)
		if !cond.expect {
			l = neg(l)
		}
		ctx.cnf.assert(l)
	}
	return nil
}

func compileMismatch(ctx *compileContext, old, new symbolicOutcome) (Lit, error) {
	if old.kind == OutcomeError && new.kind == OutcomeError {
		if old.source == new.source {
			return litFalse, nil
		}
		return litTrue, nil
	}
	if old.kind != new.kind {
		return litTrue, nil
	}
	a, err := ctx.bits(old.value)
	if err != nil {
		return litFalse, err
	}
	b, err := ctx.bits(new.value)
	if err != nil {
		return litFalse, err
	}
	return neg(signedEqual(ctx, a, b)), nil
}

func decodeInput(ctx *compileContext, inputCount int, model []int8) []byte {
	input := make([]byte, inputCount)
	for i := 0; i < inputCount; i++ {
		bits, ok := ctx.inputs[i]
		if !ok {
			continue
		}
		var value byte
		for j, lit := range bits {
			v := absLit(lit)
			if v < len(model) && model[v] == 1 {
				value |= 1 << uint(j)
			}
		}
		input[i] = value
	}
	return input
}
