package netsec

import (
	"sort"
	"strconv"
	"strings"
)

// Report is the verdict for one history.
type Report struct {
	// Linearizable is true exactly when a legal linearization exists.
	Linearizable bool
	// Order is the linearized sequence of operation IDs (set when linearizable).
	Order []int
	// Counterexample is set when the history cannot be linearized.
	Counterexample *Counterexample
}

// Counterexample is a non-linearizable subsequence from which no single
// operation can be removed while preserving the violation, together with the
// dead-end prefix that explains it.
type Counterexample struct {
	// OperationIDs is the minimal (single-removal irreducible) subsequence.
	OperationIDs []int
	// Prefix is a legal sequence after which no remaining operation can apply.
	Prefix []int
	// Blocked lists the operations that had nowhere to go at the dead end.
	Blocked []int
	// Reason is a short human-readable explanation.
	Reason string
}

type searcher struct {
	ops       []Operation
	spec      *Spec
	memo      map[string]bool
	order     []int
	remaining []bool
}

// Check decides whether h is linearizable against the challenge-service
// specification. Equal invoke/return timestamps are treated as distinct,
// strictly ordered points.
func Check(h History) (*Report, error) {
	if err := h.Validate(); err != nil {
		return nil, err
	}
	if len(h.Operations) == 0 {
		return &Report{Linearizable: true, Order: []int{}}, nil
	}
	limit := h.WrongLimit
	if limit <= 0 {
		limit = DefaultWrongLimit
	}

	ops := make([]Operation, len(h.Operations))
	copy(ops, h.Operations)
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].Invoke != ops[j].Invoke {
			return ops[i].Invoke < ops[j].Invoke
		}
		if ops[i].Return != ops[j].Return {
			return ops[i].Return < ops[j].Return
		}
		return ops[i].ID < ops[j].ID
	})

	order, ok := search(ops, NewSpec(limit))
	if ok {
		return &Report{Linearizable: true, Order: order}, nil
	}

	minimal, blocked := minimize(ops, limit)
	return &Report{
		Linearizable: false,
		Counterexample: &Counterexample{
			OperationIDs: opIDs(ops, minimal),
			Prefix:       blocked.prefix,
			Blocked:      blocked.blocked,
			Reason:       blocked.reason,
		},
	}, nil
}

// IsLinearizable is a convenience wrapper for callers that only need a verdict.
func IsLinearizable(h History) (bool, error) {
	report, err := Check(h)
	if err != nil {
		return false, err
	}
	return report.Linearizable, nil
}

func search(ops []Operation, initial *Spec) ([]int, bool) {
	st := &searcher{
		ops:       ops,
		spec:      initial,
		memo:      make(map[string]bool),
		remaining: make([]bool, len(ops)),
	}
	for i := range ops {
		st.remaining[i] = true
	}
	if st.dfs(0) {
		return st.order, true
	}
	return nil, false
}

// stateKey combines the machine state with the set of uncommitted operations.
func (st *searcher) stateKey() string {
	var b strings.Builder
	b.WriteString(st.spec.digest())
	b.WriteString("|")
	for i, keep := range st.remaining {
		if keep {
			b.WriteString(strconv.Itoa(i))
			b.WriteByte(',')
		}
	}
	return b.String()
}

func (st *searcher) dfs(depth int) bool {
	if depth == len(st.ops) {
		return true
	}
	key := st.stateKey()
	if doomed, seen := st.memo[key]; seen {
		return !doomed
	}

	ready := st.readySet()

	for _, i := range ready {
		next := st.spec.clone()
		if !next.Apply(st.ops[i]) {
			continue
		}
		prev := st.spec
		st.spec = next
		st.remaining[i] = false
		st.order = append(st.order, st.ops[i].ID)

		if st.dfs(depth + 1) {
			return true
		}

		st.order = st.order[:len(st.order)-1]
		st.remaining[i] = true
		st.spec = prev
	}
	st.memo[key] = true
	return false
}

type deadEnd struct {
	prefix  []int
	blocked []int
	reason  string
	found   bool
}

// deadEndPrefix reruns the search while recording the first fully blocked
// state, which is returned as the explanatory prefix.
func deadEndPrefix(ops []Operation, limit int) deadEnd {
	st := &searcher{
		ops:       ops,
		spec:      NewSpec(limit),
		memo:      make(map[string]bool),
		remaining: make([]bool, len(ops)),
	}
	for i := range ops {
		st.remaining[i] = true
	}
	return st.dfsDeadEnd(0)
}

func (st *searcher) dfsDeadEnd(depth int) deadEnd {
	if depth == len(st.ops) {
		return deadEnd{found: true}
	}
	ready := st.readySet()

	var blocked []int
	for _, i := range ready {
		next := st.spec.clone()
		if !next.Apply(st.ops[i]) {
			blocked = append(blocked, st.ops[i].ID)
			continue
		}
		prev := st.spec
		st.spec = next
		st.remaining[i] = false
		st.order = append(st.order, st.ops[i].ID)

		found := st.dfsDeadEnd(depth + 1)

		st.order = st.order[:len(st.order)-1]
		st.remaining[i] = true
		st.spec = prev
		if !found.found {
			return found
		}
	}

	prefix := make([]int, len(st.order))
	copy(prefix, st.order)
	return deadEnd{
		prefix:  prefix,
		blocked: blocked,
		reason:  "no ready operation can produce its observed result in this state",
		found:   false,
	}
}

// readySet returns the Wing-Wing-Gong ready operations: an operation is
// linearizable next when its invoke is no later than the earliest return of
// every operation still outstanding.
func (st *searcher) readySet() []int {
	minReturn := int64(maxInt64Value)
	for i, keep := range st.remaining {
		if keep && st.ops[i].Return < minReturn {
			minReturn = st.ops[i].Return
		}
	}
	ready := make([]int, 0, len(st.ops))
	for i, keep := range st.remaining {
		if keep && st.ops[i].Invoke <= minReturn {
			ready = append(ready, i)
		}
	}
	return ready
}

// minimize repeatedly removes operations while the residual history stays
// non-linearizable, producing a minimal counterexample.
func minimize(ops []Operation, limit int) ([]Operation, deadEnd) {
	current := make([]Operation, len(ops))
	copy(current, ops)
	for {
		removed := false
		for i := 0; i < len(current); i++ {
			trial := make([]Operation, 0, len(current)-1)
			trial = append(trial, current[:i]...)
			trial = append(trial, current[i+1:]...)
			if _, ok := search(trial, NewSpec(limit)); !ok {
				current = trial
				removed = true
				break
			}
		}
		if !removed {
			break
		}
	}
	end := deadEndPrefix(current, limit)
	return current, end
}

func opIDs(ops []Operation, subset []Operation) []int {
	ids := make([]int, 0, len(subset))
	for _, op := range subset {
		ids = append(ids, op.ID)
	}
	return ids
}

const maxInt64Value = int64(^uint64(0) >> 1)
