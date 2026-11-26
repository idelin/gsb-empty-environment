package stlf

import (
	"fmt"
	"sort"
	"time"
)

// Patch is a set of barrier insertion sites.  A position p means "insert a
// barrier before the original instruction at index p"; p == len(code) appends
// a barrier.
type Patch struct {
	Positions []int
}

// NewPatch builds a patch from insertion positions.
func NewPatch(positions ...int) Patch {
	return Patch{Positions: append([]int(nil), positions...)}
}

func (p Patch) normalized(codeLen int) ([]int, error) {
	out := append([]int(nil), p.Positions...)
	sort.Ints(out)
	uniq := out[:0]
	for _, pos := range out {
		if pos < 0 || pos > codeLen {
			return nil, fmt.Errorf("barrier insertion position %d out of range [0,%d]", pos, codeLen)
		}
		if len(uniq) == 0 || uniq[len(uniq)-1] != pos {
			uniq = append(uniq, pos)
		}
	}
	return uniq, nil
}

// String renders a patch as "barrier@i,...".
func (p Patch) String() string {
	if len(p.Positions) == 0 {
		return "barrier@none"
	}
	out := ""
	for i, pos := range p.Positions {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf("barrier@%d", pos)
	}
	return out
}

// InsertBarriers inserts the patch and repairs all branch targets so that every
// old target still names the same original instruction.  This is essential:
// naive insertion would silently change architectural control flow.
func InsertBarriers(code []Instruction, patch Patch) ([]Instruction, error) {
	positions, err := patch.normalized(len(code))
	if err != nil {
		return nil, err
	}
	if len(code)+len(positions) > MaxPatchedInstructions {
		return nil, fmt.Errorf("patched program has %d instructions; maximum is %d", len(code)+len(positions), MaxPatchedInstructions)
	}

	adjust := func(target int) int {
		return target + countLE(positions, target)
	}
	out := make([]Instruction, 0, len(code)+len(positions))
	pi := 0
	for i := 0; i <= len(code); i++ {
		for pi < len(positions) && positions[pi] == i {
			out = append(out, Barrier())
			pi++
		}
		if i == len(code) {
			break
		}
		ins := code[i]
		if ins.Op == OpCmpBranch {
			ins.Target = adjust(ins.Target)
		}
		out = append(out, ins)
	}
	return out, nil
}

// Apply is a convenience wrapper around InsertBarriers.
func (p Patch) Apply(code []Instruction) ([]Instruction, error) {
	return InsertBarriers(code, p)
}

func countLE(sorted []int, v int) int {
	return sort.SearchInts(sorted, v+1)
}

// VerifyOptions bounds full-state patch verification.
type VerifyOptions struct {
	PublicValues []uint8
	SecretValues []uint8
	// Timeout defaults to 30 seconds.
	Timeout  time.Duration
	MaxSteps int
	// MaxReportedLeaks caps representative remaining witnesses.
	MaxReportedLeaks int
	// MaxReportedSemanticMismatches caps representative state mismatches.
	MaxReportedSemanticMismatches int
}

func (o VerifyOptions) searchOptions() SearchOptions {
	return SearchOptions{
		PublicValues:       o.PublicValues,
		SecretValues:       o.SecretValues,
		Timeout:            o.Timeout,
		MaxSteps:           o.MaxSteps,
		MaxNativeWitnesses: o.MaxReportedLeaks,
	}
}

func (o VerifyOptions) timeout() time.Duration {
	if o.Timeout == 0 {
		return 30 * time.Second
	}
	return o.Timeout
}

// SemanticsMismatch records one input where the barrier patch changed the
// precise architectural reference state.
type SemanticsMismatch struct {
	Input     Input
	Unpatched MachineState
	Patched   MachineState
}

// PatchReport is the result of checking a proposed barrier patch.
type PatchReport struct {
	Patch       Patch
	PatchedCode []Instruction

	// Valid is true only when semantics are preserved, no speculative leak
	// remains, and no architectural leak remains.
	Valid bool
	// SemanticsPreserved compares every architectural state field and the
	// committed probe trace against the unpatched full-state reference model.
	SemanticsPreserved bool
	// LeakFree reports that no same-architecture/different-speculation pair was
	// found in the patched program.
	LeakFree bool
	// NativeLeakFree reports that the patched committed traces do not already
	// differ across secrets.
	NativeLeakFree bool
	// NativeVulnerable is the negation of NativeLeakFree.
	NativeVulnerable bool

	SpecLeakCount       int
	NativeCount         int
	Remaining           []Witness
	SemanticsMismatches []SemanticsMismatch

	CheckedInputs int
	CheckedPairs  int
	Elapsed       time.Duration

	Unknown  bool
	TimedOut bool
	Message  string
	Err      error
}

