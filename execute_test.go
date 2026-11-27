package iac

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestSingleBranchFailureOnlyBlocksDownstream(t *testing.T) {
	resources := []Resource{
		{Address: "b", Change: ProposedChange{Action: UpdateInPlace}},
		{Address: "a", DependsOn: []string{"b"}, Change: ProposedChange{Action: UpdateInPlace}},
		{Address: "c", Change: ProposedChange{Action: UpdateInPlace}},
		{Address: "d", DependsOn: []string{"c"}, Change: ProposedChange{Action: UpdateInPlace}},
	}
	plan := mustPlan(t, resources)
	if len(plan.Waves) != 2 {
		t.Fatalf("waves = %d, want 2: %#v", len(plan.Waves), plan.Waves)
	}

	boom := errors.New("provider rejected update")
	result := plan.ExecuteWithFailures(map[string]error{"b": boom})
	if result.Err != nil {
		t.Fatalf("unexpected execution error: %v", result.Err)
	}
	if !reflect.DeepEqual(result.Failed, []string{"update:b"}) {
		t.Fatalf("failed = %#v, want [update:b]", result.Failed)
	}
	if !reflect.DeepEqual(result.Skipped, []string{"update:a"}) {
		t.Fatalf("skipped = %#v, want [update:a]", result.Skipped)
	}
	if !reflect.DeepEqual(result.Succeeded, []string{"update:c", "update:d"}) {
		t.Fatalf("succeeded = %#v, want [update:c update:d]", result.Succeeded)
	}
	if got := result.BlockedBy["update:a"]; got != "update:b" {
		t.Fatalf("update:a blocked by %q, want update:b", got)
	}
}

func TestFailureInReplacementCreateSkipsItsDestroyOnly(t *testing.T) {
	resources := []Resource{
		{Address: "b", Change: ProposedChange{Action: Replace}, Lifecycle: Lifecycle{CreateBeforeDestroy: true}},
		{Address: "a", DependsOn: []string{"b"}, Change: ProposedChange{Action: Replace}, Lifecycle: Lifecycle{CreateBeforeDestroy: true}},
		{Address: "independent", Change: ProposedChange{Action: UpdateInPlace}},
	}
	plan := mustPlan(t, resources)
	result := plan.ExecuteWithFailures(map[string]error{"create:a": errors.New("quota exceeded")})

	if got, _ := result.Status("create:a"); got != StatusFailed {
		t.Fatalf("create:a status = %s, want failed", got)
	}
	if got, _ := result.Status("destroy:a"); got != StatusSkipped {
		t.Fatalf("destroy:a status = %s, want skipped", got)
	}
	if got, _ := result.Status("update:independent"); got != StatusSucceeded {
		t.Fatalf("independent status = %s, want succeeded", got)
	}
	// Old b and old a must not be destroyed after a failed replacement.
	if got, _ := result.Status("destroy:b"); got != StatusSkipped {
		t.Fatalf("destroy:b status = %s, want skipped", got)
	}
}

func TestExecutionCycleLeavesNothingRunnable(t *testing.T) {
	g := NewGraph()
	for _, id := range []string{"a", "b"} {
		_ = g.AddNode(&Node{ID: id, Address: id})
	}
	_ = g.AddEdge("a", "b")
	_ = g.AddEdge("b", "a")
	result := g.Execute(nil)
	if !HasErrorCode(result.Err, ErrorExecutionCycle) {
		t.Fatalf("Execute error = %v, want EXECUTION_CYCLE", result.Err)
	}
	if len(result.Skipped) != 2 {
		t.Fatalf("skipped = %#v, want both nodes", result.Skipped)
	}
}

func TestConcurrentWaveRunsIndependentNodes(t *testing.T) {
	g := NewGraph()
	for _, id := range []string{"a", "b", "c"} {
		_ = g.AddNode(&Node{ID: id, Address: id})
	}
	_ = g.AddEdge("a", "c")
	var seen []string
	var seenMu sync.Mutex
	result := g.Execute(func(n *Node) error {
		seenMu.Lock()
		seen = append(seen, n.ID)
		seenMu.Unlock()
		if n.ID == "a" {
			return errors.New("a failed")
		}
		return nil
	})
	if len(result.Succeeded) != 1 || result.Succeeded[0] != "b" {
		t.Fatalf("succeeded = %#v, want [b]", result.Succeeded)
	}
	if len(result.Failed) != 1 || result.Failed[0] != "a" {
		t.Fatalf("failed = %#v, want [a]", result.Failed)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "c" {
		t.Fatalf("skipped = %#v, want [c]", result.Skipped)
	}
	if len(seen) != 2 {
		t.Fatalf("executor saw %d independent nodes, want 2", len(seen))
	}
}
