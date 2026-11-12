package netsec

import "testing"

func TestActionAndDecisionStrings(t *testing.T) {
	if got := ActionAllow.String(); got != "ALLOW" {
		t.Fatalf("ActionAllow.String() = %q, want ALLOW", got)
	}
	if got := ActionDeny.String(); got != "DENY" {
		t.Fatalf("ActionDeny.String() = %q, want DENY", got)
	}
	if got := ActionCustom.String(); got != "CUSTOM" {
		t.Fatalf("ActionCustom.String() = %q, want CUSTOM", got)
	}
	if got := DecisionAllow.String(); got != "ALLOW" {
		t.Fatalf("DecisionAllow.String() = %q, want ALLOW", got)
	}
	if got := DecisionDeny.String(); got != "DENY" {
		t.Fatalf("DecisionDeny.String() = %q, want DENY", got)
	}
}
