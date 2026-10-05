// Package bigsqrt computes the integer square root r of a non-negative
// big integer N together with the remainder, so that r^2 <= N < (r+1)^2.
//
// The computation is a Newton iteration that can be suspended at any
// iteration boundary: it honors context cancellation and an operation
// budget counted in big-integer arithmetic operations. A suspended Task
// can be serialized with Snapshot and resumed later with Restore.
// Snapshots and Results never share mutable buffers with a Task.
package bigsqrt

import (
	"context"
	"errors"
	"fmt"
	"math/big"
)

// ErrBudgetExhausted is returned by Step when the operation budget is
// spent before the computation converges. The Task can be resumed by
// calling Step again with a fresh budget.
var ErrBudgetExhausted = errors.New("bigsqrt: operation budget exhausted")

// Result is the outcome of a finished computation. Root and Remainder
// are freshly allocated and safe for the caller to mutate.
type Result struct {
	Root      *big.Int // r with r^2 <= N < (r+1)^2
	Remainder *big.Int // N - r^2
	Ops       uint64   // big-integer operations consumed so far
}

// Task is a resumable integer-square-root computation for a fixed N.
type Task struct {
	n    *big.Int // input, never mutated after NewTask/Restore
	x    *big.Int // current Newton iterate, always >= floor(sqrt(n))
	root *big.Int // set when done
	rem  *big.Int // set when done
	done bool
	ops  uint64
}

var two = big.NewInt(2)

// NewTask starts a computation for n. n must be non-negative and is
// deep-copied, so the caller may reuse or mutate it afterwards.
func NewTask(n *big.Int) (*Task, error) {
	if n == nil {
		return nil, errors.New("bigsqrt: nil input")
	}
	if n.Sign() < 0 {
		return nil, fmt.Errorf("bigsqrt: negative input %s", n)
	}
	t := &Task{n: new(big.Int).Set(n)}
	if n.Cmp(two) < 0 {
		t.root = new(big.Int).Set(n)
		t.rem = new(big.Int)
		t.done = true
		return t, nil
	}
	t.x = initialGuess(n)
	return t, nil
}

// initialGuess returns 2^ceil(bitlen(n)/2), which is >= sqrt(n) for n >= 2.
func initialGuess(n *big.Int) *big.Int {
	half := uint(n.BitLen()+1) / 2
	return new(big.Int).Lsh(big.NewInt(1), half)
}

// Sqrt computes the integer square root of n in one shot and reports the
// number of big-integer operations used. n must be non-negative.
func Sqrt(n *big.Int) (root, remainder *big.Int, ops uint64, err error) {
	t, err := NewTask(n)
	if err != nil {
		return nil, nil, 0, err
	}
	for {
		done, err := t.Step(context.Background(), 1<<62)
		if err != nil {
			return nil, nil, 0, err
		}
		if done {
			res, err := t.Result()
			if err != nil {
				return nil, nil, 0, err
			}
			return res.Root, res.Remainder, res.Ops, nil
		}
	}
}

// Step advances the Newton iteration, consuming at most budget
// big-integer operations. It returns done=true once the root and
// remainder are known. If the budget runs out first it returns
// ErrBudgetExhausted; if ctx is cancelled it returns ctx.Err(). In both
// cases the Task keeps its progress and can be resumed or snapshotted.
func (t *Task) Step(ctx context.Context, budget uint64) (done bool, err error) {
	if t.done {
		return true, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if budget == 0 {
			return false, ErrBudgetExhausted
		}
		// One Newton probe: q = n / x. Because x >= floor(sqrt(n)),
		// q >= x implies x is the exact integer root.
		q := new(big.Int).Quo(t.n, t.x)
		t.ops++
		budget--
		if q.Cmp(t.x) >= 0 {
			r2 := new(big.Int).Mul(t.x, t.x)
			rem := new(big.Int).Sub(t.n, r2)
			t.ops += 2
			t.root = t.x
			t.rem = rem
			t.x = nil
			t.done = true
			return true, nil
		}
		// x <- (x + q) / 2, still >= floor(sqrt(n)).
		t.x.Add(t.x, q)
		t.x.Rsh(t.x, 1)
		t.ops += 2
	}
}

// Ops reports how many big-integer operations the Task has consumed.
func (t *Task) Ops() uint64 { return t.ops }

// Done reports whether the computation has finished.
func (t *Task) Done() bool { return t.done }

// Result returns the root and remainder of a finished computation.
// The returned big.Int values are deep copies.
func (t *Task) Result() (Result, error) {
	if !t.done {
		return Result{}, errors.New("bigsqrt: computation not finished")
	}
	return Result{
		Root:      new(big.Int).Set(t.root),
		Remainder: new(big.Int).Set(t.rem),
		Ops:       t.ops,
	}, nil
}

// Snapshot is a serializable, immutable view of a Task's state. It holds
// only strings and scalars, so it shares no mutable buffers with the
// Task it was taken from.
type Snapshot struct {
	N    string `json:"n"`              // decimal input
	X    string `json:"x,omitempty"`    // current iterate, empty when done
	Root string `json:"root,omitempty"` // set when done
	Rem  string `json:"rem,omitempty"`  // set when done
	Done bool   `json:"done"`           // computation finished
	Ops  uint64 `json:"ops"`            // operations consumed so far
}

// Snapshot captures the Task's state. The returned value is independent
// of the Task: later Steps do not affect it.
func (t *Task) Snapshot() Snapshot {
	s := Snapshot{Done: t.done, Ops: t.ops}
	if t.n != nil {
		s.N = t.n.String()
	}
	if t.x != nil {
		s.X = t.x.String()
	}
	if t.root != nil {
		s.Root = t.root.String()
	}
	if t.rem != nil {
		s.Rem = t.rem.String()
	}
	return s
}

// Restore rebuilds a Task from a Snapshot. The restored Task owns fresh
// buffers; mutating the Snapshot's source Task afterwards is impossible
// anyway, and mutating the restored Task does not affect the Snapshot.
func Restore(s Snapshot) (*Task, error) {
	if s.N == "" {
		return nil, errors.New("bigsqrt: snapshot missing input")
	}
	n, ok := new(big.Int).SetString(s.N, 10)
	if !ok || n.Sign() < 0 {
		return nil, errors.New("bigsqrt: snapshot has invalid input")
	}
	t := &Task{n: n, done: s.Done, ops: s.Ops}
	if s.Done {
		root, ok1 := new(big.Int).SetString(s.Root, 10)
		rem, ok2 := new(big.Int).SetString(s.Rem, 10)
		if !ok1 || !ok2 {
			return nil, errors.New("bigsqrt: snapshot has invalid result")
		}
		t.root = root
		t.rem = rem
		return t, nil
	}
	x, ok := new(big.Int).SetString(s.X, 10)
	if !ok || x.Sign() <= 0 {
		return nil, errors.New("bigsqrt: snapshot has invalid iterate")
	}
	t.x = x
	return t, nil
}
