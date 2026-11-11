package netsec

import (
	"errors"
	"sync"
	"testing"
)

func mustApply(t *testing.T, e *Engine, txn *Txn) {
	t.Helper()
	if err := e.Apply(txn); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
}

func ruleByID(ev []Evidence, id RuleID) *Evidence {
	for i := range ev {
		if ev[i].Rule.ID == id {
			return &ev[i]
		}
	}
	return nil
}

func TestBasicAllowAndExplain(t *testing.T) {
	e := NewEngine()
	mustApply(t, e, NewTxn().
		AddMember("src", "10.0.0.1").
		AddMember("dst", "10.0.0.9").
		PutRule(Rule{ID: "r1", TargetGroup: "dst", SourceGroup: "src", DstPort: 443}))

	if !e.Allows("10.0.0.1", "dst", 443) {
		t.Fatal("expected allow for matching flow")
	}
	if e.Allows("10.0.0.1", "dst", 80) {
		t.Fatal("port mismatch must not allow")
	}
	if e.Allows("10.0.0.2", "dst", 443) {
		t.Fatal("non-member must not allow")
	}

	ev := e.Explain("10.0.0.1", "dst", 443)
	if len(ev) != 1 {
		t.Fatalf("want 1 evidence, got %d", len(ev))
	}
	if ev[0].Rule.ID != "r1" || ev[0].SourceAddress != "10.0.0.1" || ev[0].Rule.DstPort != 443 {
		t.Fatalf("unexpected evidence: %+v", ev[0])
	}
	if got := e.Explain("10.0.0.2", "dst", 443); got != nil {
		t.Fatalf("expected nil evidence for non-member, got %+v", got)
	}
}

func TestUnionAndMultipleMinimalEvidence(t *testing.T) {
	e := NewEngine()
	mustApply(t, e, NewTxn().
		AddMember("srcA", "10.0.0.1").
		AddMember("srcB", "10.0.0.1").
		PutRule(Rule{ID: "r1", TargetGroup: "dst", SourceGroup: "srcA", DstPort: 443}).
		PutRule(Rule{ID: "r2", TargetGroup: "dst", SourceGroup: "srcB", DstPort: 443}))

	ev := e.Explain("10.0.0.1", "dst", 443)
	if len(ev) != 2 {
		t.Fatalf("want two independent minimal evidences, got %d", len(ev))
	}
	if ruleByID(ev, "r1") == nil || ruleByID(ev, "r2") == nil {
		t.Fatalf("missing rule evidence in %+v", ev)
	}
	for _, item := range ev {
		if item.SourceAddress != "10.0.0.1" {
			t.Fatalf("evidence must name the matching member address, got %+v", item)
		}
	}
}

func TestMemberChangeIncremental(t *testing.T) {
	e := NewEngine()
	mustApply(t, e, NewTxn().
		AddMember("src", "10.0.0.1").
		PutRule(Rule{ID: "r1", TargetGroup: "dst", SourceGroup: "src", DstPort: 443}))

	mustApply(t, e, NewTxn().AddMember("src", "10.0.0.2"))
	if !e.Allows("10.0.0.2", "dst", 443) || !e.Allows("10.0.0.1", "dst", 443) {
		t.Fatal("both members must be allowed after add")
	}

	mustApply(t, e, NewTxn().RemoveMember("src", "10.0.0.1"))
	if e.Allows("10.0.0.1", "dst", 443) {
		t.Fatal("removed member must lose allow")
	}
	if !e.Allows("10.0.0.2", "dst", 443) {
		t.Fatal("remaining member must stay allowed")
	}

	report := e.CheckRebuild()
	if !report.Consistent {
		t.Fatalf("rebuild mismatch after member changes: %v", report.Mismatches)
	}
}

func TestRuleRevokeOnlyRecomputesAffectedRelations(t *testing.T) {
	e := NewEngine()
	mustApply(t, e, NewTxn().
		AddMember("src", "10.0.0.1").
		AddMember("src", "10.0.0.2").
		PutRule(Rule{ID: "r1", TargetGroup: "dst", SourceGroup: "src", DstPort: 443}).
		PutRule(Rule{ID: "r2", TargetGroup: "dst", SourceGroup: "src", DstPort: 80}))

	mustApply(t, e, NewTxn().RemoveRule("r1"))

	if e.Allows("10.0.0.1", "dst", 443) {
		t.Fatal("revoked rule's relations must be gone")
	}
	if !e.Allows("10.0.0.1", "dst", 80) || !e.Allows("10.0.0.2", "dst", 80) {
		t.Fatal("unaffected rule's relations must survive")
	}
	if _, ok := e.ruleEv["r1"]; ok {
		t.Fatal("revoked rule must leave no per-rule evidence")
	}
	if got := e.Explain("10.0.0.1", "dst", 80); len(got) != 1 || got[0].Rule.ID != "r2" {
		t.Fatalf("unaffected evidence changed: %+v", got)
	}

	report := e.CheckRebuild()
	if !report.Consistent {
		t.Fatalf("rebuild mismatch after rule revoke: %v", report.Mismatches)
	}
}

