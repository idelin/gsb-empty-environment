package handshake

import (
	"testing"

	"prescreen.local/ps-802b9bdc43/bdd"
)

func TestFirstMatchRegionsAndEquivalentRuleSets(t *testing.T) {
	manager, err := bdd.NewManager(3)
	if err != nil {
		t.Fatal(err)
	}
	x0, _ := manager.Variable(0)
	x1, _ := manager.Variable(1)

	left := RuleSet{
		Name: "compact",
		Rules: []Rule{
			{Name: "strong", When: x0, Outcome: AuthOutcome(2)},
			{Name: "basic", When: x1, Outcome: AuthOutcome(1)},
		},
	}
	right := RuleSet{
		Name: "expanded",
		Rules: []Rule{
			{Name: "strong-a", When: manager.And(x0, x1), Outcome: AuthOutcome(2)},
			{Name: "basic", When: manager.And(manager.Not(x0), x1), Outcome: AuthOutcome(1)},
			{Name: "strong-b", When: manager.And(x0, manager.Not(x1)), Outcome: AuthOutcome(2)},
		},
	}
	if !EquivalentRuleSets(manager, left, right) {
		t.Fatal("equivalent ordered rule sets were classified differently")
	}

	partitions := left.Partitions(manager)
	if len(partitions) != 3 {
		t.Fatalf("expected three equivalence partitions, got %d", len(partitions))
	}
	if partitions[0].Outcome != AuthOutcome(2) ||
		partitions[1].Outcome != AuthOutcome(1) ||
		!partitions[2].Outcome.IsReject() {
		t.Fatalf("unexpected partition outcomes: %+v", partitions)
	}

	merged := RuleSet{
		Name: "merged-adjacent",
		Rules: []Rule{
			{Name: "a", When: x0, Outcome: AuthOutcome(1)},
			{Name: "b", When: manager.Not(x0), Outcome: AuthOutcome(1)},
		},
	}
	mergedPartitions := merged.Partitions(manager)
	if len(mergedPartitions) != 1 || !manager.IsOne(mergedPartitions[0].Cover) {
		t.Fatalf("adjacent equivalent rules were not merged: %+v", mergedPartitions)
	}

	enumerateValues(3, func(values []bool) {
		leftOutcome, err := left.Evaluate(manager, values)
		if err != nil {
			t.Fatal(err)
		}
		rightOutcome, err := right.Evaluate(manager, values)
		if err != nil {
			t.Fatal(err)
		}
		if leftOutcome != rightOutcome {
			t.Fatalf("exhaustive mismatch on %v: %s vs %s", values, leftOutcome, rightOutcome)
		}
	})
}

func enumerateValues(width int, fn func([]bool)) {
	values := make([]bool, width)
	var walk func(level int)
	walk = func(level int) {
		if level == width {
			fn(append([]bool(nil), values...))
			return
		}
		values[level] = false
		walk(level + 1)
		values[level] = true
		walk(level + 1)
		values[level] = false
	}
	walk(0)
}
