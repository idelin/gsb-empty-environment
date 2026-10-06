package trail

import (
	"reflect"
	"testing"
)

func batchEdges() []Edge {
	return []Edge{
		{ID: "e1", From: "A", To: "B"},
		{ID: "e2", From: "B", To: "A"},
		{ID: "e3", From: "A", To: "C"},
		{ID: "e4", From: "C", To: "A"},
	}
}

func TestPlannerPublishesRemainingFromCurrent(t *testing.T) {
	p, err := NewPlanner("A", batchEdges(), []string{"e1", "e2"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Current() != "A" {
		t.Fatalf("current = %q, want A", p.Current())
	}
	plan, err := p.PublishRemaining()
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != 1 {
		t.Fatalf("plan version = %d, want 1", plan.Version)
	}
	if want := []string{"e3", "e4"}; !reflect.DeepEqual(plan.EdgeIDs, want) {
		t.Fatalf("plan = %v, want %v", plan.EdgeIDs, want)
	}
	if err := p.ValidatePlan(plan); err != nil {
		t.Fatalf("fresh plan should validate: %v", err)
	}
}

func TestPlannerRejectsBrokenPrefix(t *testing.T) {
	if _, err := NewPlanner("A", batchEdges(), []string{"e1", "e3"}); err == nil {
		t.Fatal("expected broken-prefix error")
	}
	if _, err := NewPlanner("A", batchEdges(), []string{"e1", "e1"}); err == nil {
		t.Fatal("expected repeated-prefix error")
	}
	if _, err := NewPlanner("A", batchEdges(), []string{"e9"}); err == nil {
		t.Fatal("expected unknown-edge error")
	}
}

func TestImportBatchInvalidatesOldPlan(t *testing.T) {
	p, err := NewPlanner("A", batchEdges(), nil)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := p.PublishRemaining()
	if err != nil {
		t.Fatal(err)
	}
	batch := []Edge{
		{ID: "e5", From: "A", To: "D"},
		{ID: "e6", From: "D", To: "A"},
	}
	if err := p.ImportBatch(batch); err != nil {
		t.Fatal(err)
	}
	if p.Version() != 2 {
		t.Fatalf("version = %d, want 2", p.Version())
	}
	if err := p.ValidatePlan(stale); err != ErrStalePlan {
		t.Fatalf("got %v, want ErrStalePlan", err)
	}
	fresh, err := p.PublishRemaining()
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Version != 2 {
		t.Fatalf("plan version = %d, want 2", fresh.Version)
	}
	if len(fresh.EdgeIDs) != 6 {
		t.Fatalf("fresh plan covers %d edges, want 6", len(fresh.EdgeIDs))
	}
	if err := p.ValidatePlan(fresh); err != nil {
		t.Fatalf("fresh plan should validate: %v", err)
	}
}

func TestImportBatchRejectsDuplicates(t *testing.T) {
	p, err := NewPlanner("A", batchEdges(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ImportBatch([]Edge{{ID: "e1", From: "X", To: "Y"}}); err == nil {
		t.Fatal("expected duplicate-against-set error")
	}
	if err := p.ImportBatch([]Edge{
		{ID: "e7", From: "X", To: "Y"},
		{ID: "e7", From: "Y", To: "X"},
	}); err == nil {
		t.Fatal("expected duplicate-within-batch error")
	}
	if p.Version() != 1 {
		t.Fatalf("failed imports must not bump the version, got %d", p.Version())
	}
}

func TestCheckpointRecoverRoundTrip(t *testing.T) {
	p, err := NewPlanner("A", batchEdges(), []string{"e1", "e2"})
	if err != nil {
		t.Fatal(err)
	}
	want, err := p.PublishRemaining()
	if err != nil {
		t.Fatal(err)
	}
	cp := p.Checkpoint()

	q, err := Recover("A", batchEdges(), cp)
	if err != nil {
		t.Fatal(err)
	}
	if q.Current() != p.Current() {
		t.Fatalf("current = %q, want %q", q.Current(), p.Current())
	}
	if q.Version() != p.Version() {
		t.Fatalf("version = %d, want %d", q.Version(), p.Version())
	}
	got, err := q.PublishRemaining()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recovered plan %v, want %v", got, want)
	}
	if !reflect.DeepEqual(q.Walked(), []string{"e1", "e2"}) {
		t.Fatalf("walked = %v", q.Walked())
	}
}

func TestRecoverRejectsStrandingPrefix(t *testing.T) {
	// Walking e1,e2 first leaves e3,e4 (B<->C) unreachable from A.
	edges := []Edge{
		{ID: "e1", From: "A", To: "B"},
		{ID: "e2", From: "B", To: "A"},
		{ID: "e3", From: "B", To: "C"},
		{ID: "e4", From: "C", To: "B"},
	}
	cp := Checkpoint{Version: 1, WalkedEdgeIDs: []string{"e1", "e2"}}
	if _, err := Recover("A", edges, cp); err == nil {
		t.Fatal("expected recovery to reject a prefix that strands remaining edges")
	}
}

func TestRecoverRejectsTamperedPrefix(t *testing.T) {
	cp := Checkpoint{Version: 1, WalkedEdgeIDs: []string{"e2", "e1"}}
	if _, err := Recover("A", batchEdges(), cp); err == nil {
		t.Fatal("expected recovery to reject a prefix that does not chain from the start")
	}
}

func TestPublishRemainingFailsWhenPrefixStrandsEdges(t *testing.T) {
	// Same stranding scenario, but through the planner directly: the
	// failure must surface as a connectivity Failure.
	edges := []Edge{
		{ID: "e1", From: "A", To: "B"},
		{ID: "e2", From: "B", To: "A"},
		{ID: "e3", From: "B", To: "C"},
		{ID: "e4", From: "C", To: "B"},
	}
	p, err := NewPlanner("A", edges, []string{"e1", "e2"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.PublishRemaining()
	f, ok := err.(*Failure)
	if !ok || f.Kind != FailureConnectivity {
		t.Fatalf("got %v, want connectivity failure", err)
	}
	if want := []string{"e3", "e4"}; !reflect.DeepEqual(f.EdgeIDs, want) {
		t.Fatalf("unreachable = %v, want %v", f.EdgeIDs, want)
	}
}
