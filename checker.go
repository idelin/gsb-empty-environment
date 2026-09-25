package main

import "fmt"

// Verdict is the outcome of a linearizability check.
type Verdict int

const (
	// Linearizable: a legal sequential order exists; Witness is the evidence.
	Linearizable Verdict = iota
	// NotLinearizable: exhaustive search within budget proved no legal order
	// exists; the history itself is the counterexample.
	NotLinearizable
	// Unknown: the search budget was exhausted before a verdict was reached.
	Unknown
)

func (v Verdict) String() string {
	switch v {
	case Linearizable:
		return "LINEARIZABLE"
	case NotLinearizable:
		return "NOT LINEARIZABLE"
	default:
		return "UNKNOWN (budget exhausted)"
	}
}

// CheckConfig configures the checker.
type CheckConfig struct {
	InitValue int // initial register value
	Budget    int // max number of search states expanded; <= 0 means DefaultBudget
}

// DefaultBudget bounds the search when no explicit budget is given.
const DefaultBudget = 1000000

// CheckResult carries the verdict plus safety evidence.
type CheckResult struct {
	Verdict  Verdict
	Witness  []Op // legal sequential order, set iff Verdict == Linearizable
	Explored int  // number of search states expanded
}

// checkOp is the internal per-op search record.
type checkOp struct {
	op       Op
	optional bool   // incomplete write: may be dropped (extension semantics)
	pred     uint64 // bitmask of ops that must be placed before this one
}

// state is the memoization key: which ops are resolved + current register value.
type state struct {
	done  uint64
	value int
}

type checker struct {
	ops      []checkOp
	full     uint64
	budget   int
	explored int
	aborted  bool
	failed   map[state]bool
}

// Check decides whether h is linearizable on a single-key register.
//
// Semantics:
//   - real-time order: if A responded before B was invoked, A must precede B;
//   - overlapping operations may be reordered arbitrarily;
//   - incomplete reads are dropped (an extension can give them any result);
//   - incomplete writes are optional: they may be dropped, or placed anywhere
//     after their invocation (allowed extension semantics).
//
// At most 64 effective operations are supported (bitmask search).
func Check(h *History, cfg CheckConfig) (CheckResult, error) {
	if cfg.Budget <= 0 {
		cfg.Budget = DefaultBudget
	}
	cops, err := buildCheckOps(h)
	if err != nil {
		return CheckResult{}, err
	}
	c := &checker{
		ops:    cops,
		budget: cfg.Budget,
		failed: make(map[state]bool),
	}
	for i := range cops {
		c.full |= 1 << uint(i)
	}
	order, ok := c.dfs(0, cfg.InitValue, nil)
	res := CheckResult{Explored: c.explored}
	switch {
	case ok:
		res.Verdict = Linearizable
		for _, idx := range order {
			res.Witness = append(res.Witness, cops[idx].op)
		}
	case c.aborted:
		res.Verdict = Unknown
	default:
		res.Verdict = NotLinearizable
	}
	return res, nil
}

// buildCheckOps drops incomplete reads and computes real-time predecessors.
func buildCheckOps(h *History) ([]checkOp, error) {
	var ops []checkOp
	for _, o := range h.Ops {
		if !o.Complete() && o.Kind == Read {
			continue // extension can return any value: no constraint
		}
		ops = append(ops, checkOp{op: o, optional: !o.Complete()})
	}
	if len(ops) > 64 {
		return nil, fmt.Errorf("history too large: %d effective ops (max 64)", len(ops))
	}
	for i := range ops {
		for j := range ops {
			if i == j {
				continue
			}
			a, b := ops[j].op, ops[i].op
			// a must precede b iff a completed before b was invoked.
			if a.Complete() && a.Respond < b.Invoke {
				ops[i].pred |= 1 << uint(j)
			}
		}
	}
	return ops, nil
}

// dfs searches for a legal sequential order. done marks ops that are placed
// (or, for optional ops, dropped). path holds placed op indices in order.
func (c *checker) dfs(done uint64, value int, path []int) ([]int, bool) {
	if done == c.full {
		return path, true
	}
	if c.explored >= c.budget {
		c.aborted = true
		return nil, false
	}
	c.explored++
	st := state{done: done, value: value}
	if c.failed[st] {
		return nil, false
	}
	for i := range c.ops {
		bit := uint64(1) << uint(i)
		if done&bit != 0 || c.ops[i].pred&^done != 0 {
			continue
		}
		o := c.ops[i].op
		switch {
		case o.Kind == Read:
			if o.Value == value {
				if r, ok := c.dfs(done|bit, value, append(path, i)); ok {
					return r, true
				}
			}
		case o.Kind == Write:
			if r, ok := c.dfs(done|bit, o.Value, append(path, i)); ok {
				return r, true
			}
		}
		if c.ops[i].optional {
			// Extension semantics: an incomplete write may never take effect.
			if r, ok := c.dfs(done|bit, value, path); ok {
				return r, true
			}
		}
		if c.aborted {
			return nil, false
		}
	}
	c.failed[st] = true
	return nil, false
}
