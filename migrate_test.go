package migrate

import (
	"bytes"
	"strings"
	"testing"
)

func mustPaths(t *testing.T, tr *Tree, want []string) {
	t.Helper()
	got := tr.Paths()
	if len(got) != len(want) {
		t.Fatalf("paths mismatch\n got: %v\nwant: %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paths mismatch\n got: %v\nwant: %v", got, want)
		}
	}
}

func TestNormalizeCharMapAndCase(t *testing.T) {
	rules := Rules{
		Chars: map[rune]rune{' ': '_', ':': '-'},
		Case:  CaseLower,
	}
	if got := rules.Normalize("My File: Draft.TXT"); got != "my_file-_draft.txt" {
		t.Fatalf("unexpected normalization: %q", got)
	}
	upper := Rules{Case: CaseUpper}
	if got := upper.Normalize("readMe.md"); got != "README.MD" {
		t.Fatalf("unexpected upper-case normalization: %q", got)
	}
}

func TestSwapNamesNeedsTwoPhase(t *testing.T) {
	tr := NewTree()
	tr.Add("/a", false)
	tr.Add("/b", false)
	rules := Rules{Chars: map[rune]rune{'a': 'b', 'b': 'a'}}

	plan := BuildPlan(tr, rules)
	if len(plan.Ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(plan.Ops))
	}
	// A naive single-phase rename would overwrite; the plan must go
	// through temp names and the tree must end swapped.
	if err := plan.Apply(tr); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	mustPaths(t, tr, []string{"/a", "/b"})
	// The node originally named "a" must now live at "/b" and vice versa.
	if n := tr.Find("/b"); n == nil || n.ID != 1 {
		t.Fatalf("swap did not move node 1 to /b")
	}
	if n := tr.Find("/a"); n == nil || n.ID != 2 {
		t.Fatalf("swap did not move node 2 to /a")
	}
}

func TestCollisionGroupStableSuffix(t *testing.T) {
	tr := NewTree()
	tr.Add("/a b.txt", false)
	tr.Add("/a-b.txt", false)
	tr.Add("/a_b.txt", false)
	rules := Rules{Chars: map[rune]rune{' ': '_', '-': '_'}}

	plan := BuildPlan(tr, rules)
	if err := plan.Apply(tr); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	// Sorted by original path: "a b.txt" < "a-b.txt" < "a_b.txt".
	mustPaths(t, tr, []string{"/a_b.txt", "/a_b~2.txt", "/a_b~3.txt"})
	if n := tr.Find("/a_b.txt"); n == nil || n.Name != "a_b.txt" {
		t.Fatalf("first in sorted order should keep the bare name")
	}
}

func TestDirectoryRenameMovesDescendants(t *testing.T) {
	tr := NewTree()
	tr.Add("/My Dir/Inner File.txt", false)
	tr.Add("/My Dir/Sub Dir/deep.txt", false)
	rules := Rules{Chars: map[rune]rune{' ': '_'}, Case: CaseLower}

	plan := BuildPlan(tr, rules)
	if err := plan.Apply(tr); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	mustPaths(t, tr, []string{
		"/my_dir",
		"/my_dir/inner_file.txt",
		"/my_dir/sub_dir",
		"/my_dir/sub_dir/deep.txt",
	})
}

func TestAncestorCollision(t *testing.T) {
	tr := NewTree()
	tr.Add("/Docs/a.txt", false)
	tr.Add("/docs/b.txt", false)
	rules := Rules{Case: CaseLower}

	plan := BuildPlan(tr, rules)
	if err := plan.Apply(tr); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	// "Docs" sorts before "docs", so it keeps the normalized name and
	// "docs" (with its whole subtree) gets the stable suffix.
	mustPaths(t, tr, []string{
		"/docs",
		"/docs/a.txt",
		"/docs~2",
		"/docs~2/b.txt",
	})
	if n := tr.Find("/docs/a.txt"); n == nil {
		t.Fatalf("child of winning directory should follow its parent")
	}
}