func TestEmptyGroupProducesNoEvidence(t *testing.T) {
	e := NewEngine()
	mustApply(t, e, NewTxn().
		AddMember("src", "10.0.0.1").
		PutRule(Rule{ID: "r1", TargetGroup: "dst", SourceGroup: "empty", DstPort: 443}).
		PutRule(Rule{ID: "r2", TargetGroup: "dst", SourceGroup: "src", DstPort: 443}))

	report := e.CheckRebuild()
	if !report.Consistent {
		t.Fatalf("rebuild mismatch: %v", report.Mismatches)
	}
	if !containsGroup(report.EmptyGroups, "empty") {
		t.Fatalf("want empty group list to include empty, got %v", report.EmptyGroups)
	}
	if _, ok := e.ruleEv["r1"]; ok {
		t.Fatal("rule referencing empty group must have no derived evidence")
	}

	mustApply(t, e, NewTxn().RemoveMember("src", "10.0.0.1"))
	report = e.CheckRebuild()
	if !report.Consistent {
		t.Fatalf("rebuild mismatch after draining group: %v", report.Mismatches)
	}
	if !containsGroup(report.EmptyGroups, "empty") || !containsGroup(report.EmptyGroups, "src") {
		t.Fatalf("want empty and src both reported empty, got %v", report.EmptyGroups)
	}
	if e.Allows("10.0.0.1", "dst", 443) {
		t.Fatal("no rule should allow after source groups drain")
	}
}

func containsGroup(groups []GroupID, want GroupID) bool {
	for _, group := range groups {
		if group == want {
			return true
		}
	}
	return false
}

func TestSameAddressInMultipleGroups(t *testing.T) {
	e := NewEngine()
	mustApply(t, e, NewTxn().
		AddMember("g1", "10.0.0.1").
		AddMember("g2", "10.0.0.1").
		PutRule(Rule{ID: "r1", TargetGroup: "dst", SourceGroup: "g1", DstPort: 443}).
		PutRule(Rule{ID: "r2", TargetGroup: "dst", SourceGroup: "g2", DstPort: 443}))

	report := e.CheckRebuild()
	if !report.Consistent {
		t.Fatalf("rebuild mismatch: %v", report.Mismatches)
	}
	if len(report.SharedAddresses) != 1 || report.SharedAddresses[0] != "10.0.0.1" {
		t.Fatalf("want shared address 10.0.0.1, got %v", report.SharedAddresses)
	}
	if ev := e.Explain("10.0.0.1", "dst", 443); len(ev) != 2 {
		t.Fatalf("each group's rule contributes evidence, got %d", len(ev))
	}

	mustApply(t, e, NewTxn().RemoveMember("g1", "10.0.0.1"))
	report = e.CheckRebuild()
	if !report.Consistent {
		t.Fatalf("rebuild mismatch after partial removal: %v", report.Mismatches)
	}
	if len(report.SharedAddresses) != 0 {
		t.Fatalf("address no longer shared, got %v", report.SharedAddresses)
	}
	if ev := e.Explain("10.0.0.1", "dst", 443); len(ev) != 1 || ev[0].Rule.ID != "r2" {
		t.Fatalf("only g2 evidence must remain: %+v", ev)
	}
}

func TestCyclicGroupReferences(t *testing.T) {
	e := NewEngine()
	mustApply(t, e, NewTxn().
		AddMember("g1", "10.0.0.1").
		AddMember("g2", "10.0.0.2").
		AddMember("g3", "10.0.0.3").
		PutRule(Rule{ID: "r1", TargetGroup: "g1", SourceGroup: "g2", DstPort: 100}).
		PutRule(Rule{ID: "r2", TargetGroup: "g2", SourceGroup: "g3", DstPort: 200}).
		PutRule(Rule{ID: "r3", TargetGroup: "g3", SourceGroup: "g1", DstPort: 300}))

	report := e.CheckRebuild()
	if !report.Consistent {
		t.Fatalf("cyclic refs must not corrupt derived state: %v", report.Mismatches)
	}
	if len(report.Cycles) != 1 {
		t.Fatalf("want one cycle, got %v", report.Cycles)
	}
	want := []GroupID{"g1", "g2", "g3", "g1"}
	got := report.Cycles[0]
	if len(got) != len(want) {
		t.Fatalf("cycle length mismatch: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("canonical cycle = %v, want %v", got, want)
		}
	}

	if !e.Allows("10.0.0.2", "g1", 100) || !e.Allows("10.0.0.3", "g2", 200) || !e.Allows("10.0.0.1", "g3", 300) {
		t.Fatal("every one-hop rule in the cycle must be evaluated")
	}

	mustApply(t, e, NewTxn().
		PutRule(Rule{ID: "self", TargetGroup: "g1", SourceGroup: "g1", DstPort: 443}))
	cycles := e.ReferenceCycles()
	foundSelf := false
	for _, cycle := range cycles {
		if len(cycle) == 2 && cycle[0] == "g1" && cycle[1] == "g1" {
			foundSelf = true
		}
	}
	if !foundSelf {
		t.Fatalf("self loop must be reported, cycles = %v", cycles)
	}

	mustApply(t, e, NewTxn().RemoveRule("r1"))
	report = e.CheckRebuild()
	if !report.Consistent {
		t.Fatalf("state after breaking the cycle must rebuild cleanly: %v", report.Mismatches)
	}
	for _, cycle := range report.Cycles {
		for _, group := range cycle {
			if group == "g2" || group == "g3" {
				t.Fatalf("g1->g2->g3->g1 cycle must be gone, got %v", report.Cycles)
			}
		}
	}
	if e.Allows("10.0.0.2", "g1", 100) {
		t.Fatal("relations from the removed rule must be gone")
	}
	if !e.Allows("10.0.0.3", "g2", 200) {
		t.Fatal("relations from surviving rules must stay intact")
	}
}

