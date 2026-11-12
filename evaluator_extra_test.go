package netsec

import "testing"

func TestEmptyAndUnmatchedAllowRules(t *testing.T) {
	// Empty rule set denies everything.
	emptyRules := NewEvaluator()
	emptyRules.AddPolicy("orders", Policy{Name: "empty"})
	if result := emptyRules.Evaluate(Request{Workload: "orders", PeerPrincipals: []string{spiffeA}}); result.Allowed() {
		t.Fatalf("empty ALLOW rule set = ALLOW, want DENY")
	}

	// Only ALLOW policies exist but nothing matches: deny.
	nonMatching := NewEvaluator()
	nonMatching.AddPolicy("orders", Policy{
		Name:  "specific",
		Rules: []Rule{{Name: "b-only", Sources: []Source{{Principals: []StringMatch{Exact(spiffeB)}}}}},
	})
	result := nonMatching.Evaluate(Request{Workload: "orders", PeerPrincipals: []string{spiffeA}})
	if result.Allowed() {
		t.Fatalf("unmatched ALLOW policy = ALLOW, want DENY")
	}

	// An empty include list is an explicit match-nothing constraint.
	emptyInclude := NewEvaluator()
	emptyInclude.AddPolicy("orders", Policy{
		Name:  "empty-include",
		Rules: []Rule{{Name: "nobody", Sources: []Source{{Principals: []StringMatch{}}}}},
	})
	if result := emptyInclude.Evaluate(Request{Workload: "orders", PeerPrincipals: []string{spiffeA}}); result.Allowed() {
		t.Fatalf("empty principals include list = ALLOW, want DENY")
	}
}

func TestDenyPolicyOverridesAllow(t *testing.T) {
	evaluator := NewEvaluator()
	evaluator.AddPolicy("orders", Policy{
		Name:  "allow-all",
		Rules: []Rule{{Name: "any", Sources: []Source{{}}}},
	})
	evaluator.AddPolicy("orders", Policy{
		Name:   "deny-b",
		Action: ActionDeny,
		Rules:  []Rule{{Name: "b", Sources: []Source{{Principals: []StringMatch{Exact(spiffeB)}}}}},
	})

	if result := evaluator.Evaluate(Request{Workload: "orders", PeerPrincipals: []string{spiffeA}}); !result.Allowed() {
		t.Fatalf("service-a = %s, want ALLOW", result.Decision)
	}
	result := evaluator.Evaluate(Request{Workload: "orders", PeerPrincipals: []string{spiffeB}})
	if result.Allowed() {
		t.Fatalf("service-b = ALLOW, want DENY via DENY policy")
	}
}

func TestCustomActionAndOrdering(t *testing.T) {
	evaluator := NewEvaluator()
	evaluator.RegisterAuthorizer("ext-authz", func(req Request, rule Rule) bool {
		for _, principal := range req.PeerPrincipals {
			if principal == spiffeA {
				return true
			}
		}
		return false
	})
	evaluator.AddPolicy("orders", Policy{
		Name:     "external",
		Action:   ActionCustom,
		Provider: "ext-authz",
		Rules:    []Rule{{Name: "delegate", Sources: []Source{{NotPrincipals: []StringMatch{Exact(spiffeB)}}}}},
	})
	// CUSTOM runs before this broad ALLOW policy, so its deny is decisive.
	evaluator.AddPolicy("orders", Policy{
		Name:  "allow-all",
		Rules: []Rule{{Name: "any"}},
	})

	if result := evaluator.Evaluate(Request{Workload: "orders", PeerPrincipals: []string{spiffeA}}); !result.Allowed() {
		t.Fatalf("service-a = %s (%s), want ALLOW from external authorizer", result.Decision, result.Reason)
	}

	plaintext := evaluator.Evaluate(Request{Workload: "orders"})
	// The CUSTOM rule must not match the empty identity; the broad ALLOW
	// policy below is what reaches the verdict, but the bypass warning stays
	// attached to the result so the failed negative match is observable.
	if len(plaintext.Warnings) == 0 {
		t.Fatalf("plaintext = no warnings, want a notPrincipals warning")
	}
	if plaintext.Warnings[0].Policy != "external" {
		t.Fatalf("warning policy = %q, want external", plaintext.Warnings[0].Policy)
	}

	// Replace the broad ALLOW with an identity-bound one and the plaintext
	// request is denied outright.
	evaluator.AddPolicy("locked", Policy{
		Name:  "mtls-allow",
		Rules: []Rule{{Name: "a", Sources: []Source{{Principals: []StringMatch{Exact(spiffeA)}}}}},
	})
	lockedPlaintext := evaluator.Evaluate(Request{Workload: "locked"})
	if lockedPlaintext.Allowed() {
		t.Fatalf("plaintext against identity-bound ALLOW = ALLOW, want DENY")
	}

	// Unregistered provider fails closed instead of panicking or allowing.
	missingProvider := NewEvaluator()
	missingProvider.AddPolicy("orders", Policy{
		Name:     "orphan",
		Action:   ActionCustom,
		Provider: "not-registered",
		Rules:    []Rule{{Name: "delegate"}},
	})
	result := missingProvider.Evaluate(Request{Workload: "orders", PeerPrincipals: []string{spiffeA}})
	if result.Allowed() {
		t.Fatalf("unregistered provider = ALLOW, want fail-closed DENY")
	}
}

