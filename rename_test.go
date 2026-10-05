package rename

import (
	"errors"
	"fmt"
	"io/ioutil"
	"math/rand"
	"path/filepath"
	"sort"
	"testing"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "rename-test")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// memFS is an in-memory FS mapping path -> file identity. failAt, when
// positive, makes the failAt-th Rename call fail without touching state.
type memFS struct {
	files  map[string]string
	calls  int
	failAt int
}

func newMemFS(paths ...string) *memFS {
	m := &memFS{files: make(map[string]string)}
	for _, p := range paths {
		m.files[p] = "id:" + p
	}
	return m
}

func (m *memFS) Rename(from, to string) error {
	m.calls++
	if m.failAt > 0 && m.calls == m.failAt {
		return errors.New("injected failure")
	}
	if _, ok := m.files[from]; !ok {
		return fmt.Errorf("no such file: %s", from)
	}
	if _, ok := m.files[to]; ok {
		return fmt.Errorf("target exists: %s", to)
	}
	m.files[to] = m.files[from]
	delete(m.files, from)
	return nil
}

func (m *memFS) Exists(p string) bool {
	_, ok := m.files[p]
	return ok
}

func existsSet(paths ...string) func(string) bool {
	s := make(map[string]bool, len(paths))
	for _, p := range paths {
		s[p] = true
	}
	return func(p string) bool { return s[p] }
}

