package netsec

import (
	"fmt"
	"reflect"
)

// Status is the outcome of the bounded search.
type Status string

const (
	// StatusWitnessFound means two runs with the same public input, different
	// secrets, equal architectural probes, and different speculative probes
	// were found.
	StatusWitnessFound Status = "witness_found"
	// StatusNoWitnessBounded means every input assignment and every
	// non-degenerate misprediction occurrence was evaluated within the run
	// budget, and no witness exists.
	StatusNoWitnessBounded Status = "no_witness_bounded"
	// StatusUnknown means the search stopped before exhausting the candidate
	// space, or at least one run hit the 64-step limit.
	StatusUnknown Status = "unknown"
)

// Result is returned by Analyze.
type Result struct {
	Status                    Status
	Witness                   *Witness
	Runs                      int
	Budget                    int
	Exhausted                 bool
	InputCandidates           int64
	InputAssignmentsEvaluated int64
	Reason                    string
}

// Witness contains the two replayable runs. Public is the shared public input.
type Witness struct {
	Public  []uint8
	SecretA uint8
	SecretB uint8
	RunA    RunResult
	RunB    RunResult
}

type candidateRun struct {
	secret  uint8
	archKey string
	specKey string
	run     RunResult
}

type analysis struct {
	program Program
	budget  int
	runs    int

	budgetExhausted bool
	stepOverflow    bool
	assignmentsDone int64
	totalInputs     int64
}

// Analyze searches for a speculative-probe witness. Every call to Run counts
// against budget. If no witness is found, StatusNoWitnessBounded is returned
// only when the complete candidate space was evaluated and no run exceeded the
// 64-step limit; otherwise the result is StatusUnknown.
func Analyze(p Program, budget int) (Result, error) {
	if err := p.Validate(); err != nil {
		return Result{}, err
	}
	if budget < 0 {
		return Result{}, fmt.Errorf("%w: negative run budget %d", ErrInvalidInput, budget)
	}

	publicSlots := p.PublicSlots()
	a := &analysis{
		program:     p,
		budget:      budget,
		totalInputs: totalInputCandidates(len(publicSlots)),
	}
	w, err := a.search(len(publicSlots))
	if err != nil {
		return Result{}, err
	}

	out := Result{
		Runs:                      a.runs,
		Budget:                    budget,
		InputCandidates:           a.totalInputs,
		InputAssignmentsEvaluated: a.assignmentsDone,
	}
	if w != nil {
		out.Status = StatusWitnessFound
		out.Witness = w
		out.Reason = fmt.Sprintf("witness found after %d executions", a.runs)
		return out, nil
	}
	if a.budgetExhausted || a.stepOverflow {
		out.Status = StatusUnknown
		switch {
		case a.budgetExhausted && a.stepOverflow:
			out.Reason = fmt.Sprintf("run budget %d exhausted and at least one run exceeded %d steps", budget, MaxSteps)
		case a.budgetExhausted:
			out.Reason = fmt.Sprintf("run budget %d exhausted before all candidates were evaluated", budget)
		default:
			out.Reason = fmt.Sprintf("at least one run exceeded the %d-step limit", MaxSteps)
		}
		return out, nil
	}

	out.Status = StatusNoWitnessBounded
	out.Exhausted = true
	out.Reason = fmt.Sprintf("all %d input candidates evaluated with no witness", a.totalInputs)
	return out, nil
}

func totalInputCandidates(publicSlots int) int64 {
	n := int64(256) // one secret byte
	for i := 0; i < publicSlots; i++ {
		n *= 256
	}
	return n
}

func (a *analysis) beginRun() bool {
	if a.runs >= a.budget {
		a.budgetExhausted = true
		return false
	}
	a.runs++
	return true
}

func (a *analysis) search(publicSlots int) (*Witness, error) {
	switch publicSlots {
	case 0:
		w, keepGoing, err := a.evaluatePublic(nil)
		if err != nil {
			return nil, err
		}
		if w != nil {
			return w, nil
		}
		_ = keepGoing
	case 1:
		for v0 := 0; v0 < 256; v0++ {
			w, keepGoing, err := a.evaluatePublic([]uint8{uint8(v0)})
			if err != nil {
				return nil, err
			}
			if w != nil {
				return w, nil
			}
			if !keepGoing {
				return nil, nil
			}
		}
	case 2:
		for v0 := 0; v0 < 256; v0++ {
			for v1 := 0; v1 < 256; v1++ {
				w, keepGoing, err := a.evaluatePublic([]uint8{uint8(v0), uint8(v1)})
				if err != nil {
					return nil, err
				}
				if w != nil {
					return w, nil
				}
				if !keepGoing {
					return nil, nil
				}
			}
		}
	default:
		return nil, fmt.Errorf("%w: %d public slots", ErrInvalidProgram, publicSlots)
	}
	return nil, nil
}

