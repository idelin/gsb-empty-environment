package netsec

import "testing"

const (
	spiffeA = "spiffe://cluster.local/ns/team/sa/service-a"
	spiffeB = "spiffe://cluster.local/ns/team/sa/service-b"
	jwtA    = "https://issuer.example.com/subject-a"
	jwtB    = "https://issuer.example.com/subject-b"
)

func TestNoPolicyAllows(t *testing.T) {
	evaluator := NewEvaluator()

	result := evaluator.Evaluate(Request{Workload: "orders"})

	if !result.Allowed() {
		t.Fatalf("plaintext request to workload without policies = %s, want ALLOW", result.Decision)
	}
}

func TestNotPrincipalsPlaintextDoesNotBypass(t *testing.T) {
	// Policy intent: allow everyone except service-b. A naive "empty identity
	// satisfies notPrincipals" implementation would let plaintext traffic in.
	evaluator := NewEvaluator()
	evaluator.AddPolicy("orders", Policy{
		Name: "allow-except-b",
		Rules: []Rule{{
			Name:    "not-b",
			Sources: []Source{{NotPrincipals: []StringMatch{Exact(spiffeB)}}},
		}},
	})

	// 1. Plaintext request: no peer identity, notPrincipals must not match.
	plaintext := evaluator.Evaluate(Request{Workload: "orders"})
	if plaintext.Allowed() {
		t.Fatalf("plaintext request = ALLOW, want DENY (notPrincipals must not match an empty identity)")
	}
	if len(plaintext.Warnings) == 0 {
		t.Fatalf("plaintext request produced no warning, want a notPrincipals warning")
	}
	warning := plaintext.Warnings[0]
	if warning.Field != "principals" || warning.Kind != "peer" || warning.Policy != "allow-except-b" {
		t.Fatalf("warning = %+v, want a peer principals warning from allow-except-b", warning)
	}

	// 2. mTLS request from the excluded service is denied without a warning.
	fromB := evaluator.Evaluate(Request{Workload: "orders", PeerPrincipals: []string{spiffeB}})
	if fromB.Allowed() {
		t.Fatalf("request from service-b = ALLOW, want DENY")
	}
	if len(fromB.Warnings) != 0 {
		t.Fatalf("request from service-b warnings = %v, want none", fromB.Warnings)
	}

	// 3. mTLS request from another service is allowed.
	fromA := evaluator.Evaluate(Request{Workload: "orders", PeerPrincipals: []string{spiffeA}})
	if !fromA.Allowed() {
		t.Fatalf("request from service-a = %s (%s), want ALLOW", fromA.Decision, fromA.Reason)
	}
}

func TestPrincipalsAndRequestPrincipalsAreNotInterchangeable(t *testing.T) {
	evaluator := NewEvaluator()
	// Only mTLS SPIFFE identities may satisfy principals.
	evaluator.AddPolicy("orders", Policy{
		Name:  "mtls-only",
		Rules: []Rule{{Name: "spiffe-a", Sources: []Source{{Principals: []StringMatch{Exact(spiffeA)}}}}},
	})
	// Only JWT iss/sub identities may satisfy requestPrincipals.
	evaluator.AddPolicy("payments", Policy{
		Name:  "jwt-only",
		Rules: []Rule{{Name: "jwt-a", Sources: []Source{{RequestPrincipals: []StringMatch{Exact(jwtA)}}}}},
	})

	mtlsIdentity := []string{spiffeA}
	jwtIdentity := []string{jwtA}

	tests := []struct {
		name string
		req  Request
		want Decision
	}{
		{"cert accepted by principals", Request{Workload: "orders", PeerPrincipals: mtlsIdentity}, DecisionAllow},
		{"JWT rejected by principals", Request{Workload: "orders", RequestPrincipals: jwtIdentity}, DecisionDeny},
		{"JWT accepted by requestPrincipals", Request{Workload: "payments", RequestPrincipals: jwtIdentity}, DecisionAllow},
		{"cert rejected by requestPrincipals", Request{Workload: "payments", PeerPrincipals: mtlsIdentity}, DecisionDeny},
		{"plaintext rejected by both", Request{Workload: "orders"}, DecisionDeny},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := evaluator.Evaluate(tt.req).Decision; got != tt.want {
				t.Fatalf("Evaluate() = %s, want %s", got, tt.want)
			}
		})
	}

	if got := JWTPrincipal("https://issuer.example.com", "subject-a"); got != jwtA {
		t.Fatalf("JWTPrincipal() = %q, want %q", got, jwtA)
	}
}

func TestMixedJWTAndCertificateSources(t *testing.T) {
	evaluator := NewEvaluator()
	// A single source requires both identities simultaneously (AND semantics).
	evaluator.AddPolicy("orders", Policy{
		Name: "mtls-and-jwt",
		Rules: []Rule{{
			Name: "both",
			Sources: []Source{{
				Principals:        []StringMatch{Exact(spiffeA)},
				RequestPrincipals: []StringMatch{Exact(jwtA)},
			}},
		}},
	})

	both := evaluator.Evaluate(Request{
		Workload:          "orders",
		PeerPrincipals:    []string{spiffeA},
		RequestPrincipals: []string{jwtA},
	})
	if !both.Allowed() {
		t.Fatalf("cert + JWT = %s (%s), want ALLOW", both.Decision, both.Reason)
	}

	certOnly := evaluator.Evaluate(Request{Workload: "orders", PeerPrincipals: []string{spiffeA}})
	if certOnly.Allowed() {
		t.Fatalf("cert only = ALLOW, want DENY (missing JWT)")
	}

	jwtOnly := evaluator.Evaluate(Request{Workload: "orders", RequestPrincipals: []string{jwtA}})
	if jwtOnly.Allowed() {
		t.Fatalf("JWT only = ALLOW, want DENY (missing peer identity)")
	}
}

func TestNotRequestPrincipalsWithoutJWTwarnsAndDenies(t *testing.T) {
	evaluator := NewEvaluator()
	evaluator.AddPolicy("orders", Policy{
		Name: "jwt-not-b",
		Rules: []Rule{{
			Name:    "not-jwt-b",
			Sources: []Source{{NotRequestPrincipals: []StringMatch{Exact(jwtB)}}},
		}},
	})

	// No JWT at all: the negative match fails closed with a warning.
	noJWT := evaluator.Evaluate(Request{Workload: "orders", PeerPrincipals: []string{spiffeA}})
	if noJWT.Allowed() {
		t.Fatalf("request without JWT = ALLOW, want DENY")
	}
	if len(noJWT.Warnings) != 1 || noJWT.Warnings[0].Field != "requestPrincipals" || noJWT.Warnings[0].Kind != "request" {
		t.Fatalf("warnings = %+v, want one requestPrincipals warning", noJWT.Warnings)
	}

	// A different, verified JWT passes.
	withJWTA := evaluator.Evaluate(Request{
		Workload:          "orders",
		PeerPrincipals:    []string{spiffeA},
		RequestPrincipals: []string{jwtA},
	})
	if !withJWTA.Allowed() {
		t.Fatalf("request with jwtA = %s, want ALLOW", withJWTA.Decision)
	}
}
