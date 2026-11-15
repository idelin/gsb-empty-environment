package netsec

import (
	"errors"
	"fmt"
)

// ErrInsufficientInputs reports that fewer than two complete input pairs were
// available, so there was no meaningful pairwise trace comparison to perform.
var ErrInsufficientInputs = errors.New("insufficient complete inputs for pairwise trace comparison")

// Pair is one complete 32-byte secret/candidate input. Fixed arrays make it
// impossible to omit or truncate bytes in a checker witness.
type Pair struct {
	Secret    [SecretSize]byte
	Candidate [CandidateSize]byte
}

// NewPair validates lengths before copying either value.
func NewPair(secret, candidate []byte) (Pair, error) {
	if len(secret) != SecretSize || len(candidate) != CandidateSize {
		return Pair{}, ErrInvalidLength
	}
	var pair Pair
	copy(pair.Secret[:], secret)
	copy(pair.Candidate[:], candidate)
	return pair, nil
}

// CheckStatus is the outcome of finite-input trace checking. StatusConsistent
// is a statement about the supplied corpus only; it is not a proof for
// arbitrary machine code.
type CheckStatus string

const (
	StatusConsistent CheckStatus = "consistent_finite_inputs"
	StatusDivergent  CheckStatus = "divergent"
	StatusUnknown    CheckStatus = "unknown"
	StatusInvalid    CheckStatus = "invalid"
)

// FiniteCorpusScope is included in every report to keep the assurance boundary
// explicit.
const FiniteCorpusScope = "trace equality over the caller-supplied finite input set only; this is not a proof for arbitrary inputs or arbitrary machine code"

// Witness identifies the earliest trace divergence and contains both complete
// inputs. No witness is returned unless both executions completed.
type Witness struct {
	LeftIndex  int
	RightIndex int
	Left       Pair
	Right      Pair
	Difference Difference
}

// CheckReport is the result of comparing traces over a finite corpus.
type CheckReport struct {
	Status             CheckStatus
	InstructionCount   int
	InputCount         int
	PairsCompared      int
	ShortestDifference int // first differing trace step; -1 when there is none
	Witness            *Witness
	Err                error
	Reason             string
	MaxSteps           uint64
	Scope              string
}

// Passed reports whether every completed pair in the finite corpus had the
// same trace. False includes StatusUnknown and StatusInvalid, so an unknown or
// exhausted budget can never be mistaken for a pass.
func (r CheckReport) Passed() bool {
	return r.Status == StatusConsistent
}

// IsProof is always false: a finite corpus cannot prove arbitrary machine code
// or arbitrary inputs constant-time.
func (r CheckReport) IsProof() bool {
	return false
}

func (r CheckReport) String() string {
	if r.Witness != nil {
		return fmt.Sprintf("%s: earliest difference at trace step %d between inputs %d and %d (%s)",
			r.Status, r.Witness.Difference.Step, r.Witness.LeftIndex, r.Witness.RightIndex, r.Reason)
	}
	return fmt.Sprintf("%s: %s", r.Status, r.Reason)
}

// CheckFinite executes every supplied pair with the same finite step budget,
// compares every unordered pair of traces, and returns the earliest
// divergence. At least two complete inputs are required for a meaningful
// pairwise comparison. A zero budget returns StatusUnknown.
func CheckFinite(program Program, pairs []Pair, maxSteps uint64) CheckReport {
	report := CheckReport{
		InstructionCount:   len(program.Instructions),
		InputCount:         len(pairs),
		ShortestDifference: -1,
		MaxSteps:           maxSteps,
		Scope:              FiniteCorpusScope,
	}

	if err := program.Validate(); err != nil {
		report.Status = StatusInvalid
		report.Err = err
		report.Reason = err.Error()
		return report
	}
	if maxSteps == 0 {
		report.Status = StatusUnknown
		report.Err = ErrUnknownBudget
		report.Reason = "execution budget is unknown; a missing budget cannot be treated as a pass"
		return report
	}
	if len(pairs) < 2 {
		report.Status = StatusUnknown
		report.Err = ErrInsufficientInputs
		report.Reason = "at least two complete inputs are required for pairwise trace comparison"
		return report
	}

	executions := make([]Execution, len(pairs))
	for i, pair := range pairs {
		exec, err := Execute32(program, pair.Secret, pair.Candidate, RunConfig{MaxSteps: maxSteps})
		if err != nil {
			report.Status = executionErrorStatus(err)
			report.Err = err
			report.Reason = fmt.Sprintf("input %d did not complete: %v", i, err)
			return report
		}
		executions[i] = exec
	}

	var earliest *Witness
	for i := 0; i < len(pairs); i++ {
		for j := i + 1; j < len(pairs); j++ {
			report.PairsCompared++
			diff, different := DiffTraces(executions[i].Trace, executions[j].Trace)
			if !different {
				continue
			}
			if earliest == nil || diff.Step < earliest.Difference.Step {
				earliest = &Witness{
					LeftIndex:  i,
					RightIndex: j,
					Left:       pairs[i],
					Right:      pairs[j],
					Difference: diff,
				}
			}
		}
	}

	if earliest != nil {
		report.Status = StatusDivergent
		report.ShortestDifference = earliest.Difference.Step
		report.Witness = earliest
		report.Reason = "observable read/branch traces differ for at least one input pair"
		return report
	}

	report.Status = StatusConsistent
	report.Reason = "all supplied input pairs had identical observable traces"
	return report
}

