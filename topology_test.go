package taskpkg

import (
	"errors"
	"testing"
)

func mustTopology(t *testing.T, patches []Patch) *Topology {
	t.Helper()
	topo, err := NewTopology(patches)
	if err != nil {
		t.Fatalf("NewTopology: %v", err)
	}
	return topo
}

func TestLevelFromSegments(t *testing.T) {
	for _, tc := range []struct {
		segments int
		level    int
		ok       bool
	}{
		{1, 0, true}, {2, 1, true}, {4, 2, true}, {1024, 10, true},
		{0, 0, false}, {3, 0, false}, {-2, 0, false}, {6, 0, false},
	} {
		level, err := LevelFromSegments(tc.segments)
		if tc.ok != (err == nil) || level != tc.level {
			t.Errorf("LevelFromSegments(%d) = %d, %v", tc.segments, level, err)
		}
	}
	if _, err := LevelFromSegments(3); !errors.Is(err, ErrInvalidLevel) {
		t.Errorf("expected ErrInvalidLevel, got %v", err)
	}
}

func TestPropagateLevelsBox(t *testing.T) {
	levels := []int{0, 0, 0, 0, 0, 3}
	topo := mustTopology(t, boxPatches(levels))
	before := topo.Patches()
	topo.PropagateLevels()
	if diff := topo.MaxAdjacentLevelDiff(); diff > 1 {
		t.Fatalf("adjacent level diff %d after propagation", diff)
	}
	for i, p := range topo.Patches() {
		if p.Level < before[i].Level {
			t.Errorf("patch %d level decreased: %d -> %d", p.ID, before[i].Level, p.Level)
		}
	}
	// Face 5 (level 3) is adjacent to faces 0,1,2,3; those must reach
	// exactly 2, and face 4 (opposite) must reach 1 through its neighbours.
	want := map[int]int{5: 3, 0: 2, 1: 2, 2: 2, 3: 2, 4: 1}
	for id, minLevel := range want {
		got, _ := topo.LevelOf(id)
		if got != minLevel {
			t.Errorf("patch %d level = %d, want %d", id, got, minLevel)
		}
	}
	// Idempotent.
	topo.PropagateLevels()
	if diff := topo.MaxAdjacentLevelDiff(); diff > 1 {
		t.Fatalf("adjacent level diff %d after second propagation", diff)
	}
}

func TestPropagateLevelsChain(t *testing.T) {
	// Two quads sharing an edge with levels 0 and 3.
	topo := mustTopology(t, []Patch{
		{ID: 0, Corners: [4]int{0, 1, 2, 3}, Level: 0},
		{ID: 1, Corners: [4]int{2, 1, 4, 5}, Level: 3},
	})
	topo.PropagateLevels()
	got0, _ := topo.LevelOf(0)
	got1, _ := topo.LevelOf(1)
	if got0 != 2 || got1 != 3 {
		t.Fatalf("levels = %d, %d; want 2, 3", got0, got1)
	}
}

func TestRejectNonManifoldEdge(t *testing.T) {
	_, err := NewTopology([]Patch{
		{ID: 0, Corners: [4]int{0, 1, 2, 3}},
		{ID: 1, Corners: [4]int{2, 1, 4, 5}},
		{ID: 2, Corners: [4]int{1, 2, 6, 7}},
	})
	if !errors.Is(err, ErrNonManifold) {
		t.Fatalf("expected ErrNonManifold, got %v", err)
	}
}

func TestRejectNonOrientable(t *testing.T) {
	// Both patches traverse the shared edge 1-2 in the same direction.
	_, err := NewTopology([]Patch{
		{ID: 0, Corners: [4]int{0, 1, 2, 3}},
		{ID: 1, Corners: [4]int{1, 2, 5, 4}},
	})
	if !errors.Is(err, ErrNonOrientable) {
		t.Fatalf("expected ErrNonOrientable, got %v", err)
	}
}

func TestRejectNonManifoldVertex(t *testing.T) {
	// Two quads touching only at vertex 0: the link of 0 is disconnected.
	_, err := NewTopology([]Patch{
		{ID: 0, Corners: [4]int{0, 1, 2, 3}},
		{ID: 1, Corners: [4]int{0, 4, 5, 6}},
	})
	if !errors.Is(err, ErrNonManifold) {
		t.Fatalf("expected ErrNonManifold, got %v", err)
	}
}

func TestRejectInvalidPatches(t *testing.T) {
	if _, err := NewTopology(nil); !errors.Is(err, ErrInvalidPatch) {
		t.Errorf("empty: expected ErrInvalidPatch, got %v", err)
	}
	if _, err := NewTopology([]Patch{
		{ID: 0, Corners: [4]int{0, 1, 2, 3}},
		{ID: 0, Corners: [4]int{4, 5, 6, 7}},
	}); !errors.Is(err, ErrInvalidPatch) {
		t.Errorf("duplicate ID: expected ErrInvalidPatch, got %v", err)
	}
	if _, err := NewTopology([]Patch{
		{ID: 0, Corners: [4]int{0, 1, 2, 2}},
	}); !errors.Is(err, ErrInvalidPatch) {
		t.Errorf("repeated corner: expected ErrInvalidPatch, got %v", err)
	}
	if _, err := NewTopology([]Patch{
		{ID: 0, Corners: [4]int{0, 1, 2, 3}, Level: -1},
	}); !errors.Is(err, ErrInvalidLevel) {
		t.Errorf("negative level: expected ErrInvalidLevel, got %v", err)
	}
}