func TestMultiplePoliciesStacking(t *testing.T) {
	evaluator := NewEvaluator()
	// DENY wins over every ALLOW policy; ALLOW policies together form one
	// union of acceptable identities.
	evaluator.AddPolicy("orders", Policy{
		Name:   "block-b",
		Action: ActionDeny,
		Rules:  []Rule{{Name: "b", Sources: []Source{{Principals: []StringMatch{Exact(spiffeB)}}}}},
	})
	evaluator.AddPolicy("orders", Policy{
		Name:  "allow-a",
		Rules: []Rule{{Name: "a", Sources: []Source{{Principals: []StringMatch{Prefix("spiffe://cluster.local/ns/team/sa/service-a")}}}}},
	})
	evaluator.AddPolicy("orders", Policy{
		Name:  "allow-jwt",
		Rules: []Rule{{Name: "jwt", Sources: []Source{{RequestPrincipals: []StringMatch{Suffix("/subject-a")}}}}},
	})

	tests := []struct {
		name string
		req  Request
		want Decision
	}{
		{"service-a allowed by first ALLOW", Request{Workload: "orders", PeerPrincipals: []string{spiffeA}}, DecisionAllow},
		{"service-b denied despite allow policies", Request{Workload: "orders", PeerPrincipals: []string{spiffeB}}, DecisionDeny},
		{"JWT subject allowed by second ALLOW", Request{Workload: "orders", RequestPrincipals: []string{jwtA}}, DecisionAllow},
		{"plaintext denied by default", Request{Workload: "orders"}, DecisionDeny},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evaluator.Evaluate(tt.req).Decision; got != tt.want {
				t.Fatalf("Evaluate() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestStringMatchModes(t *testing.T) {
	tests := []struct {
		name    string
		matcher StringMatch
		value   string
		want    bool
	}{
		{"exact hit", Exact("spiffe://x/sa"), "spiffe://x/sa", true},
		{"exact miss", Exact("spiffe://x/sa"), "spiffe://x/sa-other", false},
		{"prefix hit", Prefix("spiffe://"), "spiffe://cluster/sa", true},
		{"prefix miss", Prefix("spiffe://"), "http://cluster/sa", false},
		{"suffix hit", Suffix("/sa"), "spiffe://cluster/sa", true},
		{"contains hit", Contains("cluster"), "spiffe://cluster/sa", true},
		{"presence hit", Presence(), "spiffe://cluster/sa", true},
		{"presence miss on empty", Presence(), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.matcher.Matches(tt.value); got != tt.want {
				t.Fatalf("%q.Matches(%q) = %v, want %v", tt.matcher, tt.value, got, tt.want)
			}
		})
	}
}

func TestWarningsSurviveAnAllow(t *testing.T) {
	// A warning from one source/policy must still be reported when another
	// rule later allows the request.
	evaluator := NewEvaluator()
	evaluator.AddPolicy("orders", Policy{
		Name: "warn-then-deny",
		Rules: []Rule{{
			Name:    "not-b",
			Sources: []Source{{NotPrincipals: []StringMatch{Exact(spiffeB)}}},
		}},
	})
	evaluator.AddPolicy("orders", Policy{
		Name:  "allow-a",
		Rules: []Rule{{Name: "a", Sources: []Source{{Principals: []StringMatch{Exact(spiffeA)}}}}},
	})

	// Plaintext: first rule warns and fails; no allow rule matches either.
	plaintext := evaluator.Evaluate(Request{Workload: "orders"})
	if plaintext.Allowed() {
		t.Fatalf("plaintext = ALLOW, want DENY")
	}
	if len(plaintext.Warnings) != 1 {
		t.Fatalf("plaintext warnings = %d, want 1", len(plaintext.Warnings))
	}

	// Authenticated service-a is allowed, and no warning is needed.
	fromA := evaluator.Evaluate(Request{Workload: "orders", PeerPrincipals: []string{spiffeA}})
	if !fromA.Allowed() {
		t.Fatalf("service-a = %s, want ALLOW", fromA.Decision)
	}
}
