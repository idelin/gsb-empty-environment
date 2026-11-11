package relexec

import (
	"errors"
	"fmt"
	"math/big"
)

// UnlimitedBudget may be set as Config.Budget to enumerate the whole input
// space regardless of how many steps are required.
const UnlimitedBudget int64 = -1

// Verdict is the relational verification conclusion.
type Verdict int

const (
	// VerdictEquivalent means every covered input is equivalent under the
	// patch-relaxation rule: inputs where the old program went out of bounds
	// impose no requirement on the new program.
	VerdictEquivalent Verdict = iota
	// VerdictCounterexample means an input was found where both programs
	// stayed in bounds but observable behavior differs.
	VerdictCounterexample
	// VerdictUnknown means the step budget was exhausted before the input
	// space could be covered.
	VerdictUnknown
)

// String renders the verdict for review-service reports.
func (v Verdict) String() string {
	switch v {
	case VerdictEquivalent:
		return "equivalent"
	case VerdictCounterexample:
		return "counterexample"
	case VerdictUnknown:
		return "unknown"
	default:
		return fmt.Sprintf("verdict(%d)", int(v))
	}
}

// CounterexampleReason classifies how the patched program breaks a legal
// input that the old program handled in bounds.
type CounterexampleReason int

const (
	ReasonNone CounterexampleReason = iota
	// ReasonNewOOB: the new program rejects with an out-of-bounds trap an
	// input on which the old program completed in bounds.
	ReasonNewOOB
	// ReasonReturnMismatch: both programs complete in bounds but r0 differs.
	ReasonReturnMismatch
	// ReasonWriteMismatch: both programs complete in bounds but the ordered
	// store trace differs.
	ReasonWriteMismatch
)

// String renders the counterexample reason.
func (r CounterexampleReason) String() string {
	switch r {
	case ReasonNone:
		return "none"
	case ReasonNewOOB:
		return "new-program-oob"
	case ReasonReturnMismatch:
		return "return-mismatch"
	case ReasonWriteMismatch:
		return "write-trace-mismatch"
	default:
		return fmt.Sprintf("reason(%d)", int(r))
	}
}

// Config parameterizes a relational verification run.
type Config struct {
	// InputLen is the fixed number of input bytes shared by both programs
	// (0..MaxInputLen). Each byte ranges over 0..255, so the space size is
	// 256^InputLen.
	InputLen int
	// ArrayLen is the shared byte-array length (1..MaxArrayLen).
	ArrayLen int
	// Budget is the shared cap on executed instructions across the whole
	// run; UnlimitedBudget removes the cap.
	Budget int64
}

// Counterexample is the lexicographically smallest witnessing input together
// with both programs' outcomes on it.
type Counterexample struct {
	Input  []byte
	Reason CounterexampleReason
	Old    *Outcome
	New    *Outcome
}

// Result is the verification report.
type Result struct {
	Verdict Verdict
	// Reason is set only when Verdict is VerdictCounterexample.
	Reason CounterexampleReason
	// Counterexample is set only when Verdict is VerdictCounterexample.
	Counterexample *Counterexample
	// Checked is the number of common inputs fully processed.
	Checked *big.Int
	// Total is the full input-space size 256^InputLen.
	Total *big.Int
	// StepsUsed counts instructions executed in both programs so far.
	StepsUsed int64
}

// Verify enumerates the common input space of oldProg and newProg in
// lexicographic byte order and compares observable behavior on each input.
//
// An input where oldProg traps out of bounds is exempt (the patch is allowed
// to reject it). Every other input requires newProg to stay in bounds, return
// the same r0 and produce the same ordered array-write trace. The first
// violating input in lexicographic order is reported. Structural validation
// errors and invalid configurations are returned as errors instead.
func Verify(oldProg, newProg *Program, cfg Config) (*Result, error) {
	if err := oldProg.Validate(); err != nil {
		return nil, fmt.Errorf("relexec: old program: %w", err)
	}
	if err := newProg.Validate(); err != nil {
		return nil, fmt.Errorf("relexec: new program: %w", err)
	}
	if cfg.InputLen < 0 || cfg.InputLen > MaxInputLen {
		return nil, fmt.Errorf("relexec: InputLen %d out of range [0,%d]", cfg.InputLen, MaxInputLen)
	}
	if cfg.ArrayLen < 1 || cfg.ArrayLen > MaxArrayLen {
		return nil, fmt.Errorf("relexec: ArrayLen %d out of range [1,%d]", cfg.ArrayLen, MaxArrayLen)
	}
	if cfg.Budget == 0 || cfg.Budget < UnlimitedBudget {
		return nil, errors.New("relexec: Budget must be positive or UnlimitedBudget")
	}

	total := new(big.Int).Exp(big.NewInt(256), big.NewInt(int64(cfg.InputLen)), nil)
	res := &Result{
		Checked: new(big.Int),
		Total:   total,
	}

	remaining := func() int64 {
		if cfg.Budget == UnlimitedBudget {
			return UnlimitedBudget
		}
		return cfg.Budget - res.StepsUsed
	}

	input := make([]byte, cfg.InputLen)
	for {
		oldOut, err := Run(oldProg, input, cfg.ArrayLen, remaining())
		if err != nil {
			res.Verdict = VerdictUnknown
			return res, nil
		}
		res.StepsUsed += oldOut.Steps

		newOut, err := Run(newProg, input, cfg.ArrayLen, remaining())
		if err != nil {
			res.Verdict = VerdictUnknown
			return res, nil
		}
		res.StepsUsed += newOut.Steps
		res.Checked.Add(res.Checked, big.NewInt(1))

		// The original program went out of bounds: the patch owes no
		// equivalence on this input.
		if !oldOut.OOB {
			if newOut.OOB {
				res.Verdict = VerdictCounterexample
				res.Reason = ReasonNewOOB
				res.Counterexample = &Counterexample{
					Input:  append([]byte(nil), input...),
					Reason: ReasonNewOOB,
					Old:    oldOut,
					New:    newOut,
				}
				return res, nil
			}
			if newOut.Return != oldOut.Return {
				res.Verdict = VerdictCounterexample
				res.Reason = ReasonReturnMismatch
				res.Counterexample = &Counterexample{
					Input:  append([]byte(nil), input...),
					Reason: ReasonReturnMismatch,
					Old:    oldOut,
					New:    newOut,
				}
				return res, nil
			}
			if !writesEqual(oldOut.Writes, newOut.Writes) {
				res.Verdict = VerdictCounterexample
				res.Reason = ReasonWriteMismatch
				res.Counterexample = &Counterexample{
					Input:  append([]byte(nil), input...),
					Reason: ReasonWriteMismatch,
					Old:    oldOut,
					New:    newOut,
				}
				return res, nil
			}
		}

		if !nextLex(input) {
			break
		}
	}

	res.Verdict = VerdictEquivalent
	return res, nil
}

// writesEqual compares ordered store traces element by element.
func writesEqual(a, b []Write) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// nextLex advances buf to the next byte string in lexicographic order with
// the most significant byte first. It reports whether a next value exists;
// an all-0xff input returns false. For an empty buffer it also returns false,
// covering the single-input InputLen == 0 case.
func nextLex(buf []byte) bool {
	for i := len(buf) - 1; i >= 0; i-- {
		if buf[i] != 0xff {
			buf[i]++
			return true
		}
		buf[i] = 0
	}
	return false
}
