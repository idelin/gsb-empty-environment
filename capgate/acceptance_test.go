package capgate_test

import (
	"testing"

	"formal.local/h26-25236a386a/capgate"
)

func boolPtr(v bool) *bool { return &v }

func scriptGraph(sandboxed bool) *capgate.Graph {
	return &capgate.Graph{
		Name: "script",
		Edges: []*capgate.Edge{
			{
				ID: "trigger-grant", From: "ci", To: "pre-node",
				Pkg: "root-trigger", Digest: "sha256:root", Phase: capgate.PhaseFetch,
				Grants: []capgate.Grant{{
					Capability: "credential:temp-publish", TransactionID: "tx-script", Temporary: true,
					Signer: "ci", Signature: "test",
				}},
			},
			{
				ID: "optional-preinstall", From: "pre-node", To: "build-node",
				Pkg: "installer", Digest: "sha256:installer", Phase: capgate.PhasePreinstall,
				Condition: "linux", Optional: true, Sandboxed: sandboxed,
			},
			{
				ID: "build", From: "build-node", To: "publish-input",
				Pkg: "builder", Digest: "sha256:builder", Phase: capgate.PhaseBuild,
			},
			{
				ID: "publish", From: "publish-input", To: "registry",
				Pkg: "release", Digest: "sha256:release", Phase: capgate.PhasePublish,
			},
		},
	}
}