// VerifyBarrierPatch checks a proposed patch against the full-state reference
// model for every input in the selected domain.
func VerifyBarrierPatch(host Host, code []Instruction, patch Patch, opts ...VerifyOptions) PatchReport {
	start := time.Now()
	var opt VerifyOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	report := PatchReport{Patch: patch}
	if err := Validate(host, code); err != nil {
		return invalidReport(report, start, err)
	}
	patched, err := InsertBarriers(code, patch)
	if err != nil {
		return invalidReport(report, start, err)
	}
	if err := ValidatePatched(host, patched); err != nil {
		return invalidReport(report, start, err)
	}
	report.PatchedCode = patched

	deadline := start.Add(opt.timeout())
	pubs := opt.searchOptions().publicValues()
	secs := opt.searchOptions().secretValues()
	runOpt := RunOptions{MaxSteps: opt.MaxSteps}
	maxLeaks := opt.MaxReportedLeaks
	if maxLeaks <= 0 {
		maxLeaks = 8
	}
	maxMismatch := opt.MaxReportedSemanticMismatches
	if maxMismatch <= 0 {
		maxMismatch = 8
	}

	report.SemanticsPreserved = true
	checkedPairs := 0
	for _, pub := range pubs {
		execs := make([]Execution, len(secs))
		for j, secret := range secs {
			if time.Now().After(deadline) {
				return unknownPatchReport(report, checkedPairs, start)
			}
			in := Input{PublicLow6: pub, Secret: secret}
			baseState, err := ReferenceRun(host, code, in, runOpt)
			if err != nil {
				return invalidReport(report, start, err)
			}
			patchedState, err := ReferenceRun(host, patched, in, runOpt)
			if err != nil {
				return invalidReport(report, start, err)
			}
			if !StatesEquivalent(baseState, patchedState, code, patched) {
				report.SemanticsPreserved = false
				if len(report.SemanticsMismatches) < maxMismatch {
					report.SemanticsMismatches = append(report.SemanticsMismatches, SemanticsMismatch{
						Input: in, Unpatched: baseState, Patched: patchedState,
					})
				}
			}
			execs[j] = Execute(host, patched, in, runOpt)
			if execs[j].Truncated {
				return unknownPatchReport(report, checkedPairs, start)
			}
			report.CheckedInputs++
		}

		for i := 0; i < len(secs); i++ {
			for j := i + 1; j < len(secs); j++ {
				checkedPairs++
				if checkedPairs%4096 == 0 && time.Now().After(deadline) {
					return unknownPatchReport(report, checkedPairs, start)
				}
				a, b := execs[i], execs[j]
				if !a.ArchTrace.Equal(b.ArchTrace) {
					report.NativeCount++
					continue
				}
				if a.SpecTrace.Equal(b.SpecTrace) {
					continue
				}
				report.SpecLeakCount++
				if len(report.Remaining) < maxLeaks {
					report.Remaining = append(report.Remaining, makeWitness(pub, secs[i], secs[j], a, b, false))
				}
			}
		}
	}

	report.CheckedPairs = checkedPairs
	report.Elapsed = time.Since(start)
	report.LeakFree = report.SpecLeakCount == 0
	report.NativeLeakFree = report.NativeCount == 0
	report.NativeVulnerable = !report.NativeLeakFree
	report.Valid = report.SemanticsPreserved && report.LeakFree && report.NativeLeakFree
	report.Message = patchMessage(report)
	return report
}

// VerifyPatch is an alias for VerifyBarrierPatch.
func VerifyPatch(host Host, code []Instruction, patch Patch, opts ...VerifyOptions) PatchReport {
	return VerifyBarrierPatch(host, code, patch, opts...)
}

func patchMessage(r PatchReport) string {
	switch {
	case !r.SemanticsPreserved:
		return "patch changes architectural semantics"
	case r.NativeCount > 0:
		return fmt.Sprintf("native architectural leak remains across %d pairs; barriers cannot repair it without changing semantics", r.NativeCount)
	case r.SpecLeakCount > 0:
		return fmt.Sprintf("%d speculative leak pairs remain", r.SpecLeakCount)
	default:
		return "patch preserves semantics and eliminates all modelled leaks"
	}
}

