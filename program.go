package netsec

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Program is an immutable, compiled policy. It may be evaluated concurrently;
// concurrency safety of the host-supplied checks remains the host's
// responsibility.
type Program struct {
	version string
	root    *Expr
	rules   map[string]Rule
	digest  Digest
}

// NewProgram validates and compiles a policy expression.
//
// version identifies the policy/rule-set revision. Every rule must have a
// non-empty ID and version, a non-nil check, and must be referenced by the
// expression. Extra registered rules are allowed; they remain part of the
// program digest so a host can bind the full rule set.
func NewProgram(version string, root *Expr, rules []Rule) (*Program, error) {
	if version == "" {
		return nil, errors.New("netsec: program version must not be empty")
	}
	if root == nil {
		return nil, errors.New("netsec: policy expression must not be nil")
	}

	byID := make(map[string]Rule, len(rules))
	for i, rule := range rules {
		if rule.ID == "" {
			return nil, fmt.Errorf("netsec: rule %d has an empty id", i)
		}
		if rule.Version == "" {
			return nil, fmt.Errorf("netsec: rule %q has an empty version", rule.ID)
		}
		if rule.Check == nil {
			return nil, fmt.Errorf("netsec: rule %q has a nil check", rule.ID)
		}
		if _, exists := byID[rule.ID]; exists {
			return nil, fmt.Errorf("netsec: duplicate rule id %q", rule.ID)
		}
		byID[rule.ID] = rule
	}

	if err := validateExpr(root, byID); err != nil {
		return nil, fmt.Errorf("netsec: invalid policy: %w", err)
	}

	return &Program{
		version: version,
		root:    root,
		rules:   byID,
		digest:  computeProgramDigest(version, root, byID),
	}, nil
}

// MustNewProgram is like NewProgram but panics on invalid input. It is intended
// for package-level policy definitions.
func MustNewProgram(version string, root *Expr, rules []Rule) *Program {
	p, err := NewProgram(version, root, rules)
	if err != nil {
		panic(err)
	}
	return p
}

// Version returns the policy/rule-set revision.
func (p *Program) Version() string {
	if p == nil {
		return ""
	}
	return p.version
}

// Digest returns the canonical digest binding the policy version, expression,
// and all rule versions.
func (p *Program) Digest() Digest {
	if p == nil {
		return Digest{}
	}
	return p.digest
}

// Root returns the root expression.
func (p *Program) Root() *Expr {
	if p == nil {
		return nil
	}
	return p.root
}

// Rule returns a registered rule.
func (p *Program) Rule(id string) (Rule, bool) {
	if p == nil {
		return Rule{}, false
	}
	rule, ok := p.rules[id]
	return rule, ok
}