func TestScriptContagionExhaustive(t *testing.T) {
	policy := capgate.DefaultPolicy()
	policy.Features = []string{"linux", "arm64"}

	report, err := capgate.Compare(scriptGraph(true), scriptGraph(false), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := report.VerifyCoverage(); err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 1 {
		t.Fatalf("Added = %d, want 1: %+v", len(report.Added), report.Added)
	}
	added := report.Added[0]
	if added.Instance.Pkg != "release" || added.Instance.Phase != capgate.PhasePublish {
		t.Fatalf("unexpected instance %s", added.Instance)
	}
	if !contains(added.Kinds, "script-credential-smuggling") {
		t.Fatalf("kinds = %v, want script-credential-smuggling", added.Kinds)
	}
	if len(added.Blocks) != 1 || added.Blocks[0].Expr != "linux" {
		t.Fatalf("blocks = %+v, want one linux block", added.Blocks)
	}
	if len(added.Blocks[0].Assignments) != 2 {
		t.Fatalf("linux block covers %d assignments, want 2", len(added.Blocks[0].Assignments))
	}
	if len(added.Blocks[0].ChainIDs) == 0 {
		t.Fatal("condition block has no replay chain")
	}
	var foundInherit bool
	for _, chain := range report.Chains {
		if chain.Kind != "script-credential-smuggling" {
			continue
		}
		if _, err := chain.Replay(); err != nil {
			t.Fatalf("Replay: %v", err)
		}
		for _, st := range chain.Steps {
			if st.Kind == "inherit-credentials" && st.EdgeID == "optional-preinstall" {
				foundInherit = true
			}
		}
	}
	if !foundInherit {
		t.Fatal("replay chain does not show unsandboxed preinstall inheritance")
	}

	for _, row := range report.Coverage.Assignments {
		linux := row.Assignment["linux"]
		hasAddedRelease := false
		for _, k := range row.Added {
			if k.Pkg == "release" {
				hasAddedRelease = true
			}
		}
		if linux && !hasAddedRelease {
			t.Fatalf("linux assignment missing added release: %v", row.Assignment)
		}
		if !linux && hasAddedRelease {
			t.Fatalf("non-linux assignment falsely added release: %v", row.Assignment)
		}
	}
}

func TestSandboxedInstallDoesNotInherit(t *testing.T) {
	policy := capgate.DefaultPolicy()
	policy.Features = []string{"linux"}
	report, err := capgate.Compare(&capgate.Graph{Name: "empty"}, scriptGraph(true), policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 0 || !report.Coverage.NoAdded {
		t.Fatalf("sandboxed graph produced additions: %+v", report.Added)
	}
	if err := report.VerifyCoverage(); err != nil {
		t.Fatal(err)
	}
}

func cacheGraph(shared bool) *capgate.Graph {
	writerKey, readerKey := "writer-v1", "shared-v1"
	if shared {
		writerKey = "shared-v1"
	}
	return &capgate.Graph{
		Name: "cache",
		Edges: []*capgate.Edge{
			{
				ID: "optional-writer", From: "task-a", To: "cache-node",
				Pkg: "poisoned-dep", Digest: "sha256:writer", Phase: capgate.PhaseBuild,
				Condition: "linux && cache_poison", Optional: true,
				CacheKey: writerKey, CacheMode: capgate.CacheModeWrite,
			},
			{
				ID: "publish-reader", From: "publish-job", To: "registry",
				Pkg: "publisher", Digest: "sha256:publisher", Phase: capgate.PhasePublish,
				CacheKey: readerKey, CacheMode: capgate.CacheModeRead,
			},
		},
	}
}

func TestCacheKeyPoisoningExhaustive(t *testing.T) {
	policy := capgate.DefaultPolicy()
	policy.Features = []string{"linux", "cache_poison", "irrelevant_arch"}

	report, err := capgate.Compare(cacheGraph(false), cacheGraph(true), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := report.VerifyCoverage(); err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 1 {
		t.Fatalf("Added = %+v", report.Added)
	}
	added := report.Added[0]
	if !contains(added.Kinds, "cache-key-poisoning") {
		t.Fatalf("kinds = %v", added.Kinds)
	}
	if len(added.Blocks) != 1 || added.Blocks[0].Expr != "cache_poison && linux" {
		t.Fatalf("block expr = %q, want cache_poison && linux", exprs(added.Blocks))
	}
	if len(added.Blocks[0].Assignments) != 2 {
		t.Fatalf("block size = %d, want irrelevant feature expanded", len(added.Blocks[0].Assignments))
	}
	var cacheChain *capgate.GrantChain
	for _, c := range report.Chains {
		if c.Kind == "cache-key-poisoning" {
			cacheChain = c
		}
	}
	if cacheChain == nil {
		t.Fatal("missing cache chain")
	}
	if _, err := cacheChain.Replay(); err != nil {
		t.Fatalf("cache Replay: %v", err)
	}
}

func optionalUnionGraph(tx1, tx2 string) *capgate.Graph {
	return &capgate.Graph{
		Name: "optional-union",
		Edges: []*capgate.Edge{
			{
				ID: "branch-a", From: "source-a", To: "unify-node",
				Pkg: "branch-a", Digest: "sha256:a", Phase: capgate.PhaseBuild,
				Condition: "linux && feat_a", Optional: true,
				Grants: []capgate.Grant{{
					Capability: "cap:sign", TransactionID: tx1,
					Half: &capgate.HalfRef{Family: "release", Index: 1, Total: 2},
				}},
			},
			{
				ID: "branch-b", From: "source-b", To: "unify-node",
				Pkg: "branch-b", Digest: "sha256:b", Phase: capgate.PhaseBuild,
				Condition: "linux && feat_b", Optional: true,
				Grants: []capgate.Grant{{
					Capability: "cap:metadata", TransactionID: tx2,
					Half: &capgate.HalfRef{Family: "release", Index: 2, Total: 2},
				}},
			},
			{
				ID: "publish-unified", From: "unify-node", To: "registry",
				Pkg: "unified-release", Digest: "sha256:unified", Phase: capgate.PhasePublish,
			},
		},
	}
}

func unionPolicy() capgate.Policy {
	p := capgate.DefaultPolicy()
	p.Features = []string{"linux", "feat_a", "feat_b"}
	p.HighPrivilegeUnions = []capgate.PrivilegeUnion{{
		Name: "release-boundary", Capabilities: []string{"cap:sign", "cap:metadata"},
	}}
	return p
}

func TestDualOptionalBranchUnion(t *testing.T) {
	old := optionalUnionGraph("tx-same", "tx-same")
	old.Edges = old.Edges[:1] // old graph has only one individually compliant branch

	report, err := capgate.Compare(old, optionalUnionGraph("tx-same", "tx-same"), unionPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err := report.VerifyCoverage(); err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 1 {
		t.Fatalf("Added = %+v", report.Added)
	}
	added := report.Added[0]
	if !contains(added.Kinds, "dual-optional-union-escalation") {
		t.Fatalf("kinds = %v", added.Kinds)
	}
	if len(added.Blocks) != 1 || added.Blocks[0].Expr != "feat_a && feat_b && linux" {
		t.Fatalf("blocks = %+v", added.Blocks)
	}
	if len(report.Chains) == 0 {
		t.Fatal("missing union chain")
	}
	var branches int
	for _, c := range report.Chains {
		if c.Kind != "dual-optional-union-escalation" {
			continue
		}
		if _, err := c.Replay(); err != nil {
			t.Fatal(err)
		}
		branches = len(c.Branches)
	}
	if branches != 2 {
		t.Fatalf("union chain branches = %d, want 2", branches)
	}
}

func TestHalfCredentialsDifferentTransactionsDoNotSplice(t *testing.T) {
	policy := capgate.DefaultPolicy()
	policy.Features = []string{"linux", "feat_a", "feat_b"}
	policy.HighPrivilegeUnions = []capgate.PrivilegeUnion{{
		Name: "full-release", Capabilities: []string{"credential:release"}, SameTransaction: true,
	}}
	policy.DenyOptionalUnification = boolPtr(false)

	report, err := capgate.Compare(nil, optionalUnionGraph("tx-A", "tx-B"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 0 {
		t.Fatalf("different transactions were spliced: %+v", report.Added)
	}
	if !report.Coverage.NoAdded {
		t.Fatal("coverage should prove no additions")
	}

	same, err := capgate.Compare(nil, optionalUnionGraph("tx-same", "tx-same"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(same.Added) != 1 || !contains(same.Added[0].Kinds, "dual-optional-union-escalation") {
		t.Fatalf("same-transaction halves should compose: %+v", same.Added)
	}
	if err := same.VerifyCoverage(); err != nil {
		t.Fatal(err)
	}
}

func TestConditionalCycleConverges(t *testing.T) {
	policy := capgate.DefaultPolicy()
	policy.Features = []string{"cycle_a", "cycle_b"}
	g := &capgate.Graph{Edges: []*capgate.Edge{
		{
			ID: "grant", From: "ci", To: "n1", Pkg: "root", Digest: "d0", Phase: capgate.PhaseFetch,
			Grants: []capgate.Grant{{Capability: "credential:temp", Temporary: true, TransactionID: "tx"}},
		},
		{
			ID: "a-to-b", From: "n1", To: "n2", Pkg: "a", Digest: "d1", Phase: capgate.PhaseBuild,
			Condition: "cycle_a",
		},
		{
			ID: "b-to-a-cycle", From: "n2", To: "n1", Pkg: "b", Digest: "d2", Phase: capgate.PhaseBuild,
			Condition: "cycle_b",
		},
		{
			ID: "publish", From: "n2", To: "registry", Pkg: "cycle-app", Digest: "d3", Phase: capgate.PhasePublish,
		},
	}}
	report, err := capgate.Compare(nil, g, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := report.VerifyCoverage(); err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 1 || report.Added[0].Blocks[0].Expr != "cycle_a" {
		t.Fatalf("cycle delta = %+v", report.Added)
	}
	if report.Stats.Iterations <= 0 {
		t.Fatal("expected convergence iteration metadata")
	}
}

func TestGraphOnlyOptionalBranchesUnifyWithoutGrants(t *testing.T) {
	policy := capgate.DefaultPolicy()
	policy.Features = []string{"feat_a", "feat_b"}
	g := &capgate.Graph{Edges: []*capgate.Edge{
		{ID: "opt-a", From: "a", To: "join", Pkg: "opt-a", Digest: "da", Phase: capgate.PhaseBuild, Optional: true, Condition: "feat_a"},
		{ID: "opt-b", From: "b", To: "join", Pkg: "opt-b", Digest: "db", Phase: capgate.PhaseBuild, Optional: true, Condition: "feat_b"},
		{ID: "publish", From: "join", To: "reg", Pkg: "unified", Digest: "du", Phase: capgate.PhasePublish},
	}}
	report, err := capgate.Compare(nil, g, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := report.VerifyCoverage(); err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 1 || !contains(report.Added[0].Kinds, "dual-optional-union-escalation") {
		t.Fatalf("expected graph-only dual unification: %+v", report.Added)
	}
	for _, c := range report.Chains {
		if c.Kind != "dual-optional-union-escalation" {
			continue
		}
		if len(c.Branches) != 2 {
			t.Fatalf("branches = %d, want 2", len(c.Branches))
		}
		if _, err := c.Replay(); err != nil {
			t.Fatalf("graph-only chain replay: %v", err)
		}
	}
}

func TestCacheKeyWithoutExplicitModeDefaultsToWritable(t *testing.T) {
	policy := capgate.DefaultPolicy()
	policy.Features = []string{"linux"}
	g := cacheGraph(true)
	for _, e := range g.Edges {
		e.CacheMode = ""
	}
	report, err := capgate.Compare(nil, g, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 1 || !contains(report.Added[0].Kinds, "cache-key-poisoning") {
		t.Fatalf("expected implicit shared-cache finding: %+v", report.Added)
	}
	if err := report.VerifyCoverage(); err != nil {
		t.Fatal(err)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func exprs(blocks []capgate.ConditionBlock) []string {
	var out []string
	for _, b := range blocks {
		out = append(out, b.Expr)
	}
	return out
}
