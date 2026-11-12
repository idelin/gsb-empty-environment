package netsec

// Decision is the final verdict for a request.
type Decision int

const (
	// DecisionAllow permits the request.
	DecisionAllow Decision = iota
	// DecisionDeny rejects the request.
	DecisionDeny
)

func (d Decision) String() string {
	if d == DecisionDeny {
		return "DENY"
	}
	return "ALLOW"
}

// Result carries the verdict, the reason and all warnings collected while
// evaluating the request.
type Result struct {
	Decision Decision
	// Reason explains which action, policy and rule (if any) produced the
	// verdict, or why a default decision was taken.
	Reason   string
	Warnings []Warning
}

// Allowed is a convenience accessor.
func (r Result) Allowed() bool { return r.Decision == DecisionAllow }

// Evaluator holds the policies bound to each workload and the external
// authorizers used by CUSTOM policies. It is safe for concurrent use once
// registration has finished; registration and evaluation are not expected to
// overlap.
type Evaluator struct {
	policies    map[string][]Policy
	authorizers map[string]CustomAuthorizer
}

// NewEvaluator creates an empty Evaluator.
func NewEvaluator() *Evaluator {
	return &Evaluator{
		policies:    make(map[string][]Policy),
		authorizers: make(map[string]CustomAuthorizer),
	}
}

// RegisterAuthorizer makes an external authorizer available under a name. The
// same name is referenced by CUSTOM policies or their rules via Provider.
func (e *Evaluator) RegisterAuthorizer(name string, authorizer CustomAuthorizer) {
	e.authorizers[name] = authorizer
}

// AddPolicy binds a policy to a workload. Evaluation order within one action
// follows the order in which policies are added, and rule order within a
// policy is preserved.
func (e *Evaluator) AddPolicy(workload string, policy Policy) {
	e.policies[workload] = append(e.policies[workload], policy)
}

// Evaluate applies every policy bound to req.Workload in the fixed order
// CUSTOM, DENY, ALLOW.
func (e *Evaluator) Evaluate(req Request) Result {
	policies := e.policies[req.Workload]

	// A workload without any policy allows all traffic, including plaintext.
	if len(policies) == 0 {
		return Result{Decision: DecisionAllow, Reason: "no policies bound to workload; default allow"}
	}

	var warnings []Warning

	// 1. CUSTOM: a matched rule delegates to the named external authorizer.
	for _, policy := range policies {
		if policy.Action != ActionCustom {
			continue
		}
		for _, rule := range policy.Rules {
			matched, w := rule.Matches(req, policy.Name)
			warnings = append(warnings, w...)
			if !matched {
				continue
			}
			provider := rule.Provider
			if provider == "" {
				provider = policy.Provider
			}
			authorizer, ok := e.authorizers[provider]
			if !ok {
				// Fail closed: an unregistered provider cannot be trusted.
				warnings = append(warnings, Warning{
					Policy:  policy.Name,
					Rule:    rule.Name,
					Field:   "customProvider",
					Kind:    "custom",
					Message: "no external authorizer registered for provider \"" + provider + "\"; denying",
				})
				return Result{Decision: DecisionDeny, Reason: "CUSTOM policy \"" + policy.Name + "\" matched but provider \"" + provider + "\" is not registered", Warnings: warnings}
			}
			if !authorizer(req, rule) {
				return Result{Decision: DecisionDeny, Reason: "CUSTOM policy \"" + policy.Name + "\", rule \"" + rule.Name + "\" denied by provider \"" + provider + "\"", Warnings: warnings}
			}
		}
	}

	// 2. DENY: the first matching rule rejects immediately.
	for _, policy := range policies {
		if policy.Action != ActionDeny {
			continue
		}
		for _, rule := range policy.Rules {
			matched, w := rule.Matches(req, policy.Name)
			warnings = append(warnings, w...)
			if matched {
				return Result{Decision: DecisionDeny, Reason: "DENY policy \"" + policy.Name + "\", rule \"" + rule.Name + "\" matched", Warnings: warnings}
			}
		}
	}

	// 3. ALLOW: only policies of action ALLOW participate. The request is
	// allowed only when at least one allow rule matches; having no ALLOW
	// policy, no rules, or no matching rule all default to deny.
	allowPolicies := 0
	for _, policy := range policies {
		if policy.Action != ActionAllow {
			continue
		}
		allowPolicies++
		// An empty rule set denies everything, including plaintext.
		if len(policy.Rules) == 0 {
			continue
		}
		for _, rule := range policy.Rules {
			matched, w := rule.Matches(req, policy.Name)
			warnings = append(warnings, w...)
			if matched {
				return Result{Decision: DecisionAllow, Reason: "ALLOW policy \"" + policy.Name + "\", rule \"" + rule.Name + "\" matched", Warnings: warnings}
			}
		}
	}

	if allowPolicies == 0 {
		return Result{Decision: DecisionDeny, Reason: "no ALLOW policy matched the request; default deny", Warnings: warnings}
	}
	return Result{Decision: DecisionDeny, Reason: "ALLOW policy present but no rule matched; default deny", Warnings: warnings}
}
