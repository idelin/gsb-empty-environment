package taskpkg_test

import (
	"testing"

	taskpkg "example.org/domainreissue/q005"
)

// TestEnumeratePlatformGate enumerates every configuration independently and
// shows that the unauthorized node is reachable only behind condition bits.
func TestEnumeratePlatformGate(t *testing.T) {
	internalLib := taskpkg.NodeID{Source: srcInternal, Path: libPath}
	publicLib := taskpkg.NodeID{Source: srcPublic, Path: libPath}

	rootCand := mkT(t, rootNode, "v1.0.0", signInternal, "dig-gate-app")
	internalCand := mkT(t, internalLib, "v1.0.0", signInternal, "dig-gate-internal")
	publicHigh := mkT(t, publicLib, "v9.0.0", signPublic, "dig-gate-public")

	registry := taskpkg.MapRegistry{Data: map[taskpkg.NodeID][]taskpkg.Candidate{
		rootNode:    {rootCand},
		internalLib: {internalCand},
		publicLib:   {publicHigh},
	}}
	verifier := verifierFor(rootCand, internalCand, publicHigh)

	// Bit 0 gates the always-present internal edge; bit 1 gates the risky
	// public edge. The minimal unauthorized config therefore needs bit 1 only.
	graph := taskpkg.Graph{
		Root: rootNode,
		Edges: []taskpkg.Edge{
			edgeWhen(t, rootNode, internalLib, "v1.0.0", "v2.0.0", taskpkg.Bool(0), signInternal),
			edgeWhen(t, rootNode, publicLib, "v1.0.0", "v10.0.0", taskpkg.Bool(1), signPublic),
		},
	}
	r := &taskpkg.Resolver{Graph: graph, Registry: registry, Verifier: verifier}
	policy := taskpkg.NewPolicy(
		[]taskpkg.SourceID{srcInternal},
		[]taskpkg.SignerID{signInternal},
	)

	report, err := r.Enumerate(taskpkg.AuditConfig{Bits: 2}, policy)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if len(report.Failures) != 0 {
		t.Fatalf("unexpected failures: %v", report.Failures[0].Err)
	}
	if report.Minimal == nil {
		t.Fatalf("expected at least one unauthorized finding")
	}

	// Only the two configurations with bit 1 set can reach the public node.
	wantConfigs := map[taskpkg.PlatformConfig]bool{
		taskpkg.PlatformConfig(0b10): true,
		taskpkg.PlatformConfig(0b11): true,
	}
	if len(report.Findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(report.Findings))
	}
	for _, f := range report.Findings {
		if !wantConfigs[f.Platform] {
			t.Fatalf("unexpected finding under %s", f.Platform)
		}
		if f.Target != publicLib || f.Reason != taskpkg.ReasonUntrustedSource {
			t.Fatalf("finding target/reason mismatch: %v %q", f.Target, f.Reason)
		}
	}

	minimal := report.Minimal
	if minimal.Popcount != 1 || minimal.Platform != taskpkg.PlatformConfig(0b10) {
		t.Fatalf("minimal = %s popcount %d, want bits{1}", minimal.Platform, minimal.Popcount)
	}
	assertPath(t, minimal.Path.Nodes, []taskpkg.NodeID{rootNode, publicLib})

	// Independent enumeration: re-running must produce identical results.
	again, err := r.Enumerate(taskpkg.AuditConfig{Bits: 2}, policy)
	if err != nil {
		t.Fatalf("second enumerate: %v", err)
	}
	if len(again.Findings) != len(report.Findings) || again.Minimal.Platform != minimal.Platform {
		t.Fatalf("enumeration is not independently reproducible")
	}
}

// TestConditionLimit locks the platform condition bound at 12 bits.
func TestConditionLimit(t *testing.T) {
	if _, err := taskpkg.Configs(12); err != nil {
		t.Fatalf("12 conditions must be allowed: %v", err)
	}
	if _, err := taskpkg.Configs(13); err == nil {
		t.Fatalf("13 conditions must be rejected")
	}
	all, err := taskpkg.Configs(12)
	if err != nil || len(all) != 1<<12 {
		t.Fatalf("12-bit enumeration = %d configs, err=%v", len(all), err)
	}
}

// TestConditionExpressions exercises the condition AST and mask helpers.
func TestConditionExpressions(t *testing.T) {
	cfg := taskpkg.PlatformConfig(0).WithBit(0, true).WithBit(2, true)
	cases := []struct {
		name string
		cond taskpkg.Condition
		want bool
	}{
		{"bool set", taskpkg.Bool(0), true},
		{"bool unset", taskpkg.Bool(1), false},
		{"not", taskpkg.Not{Inner: taskpkg.Bool(0)}, false},
		{"and true", taskpkg.And{taskpkg.Bool(0), taskpkg.Bool(2)}, true},
		{"and false", taskpkg.And{taskpkg.Bool(0), taskpkg.Bool(1)}, false},
		{"or true", taskpkg.Or{taskpkg.Bool(1), taskpkg.Bool(2)}, true},
		{"or false", taskpkg.Or{taskpkg.Bool(1)}, false},
		{"always", taskpkg.Always{}, true},
		{"never", taskpkg.Never{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cond.Eval(cfg); got != tc.want {
				t.Fatalf("Eval = %v, want %v", got, tc.want)
			}
		})
	}
	if (taskpkg.Bool(12)).Eval(cfg) {
		t.Fatalf("out-of-range bit must evaluate to false")
	}
}
