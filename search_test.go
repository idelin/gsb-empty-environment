package netsec

import (
	"strings"
	"testing"
)

// The reviewed implementation must allow request A's payload to be called
// under a foreign identity; the search finds the shortest such trace.
func TestSearchFindsShortestExploitOnVulnerable(t *testing.T) {
	trace, found := SearchShortestViolation(false, 10)
	if !found {
		t.Fatal("no exploit trace found on reviewed implementation")
	}
	want := []string{"A.connect", "A.write", "svc.read", "A.disconnect", "svc.impersonate", "svc.call"}
	if strings.Join(trace, ",") != strings.Join(want, ",") {
		t.Fatalf("trace = %v, want shortest %v", trace, want)
	}
	w := replay(false, trace)
	if !w.violation() {
		t.Fatal("replayed trace does not violate")
	}
	if w.lastCall.Identity != HostIdentity {
		t.Fatalf("identity = %+v, want host process identity", w.lastCall.Identity)
	}
	if string(w.lastCall.Payload) != string(PayloadA) {
		t.Fatalf("payload = %q, want %q", w.lastCall.Payload, PayloadA)
	}
}

// A longer variant: client B reconnects on the reused instance (with A's
// reused PID) and A's request runs under B's token.
func TestExploitViaInstanceReuseByClientB(t *testing.T) {
	trace := []string{
		"A.connect", "A.write", "svc.read",
		"A.disconnect", "svc.listen", "B.connect",
		"svc.impersonate", "svc.call",
	}
	w := replay(false, trace)
	if !w.violation() {
		t.Fatal("expected violation on reviewed implementation")
	}
	if w.lastCall.Identity != IdentityB {
		t.Fatalf("identity = %+v, want client B %+v", w.lastCall.Identity, IdentityB)
	}
	if w.lastCall.Identity.ProcessID != IdentityA.ProcessID {
		t.Fatalf("client B should be reusing A's PID")
	}
}

// The same traces against the secure implementation must fail closed.
func TestSecureImplementationResistsExploitTraces(t *testing.T) {
	traces := [][]string{
		{"A.connect", "A.write", "svc.read", "A.disconnect", "svc.impersonate", "svc.call"},
		{"A.connect", "A.write", "svc.read", "A.disconnect", "svc.listen", "B.connect", "svc.impersonate", "svc.call"},
		{"A.connect", "A.write", "svc.read", "svc.impersonate", "A.disconnect", "svc.call"},
	}
	for _, trace := range traces {
		w := replay(true, trace)
		if w.violation() {
			t.Fatalf("secure implementation violated by trace %v", trace)
		}
		if w.lastCall != nil && w.lastCall.Identity != IdentityA {
			t.Fatalf("secure call ran as %+v for trace %v", w.lastCall.Identity, trace)
		}
	}
}

// Exhaustive: within the alphabet and depth bound, the secure
// implementation admits no violating interleaving at all.
func TestSearchFindsNoViolationOnSecure(t *testing.T) {
	if trace, found := SearchShortestViolation(true, 10); found {
		t.Fatalf("secure implementation violated by %v", trace)
	}
}