// Evaluate executes the policy with short-circuit semantics.
//
// The returned trace is always sealed, including when a check fails, panics,
// or evaluation is canceled. On a completed evaluation the error is nil and
// trace.Verdict is allow or deny. On an abort trace.Verdict is invalid and the
// error identifies the cause.
func (p *Program) Evaluate(ctx context.Context) (Trace, error) {
	if p == nil || p.root == nil || p.rules == nil {
		return Trace{}, errors.New("netsec: uninitialized program")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	e := &evaluator{program: p, ctx: ctx}
	verdict := e.eval(p.root, rootSite)

	trace := Trace{
		ProgramVersion: p.version,
		ProgramDigest:  p.digest,
		Steps:          e.steps,
	}

	if e.abort != nil {
		trace.Verdict = VerdictInvalid
		trace.Status = TraceCheckFailed
		if len(e.steps) > 0 {
			trace.Status = traceStatusFor(e.steps[len(e.steps)-1].Status)
			if trace.Status == TraceInvalid {
				trace.Status = TraceCheckFailed
			}
		}
	} else {
		trace.Status = TraceCompleted
		trace.Verdict = verdict
	}

	trace.Seal = sealTrace(p.digest, trace)
	return trace, e.abort
}

type evaluator struct {
	program *Program
	ctx     context.Context
	steps   []Step
	abort   error
}

func (e *evaluator) eval(expr *Expr, site string) Verdict {
	if e.abort != nil {
		return VerdictInvalid
	}

	switch expr.kind {
	case KindRule:
		return e.evalRule(expr, site)

	case KindAnd:
		for i, child := range expr.children {
			verdict := e.eval(child, childSite(site, KindAnd, i))
			if e.abort != nil {
				return VerdictInvalid
			}
			if verdict == VerdictDeny {
				return VerdictDeny
			}
		}
		return VerdictAllow

	case KindOr:
		for i, child := range expr.children {
			verdict := e.eval(child, childSite(site, KindOr, i))
			if e.abort != nil {
				return VerdictInvalid
			}
			if verdict == VerdictAllow {
				return VerdictAllow
			}
		}
		return VerdictDeny

	case KindNot:
		verdict := e.eval(expr.children[0], childSite(site, KindNot, 0))
		if e.abort != nil {
			return VerdictInvalid
		}
		if verdict == VerdictAllow {
			return VerdictDeny
		}
		return VerdictAllow

	default:
		e.abort = fmt.Errorf("netsec: invalid expression node at %s", site)
		return VerdictInvalid
	}
}

func (e *evaluator) evalRule(expr *Expr, site string) Verdict {
	rule, ok := e.program.rules[expr.ruleID]
	if !ok || rule.Check == nil {
		e.abort = fmt.Errorf("netsec: rule %q at %s is not executable", expr.ruleID, site)
		return VerdictInvalid
	}

	if err := e.ctx.Err(); err != nil {
		e.record(Step{
			Node:        site,
			RuleID:      rule.ID,
			RuleVersion: rule.Version,
			Status:      StepCanceled,
			Detail:      err.Error(),
		})
		e.abort = &CanceledError{
			RuleID:           rule.ID,
			Node:             site,
			BeforeInvocation: true,
			Err:              err,
		}
		return VerdictInvalid
	}

	result, err, panicValue := invokeCheck(e.ctx, rule.Check)
	if panicValue != nil {
		detail := safeSprint(panicValue)
		e.record(Step{
			Node:        site,
			RuleID:      rule.ID,
			RuleVersion: rule.Version,
			Status:      StepPanicked,
			Detail:      detail,
		})
		e.abort = &PanicError{RuleID: rule.ID, Node: site, Value: panicValue}
		return VerdictInvalid
	}

	if err != nil {
		cause := err
		status := StepCheckError
		if ctxErr := e.ctx.Err(); ctxErr != nil {
			cause = ctxErr
			status = StepCheckCanceled
		} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = StepCheckCanceled
		}

		e.record(Step{
			Node:        site,
			RuleID:      rule.ID,
			RuleVersion: rule.Version,
			Status:      status,
			Detail:      cause.Error(),
		})
		if status == StepCheckCanceled {
			e.abort = &CanceledError{RuleID: rule.ID, Node: site, Err: cause}
		} else {
			e.abort = &CheckError{RuleID: rule.ID, Node: site, Err: err}
		}
		return VerdictInvalid
	}

	var reasons []string
	if !result.Verdict.Valid() {
		reasons = append(reasons, "verdict is not allow or deny")
	}
	if result.Evidence.Digest.IsZero() {
		reasons = append(reasons, "evidence digest is zero")
	}
	if len(reasons) > 0 {
		reason := strings.Join(reasons, "; ")
		e.record(Step{
			Node:        site,
			RuleID:      rule.ID,
			RuleVersion: rule.Version,
			Status:      StepCheckError,
			Detail:      reason,
		})
		e.abort = &InvalidResultError{RuleID: rule.ID, Node: site, Reason: reason}
		return VerdictInvalid
	}

	e.record(Step{
		Node:        site,
		RuleID:      rule.ID,
		RuleVersion: rule.Version,
		Status:      StepOK,
		Verdict:     result.Verdict,
		Evidence:    result.Evidence,
	})
	return result.Verdict
}

func (e *evaluator) record(step Step) {
	step.Seq = len(e.steps)
	e.steps = append(e.steps, step)
}

func invokeCheck(ctx context.Context, check Check) (result CheckResult, err error, panicValue any) {
	defer func() {
		if recovered := recover(); recovered != nil {
			panicValue = recovered
		}
	}()
	result, err = check.Evaluate(ctx)
	return result, err, nil
}
