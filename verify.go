package netsec

import (
	"context"
	"errors"
	"fmt"
)

// Verify independently replays trace against p. It does not call the executor
// or trust trace completeness flags.
//
// A faithful aborted trace (cancellation, check error, or panic) is accepted as
// an aborted trace: the verifier confirms the exact stop point and rejects any
// step after it. Use VerifyAllow when the audit question is specifically
// whether the trace proves an allow decision.
func (p *Program) Verify(trace Trace) error {
	if p == nil || p.root == nil || p.rules == nil {
		return verifyError(VerifyProgramMismatch, -1, "", "program is nil or uninitialized")
	}
	if trace.ProgramVersion != p.version {
		return verifyError(VerifyProgramMismatch, -1, "",
			fmt.Sprintf("trace policy version %q does not match program version %q", trace.ProgramVersion, p.version))
	}
	if trace.ProgramDigest != p.digest {
		return verifyError(VerifyProgramMismatch, -1, "",
			"trace program digest does not match the supplied policy, expression, and rule versions")
	}

	expectedSeal := sealTrace(p.digest, trace)
	if trace.Seal != expectedSeal {
		return verifyError(VerifySealMismatch, -1, "",
			fmt.Sprintf("trace seal is %s, expected %s", trace.Seal, expectedSeal))
	}

	replay := replayer{program: p, steps: trace.Steps}
	verdict, terminal := replay.eval(p.root, rootSite)
	if replay.err != nil {
		return replay.err
	}

	if terminal != StepInvalid {
		if replay.index != len(trace.Steps) {
			step := trace.Steps[replay.index]
			return verifyError(VerifyExtraStep, replay.index, step.Node,
				"trace contains a step after the recorded abort point; the branch was never executed")
		}
		if trace.Verdict != VerdictInvalid {
			return verifyError(VerifyVerdictMismatch, replay.index-1, terminalNode(trace.Steps),
				fmt.Sprintf("aborted trace must have an invalid verdict, got %s", trace.Verdict))
		}
		wantStatus := traceStatusFor(terminal)
		if trace.Status != wantStatus {
			return verifyError(VerifyStatusMismatch, replay.index-1, terminalNode(trace.Steps),
				fmt.Sprintf("trace status is %s, expected %s", trace.Status, wantStatus))
		}
		return nil
	}

	if replay.index != len(trace.Steps) {
		step := trace.Steps[replay.index]
		return verifyError(VerifyExtraStep, replay.index, step.Node,
			"trace contains a step for a branch that short-circuit evaluation did not execute")
	}
	if trace.Status != TraceCompleted {
		return verifyError(VerifyStatusMismatch, len(trace.Steps)-1, terminalNode(trace.Steps),
			fmt.Sprintf("trace status is %s, expected %s", trace.Status, TraceCompleted))
	}
	if trace.Verdict != verdict {
		return verifyError(VerifyVerdictMismatch, len(trace.Steps)-1, terminalNode(trace.Steps),
			fmt.Sprintf("trace verdict is %s, replayed verdict is %s", trace.Verdict, verdict))
	}
	return nil
}

// VerifyAllow verifies the trace and additionally requires a completed allow
// decision. It does not perform live re-execution; use VerifyLive as well when
// check outputs must be independently reproduced.
func (p *Program) VerifyAllow(trace Trace) error {
	if err := p.Verify(trace); err != nil {
		return err
	}
	if trace.Status != TraceCompleted {
		return verifyError(VerifyNotCompleted, -1, "", fmt.Sprintf("trace ended in %s", trace.Status))
	}
	if trace.Verdict != VerdictAllow {
		return verifyError(VerifyNotAllow, -1, "", fmt.Sprintf("trace verdict is %s", trace.Verdict))
	}
	return nil
}

// VerifyLive verifies trace structurally, then re-executes the policy with ctx
// and requires every step, verdict, evidence digest, and evidence summary to be
// reproduced exactly.
//
// This detects a trace whose internally consistent steps were fabricated from
// a desired final value. It is only meaningful for deterministic checks and
// completed traces; it re-invokes checks and therefore must not be used when a
// check has irreversible side effects.
func (p *Program) VerifyLive(ctx context.Context, trace Trace) error {
	if err := p.Verify(trace); err != nil {
		return err
	}
	if trace.Status != TraceCompleted {
		return verifyError(VerifyNotCompleted, -1, "",
			fmt.Sprintf("live verification requires a completed trace, got %s", trace.Status))
	}

	actual, err := p.Evaluate(ctx)
	if err != nil {
		return verifyError(VerifyLiveFailed, -1, "", fmt.Sprintf("re-execution failed: %v", err))
	}
	if actual.Status != TraceCompleted {
		return verifyError(VerifyLiveFailed, -1, "",
			fmt.Sprintf("re-execution ended in %s", actual.Status))
	}
	if actual.ProgramDigest != trace.ProgramDigest || actual.ProgramVersion != trace.ProgramVersion {
		return verifyError(VerifyLiveMismatch, -1, "", "re-execution policy binding differs")
	}
	if actual.Verdict != trace.Verdict {
		return verifyError(VerifyLiveMismatch, -1, "",
			fmt.Sprintf("recorded verdict %s, re-executed verdict %s", trace.Verdict, actual.Verdict))
	}
	if len(actual.Steps) != len(trace.Steps) {
		return verifyError(VerifyLiveMismatch, -1, "",
			fmt.Sprintf("recorded %d steps, re-execution produced %d", len(trace.Steps), len(actual.Steps)))
	}

	for i := range trace.Steps {
		want := actual.Steps[i]
		got := trace.Steps[i]
		if got.Seq != want.Seq ||
			got.Node != want.Node ||
			got.RuleID != want.RuleID ||
			got.RuleVersion != want.RuleVersion ||
			got.Status != want.Status ||
			got.Verdict != want.Verdict ||
			got.Evidence != want.Evidence ||
			got.Detail != want.Detail {
			return verifyError(VerifyLiveMismatch, i, got.Node,
				fmt.Sprintf("recorded %s, re-executed %s", formatStep(got), formatStep(want)))
		}
	}
	if actual.Seal != trace.Seal {
		return verifyError(VerifySealMismatch, -1, "", "re-execution seal differs")
	}
	return nil
}

