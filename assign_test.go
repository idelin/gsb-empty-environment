package flowlb

import (
	"fmt"
	"math"
	"testing"
)

func testPaths() []Path {
	return []Path{
		{ID: "path-A", Weight: 3, Rate: 40},
		{ID: "path-B", Weight: 2, Rate: 25},
		{ID: "path-C", Weight: 1, Rate: 12},
	}
}

func assignN(t *testing.T, a *Assigner, n int) map[string]string {
	out := make(map[string]string, n)
	for i := 0; i < n; i++ {
		flowID := fmt.Sprintf("flow-%05d", i)
		pathID, ok := a.Assign(flowID)
		if !ok {
			t.Helper()
			t.Fatalf("Assign(%q) failed", flowID)
		}
		out[flowID] = pathID
	}
	return out
}

func TestStableAssignment(t *testing.T) {
	a := NewAssigner()
	for _, p := range testPaths() {
		a.AddPath(p)
	}
	first := assignN(t, a, 500)
	// Repeated lookups must return the same path.
	for i := 0; i < 10; i++ {
		for flowID, want := range first {
			got, _ := a.Assign(flowID)
			if got != want {
				t.Fatalf("flow %s moved from %s to %s without topology change", flowID, want, got)
			}
		}
	}
	// A fresh assigner with the same paths must reproduce the mapping.
	b := NewAssigner()
	for _, p := range testPaths() {
		b.AddPath(p)
	}
	for flowID, want := range first {
		got, _ := b.Assign(flowID)
		if got != want {
			t.Fatalf("flow %s assigned %s, want %s (not reproducible)", flowID, got, want)
		}
	}
}

func TestRemoveMigratesOnlyAffectedFlows(t *testing.T) {
	a := NewAssigner()
	for _, p := range testPaths() {
		a.AddPath(p)
	}
	before := assignN(t, a, 2000)
	onRemoved := 0
	for _, p := range before {
		if p == "path-A" {
			onRemoved++
		}
	}
	migrated, dropped := a.RemovePath("path-A")
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if migrated != onRemoved {
		t.Fatalf("migrated = %d, want exactly the %d flows that were on path-A", migrated, onRemoved)
	}
	for flowID, prev := range before {
		got, ok := a.Assign(flowID)
		if !ok {
			t.Fatalf("flow %s unassigned after removal", flowID)
		}
		if prev != "path-A" && got != prev {
			t.Fatalf("flow %s moved from %s to %s though its path was not removed", flowID, prev, got)
		}
		if got == "path-A" {
			t.Fatalf("flow %s still on removed path", flowID)
		}
	}
}

func TestZeroWeightPathGetsNoFlows(t *testing.T) {
	a := NewAssigner()
	for _, p := range testPaths() {
		a.AddPath(p)
	}
	a.AddPath(Path{ID: "path-standby", Weight: 0, Rate: 100})
	for flowID, pathID := range assignN(t, a, 2000) {
		if pathID == "path-standby" {
			t.Fatalf("flow %s assigned to weight-0 path", flowID)
		}
	}
	// A weight-0 path must not steal flows when added later either.
	if moved := a.AddPath(Path{ID: "path-standby-2", Weight: 0, Rate: 100}); moved != 0 {
		t.Fatalf("adding weight-0 path moved %d flows, want 0", moved)
	}
}

func TestAllZeroWeight(t *testing.T) {
	a := NewAssigner()
	a.AddPath(Path{ID: "path-X", Weight: 0})
	if _, ok := a.Assign("flow-1"); ok {
		t.Fatal("Assign succeeded with no positive-weight path")
	}
}

func TestWeightsProportional(t *testing.T) {
	a := NewAssigner()
	for _, p := range testPaths() {
		a.AddPath(p)
	}
	counts := map[string]int{}
	for _, pathID := range assignN(t, a, 30000) {
		counts[pathID]++
	}
	weights := map[string]float64{"path-A": 3, "path-B": 2, "path-C": 1}
	total := 0.0
	for _, w := range weights {
		total += w
	}
	for id, w := range weights {
		want := w / total
		got := float64(counts[id]) / 30000
		if math.Abs(got-want) > 0.02 {
			t.Fatalf("%s share = %.4f, want %.4f +/- 0.02", id, got, want)
		}
	}
}

func TestAddPathMinimalMigration(t *testing.T) {
	a := NewAssigner()
	for _, p := range testPaths() {
		a.AddPath(p)
	}
	before := assignN(t, a, 20000)
	moved := a.AddPath(Path{ID: "path-D", Weight: 3, Rate: 40})
	// New weight total is 9, path-D has 3, so about 1/3 of flows should move.
	frac := float64(moved) / float64(len(before))
	if math.Abs(frac-1.0/3.0) > 0.02 {
		t.Fatalf("migration fraction = %.4f, want ~0.3333", frac)
	}
	for flowID, prev := range before {
		got, _ := a.Assign(flowID)
		if got != prev && got != "path-D" {
			t.Fatalf("flow %s moved to %s; only the new path may gain flows", flowID, got)
		}
	}
}
