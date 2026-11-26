package capgate_test

import (
	"strings"
	"testing"

	"formal.local/h26-25236a386a/capgate"
)

func TestSignatureVerifierReplay(t *testing.T) {
	policy := capgate.DefaultPolicy()
	policy.Features = []string{"linux"}
	policy.SignatureVerifier = capgate.SignatureVerifierFunc(func(g capgate.Grant) error {
		if g.Signature != "valid" {
			return capgate.ErrInvalidSignature
		}
		return nil
	})

	g := scriptGraph(false)
	for _, e := range g.Edges {
		for i := range e.Grants {
			e.Grants[i].Signature = "invalid"
		}
	}
	report, err := capgate.Compare(nil, g, policy)
	if err != nil {
		t.Fatal(err)
	}
	foundInvalid := false
	for _, a := range report.Added {
		if contains(a.Kinds, "invalid-signature") {
			foundInvalid = true
		}
	}
	if !foundInvalid {
		t.Fatalf("expected invalid-signature finding: %+v", report.Added)
	}
	if err := report.ReplayChains(policy.SignatureVerifier); err == nil ||
		!strings.Contains(err.Error(), capgate.ErrInvalidSignature.Error()) {
		t.Fatalf("expected signature replay error, got %v", err)
	}
}
