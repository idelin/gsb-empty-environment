package netsec

import (
	"errors"
	"time"
)

// Verdict is the platform callback's classification of one trial
// sample.
type Verdict int

const (
	// VerdictPass means the sample does not fail.
	VerdictPass Verdict = iota
	// VerdictSameFailure means the sample fails with the same class of
	// failure as the original input. Only this verdict keeps a
	// reduction.
	VerdictSameFailure
	// VerdictOtherFailure means the sample fails differently (for
	// example a syntax error introduced by the edit). It is never
	// treated as the original vulnerability.
	VerdictOtherFailure
	// VerdictTimeout means the callback did not answer within
	// Options.Timeout. The candidate is discarded.
	VerdictTimeout
	// VerdictInvalid means the edited tree could not be encoded (for
	// example a dangling reference after deleting a shared node). The
	// callback was not invoked and the predicate budget was not spent.
	VerdictInvalid
)

func (v Verdict) String() string {
	switch v {
	case VerdictPass:
		return "pass"
	case VerdictSameFailure:
		return "same-failure"
	case VerdictOtherFailure:
		return "other-failure"
	case VerdictTimeout:
		return "timeout"
	case VerdictInvalid:
		return "invalid"
	default:
		return "unknown"
	}
}

// Predicate is the platform's deterministic failure classifier. It
// must be side-effect free with respect to the reducer: the same
// sample must always yield the same verdict.
type Predicate func(sample []byte) Verdict

// Options controls the reduction budget.
type Options struct {
	// MaxCalls caps the number of predicate invocations. Zero means
	// unlimited. If the cap is hit before a fixpoint is reached, the
	// result is marked not minimized.
	MaxCalls int
	// Timeout caps a single predicate call. Zero means no timeout. A
	// timed-out call counts against MaxCalls and its candidate is
	// discarded.
	Timeout time.Duration
}

// Trial summarizes one reduction attempt.
type Trial struct {
	Seq      int           // order in which the trial happened
	Op       string        // "initial", "delete", "shrink-half", "shrink-empty"
	Target   string        // ID of the affected node, empty for "initial"
	Size     int           // encoded sample size in bytes
	Verdict  Verdict       // callback (or validation) outcome
	Kept     bool          // whether the candidate became the new current tree
	Duration time.Duration // wall time of the predicate call
}

// Result is the outcome of Reduce.
type Result struct {
	// Root is the reduced tree; Sample is its encoding.
	Root   *Node
	Sample []byte
	// Minimized is true only when a fixpoint was reached: no single
	// deletion or shrink of the result reproduces the failure. It is
	// false when the budget ran out first.
	Minimized bool
	// BudgetExhausted is true when MaxCalls stopped the search.
	BudgetExhausted bool
	// Calls is the number of predicate invocations spent.
	Calls int
	// Trials lists every attempt in order, including invalid
	// candidates that never reached the predicate.
	Trials []Trial
}

// ErrNotFailing is returned when the original sample does not
// reproduce the failure (predicate did not return VerdictSameFailure).
var ErrNotFailing = errors.New("netsec: initial sample does not reproduce the failure")

// Reduce greedily deletes and shrinks nodes of root, keeping a
// candidate only when pred confirms the same class of failure. It
// stops when no single edit helps (1-minimal) or the budget runs out.
func Reduce(root *Node, pred Predicate, opts Options) (*Result, error) {
	if pred == nil {
		return nil, errors.New("netsec: nil predicate")
	}
	initial, err := Encode(root)
	if err != nil {
		return nil, err
	}
	r := &reducer{pred: pred, opts: opts}
	res := &Result{}

	v, d := r.run(initial)
	r.record(Trial{Op: "initial", Size: len(initial), Verdict: v, Duration: d})
	if v != VerdictSameFailure {
		res.Calls = r.calls
		res.Trials = r.trials
		return res, ErrNotFailing
	}

	cur := Clone(root)
	minimized := true
outer:
	for {
		progressed := false
		for _, cand := range candidates(cur) {
			if !r.budgetLeft() {
				minimized = false
				break outer
			}
			next := Clone(cur)
			switch cand.op {
			case "delete":
				if !removeNode(next, cand.target) {
					continue
				}
			default: // shrink variants
				n := findNode(next, cand.target)
				if n == nil {
					continue
				}
				n.Data = cand.shrink
			}
			enc, err := Encode(next)
			if err != nil {
				// Invalid candidates (e.g. dangling references to a
				// deleted shared node) never reach the predicate.
				r.record(Trial{Op: cand.op, Target: cand.target, Verdict: VerdictInvalid})
				continue
			}
			v, d := r.run(enc)
			kept := v == VerdictSameFailure
			r.record(Trial{
				Op:       cand.op,
				Target:   cand.target,
				Size:     len(enc),
				Verdict:  v,
				Kept:     kept,
				Duration: d,
			})
			if kept {
				cur = next
				progressed = true
				break
			}
		}
		if !progressed {
			break
		}
	}

	final, err := Encode(cur)
	if err != nil {
		return nil, err
	}
	res.Root = cur
	res.Sample = final
	res.Minimized = minimized
	res.BudgetExhausted = !minimized
	res.Calls = r.calls
	res.Trials = r.trials
	return res, nil
}

type reducer struct {
	pred   Predicate
	opts   Options
	calls  int
	trials []Trial
}

func (r *reducer) budgetLeft() bool {
	return r.opts.MaxCalls <= 0 || r.calls < r.opts.MaxCalls
}

func (r *reducer) record(t Trial) {
	t.Seq = len(r.trials)
	r.trials = append(r.trials, t)
}

func (r *reducer) run(sample []byte) (Verdict, time.Duration) {
	r.calls++
	start := time.Now()
	if r.opts.Timeout <= 0 {
		return r.pred(sample), time.Since(start)
	}
	// Buffered channel: a late predicate answer after a timeout does
	// not block forever.
	ch := make(chan Verdict, 1)
	go func() { ch <- r.pred(sample) }()
	t := time.NewTimer(r.opts.Timeout)
	select {
	case v := <-ch:
		t.Stop()
		return v, time.Since(start)
	case <-t.C:
		return VerdictTimeout, time.Since(start)
	}
}

type candidate struct {
	op     string
	target string
	shrink []byte
}

// candidates lists every single-edit reduction of the tree: delete
// each named non-root node, and halve or empty each named blob leaf.
func candidates(root *Node) []candidate {
	var out []candidate
	var walk func(n *Node, isRoot bool)
	walk = func(n *Node, isRoot bool) {
		if n.ID != "" {
			if !isRoot {
				out = append(out, candidate{op: "delete", target: n.ID})
			}
			if len(n.Children) == 0 && !n.derived() && len(n.Data) > 0 {
				half := len(n.Data) / 2
				out = append(out,
					candidate{op: "shrink-half", target: n.ID, shrink: append([]byte(nil), n.Data[:half]...)},
					candidate{op: "shrink-empty", target: n.ID},
				)
			}
		}
		for _, c := range n.Children {
			walk(c, false)
		}
	}
	walk(root, true)
	return out
}

func findNode(root *Node, id string) *Node {
	if id != "" && root.ID == id {
		return root
	}
	for _, c := range root.Children {
		if n := findNode(c, id); n != nil {
			return n
		}
	}
	return nil
}

func removeNode(root *Node, id string) bool {
	for i, c := range root.Children {
		if id != "" && c.ID == id {
			root.Children = append(root.Children[:i], root.Children[i+1:]...)
			return true
		}
		if removeNode(c, id) {
			return true
		}
	}
	return false
}