func TestSelfRenameIsNoOp(t *testing.T) {
	plan, err := PlanMoves([]Move{{"a", "a"}}, existsSet("a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 0 || len(plan.Temps) != 0 {
		t.Fatalf("self-rename should plan nothing, got %+v", plan)
	}
	if err := Verify(map[string]string{"a": "id:a"}, []Move{{"a", "a"}}, plan); err != nil {
		t.Fatal(err)
	}
}

func TestChainNeedsNoTemp(t *testing.T) {
	moves := []Move{{"a", "b"}, {"b", "c"}}
	plan, err := PlanMoves(moves, existsSet("a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Temps) != 0 {
		t.Fatalf("chain needs no temp, got %v", plan.Temps)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(plan.Steps))
	}
	// b -> c must happen before a -> b.
	if plan.Steps[0] != (Step{From: "b", To: "c"}) || plan.Steps[1] != (Step{From: "a", To: "b"}) {
		t.Fatalf("bad order: %+v", plan.Steps)
	}
	if err := Verify(map[string]string{"a": "id:a", "b": "id:b"}, moves, plan); err != nil {
		t.Fatal(err)
	}
}

func TestSingleCycleUsesOneTemp(t *testing.T) {
	moves := []Move{{"a", "b"}, {"b", "c"}, {"c", "a"}}
	plan, err := PlanMoves(moves, existsSet("a", "b", "c"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Temps) != 1 {
		t.Fatalf("one cycle needs exactly one temp, got %v", plan.Temps)
	}
	if len(plan.Steps) != 4 {
		t.Fatalf("3-cycle needs 4 steps, got %d", len(plan.Steps))
	}
	initial := map[string]string{"a": "id:a", "b": "id:b", "c": "id:c"}
	if err := Verify(initial, moves, plan); err != nil {
		t.Fatal(err)
	}
	final, err := Simulate(initial, plan)
	if err != nil {
		t.Fatal(err)
	}
	if final["b"] != "id:a" || final["c"] != "id:b" || final["a"] != "id:c" {
		t.Fatalf("identities not preserved: %v", final)
	}
}

func TestMultipleCyclesAndChain(t *testing.T) {
	moves := []Move{
		{"a", "b"}, {"b", "a"}, // 2-cycle
		{"x", "y"}, {"y", "z"}, {"z", "x"}, // 3-cycle
		{"p", "q"}, {"q", "r"}, // chain into free r
		{"s", "s"}, // self-rename no-op
	}
	plan, err := PlanMoves(moves, existsSet("a", "b", "x", "y", "z", "p", "q", "s"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Temps) != 2 {
		t.Fatalf("two cycles need exactly two temps, got %v", plan.Temps)
	}
	if plan.Temps[0] == plan.Temps[1] {
		t.Fatalf("temp names collide: %v", plan.Temps)
	}
	// 2 steps (chain) + 3 steps (2-cycle) + 4 steps (3-cycle).
	if len(plan.Steps) != 9 {
		t.Fatalf("want 9 steps, got %d", len(plan.Steps))
	}
	initial := map[string]string{}
	for _, p := range []string{"a", "b", "x", "y", "z", "p", "q", "s", "untouched"} {
		initial[p] = "id:" + p
	}
	if err := Verify(initial, moves, plan); err != nil {
		t.Fatal(err)
	}
}

func TestOccupiedTargetFails(t *testing.T) {
	_, err := PlanMoves([]Move{{"a", "b"}}, existsSet("a", "b"))
	if err == nil {
		t.Fatal("expected error: b occupied by non-participating file")
	}
}

func TestMissingSourceFails(t *testing.T) {
	if _, err := PlanMoves([]Move{{"a", "b"}}, existsSet()); err == nil {
		t.Fatal("expected error: source missing")
	}
}

func TestDuplicateSourceAndTargetFail(t *testing.T) {
	if _, err := PlanMoves([]Move{{"a", "c"}, {"a", "d"}}, existsSet("a")); err == nil {
		t.Fatal("expected error: duplicate source")
	}
	if _, err := PlanMoves([]Move{{"a", "c"}, {"b", "c"}}, existsSet("a", "b")); err == nil {
		t.Fatal("expected error: duplicate target")
	}
}

func TestTempNameAvoidsExistingFiles(t *testing.T) {
	moves := []Move{{"a", "b"}, {"b", "a"}}
	plan, err := PlanMoves(moves, existsSet("a", "b", ".rename-tmp-0-0"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Temps) != 1 || plan.Temps[0] == ".rename-tmp-0-0" {
		t.Fatalf("temp must avoid existing file, got %v", plan.Temps)
	}
	initial := map[string]string{"a": "id:a", "b": "id:b", ".rename-tmp-0-0": "id:keep"}
	if err := Verify(initial, moves, plan); err != nil {
		t.Fatal(err)
	}
}

func TestPartialFailureRecovery(t *testing.T) {
	moves := []Move{{"a", "b"}, {"b", "c"}, {"c", "a"}}
	plan, err := PlanMoves(moves, existsSet("a", "b", "c"))
	if err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(tempDir(t), "rename.journal")

	fsys := newMemFS("a", "b", "c")
	fsys.failAt = 3 // crash on the third rename
	j, err := OpenJournal(journalPath, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := Execute(plan, fsys, j); err == nil {
		t.Fatal("expected injected failure")
	}
	done := 0
	for i := range plan.Steps {
		if j.Done(i) {
			done++
		}
	}
	if done != 2 {
		t.Fatalf("journal should record 2 done steps, got %d", done)
	}

	// Recover: reopen the journal from disk, resume with a healthy FS.
	fsys.failAt = 0
	callsBefore := fsys.calls
	j2, err := OpenJournal(journalPath, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := Execute(plan, fsys, j2); err != nil {
		t.Fatal(err)
	}
	if resumed := fsys.calls - callsBefore; resumed != len(plan.Steps)-2 {
		t.Fatalf("resume should run %d steps, ran %d", len(plan.Steps)-2, resumed)
	}

	want := map[string]string{"a": "id:c", "b": "id:a", "c": "id:b"}
	if len(fsys.files) != len(want) {
		t.Fatalf("final state %v, want %v", fsys.files, want)
	}
	for p, id := range want {
		if fsys.files[p] != id {
			t.Fatalf("final state %v, want %v", fsys.files, want)
		}
	}
}

func TestJournalRejectsMismatchedPlan(t *testing.T) {
	dir := tempDir(t)
	moves := []Move{{"a", "b"}, {"b", "a"}}
	plan, err := PlanMoves(moves, existsSet("a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenJournal(filepath.Join(dir, "j"), plan); err != nil {
		t.Fatal(err)
	}
	other, err := PlanMoves([]Move{{"x", "y"}, {"y", "x"}}, existsSet("x", "y"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenJournal(filepath.Join(dir, "j"), other); err == nil {
		t.Fatal("expected error: journal does not match plan")
	}
}

// TestRandomPermutations cross-checks the planner against the independent
// simulation on many random permutations, and confirms the temp count
// equals the number of cycles (the provable minimum).
func TestRandomPermutations(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		n := 1 + rng.Intn(12)
		names := make([]string, n)
		initial := make(map[string]string)
		for i := range names {
			names[i] = fmt.Sprintf("f%02d", i)
			initial[names[i]] = "id:" + names[i]
		}
		perm := rng.Perm(n)
		var moves []Move
		for i, j := range perm {
			moves = append(moves, Move{From: names[i], To: names[j]})
		}
		plan, err := PlanMoves(moves, existsSet(names...))
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if err := Verify(initial, moves, plan); err != nil {
			t.Fatalf("trial %d perm %v: %v", trial, perm, err)
		}
		// Count non-trivial cycles in the permutation.
		seen := make([]bool, n)
		cycles := 0
		for i := 0; i < n; i++ {
			if seen[i] || perm[i] == i {
				continue
			}
			cycles++
			for j := i; !seen[j]; j = perm[j] {
				seen[j] = true
			}
		}
		if len(plan.Temps) != cycles {
			t.Fatalf("trial %d: temps %d, cycles %d", trial, len(plan.Temps), cycles)
		}
	}
}

// TestExecuteOnMemFS runs a full plan end to end on the in-memory FS.
func TestExecuteOnMemFS(t *testing.T) {
	moves := []Move{{"a", "b"}, {"b", "c"}, {"c", "a"}, {"keep", "keep"}}
	plan, err := PlanMoves(moves, existsSet("a", "b", "c", "keep"))
	if err != nil {
		t.Fatal(err)
	}
	fsys := newMemFS("a", "b", "c", "keep")
	j, err := OpenJournal(filepath.Join(tempDir(t), "j"), plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := Execute(plan, fsys, j); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(fsys.files))
	for p, id := range fsys.files {
		got = append(got, p+"="+id)
	}
	sort.Strings(got)
	want := []string{"a=id:c", "b=id:a", "c=id:b", "keep=id:keep"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