// evaluatePublic returns a witness if the public-input group contains one.
// keepGoing is false only when the run budget stopped the search.
func (a *analysis) evaluatePublic(public []uint8) (*Witness, bool, error) {
	var group []candidateRun
	for secret := 0; secret < 256; secret++ {
		assignment := Assignment{
			Public: clonePublic(public),
			Secret: uint8(secret),
		}
		if !a.beginRun() {
			if w := findWitnessInGroup(public, group); w != nil {
				return w, false, nil
			}
			return nil, false, nil
		}
		arch, err := run(a.program, assignment, nil)
		if err != nil {
			return nil, false, err
		}
		if arch.OverLimit {
			a.stepOverflow = true
			continue
		}

		for _, branch := range arch.Branches {
			if branch.Degenerate {
				continue
			}
			if !a.beginRun() {
				if w := findWitnessInGroup(public, group); w != nil {
					return w, false, nil
				}
				return nil, false, nil
			}
			runResult, err := run(a.program, assignment, &MispredictSpec{BranchOccurrence: branch.Occurrence})
			if err != nil {
				return nil, false, err
			}
			if runResult.OverLimit {
				a.stepOverflow = true
				continue
			}
			group = append(group, candidateRun{
				secret:  uint8(secret),
				archKey: probeKey(runResult.ArchProbes),
				specKey: probeKey(runResult.SpecProbes),
				run:     runResult,
			})
		}
	}
	a.assignmentsDone++
	if w := findWitnessInGroup(public, group); w != nil {
		return w, true, nil
	}
	return nil, true, nil
}

func probeKey(records []ProbeRecord) string {
	buf := make([]byte, len(records))
	for i := range records {
		buf[i] = byte(records[i].Line)
	}
	return string(buf)
}

func findWitnessInGroup(public []uint8, group []candidateRun) *Witness {
	buckets := make(map[string][]int)
	for i := range group {
		for _, j := range buckets[group[i].archKey] {
			if group[j].secret == group[i].secret {
				continue
			}
			if group[j].specKey == group[i].specKey {
				continue
			}
			return &Witness{
				Public:  clonePublic(public),
				SecretA: group[j].secret,
				SecretB: group[i].secret,
				RunA:    group[j].run,
				RunB:    group[i].run,
			}
		}
		buckets[group[i].archKey] = append(buckets[group[i].archKey], i)
	}
	return nil
}

func sameProbeLines(a, b []ProbeRecord) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Line != b[i].Line {
			return false
		}
	}
	return true
}

func witnessAssignments(p Program, w Witness) (Assignment, Assignment, error) {
	if len(w.Public) != len(p.PublicSlots()) {
		return Assignment{}, Assignment{}, fmt.Errorf("%w: witness has %d public bytes, program wants %d", ErrInvalidInput, len(w.Public), len(p.PublicSlots()))
	}
	if w.SecretA == w.SecretB {
		return Assignment{}, Assignment{}, fmt.Errorf("%w: witness secrets are both %d", ErrInvalidInput, w.SecretA)
	}
	a := Assignment{Public: clonePublic(w.Public), Secret: w.SecretA}
	b := Assignment{Public: clonePublic(w.Public), Secret: w.SecretB}
	return a, b, nil
}

// Replay re-executes both witness runs from their stored misprediction
// occurrences.
func Replay(p Program, w Witness) (RunResult, RunResult, error) {
	if err := p.Validate(); err != nil {
		return RunResult{}, RunResult{}, err
	}
	a, b, err := witnessAssignments(p, w)
	if err != nil {
		return RunResult{}, RunResult{}, err
	}
	if w.RunA.Misprediction == nil || w.RunB.Misprediction == nil {
		return RunResult{}, RunResult{}, fmt.Errorf("%w: witness run has no misprediction", ErrInvalidInput)
	}
	runA, err := Run(p, a, &MispredictSpec{BranchOccurrence: w.RunA.Misprediction.BranchOccurrence})
	if err != nil {
		return RunResult{}, RunResult{}, err
	}
	runB, err := Run(p, b, &MispredictSpec{BranchOccurrence: w.RunB.Misprediction.BranchOccurrence})
	if err != nil {
		return RunResult{}, RunResult{}, err
	}
	return runA, runB, nil
}

// VerifyWitness checks a witness against the program by replaying it and
// enforcing the definition of the leak.
func VerifyWitness(p Program, w Witness) error {
	runA, runB, err := Replay(p, w)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(runA, w.RunA) {
		return fmt.Errorf("%w: run A does not replay exactly", ErrInvalidInput)
	}
	if !reflect.DeepEqual(runB, w.RunB) {
		return fmt.Errorf("%w: run B does not replay exactly", ErrInvalidInput)
	}
	if runA.OverLimit || runB.OverLimit {
		return fmt.Errorf("%w: witness run exceeded %d steps", ErrInvalidInput, MaxSteps)
	}
	if !sameProbeLines(runA.ArchProbes, runB.ArchProbes) {
		return fmt.Errorf("%w: architectural probe records differ", ErrInvalidInput)
	}
	if sameProbeLines(runA.SpecProbes, runB.SpecProbes) {
		return fmt.Errorf("%w: speculative probe records are identical", ErrInvalidInput)
	}
	if runA.Misprediction == nil || runB.Misprediction == nil {
		return fmt.Errorf("%w: both witness runs must mispredict", ErrInvalidInput)
	}
	return nil
}

// Verify is a convenience method for VerifyWitness.
func (w Witness) Verify(p Program) error {
	return VerifyWitness(p, w)
}