func TestDirAndFileCollisionAcrossTypes(t *testing.T) {
	tr := NewTree()
	tr.Add("/LOGS", true)
	tr.Add("/logs.txt", false)
	rules := Rules{Case: CaseLower}
	// No collision: "logs" != "logs.txt".
	plan := BuildPlan(tr, rules)
	if err := plan.Apply(tr); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	mustPaths(t, tr, []string{"/logs", "/logs.txt"})
}

func TestNoOverwriteOnRename(t *testing.T) {
	tr := NewTree()
	tr.Add("/x", false)
	tr.Add("/y", false)
	err := tr.Rename(tr.Find("/x"), "y")
	if err == nil || !strings.Contains(err.Error(), ErrCollision.Error()) {
		t.Fatalf("expected collision error, got %v", err)
	}
	mustPaths(t, tr, []string{"/x", "/y"})
}

func TestInterruptAndRecover(t *testing.T) {
	build := func() *Tree {
		tr := NewTree()
		tr.Add("/A Dir/One File.txt", false)
		tr.Add("/A Dir/Two.txt", false)
		tr.Add("/b dir/Three.TXT", false)
		tr.Add("/swap1", false)
		tr.Add("/swap2", false)
		return tr
	}
	rules := Rules{
		Chars: map[rune]rune{' ': '_', '1': '2', '2': '1'},
		Case:  CaseLower,
	}
	want := []string{
		"/a_dir",
		"/a_dir/one_file.txt",
		"/a_dir/two.txt",
		"/b_dir",
		"/b_dir/three.txt",
		"/swap1",
		"/swap2",
	}

	// Clean run establishes the expected end state.
	clean := build()
	plan := BuildPlan(clean, rules)
	if err := plan.Apply(clean); err != nil {
		t.Fatalf("clean apply failed: %v", err)
	}
	mustPaths(t, clean, want)

	totalOps := 2 * len(plan.Ops)
	if totalOps == 0 {
		t.Fatalf("expected a non-empty plan")
	}

	// Interrupt at every possible point and recover each time.
	for cut := 1; cut <= totalOps; cut++ {
		tr := build()
		p := BuildPlan(tr, rules)
		tr.MaxOps = cut
		if err := p.Apply(tr); err != ErrInjected {
			t.Fatalf("cut=%d: expected injected failure, got %v", cut, err)
		}
		tr.MaxOps = 0
		if err := p.Recover(tr); err != nil {
			t.Fatalf("cut=%d: recover failed: %v", cut, err)
		}
		mustPaths(t, tr, want)
		// Recovering again must be a no-op.
		if err := p.Recover(tr); err != nil {
			t.Fatalf("cut=%d: second recover failed: %v", cut, err)
		}
		mustPaths(t, tr, want)
	}
}

func TestRecoverFromSavedPlan(t *testing.T) {
	tr := NewTree()
	tr.Add("/Old Name/Child File.txt", false)
	rules := Rules{Chars: map[rune]rune{' ': '_'}, Case: CaseLower}

	plan := BuildPlan(tr, rules)
	var buf bytes.Buffer
	if err := plan.Save(&buf); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Interrupt after phase 1 only.
	tr.MaxOps = len(plan.Ops)
	if err := plan.Apply(tr); err != ErrInjected {
		t.Fatalf("expected injected failure, got %v", err)
	}
	tr.MaxOps = 0

	// A fresh process loads the plan from disk and finishes the job.
	loaded, err := LoadPlan(&buf)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if err := loaded.Recover(tr); err != nil {
		t.Fatalf("recover failed: %v", err)
	}
	mustPaths(t, tr, []string{"/old_name", "/old_name/child_file.txt"})
}

func TestAlreadyNormalizedIsNoOp(t *testing.T) {
	tr := NewTree()
	tr.Add("/plain/file.txt", false)
	plan := BuildPlan(tr, Rules{Case: CaseLower})
	if len(plan.Ops) != 0 {
		t.Fatalf("expected empty plan, got %v", plan.Ops)
	}
	if err := plan.Apply(tr); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	mustPaths(t, tr, []string{"/plain", "/plain/file.txt"})
}
