package capgate_test

import (
	"testing"

	"formal.local/h26-25236a386a/capgate"
)

func TestSerialOptionalEdgesAreNotIndependentBranches(t *testing.T) {
	policy := capgate.DefaultPolicy()
	policy.Features = []string{"feat_a", "feat_b"}
	g := &capgate.Graph{Edges: []*capgate.Edge{
		{
			ID: "opt-a", From: "n0", To: "n1", Pkg: "a", Digest: "da", Phase: capgate.PhaseBuild,
			Optional: true, Condition: "feat_a",
			Grants: []capgate.Grant{{Capability: "cap:a"}},
		},
		{
			ID: "opt-b", From: "n1", To: "n2", Pkg: "b", Digest: "db", Phase: capgate.PhasePostinstall,
			Optional: true, Condition: "feat_b",
			Grants: []capgate.Grant{{Capability: "cap:b"}},
		},
		{ID: "publish", From: "n2", To: "reg", Pkg: "app", Digest: "d", Phase: capgate.PhasePublish},
	}}

	report, err := capgate.Compare(nil, g, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 0 {
		t.Fatalf("serial optional chain falsely treated as union: %+v", report.Added)
	}

	policy.HighPrivilegeUnions = []capgate.PrivilegeUnion{{
		Name: "serial-union-still-checked", Capabilities: []string{"cap:a", "cap:b"},
	}}
	report, err = capgate.Compare(nil, g, policy)
	if err != nil {
		t.Fatal(err)
	}
	var publish *capgate.AddedInstance
	for i := range report.Added {
		if report.Added[i].Instance.Pkg == "app" {
			publish = &report.Added[i]
		}
	}
	if publish == nil {
		t.Fatalf("missing publish addition: %+v", report.Added)
	}
	if contains(publish.Kinds, "dual-optional-union-escalation") {
		t.Fatalf("serial union should not be dual independent: %+v", publish.Kinds)
	}
	if !contains(publish.Kinds, "privilege-union") {
		t.Fatalf("kinds = %v, want privilege-union", publish.Kinds)
	}
}
