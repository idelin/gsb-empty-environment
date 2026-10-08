package exact

import (
	"fmt"
	"math/big"
	"strings"
)

// ChainOp is one step of a unit-conversion chain: multiply or divide by an
// exact rational factor.
type ChainOp struct {
	Mul    bool     // true: multiply by Factor; false: divide by Factor
	Factor *big.Rat // exact rational factor, must be non-zero for division
	Label  string   // optional human-readable step label, e.g. "in->cm"
}

// MulBy builds a multiply step.
func MulBy(factor *big.Rat, label string) ChainOp {
	return ChainOp{Mul: true, Factor: new(big.Rat).Set(factor), Label: label}
}

// DivBy builds a divide step.
func DivBy(factor *big.Rat, label string) ChainOp {
	return ChainOp{Mul: false, Factor: new(big.Rat).Set(factor), Label: label}
}

// StepError annotates a failure with the chain step that caused it.
type StepError struct {
	Step  int
	Label string
	Err   error
}

func (e *StepError) Error() string {
	return fmt.Sprintf("exact: step %d (%s): %v", e.Step, e.Label, e.Err)
}

// Unwrap exposes the underlying cause.
func (e *StepError) Unwrap() error { return e.Err }

// IsDivideByZero reports whether err (or any error it wraps) is ErrDivideByZero.
func IsDivideByZero(err error) bool {
	for err != nil {
		if err == ErrDivideByZero {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ChainStep records the exact value after each chain operation.
type ChainStep struct {
	Label  string
	Op     string   // "*" or "/"
	Factor *big.Rat // exact factor applied
	Value  *big.Rat // exact value after applying the step
}

// ChainResult is the outcome of evaluating a conversion chain: the exact
// final value, the rounded rendering, and full per-step evidence.
type ChainResult struct {
	Steps    []ChainStep
	Exact    *big.Rat       // exact value after the full chain
	Rounded  *big.Rat       // half-even rounded to Places
	Text     string         // fixed-point rendering of Rounded
	Evidence *RoundEvidence // nil when Places < 0 (no rounding requested)
}

// EvalChain applies the multiply/divide chain to an exact starting value,
// keeping full rational precision through every step. Only at the end is the
// result rounded (when places >= 0) with half-even rounding. Division by a
// zero factor is an explicit error.
func EvalChain(start *big.Rat, ops []ChainOp, places int) (*ChainResult, error) {
	value := new(big.Rat).Set(start)
	res := &ChainResult{}
	for i, op := range ops {
		if op.Factor == nil {
			return nil, &StepError{Step: i, Label: op.Label, Err: fmt.Errorf("nil factor")}
		}
		step := ChainStep{Label: op.Label, Factor: new(big.Rat).Set(op.Factor)}
		if op.Mul {
			step.Op = "*"
			value.Mul(value, op.Factor)
		} else {
			if op.Factor.Sign() == 0 {
				return nil, &StepError{Step: i, Label: op.Label, Err: ErrDivideByZero}
			}
			step.Op = "/"
			value.Quo(value, op.Factor)
		}
		step.Value = new(big.Rat).Set(value)
		res.Steps = append(res.Steps, step)
	}
	res.Exact = value
	if places >= 0 {
		rounded, ev := RoundHalfEven(value, places)
		res.Rounded = rounded
		res.Evidence = ev
		res.Text = FormatCoef(ev.Result, places)
	} else {
		res.Rounded = new(big.Rat).Set(value)
		res.Text = value.RatString()
	}
	return res, nil
}

// EvalChainDecimal parses a decimal fixed-point gauge reading and evaluates
// the conversion chain over it.
func EvalChainDecimal(reading string, ops []ChainOp, places int) (*ChainResult, error) {
	start, err := ParseDecimal(reading)
	if err != nil {
		return nil, err
	}
	return EvalChain(start, ops, places)
}

// Describe renders the chain result as an auditable multi-line string.
func (r *ChainResult) Describe() string {
	var b strings.Builder
	for i, s := range r.Steps {
		label := s.Label
		if label == "" {
			label = fmt.Sprintf("step%d", i)
		}
		fmt.Fprintf(&b, "%s: %s %s -> %s\n", label, s.Op, s.Factor.RatString(), s.Value.RatString())
	}
	fmt.Fprintf(&b, "exact: %s\n", r.Exact.RatString())
	if r.Evidence != nil {
		fmt.Fprintf(&b, "evidence: %s\n", r.Evidence.String())
	}
	fmt.Fprintf(&b, "result: %s\n", r.Text)
	return b.String()
}