type replayer struct {
	program *Program
	steps   []Step
	index   int
	err     error
}

func (r *replayer) eval(expr *Expr, site string) (Verdict, StepStatus) {
	if r.err != nil {
		return VerdictInvalid, StepInvalid
	}

	switch expr.kind {
	case KindRule:
		return r.evalRule(expr, site)

	case KindAnd:
		for i, child := range expr.children {
			verdict, terminal := r.eval(child, childSite(site, KindAnd, i))
			if r.err != nil || terminal != StepInvalid {
				return VerdictInvalid, terminal
			}
			if verdict == VerdictDeny {
				return VerdictDeny, StepInvalid
			}
		}
		return VerdictAllow, StepInvalid

	case KindOr:
		for i, child := range expr.children {
			verdict, terminal := r.eval(child, childSite(site, KindOr, i))
			if r.err != nil || terminal != StepInvalid {
				return VerdictInvalid, terminal
			}
			if verdict == VerdictAllow {
				return VerdictAllow, StepInvalid
			}
		}
		return VerdictDeny, StepInvalid

	case KindNot:
		verdict, terminal := r.eval(expr.children[0], childSite(site, KindNot, 0))
		if r.err != nil || terminal != StepInvalid {
			return VerdictInvalid, terminal
		}
		if verdict == VerdictAllow {
			return VerdictDeny, StepInvalid
		}
		return VerdictAllow, StepInvalid

	default:
		r.err = verifyError(VerifyStepMismatch, -1, site, "invalid expression node in policy")
		return VerdictInvalid, StepInvalid
	}
}

func (r *replayer) evalRule(expr *Expr, site string) (Verdict, StepStatus) {
	rule, ok := r.program.rules[expr.ruleID]
	if !ok {
		r.err = verifyError(VerifyStepMismatch, -1, site,
			fmt.Sprintf("policy references unregistered rule %q", expr.ruleID))
		return VerdictInvalid, StepInvalid
	}

	if r.index >= len(r.steps) {
		r.err = verifyError(VerifyMissingStep, r.index, site,
			fmt.Sprintf("trace ended before rule %q@%s was recorded", rule.ID, rule.Version))
		return VerdictInvalid, StepInvalid
	}

	step := r.steps[r.index]
	seq := r.index
	r.index++

	if step.Seq != seq {
		r.err = verifyError(VerifyStepMismatch, seq, site,
			fmt.Sprintf("step sequence is %d, expected %d", step.Seq, seq))
		return VerdictInvalid, StepInvalid
	}
	if step.Node != site {
		r.err = verifyError(VerifyStepMismatch, seq, site,
			fmt.Sprintf("step node is %q, expected %q", step.Node, site))
		return VerdictInvalid, StepInvalid
	}
	if step.RuleID != rule.ID {
		r.err = verifyError(VerifyStepMismatch, seq, site,
			fmt.Sprintf("step rule is %q, expected %q", step.RuleID, rule.ID))
		return VerdictInvalid, StepInvalid
	}
	if step.RuleVersion != rule.Version {
		r.err = verifyError(VerifyStepMismatch, seq, site,
			fmt.Sprintf("step rule version is %q, expected %q", step.RuleVersion, rule.Version))
		return VerdictInvalid, StepInvalid
	}

	switch step.Status {
	case StepOK:
		if !step.Verdict.Valid() {
			r.err = verifyError(VerifyStepState, seq, site,
				fmt.Sprintf("ok step has invalid verdict %s", step.Verdict))
			return VerdictInvalid, StepInvalid
		}
		if step.Evidence.Digest.IsZero() {
			r.err = verifyError(VerifyStepState, seq, site, "ok step has a zero evidence digest")
			return VerdictInvalid, StepInvalid
		}
		return step.Verdict, StepInvalid

	case StepCanceled, StepCheckCanceled, StepCheckError, StepPanicked:
		if step.Verdict != VerdictInvalid {
			r.err = verifyError(VerifyStepState, seq, site,
				fmt.Sprintf("%s step must not claim a verdict", step.Status))
			return VerdictInvalid, StepInvalid
		}
		if step.Evidence != (Evidence{}) {
			r.err = verifyError(VerifyStepState, seq, site,
				fmt.Sprintf("%s step must not claim evidence", step.Status))
			return VerdictInvalid, StepInvalid
		}
		return VerdictInvalid, step.Status

	default:
		r.err = verifyError(VerifyStepState, seq, site,
			fmt.Sprintf("unknown step status %d", step.Status))
		return VerdictInvalid, StepInvalid
	}
}

func terminalNode(steps []Step) string {
	if len(steps) == 0 {
		return ""
	}
	return steps[len(steps)-1].Node
}

// IsVerificationError reports whether err is a trace verification failure.
func IsVerificationError(err error) bool {
	return errors.Is(err, ErrVerification)
}