func TestAtomicFailureLeavesStateUntouched(t *testing.T) {
	e := NewEngine()
	mustApply(t, e, NewTxn().
		AddMember("src", "10.0.0.1").
		PutRule(Rule{ID: "r1", TargetGroup: "dst", SourceGroup: "src", DstPort: 443}))
	before := e.Version()

	cases := []*Txn{
		NewTxn(),
		NewTxn().AddMember("", "10.0.0.1"),
		NewTxn().AddMember("src", ""),
		NewTxn().AddMember("src", "10.0.0.1"),
		NewTxn().RemoveMember("src", "10.0.0.9"),
		NewTxn().PutRule(Rule{ID: "r1", TargetGroup: "dst", SourceGroup: "src", DstPort: 443}),
		NewTxn().RemoveRule("missing"),
		NewTxn().PutRule(Rule{ID: "r9", TargetGroup: "dst", SourceGroup: "src", DstPort: 0}),
		NewTxn().AddMember("src", "10.0.0.1").RemoveMember("src", "10.0.0.1"),
		NewTxn().PutRule(Rule{ID: "r9", TargetGroup: "dst", SourceGroup: "src", DstPort: 80}).RemoveRule("r9"),
	}
	for i, txn := range cases {
		if err := e.Apply(txn); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}

	if e.Version() != before {
		t.Fatalf("failed txns must not bump version: before=%d after=%d", before, e.Version())
	}
	if !e.Allows("10.0.0.1", "dst", 443) {
		t.Fatal("state changed despite failed transactions")
	}
	if ev := e.Explain("10.0.0.1", "dst", 443); len(ev) != 1 {
		t.Fatalf("evidence changed after failed txns: %+v", ev)
	}

	if err := e.Apply(nil); !errors.Is(err, ErrEmptyTxn) {
		t.Fatalf("nil txn: want ErrEmptyTxn, got %v", err)
	}
}

func TestRuleReplacementInOneTransaction(t *testing.T) {
	e := NewEngine()
	mustApply(t, e, NewTxn().
		AddMember("src", "10.0.0.1").
		PutRule(Rule{ID: "r1", TargetGroup: "dst", SourceGroup: "src", DstPort: 443}))

	mustApply(t, e, NewTxn().
		RemoveRule("r1").
		PutRule(Rule{ID: "r1", TargetGroup: "dst", SourceGroup: "src", DstPort: 8443}))

	if e.Allows("10.0.0.1", "dst", 443) {
		t.Fatal("old port relation must be replaced")
	}
	if !e.Allows("10.0.0.1", "dst", 8443) {
		t.Fatal("new port relation must be published")
	}
	report := e.CheckRebuild()
	if !report.Consistent {
		t.Fatalf("rebuild mismatch after replacement: %v", report.Mismatches)
	}
}

func TestConcurrentReadersDuringTransactions(t *testing.T) {
	e := NewEngine()
	mustApply(t, e, NewTxn().
		AddMember("src", "10.0.0.1").
		PutRule(Rule{ID: "r1", TargetGroup: "dst", SourceGroup: "src", DstPort: 443}))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = e.Allows("10.0.0.1", "dst", 443)
					_ = e.Explain("10.0.0.1", "dst", 443)
					_ = e.Version()
				}
			}
		}()
	}

	for i := 0; i < 50; i++ {
		addr := Address("10.0.1." + string(rune('a'+i%26)))
		txn := NewTxn()
		if i%2 == 0 {
			txn.AddMember("src", addr)
		} else {
			txn.RemoveMember("src", Address("10.0.1."+string(rune('a'+(i-1)%26))))
		}
		if err := e.Apply(txn); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()

	report := e.CheckRebuild()
	if !report.Consistent {
		t.Fatalf("rebuild mismatch after concurrent workload: %v", report.Mismatches)
	}
}
