package bigsqrt

import (
	"errors"
	"math/big"
)

// ErrNegative is returned when a negative radicand is supplied.
var ErrNegative = errors.New("bigsqrt: negative radicand")

// ErrNotConverged is returned by State.Result before the iteration
// has converged.
var ErrNotConverged = errors.New("bigsqrt: iteration has not converged")

// stepOps is the number of big-integer operations charged per
// Newton iteration: one division, one addition, one shift.
const stepOps = 3

// finishOps is the number of big-integer operations charged once at
// convergence to compute the remainder: one multiply, one subtract.
const finishOps = 2

// State is a resumable integer square-root computation. It finds
// r = floor(sqrt(n)) and the remainder n - r*r using integer Newton
// iteration, one iteration per Step. A State never shares mutable
// buffers with the values it was built from, and Snapshot returns a
// fully independent copy.
type State struct {
	n    *big.Int
	x    *big.Int // current approximation, always >= floor(sqrt(n))
	r    *big.Int // set once done
	rem  *big.Int // set once done
	done bool
}

// NewState prepares a computation for floor(sqrt(n)); n must be
// non-negative and is deep-copied.
func NewState(n *big.Int) (*State, error) {
	if n == nil || n.Sign() < 0 {
		return nil, ErrNegative
	}
	nc := new(big.Int).Set(n)
	s := &State{n: nc}
	if nc.Sign() == 0 {
		s.r = new(big.Int)
		s.rem = new(big.Int)
		s.done = true
		return s, nil
	}
	// Initial estimate 2^ceil(bitlen/2) >= sqrt(n).
	s.x = new(big.Int).Lsh(big.NewInt(1), uint(nc.BitLen()+1)/2)
	return s, nil
}

// Done reports whether the computation has converged.
func (s *State) Done() bool { return s.done }

// Step performs a single Newton iteration, charging the budget.
// It reports whether the computation has converged. On
// ErrBudgetExhausted the state is left exactly as before the call
// (other than the spent budget), so it can be resumed with a fresh
// budget.
func (s *State) Step(budget *Budget) (bool, error) {
	if s.done {
		return true, nil
	}
	if err := budget.charge(stepOps); err != nil {
		return false, err
	}
	q := new(big.Int).Quo(s.n, s.x)
	y := new(big.Int).Add(s.x, q)
	y.Rsh(y, 1)
	if y.Cmp(s.x) < 0 {
		s.x = y
		return false, nil
	}
	// Converged: s.x is floor(sqrt(n)). Compute the remainder.
	if err := budget.charge(finishOps); err != nil {
		return false, err
	}
	r := new(big.Int).Set(s.x)
	rem := new(big.Int).Mul(r, r)
	rem.Sub(s.n, rem)
	s.r, s.rem, s.done = r, rem, true
	return true, nil
}

// Run steps until convergence or budget exhaustion.
func (s *State) Run(budget *Budget) error {
	for {
		done, err := s.Step(budget)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

// Result returns deep copies of r and the remainder once converged,
// satisfying r*r <= n < (r+1)*(r+1).
func (s *State) Result() (r, rem *big.Int, err error) {
	if !s.done {
		return nil, nil, ErrNotConverged
	}
	return new(big.Int).Set(s.r), new(big.Int).Set(s.rem), nil
}

// Snapshot returns an independent copy of the state. The snapshot
// and the original share no mutable buffers, so either can be
// advanced or mutated without affecting the other.
func (s *State) Snapshot() *State {
	cp := &State{
		n:    new(big.Int).Set(s.n),
		done: s.done,
	}
	if s.x != nil {
		cp.x = new(big.Int).Set(s.x)
	}
	if s.r != nil {
		cp.r = new(big.Int).Set(s.r)
	}
	if s.rem != nil {
		cp.rem = new(big.Int).Set(s.rem)
	}
	return cp
}

// Sqrt computes r = floor(sqrt(n)) and rem = n - r*r in one shot,
// charging the given budget (nil = unlimited).
func Sqrt(n *big.Int, budget *Budget) (r, rem *big.Int, err error) {
	s, err := NewState(n)
	if err != nil {
		return nil, nil, err
	}
	if err := s.Run(budget); err != nil {
		return nil, nil, err
	}
	return s.Result()
}
