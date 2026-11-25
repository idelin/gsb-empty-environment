package capsum

import (
	"encoding/json"
	"testing"
)

func TestCertificateRoundTripAndBinding(t *testing.T) {
	p := rootProgram()
	p.Domains = []Domain{
		{Name: "D", Grants: []Grant{grant("TENANT", GrantChild, "data")}},
	}
	p.EntryNode = "main"
	p.EntryDomains = []string{"D"}
	p.Nodes = []string{"main", "use"}
	p.Edges = []Edge{
		{ID: "e1", From: "main", To: "use", Kind: EdgeLocal},
	}

	res, a, err := AnalyzeWithEvidence(p, 0)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	cert := res.Certificate(p, a)
	if cert.InputHash == "" || cert.ProgramBinding.RootViewHash == "" {
		t.Fatal("certificate missing binding hashes")
	}

	raw, err := cert.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Certificate
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.InputHash != cert.InputHash {
		t.Fatal("input hash did not survive round trip")
	}
	if len(back.Generators) == 0 || back.Generators[0].DFA == nil {
		t.Fatal("generator DFAs missing after round trip")
	}

	// Tenant-root alias grant binds to the alias version, not the physical root.
	if back.Roots[1].AliasOf != "ROOT" || back.Roots[1].Version != "tenant-v1" {
		t.Fatal("tenant-root alias binding lost")
	}
}
