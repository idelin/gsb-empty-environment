package netsec

import "context"

// Check is a cancellable rule probe. Evaluate must return promptly when ctx is
// canceled. A successful result must contain a valid verdict and a non-zero
// evidence digest derived from the observations that produced the verdict.
type Check interface {
	Evaluate(ctx context.Context) (CheckResult, error)
}

// CheckFunc adapts a function to Check.
type CheckFunc func(ctx context.Context) (CheckResult, error)

// Evaluate calls f(ctx).
func (f CheckFunc) Evaluate(ctx context.Context) (CheckResult, error) {
	return f(ctx)
}

// Rule binds a stable rule identity and version to a check implementation.
//
// Version is part of the policy digest and of every trace step for this rule.
// It must be changed whenever the implementation, its inputs, or its evidence
// format changes.
type Rule struct {
	ID      string
	Version string
	Check   Check
}
