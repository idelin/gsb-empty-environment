package main

import (
	"fmt"
	"sort"
	"strings"
)

// Verdict is the outcome of a linearizability check.
type Verdict int

const (
	// Linearizable: a legal sequential order exists (witness attached).
	Linearizable Verdict = iota
	// NotLinearizable: exhaustive search proved no legal order exists.
	NotLinearizable
	// Unknown: the search budget was exhausted before a verdict.
	Unknown
)

func (v Verdict) String() string {
	switch v {
	case Linearizable:
		return "LINEARIZABLE"
	case NotLinearizable:
		return "NOT LINEARIZABLE"
	default:
		return "UNKNOWN"
	}
}

// Result is the checker's output: a verdict plus evidence.
//   - Linearizable:    Witness is a legal sequential order.
//   - NotLinearizable: Reason describes the counterexample evidence.
//   - Unknown:         Reason describes budget exhaustion.
type Result struct {
	Verdict Verdict
	Witness []Op
	Reason  string
	Nodes   int // search nodes expanded
	States  int // distinct memoized failure states
}

// Checker checks single-key register histories for linearizability.
type Checker struct {
	Init   int // initial register value
	Budget int // max search nodes before giving up (Unknown)
}

// pending-read semantics: a pending read is a wildcard (its response is
// unknown) and nothing is ordered after it, so it can always be appended
// at the end of any linearization; it never affects the verdict.
//
// pending-write semantics (Wing-Herlihy completion): each pending write
// may either be completed (placed anywhere after its real-time
// predecessors) or removed entirely. This matters: a pending write may be
// the only justification for a completed read's value.

type memoKey struct {
	placed   uint64
	resolved uint64
	val      int
}

type search struct {
	ops      []Op
	optional []bool   // true for pending writes (may be dropped)
	pred     []uint64 // real-time predecessors within the search set
	budget   int
	nodes    int
	memo     map[memoKey]bool
}

// Check decides whether the history is linearizable.
func (c Checker) Check(ops []Op) Result {
	if err := Validate(ops); err != nil {
		return Result{Verdict: Unknown, Reason: "invalid history: " + err.Error()}
	}

	var completed, pendingWrites, pendingReads []Op
	for _, op := range ops {
		switch {
		case !op.Pending:
			completed = append(completed, op)
		case op.Kind == OpWrite:
			pendingWrites = append(pendingWrites, op)
		default:
			pendingReads = append(pendingReads, op)
		}
	}

	// Quick necessary condition: every completed read's value must be
	// the initial value or written by some (completed or pending) write.
	writable := map[int]bool{c.Init: true}
	for _, op := range completed {
		if op.Kind == OpWrite {
			writable[op.Val] = true
		}
	}
	for _, op := range pendingWrites {
		writable[op.Val] = true
	}
	for _, op := range completed {
		if op.Kind == OpRead && !writable[op.Val] {
			return Result{
				Verdict: NotLinearizable,
				Reason: fmt.Sprintf("read op #%d returns %d, but %d is never written and is not the initial value %d",
					op.ID, op.Val, op.Val, c.Init),
			}
		}
	}

	// Search set: all completed ops plus (optional) pending writes.
	sops := append(append([]Op{}, completed...), pendingWrites...)
	if len(sops) > 62 {
		return Result{Verdict: Unknown, Reason: fmt.Sprintf("too many operations (%d > 62) for bitmask search", len(sops))}
	}
	s := &search{
		ops:    sops,
		budget: c.Budget,
		memo:   map[memoKey]bool{},
	}
	for i := range sops {
		s.optional = append(s.optional, i >= len(completed))
		var mask uint64
		for j := range sops {
			if i != j && precedes(sops[j], sops[i]) {
				mask |= 1 << uint(j)
			}
		}
		s.pred = append(s.pred, mask)
	}

	var order []Op
	found, out := s.dfs(0, 0, c.Init, &order)
	res := Result{Nodes: s.nodes, States: len(s.memo)}
	switch {
	case found:
		res.Verdict = Linearizable
		res.Witness = order
		// Pending reads are wildcards with no successors: append them.
		sort.SliceStable(pendingReads, func(a, b int) bool { return pendingReads[a].Invoke < pendingReads[b].Invoke })
		res.Witness = append(res.Witness, pendingReads...)
		dropped := 0
		for i, op := range sops {
			if s.optional[i] && !containsOp(order, op.ID) {
				dropped++
			}
		}
		res.Reason = fmt.Sprintf("legal sequential order found (%d ops, %d pending writes dropped)", len(res.Witness), dropped)
	case out:
		res.Verdict = Unknown
		res.Reason = fmt.Sprintf("budget exhausted after %d search nodes (%d distinct failure states); no verdict", s.nodes, len(s.memo))
	default:
		res.Verdict = NotLinearizable
		res.Reason = fmt.Sprintf("exhaustive search over all real-time-respecting orders (%d nodes, %d distinct states): every order violates the register semantics", s.nodes, len(s.memo))
	}
	return res
}