func invalidReport(r PatchReport, start time.Time, err error) PatchReport {
	r.Elapsed = time.Since(start)
	r.Err = err
	r.Message = err.Error()
	r.Valid = false
	r.Unknown = false
	return r
}

func unknownPatchReport(r PatchReport, pairs int, start time.Time) PatchReport {
	r.CheckedPairs = pairs
	r.Elapsed = time.Since(start)
	r.Unknown = true
	r.TimedOut = true
	r.Valid = false
	r.Message = "Unknown"
	return r
}

// PatchSearchOptions controls minimal barrier selection.
type PatchSearchOptions struct {
	VerifyOptions
	// Positions restricts candidate insertion sites.  Empty means all sites
	// 0..len(code).  Search order is descending so that the latest, least
	// disruptive effective barrier is chosen among equal-size patches.
	Positions []int
}

// FindMinimalBarrierPatch searches for a minimum-size (zero or one site in this
// single-window model) barrier patch.  It returns the latest effective site so
// that speculation is preserved for as long as possible.
func FindMinimalBarrierPatch(host Host, code []Instruction, opts ...PatchSearchOptions) (Patch, PatchReport) {
	start := time.Now()
	var opt PatchSearchOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	base := FindWitness(host, code, opt.searchOptions())
	if base.Unknown {
		return Patch{}, PatchReport{Unknown: true, TimedOut: base.TimedOut, Message: "Unknown", Err: base.Err, Elapsed: time.Since(start)}
	}
	if base.NativeCount > 0 {
		return Patch{}, PatchReport{
			NativeCount:      base.NativeCount,
			NativeVulnerable: true,
			Message:          "architectural leak is not repairable by a semantics-preserving speculation barrier",
			Elapsed:          time.Since(start),
		}
	}
	if base.Witness == nil {
		return Patch{}, PatchReport{
			Valid:              true,
			SemanticsPreserved: true,
			LeakFree:           true,
			NativeLeakFree:     true,
			Message:            "program is already leak-free; empty patch is minimal",
			Elapsed:            time.Since(start),
		}
	}

	candidates := append([]int(nil), opt.Positions...)
	if len(candidates) == 0 {
		for pos := len(code); pos >= 0; pos-- {
			candidates = append(candidates, pos)
		}
	} else {
		sort.Sort(sort.Reverse(sort.IntSlice(candidates)))
	}

	var last PatchReport
	for _, pos := range candidates {
		patch := NewPatch(pos)
		patched, err := InsertBarriers(code, patch)
		if err != nil {
			continue
		}
		// Cheap prefilter: the patch must at least remove the known witness.
		if !patchBlocksWitness(host, code, patched, base.Witness, opt.VerifyOptions) {
			continue
		}
		report := VerifyBarrierPatch(host, code, patch, opt.VerifyOptions)
		report.Elapsed = time.Since(start)
		last = report
		if report.Unknown {
			return patch, report
		}
		if report.Err != nil {
			return patch, report
		}
		if report.Valid {
			report.Message = fmt.Sprintf("%s; %s", patch.String(), report.Message)
			return patch, report
		}
	}
	if last.Message == "" {
		last = PatchReport{Message: "no single barrier candidate eliminated the leak", Unknown: true, Elapsed: time.Since(start)}
	}
	return Patch{}, last
}

func patchBlocksWitness(host Host, original, patched []Instruction, w *Witness, opt VerifyOptions) bool {
	runOpt := RunOptions{MaxSteps: opt.MaxSteps}
	for _, in := range []Input{
		{PublicLow6: w.PublicLow6, Secret: w.SecretA},
		{PublicLow6: w.PublicLow6, Secret: w.SecretB},
	} {
		a := Execute(host, patched, in, runOpt)
		b := Execute(host, original, in, runOpt)
		if a.Truncated || b.Truncated {
			return false
		}
		if !a.ArchTrace.Equal(b.ArchTrace) {
			return false
		}
	}
	a := Execute(host, patched, Input{PublicLow6: w.PublicLow6, Secret: w.SecretA}, runOpt)
	b := Execute(host, patched, Input{PublicLow6: w.PublicLow6, Secret: w.SecretB}, runOpt)
	if a.Truncated || b.Truncated {
		return false
	}
	return a.ArchTrace.Equal(b.ArchTrace) && a.SpecTrace.Equal(b.SpecTrace)
}
