package netsec

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// TestTwentyPatchesExhaustive forces a full 2^20 subset exploration: no patch
// can block the target until the single critical action is disabled, and that
// patch is the most expensive one so cost pruning stays weak.
func TestTwentyPatchesExhaustive(t *testing.T) {
	nodes := []Node{{ID: "f0", Gate: GateAND}}
	for i := 1; i < 20; i++ {
		nodes = append(nodes, Node{
			ID:            fmt.Sprintf("f%d", i),
			Gate:          GateAND,
			Preconditions: []string{fmt.Sprintf("f%d", i-1)},
		})
	}
	nodes = append(nodes, Node{
		ID:            "target",
		Gate:          GateAND,
		Preconditions: []string{"f19"},
	})

	patches := make([]Patch, 0, 20)
	// Nineteen decoys act on isolated nodes and can never affect the target.
	for i := 0; i < 19; i++ {
		nodes = append(nodes, Node{ID: fmt.Sprintf("d%d", i), Gate: GateAND})
	}
	for i := 0; i < 19; i++ {
		patches = append(patches, Patch{
			ID:     fmt.Sprintf("patch-%02d", i),
			Action: fmt.Sprintf("d%d", i),
			Cost:   i + 1,
		})
	}
	// The unique blocker is the last and most expensive patch.
	patches = append(patches, Patch{ID: "patch-19", Action: "f19", Cost: 20})

	p := &Problem{Nodes: nodes, Target: "target", Patches: patches}

	start := time.Now()
	s, err := Solve(p)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("exhaustive solve took %s, want under 5s", elapsed)
	}

	if got, want := s.Selected, []string{"patch-19"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
	if s.Cost != 20 {
		t.Fatalf("cost = %d, want 20", s.Cost)
	}
	validateSolution(t, p, s)
}

// TestTwentyPatchesAllBlockersWorstCase makes every patch a potential blocker
// and exercises the deterministic tie-break machinery at maximum width.
func TestTwentyPatchesAllBlockersWorstCase(t *testing.T) {
	nodes := []Node{{ID: "target", Gate: GateAND}}
	patches := make([]Patch, 0, 20)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("p%02d", 19-i)
		patches = append(patches, Patch{ID: id, Action: "target", Cost: 1})
	}
	p := &Problem{Nodes: nodes, Target: "target", Patches: patches}

	s, err := Solve(p)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if got, want := s.Selected, []string{"p00"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected %v, want [%v]", got, want)
	}
	if s.Cost != 1 {
		t.Fatalf("cost = %d, want 1", s.Cost)
	}
	validateSolution(t, p, s)
}