func containsOp(ops []Op, id int) bool {
	for _, op := range ops {
		if op.ID == id {
			return true
		}
	}
	return false
}

// dfs explores linearizations. placed: ops already in the order;
// resolved: placed plus dropped pending writes. Returns (found, budgetOut).
func (s *search) dfs(placed, resolved uint64, val int, order *[]Op) (bool, bool) {
	s.nodes++
	if s.budget > 0 && s.nodes > s.budget {
		return false, true
	}
	key := memoKey{placed, resolved, val}
	if s.memo[key] {
		return false, false
	}
	if resolved == (uint64(1)<<uint(len(s.ops)))-1 {
		return true, false
	}
	budgetOut := false
	for i := range s.ops {
		bit := uint64(1) << uint(i)
		if resolved&bit != 0 || s.pred[i] & ^placed != 0 {
			continue // already resolved, or predecessors not all placed
		}
		op := s.ops[i]
		if s.optional[i] {
			// Choice 1: drop this pending write (Wing-Herlihy removal).
			f, out := s.dfs(placed, resolved|bit, val, order)
			if f {
				return true, false
			}
			budgetOut = budgetOut || out
		}
		// Choice 2 (or only choice): place the op next.
		next := val
		if op.Kind == OpRead {
			if op.Val != val {
				continue // read would return the wrong value here
			}
		} else {
			next = op.Val
		}
		*order = append(*order, op)
		f, out := s.dfs(placed|bit, resolved|bit, next, order)
		if f {
			return true, false
		}
		*order = (*order)[:len(*order)-1]
		budgetOut = budgetOut || out
	}
	if !budgetOut {
		s.memo[key] = true // only proven failures are memoized
	}
	return false, budgetOut
}

// VerifyLinearization independently checks that order is a legal
// linearization of ops with initial register value init. Used as a
// cross-check of the checker itself (and in tests).
func VerifyLinearization(ops []Op, order []Op, init int) error {
	byID := map[int]Op{}
	for _, op := range ops {
		byID[op.ID] = op
	}
	seen := map[int]bool{}
	for _, op := range order {
		if _, ok := byID[op.ID]; !ok {
			return fmt.Errorf("witness contains unknown op #%d", op.ID)
		}
		if seen[op.ID] {
			return fmt.Errorf("witness contains op #%d twice", op.ID)
		}
		seen[op.ID] = true
	}
	for _, op := range ops {
		if !op.Pending && !seen[op.ID] {
			return fmt.Errorf("completed op #%d missing from witness", op.ID)
		}
	}
	pos := map[int]int{}
	for i, op := range order {
		pos[op.ID] = i
	}
	for _, a := range order {
		for _, b := range order {
			if precedes(byID[a.ID], byID[b.ID]) && pos[a.ID] > pos[b.ID] {
				return fmt.Errorf("real-time order violated: op #%d must precede op #%d", a.ID, b.ID)
			}
		}
	}
	val := init
	for _, op := range order {
		src := byID[op.ID]
		if src.Kind == OpWrite {
			val = src.Val
		} else if !src.Pending && src.Val != val {
			return fmt.Errorf("read op #%d returns %d but register holds %d", src.ID, src.Val, val)
		}
	}
	return nil
}

// FormatHistory renders a history for display.
func FormatHistory(ops []Op) string {
	sorted := append([]Op{}, ops...)
	sort.SliceStable(sorted, func(a, b int) bool {
		if sorted[a].Invoke != sorted[b].Invoke {
			return sorted[a].Invoke < sorted[b].Invoke
		}
		return sorted[a].ID < sorted[b].ID
	})
	lines := make([]string, len(sorted))
	for i, op := range sorted {
		lines[i] = "  " + op.String()
	}
	return strings.Join(lines, "\n")
}

// FormatWitness renders a witness order for display.
func FormatWitness(order []Op) string {
	parts := make([]string, len(order))
	for i, op := range order {
		if op.Kind == OpRead {
			if op.Pending {
				parts[i] = fmt.Sprintf("R#%d->?", op.ID)
			} else {
				parts[i] = fmt.Sprintf("R#%d->%d", op.ID, op.Val)
			}
		} else {
			parts[i] = fmt.Sprintf("W#%d(%d)", op.ID, op.Val)
		}
	}
	return strings.Join(parts, "  ")
}
