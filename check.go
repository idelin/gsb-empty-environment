package netsec

import "fmt"

// Verdict is the outcome of a finite-set trace check.
type Verdict int

const (
	// VerdictPass means every pair of caller-supplied inputs produced an
	// identical trace within the step budget. It is strictly a statement
	// about that finite set and that budget.
	VerdictPass Verdict = iota
	// VerdictFail means two inputs produced different traces; Report.Witness
	// carries the shortest divergence found.
	VerdictFail
	// VerdictUnknown means no conclusion can be drawn: missing or exhausted
	// budget, invalid program, malformed inputs, or too few inputs. An
	// unknown budget never counts as a pass.
	VerdictUnknown
)

func (v Verdict) String() string {
	switch v {
	case VerdictPass:
		return "pass"
	case VerdictFail:
		return "fail"
	default:
		return "unknown"
	}
}

// PassDisclaimer is attached to every passing report. A finite input set
// exercised under a finite step budget does not prove constant-time
// behaviour for arbitrary inputs, other programs, or arbitrary machine code.
const PassDisclaimer = "trace-equivalent only on the caller-supplied finite input set under the given step budget; " +
	"this does not prove constant-time behaviour for arbitrary inputs, other programs, or arbitrary machine code"

// Witness is a complete counterexample: both full inputs, both full traces,
// and the index of the first diverging event.
type Witness struct {
	SecretA    []byte
	CandidateA []byte
	SecretB    []byte
	CandidateB []byte
	TraceA     []Event
	TraceB     []Event
	DivergeAt  int
}

// Report summarises a Check run.
type Report struct {
	Verdict Verdict
	Witness *Witness // non-nil only when Verdict == VerdictFail
	Reason  string
	Inputs  int // inputs executed
	Pairs   int // trace pairs compared
}

// Check runs prog (at most MaxProgramLen instructions) over the cross
// product of the caller-supplied finite secret and candidate sets, then
// compares the recorded traces pairwise. On divergence it returns the
// shortest difference found — earliest first-diverging event, ties broken by
// shorter trace — together with the full input witness.
//
// maxSteps must be a positive, explicit budget: an unknown budget can never
// count as a pass, and any execution that exceeds the budget downgrades the
// verdict to VerdictUnknown unless a definite divergence was already found.
func Check(prog []Instruction, secrets, candidates [][]byte, maxSteps uint64) Report {
	if err := Validate(prog); err != nil {
		return Report{Verdict: VerdictUnknown, Reason: fmt.Sprintf("invalid program: %v", err)}
	}
	if maxSteps == 0 {
		return Report{Verdict: VerdictUnknown, Reason: "no step budget supplied: an unknown budget can never count as a pass"}
	}
	// Length validation is public and completes before the secret region is
	// entered by any execution below.
	for i, s := range secrets {
		if len(s) != SecretSize {
			return Report{Verdict: VerdictUnknown, Reason: fmt.Sprintf("secret %d has length %d, want %d", i, len(s), SecretSize)}
		}
	}
	for i, c := range candidates {
		if len(c) != CandidateSize {
			return Report{Verdict: VerdictUnknown, Reason: fmt.Sprintf("candidate %d has length %d, want %d", i, len(c), CandidateSize)}
		}
	}

	type run struct {
		secret, candidate []byte
		trace             []Event // nil when execution failed
	}
	runs := make([]run, 0, len(secrets)*len(candidates))
	var runErr error
	for _, s := range secrets {
		for _, c := range candidates {
			vm := VM{MaxSteps: maxSteps}
			copy(vm.Mem[SecretBase:], s)
			copy(vm.Mem[CandidateBase:], c)
			if err := vm.Run(prog); err != nil {
				if runErr == nil {
					runErr = err
				}
				runs = append(runs, run{s, c, nil})
				continue
			}
			runs = append(runs, run{s, c, vm.Trace})
		}
	}

	rep := Report{Inputs: len(runs)}
	if len(runs) < 2 {
		rep.Verdict = VerdictUnknown
		rep.Reason = "fewer than two inputs: nothing to compare pairwise"
		return rep
	}

	var best *Witness
	bestTraceLen := 0
	for i := 0; i < len(runs); i++ {
		for j := i + 1; j < len(runs); j++ {
			a, b := runs[i], runs[j]
			if a.trace == nil || b.trace == nil {
				continue
			}
			rep.Pairs++
			d := FirstDivergence(a.trace, b.trace)
			if d < 0 {
				continue
			}
			traceLen := max(len(a.trace), len(b.trace))
			if best == nil || d < best.DivergeAt || (d == best.DivergeAt && traceLen < bestTraceLen) {
				best = &Witness{
					SecretA: a.secret, CandidateA: a.candidate,
					SecretB: b.secret, CandidateB: b.candidate,
					TraceA: a.trace, TraceB: b.trace,
					DivergeAt: d,
				}
				bestTraceLen = traceLen
			}
		}
	}

	if best != nil {
		rep.Verdict = VerdictFail
		rep.Witness = best
		rep.Reason = fmt.Sprintf("traces diverge at event %d", best.DivergeAt)
		return rep
	}
	if runErr != nil {
		rep.Verdict = VerdictUnknown
		rep.Reason = fmt.Sprintf("execution incomplete, no conclusion possible: %v", runErr)
		return rep
	}
	rep.Verdict = VerdictPass
	rep.Reason = PassDisclaimer
	return rep
}
