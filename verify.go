package netsec

import "fmt"

// Status is the verdict of comparing an old (pre-rewrite) graph against a
// new (hardened/widened) graph.
type Status uint8

const (
	StatusUnknown Status = iota // search budget exhausted
	StatusEquivalent
	StatusMismatch
)

func (s Status) String() string {
	switch s {
	case StatusEquivalent:
		return "equivalent"
	case StatusMismatch:
		return "mismatch"
	default:
		return "unknown"
	}
}

// Witness carries one input on which the two graphs disagree, together with
// the interpreter-computed outcomes for both graphs.
type Witness struct {
	Inputs []byte
	Old    Outcome
	New    Outcome
}

// Result is returned by Verify.
type Result struct {
	Status  Status
	Witness *Witness
	Reason  string
}

// Config controls the search effort.
type Config struct {
	// Budget bounds total SAT propagation/decision work across all queries.
	// Zero selects DefaultBudget.
	Budget int64
}

// DefaultBudget is used when Config.Budget is zero.
const DefaultBudget int64 = 500_000

// Verify checks that old and new report the same source-node error on every
// input and otherwise return equal integers. Symbolic path conditions are
// searched with a bounded SAT solver; exhaustion yields StatusUnknown.
func Verify(oldGraph, newGraph *Graph, cfg Config) (Result, error) {
	if err := oldGraph.Validate(); err != nil {
		return Result{}, fmt.Errorf("netsec: old graph invalid: %w", err)
	}
	if err := newGraph.Validate(); err != nil {
		return Result{}, fmt.Errorf("netsec: new graph invalid: %w", err)
	}
	if oldGraph.Inputs != newGraph.Inputs {
		return Result{}, fmt.Errorf("netsec: input count differs: %d vs %d",
			oldGraph.Inputs, newGraph.Inputs)
	}
	budget := cfg.Budget
	if budget == 0 {
		budget = DefaultBudget
	} else if budget < 0 {
		return Result{}, fmt.Errorf("netsec: negative search budget %d", budget)
	}

	factory := NewBFactory()
	oldOut, oldTrunc, err := symbolicExecute(oldGraph, factory, "old")
	if err != nil {
		return Result{}, err
	}
	newOut, newTrunc, err := symbolicExecute(newGraph, factory, "new")
	if err != nil {
		return Result{}, err
	}
	unknown := oldTrunc || newTrunc

	queries := buildMismatchQueries(factory, oldOut, newOut)
	if budget <= 0 {
		return Result{Status: StatusUnknown, Reason: "sat budget exhausted"}, nil
	}
	for _, q := range queries {
		res, model := satCheck([]cnfQuery{{factory: factory, roots: q.roots}}, &budget)
		if res == SatUnknown {
			return Result{Status: StatusUnknown, Reason: "sat budget exhausted"}, nil
		}
		if res == SatSat {
			wit, err := materializeWitness(oldGraph, newGraph, model)
			if err != nil {
				return Result{}, err
			}
			return Result{
				Status:  StatusMismatch,
				Witness: wit,
				Reason:  q.reason,
			}, nil
		}
	}
	if unknown {
		return Result{Status: StatusUnknown, Reason: "path enumeration truncated"}, nil
	}
	return Result{Status: StatusEquivalent}, nil
}

type mismatchQuery struct {
	roots  []*BNode
	reason string
}

// buildMismatchQueries forms, for every old/new terminal pair, the condition
// under which both path guards hold but the observable outcomes differ.
func buildMismatchQueries(f *BFactory, oldOut, newOut []guardedOutcome) []mismatchQuery {
	var queries []mismatchQuery
	for i := range oldOut {
		oc := oldOut[i]
		for j := range newOut {
			nc := newOut[j]
			both := f.And(oc.guard, nc.guard)
			if both.op == BConst && !both.imm {
				continue
			}
			diff, reason := outcomeDiff(f, oc, nc)
			if diff == nil {
				continue
			}
			if diff.op == BConst && !diff.imm {
				continue
			}
			queries = append(queries, mismatchQuery{
				roots:  []*BNode{both, diff},
				reason: reason,
			})
		}
	}
	return queries
}

func outcomeDiff(f *BFactory, oc, nc guardedOutcome) (*BNode, string) {
	switch {
	case oc.kind == OutcomeError && nc.kind == OutcomeError:
		if oc.errNode == nc.errNode {
			return nil, ""
		}
		return f.Const(true), fmt.Sprintf("error node differs: old=%d new=%d", oc.errNode, nc.errNode)
	case oc.kind == OutcomeError && nc.kind == OutcomeReturn:
		return f.Const(true), fmt.Sprintf("old reports error node %d, new returns", oc.errNode)
	case oc.kind == OutcomeReturn && nc.kind == OutcomeError:
		return f.Const(true), fmt.Sprintf("old returns, new reports error node %d", nc.errNode)
	case oc.kind == OutcomeReturn && nc.kind == OutcomeReturn:
		if oc.width != nc.width {
			return f.Const(true), fmt.Sprintf("return width differs: %d vs %d", oc.width, nc.width)
		}
		diff := f.Const(false)
		for i := 0; i < oc.width; i++ {
			diff = f.Or(diff, f.Xor(oc.value.bits[i], nc.value.bits[i]))
		}
		return diff, "returned values differ"
	default:
		return nil, ""
	}
}

func materializeWitness(oldGraph, newGraph *Graph, model map[string]bool) (*Witness, error) {
	inputs := make([]byte, oldGraph.Inputs)
	for i := 0; i < oldGraph.Inputs; i++ {
		var b byte
		for bit := 0; bit < 8; bit++ {
			if model[fmt.Sprintf("in%d:%d", i, bit)] {
				b |= 1 << bit
			}
		}
		inputs[i] = b
	}
	oldRes, err := Interpret(oldGraph, inputs)
	if err != nil {
		return nil, err
	}
	newRes, err := Interpret(newGraph, inputs)
	if err != nil {
		return nil, err
	}
	return &Witness{Inputs: append([]byte(nil), inputs...), Old: oldRes, New: newRes}, nil
}
