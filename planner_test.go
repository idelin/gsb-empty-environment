package rename

import (
	"strings"
	"testing"
)

// existsFrom returns an exists func backed by a set of paths.
func existsFrom(paths ...string) func(string) bool {
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	return func(rel string) bool { return set[rel] }
}

func TestCycleABC(t *testing.T) {
	renames := []Rename{{"a", "b"}, {"b", "c"}, {"c", "a"}}
	plan, err := Build(renames, existsFrom("a", "b", "c"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Temps) != 1 {
		t.Fatalf("temps = %v, want exactly 1", plan.Temps)
	}
	if len(plan.Ops) != 4 { // park + 2 unwinds + place
		t.Fatalf("ops = %d, want 4: %v", len(plan.Ops), plan.Ops)
	}
	initial := map[string]string{"a": "A", "b": "B", "c": "C"}
	if err := Verify(initial, plan.Ops, renames); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestSelfRenameIsNoOp(t *testing.T) {
	plan, err := Build([]Rename{{"a", "a"}}, existsFrom("a"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Ops) != 0 || len(plan.Temps) != 0 {
		t.Fatalf("self-rename produced work: %+v", plan)
	}
	initial := map[string]string{"a": "A"}
	if err := Verify(initial, plan.Ops, []Rename{{"a", "a"}}); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestSelfRenameMixedWithMove(t *testing.T) {
	renames := []Rename{{"keep", "keep"}, {"old", "new"}}
	plan, err := Build(renames, existsFrom("keep", "old"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Ops) != 1 || plan.Ops[0] != (Op{Src: "old", Dst: "new"}) {
		t.Fatalf("ops = %v", plan.Ops)
	}
	initial := map[string]string{"keep": "K", "old": "O"}
	if err := Verify(initial, plan.Ops, renames); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestMultipleCyclesAndChain(t *testing.T) {
	renames := []Rename{
		{"a", "b"}, {"b", "a"}, // 2-cycle
		{"x", "y"}, {"y", "z"}, {"z", "x"}, // 3-cycle
		{"p", "q"}, {"q", "free"}, // chain into a free slot
	}
	plan, err := Build(renames, existsFrom("a", "b", "x", "y", "z", "p", "q"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Temps) != 2 {
		t.Fatalf("temps = %v, want exactly 2 (one per cycle)", plan.Temps)
	}
	// 2-cycle: 3 ops; 3-cycle: 4 ops; chain: 2 ops.
	if len(plan.Ops) != 9 {
		t.Fatalf("ops = %d, want 9: %v", len(plan.Ops), plan.Ops)
	}
	initial := map[string]string{
		"a": "A", "b": "B", "x": "X", "y": "Y", "z": "Z", "p": "P", "q": "Q",
	}
	if err := Verify(initial, plan.Ops, renames); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestChainIntoVacatedSlot(t *testing.T) {
	// c -> a where a is itself moved away: no temp needed, order matters.
	renames := []Rename{{"a", "out"}, {"c", "a"}}
	plan, err := Build(renames, existsFrom("a", "c"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Temps) != 0 {
		t.Fatalf("temps = %v, want none", plan.Temps)
	}
	initial := map[string]string{"a": "A", "c": "C"}
	if err := Verify(initial, plan.Ops, renames); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestOccupiedTargetFails(t *testing.T) {
	// "b" exists on disk but does not participate in the renames.
	_, err := Build([]Rename{{"a", "b"}}, existsFrom("a", "b"))
	if err == nil || !strings.Contains(err.Error(), "occupied") {
		t.Fatalf("err = %v, want occupied-target error", err)
	}
}

func TestOccupiedTargetOKWhenParticipant(t *testing.T) {
	// "b" exists but is itself renamed away: allowed.
	plan, err := Build([]Rename{{"a", "b"}, {"b", "c"}}, existsFrom("a", "b"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	initial := map[string]string{"a": "A", "b": "B"}
	if err := Verify(initial, plan.Ops, []Rename{{"a", "b"}, {"b", "c"}}); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestTempNameAvoidsConflicts(t *testing.T) {
	// ".rename-tmp-0" is taken by an unrelated file; the planner must
	// pick a different temp name.
	renames := []Rename{{"a", "b"}, {"b", "a"}}
	plan, err := Build(renames, existsFrom("a", "b", ".rename-tmp-0"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Temps) != 1 || plan.Temps[0] == ".rename-tmp-0" {
		t.Fatalf("temps = %v, want a non-conflicting name", plan.Temps)
	}
	initial := map[string]string{"a": "A", "b": "B", ".rename-tmp-0": "T"}
	if err := Verify(initial, plan.Ops, renames); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestDuplicateSourceFails(t *testing.T) {
	_, err := Build([]Rename{{"a", "x"}, {"a", "y"}}, existsFrom("a"))
	if err == nil || !strings.Contains(err.Error(), "duplicate source") {
		t.Fatalf("err = %v", err)
	}
}

func TestDuplicateTargetFails(t *testing.T) {
	_, err := Build([]Rename{{"a", "x"}, {"b", "x"}}, existsFrom("a", "b"))
	if err == nil || !strings.Contains(err.Error(), "share target") {
		t.Fatalf("err = %v", err)
	}
}

func TestEscapingAndAbsolutePathsFail(t *testing.T) {
	for _, r := range []Rename{
		{"../evil", "x"},
		{"x", "../evil"},
		{"/abs/path", "x"},
		{"", "x"},
	} {
		if _, err := Build([]Rename{r}, existsFrom()); err == nil {
			t.Fatalf("Build(%+v) succeeded, want error", r)
		}
	}
}

func TestSubdirectoryPaths(t *testing.T) {
	// Paths in subdirectories are interpreted relative to the base dir.
	renames := []Rename{{"dir/a", "dir/b"}, {"dir/b", "dir/a"}}
	plan, err := Build(renames, existsFrom("dir/a", "dir/b"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Temps) != 1 {
		t.Fatalf("temps = %v", plan.Temps)
	}
	initial := map[string]string{"dir/a": "A", "dir/b": "B"}
	if err := Verify(initial, plan.Ops, renames); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
