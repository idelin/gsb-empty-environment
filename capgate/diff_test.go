package capgate_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"formal.local/h26-25236a386a/capgate"
)

func TestDeltaExcludesAssignmentsAlreadyHighInOldGraph(t *testing.T) {
	old := scriptGraph(false)
	old.Edges[1].Condition = "ubuntu"
	newGraph := scriptGraph(false)
	newGraph.Edges[1].Condition = "linux"

	policy := capgate.DefaultPolicy()
	policy.Features = []string{"linux", "ubuntu"}
	// ubuntu is a legal Linux distribution feature: ubuntu => linux.
	policy.LegalCondition = "!ubuntu || linux"

	report, err := capgate.Compare(old, newGraph, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := report.VerifyCoverage(); err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 1 {
		t.Fatalf("Added = %+v", report.Added)
	}
	blocks := report.Added[0].Blocks
	if len(blocks) != 1 || blocks[0].Expr != "!ubuntu && linux" {
		t.Fatalf("delta block = %+v, want !ubuntu && linux", blocks)
	}
	if len(blocks[0].Assignments) != 1 ||
		!blocks[0].Assignments[0]["linux"] || blocks[0].Assignments[0]["ubuntu"] {
		t.Fatalf("unexpected delta assignment: %+v", blocks[0].Assignments)
	}
}

func TestNoDeltaCoverageProofAndJSONRoundTrip(t *testing.T) {
	policy := capgate.DefaultPolicy()
	policy.Features = []string{"linux", "arm64"}
	report, err := capgate.Compare(scriptGraph(false), scriptGraph(false), policy)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Pass() || !report.Coverage.NoAdded {
		t.Fatal("identical graphs must have a no-addition coverage proof")
	}
	if err := report.GateError(); err != nil {
		t.Fatalf("identical graph gate: %v", err)
	}

	var buf bytes.Buffer
	if err := report.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var decoded capgate.DiffReport
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.VerifyCoverage(); err != nil {
		t.Fatalf("round-trip coverage: %v", err)
	}
	if err := decoded.ReplayChains(nil); err != nil {
		t.Fatalf("round-trip replay: %v", err)
	}
}

func TestTamperedChainFailsReplay(t *testing.T) {
	policy := capgate.DefaultPolicy()
	policy.Features = []string{"linux"}
	report, err := capgate.Compare(nil, scriptGraph(false), policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Chains) == 0 {
		t.Fatal("expected chain")
	}
	chain := *report.Chains[0]
	chain.Assignment = capgate.Assignment{"linux": false}
	if _, err := chain.Replay(); err == nil {
		t.Fatal("tampered inactive assignment unexpectedly replayed")
	}
}
