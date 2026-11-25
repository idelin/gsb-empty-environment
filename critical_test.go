package handoff

import "testing"

func TestCriticalPrefixesPreventBlanketWithdraw(t *testing.T) {
	spec := testSpec("e1", "e2")
	protocol, err := NewProtocol(spec)
	if err != nil {
		t.Fatal(err)
	}
	withdrawAll := Statement{
		PlanID:     spec.PlanID,
		ID:         "withdraw-everything",
		Generation: 1,
		Withdraws:  []Digest{spec.InitialClaims[0].Digest()},
	}.Canonical()
	protocol.AddStatementBroadcast(withdrawAll)

	cert, err := IssueCertificate(protocol, VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cert.CriticalReachable {
		t.Fatal("blanket withdrawal must fail critical-prefix reachability")
	}
	if err := cert.RequireSafe(); err == nil {
		t.Fatal("RequireSafe must reject critical unreachability")
	}
}
