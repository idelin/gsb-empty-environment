package difftest

import "fmt"

// VerdictKind is the top-level classification of a differential run.
// The classes are mutually exclusive: a timeout is never a value mismatch,
// a parse failure is never a semantic difference, and so on.
type VerdictKind int

const (
	// VerdictAgree: both sides produced values equal under the rules.
	VerdictAgree VerdictKind = iota
	// VerdictSemanticDiff: both sides produced values, normalized forms differ.
	VerdictSemanticDiff
	// VerdictTimeoutDiff: exactly one side timed out.
	VerdictTimeoutDiff
	// VerdictBothTimeout: both sides timed out (no semantic conclusion).
	VerdictBothTimeout
	// VerdictRunErrorDiff: exactly one side failed to run, or both failed
	// with different exit codes.
	VerdictRunErrorDiff
	// VerdictBothRunError: both sides failed to run with the same exit code.
	VerdictBothRunError
	// VerdictParseError: a side's output could not be parsed under the rules.
	VerdictParseError
)

func (k VerdictKind) String() string {
	switch k {
	case VerdictAgree:
		return "agree"
	case VerdictSemanticDiff:
		return "semantic_diff"
	case VerdictTimeoutDiff:
		return "timeout_diff"
	case VerdictBothTimeout:
		return "both_timeout"
	case VerdictRunErrorDiff:
		return "run_error_diff"
	case VerdictBothRunError:
		return "both_run_error"
	case VerdictParseError:
		return "parse_error"
	}
	return "unknown"
}

// Divergent reports whether the verdict represents any kind of divergence
// worth saving as a reproducer.
func (k VerdictKind) Divergent() bool {
	return k != VerdictAgree && k != VerdictBothRunError
}

// Verdict is the judge's decision for one input.
type Verdict struct {
	Kind   VerdictKind `json:"kind"`
	Side   string      `json:"side,omitempty"` // "A" or "B" when one side is responsible
	Detail string      `json:"detail,omitempty"`
}

func (v Verdict) String() string {
	s := v.Kind.String()
	if v.Side != "" {
		s += " (side " + v.Side + ")"
	}
	if v.Detail != "" {
		s += ": " + v.Detail
	}
	return s
}

// Judge compares two outcomes strictly by outcome class first, then by the
// normalization rules. Timeout and run-error classes never fall through to
// value comparison.
func Judge(rules *Rules, a, b Outcome) Verdict {
	if err := rules.Compile(); err != nil {
		return Verdict{Kind: VerdictParseError, Detail: "invalid rules: " + err.Error()}
	}

	aT, bT := a.Kind == OutcomeTimeout, b.Kind == OutcomeTimeout
	switch {
	case aT && bT:
		return Verdict{Kind: VerdictBothTimeout}
	case aT:
		return Verdict{Kind: VerdictTimeoutDiff, Side: "A",
			Detail: fmt.Sprintf("A timed out after %s; B exited with %s", a.Duration, b.Kind)}
	case bT:
		return Verdict{Kind: VerdictTimeoutDiff, Side: "B",
			Detail: fmt.Sprintf("B timed out after %s; A exited with %s", b.Duration, a.Kind)}
	}

	aR, bR := a.Kind == OutcomeRunError, b.Kind == OutcomeRunError
	switch {
	case aR && bR:
		if a.ExitCode == b.ExitCode && a.SpawnErr == "" && b.SpawnErr == "" {
			return Verdict{Kind: VerdictBothRunError,
				Detail: fmt.Sprintf("both exited with code %d", a.ExitCode)}
		}
		return Verdict{Kind: VerdictRunErrorDiff,
			Detail: fmt.Sprintf("A: %s; B: %s", runErrorDesc(a), runErrorDesc(b))}
	case aR:
		return Verdict{Kind: VerdictRunErrorDiff, Side: "A",
			Detail: "A failed (" + runErrorDesc(a) + "); B produced a value"}
	case bR:
		return Verdict{Kind: VerdictRunErrorDiff, Side: "B",
			Detail: "B failed (" + runErrorDesc(b) + "); A produced a value"}
	}

	canonA, toksA, errA := rules.normalize("A", a.Stdout)
	canonB, toksB, errB := rules.normalize("B", b.Stdout)
	if errA != nil {
		return Verdict{Kind: VerdictParseError, Side: "A", Detail: errA.Error()}
	}
	if errB != nil {
		return Verdict{Kind: VerdictParseError, Side: "B", Detail: errB.Error()}
	}

	equal := false
	if rules.FloatMode {
		equal = rules.tokensEqual(toksA, toksB)
	} else {
		equal = canonA == canonB
	}
	if equal {
		return Verdict{Kind: VerdictAgree}
	}
	return Verdict{Kind: VerdictSemanticDiff,
		Detail: fmt.Sprintf("normalized outputs differ:\nA: %q\nB: %q", canonA, canonB)}
}

func runErrorDesc(o Outcome) string {
	if o.SpawnErr != "" {
		return "spawn error: " + o.SpawnErr
	}
	return fmt.Sprintf("exit code %d", o.ExitCode)
}