// CheckFiniteWithConfig is CheckFinite with a RunConfig budget. A zero
// MaxSteps is still an unknown budget, not an unlimited one.
func CheckFiniteWithConfig(program Program, pairs []Pair, cfg RunConfig) CheckReport {
	return CheckFinite(program, pairs, cfg.MaxSteps)
}

func executionErrorStatus(err error) CheckStatus {
	if errors.Is(err, ErrBudgetExceeded) || errors.Is(err, ErrUnknownBudget) {
		return StatusUnknown
	}
	return StatusInvalid
}

// CheckFiniteSets validates all set members before executing anything, forms
// the Cartesian product of the secret and candidate sets, and checks every
// resulting input pair.
func CheckFiniteSets(program Program, secrets, candidates [][]byte, maxSteps uint64) CheckReport {
	report := CheckReport{
		InstructionCount:   len(program.Instructions),
		ShortestDifference: -1,
		MaxSteps:           maxSteps,
		Scope:              FiniteCorpusScope,
	}

	if err := program.Validate(); err != nil {
		report.Status = StatusInvalid
		report.Err = err
		report.Reason = err.Error()
		return report
	}
	if maxSteps == 0 {
		report.Status = StatusUnknown
		report.Err = ErrUnknownBudget
		report.Reason = "execution budget is unknown; a missing budget cannot be treated as a pass"
		return report
	}

	for i, secret := range secrets {
		if len(secret) != SecretSize {
			report.Status = StatusInvalid
			report.Err = ErrInvalidLength
			report.Reason = fmt.Sprintf("secret %d has length %d, want %d; no input was executed", i, len(secret), SecretSize)
			return report
		}
	}
	for i, candidate := range candidates {
		if len(candidate) != CandidateSize {
			report.Status = StatusInvalid
			report.Err = ErrInvalidLength
			report.Reason = fmt.Sprintf("candidate %d has length %d, want %d; no input was executed", i, len(candidate), CandidateSize)
			return report
		}
	}

	pairs := make([]Pair, 0, len(secrets)*len(candidates))
	for _, secret := range secrets {
		for _, candidate := range candidates {
			var pair Pair
			copy(pair.Secret[:], secret)
			copy(pair.Candidate[:], candidate)
			pairs = append(pairs, pair)
		}
	}
	report.InputCount = len(pairs)
	if len(pairs) < 2 {
		report.Status = StatusUnknown
		report.Err = ErrInsufficientInputs
		report.Reason = "the Cartesian product has fewer than two complete inputs"
		return report
	}

	return CheckFinite(program, pairs, maxSteps)
}

// CheckFiniteSetsWithConfig is CheckFiniteSets with a RunConfig budget.
func CheckFiniteSetsWithConfig(program Program, secrets, candidates [][]byte, cfg RunConfig) CheckReport {
	return CheckFiniteSets(program, secrets, candidates, cfg.MaxSteps)
}

// Check is an alias for CheckFinite.
func Check(program Program, pairs []Pair, maxSteps uint64) CheckReport {
	return CheckFinite(program, pairs, maxSteps)
}

// Checker attaches one explicit instruction-step budget to a sequence of
// finite-corpus checks. MaxSteps must be nonzero; there is no unlimited mode.
type Checker struct {
	MaxSteps uint64
}

// CheckFinite compares every unordered pair in pairs.
func (c Checker) CheckFinite(program Program, pairs []Pair) CheckReport {
	return CheckFinite(program, pairs, c.MaxSteps)
}

// CheckFiniteSets validates all members, forms the Cartesian product, and
// compares every resulting input pair.
func (c Checker) CheckFiniteSets(program Program, secrets, candidates [][]byte) CheckReport {
	return CheckFiniteSets(program, secrets, candidates, c.MaxSteps)
}
