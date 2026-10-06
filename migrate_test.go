package migrate

import (
	"reflect"
	"testing"
)

func normSwapAB() Normalizer {
	return Normalizer{CharMap: map[rune]string{'a': "b", 'b': "a"}}
}

func assertPaths(t *testing.T, tr *Tree, want ...string) {
	t.Helper()
	got := tr.Paths()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func assertContent(t *testing.T, tr *Tree, path, want string) {
	t.Helper()
	n := tr.Find(path)
	if n == nil {
		t.Fatalf("missing node %q", path)
	}
	if string(n.Content) != want {
		t.Fatalf("content of %q = %q, want %q", path, n.Content, want)
	}
}

// Two files whose normalized names are each other's original names must be
// swapped, not overwritten.
func TestSwapNames(t *testing.T) {
	tr := NewTree()
	tr.Add("/aaa", false).Content = []byte("A")
	tr.Add("/bbb", false).Content = []byte("B")

	if err := Apply(tr, normSwapAB()); err != nil {
		t.Fatal(err)
	}
	assertPaths(t, tr, "/aaa", "/bbb")
	assertContent(t, tr, "/aaa", "B")
	assertContent(t, tr, "/bbb", "A")
}

// A directory swapping names with its own child, across levels.
func TestSwapAcrossLevels(t *testing.T) {
	tr := NewTree()
	tr.Add("/x/y", false).Content = []byte("child")
	tr.Add("/top", false).Content = []byte("top")
	norm := Normalizer{CharMap: map[rune]string{'x': "y", 'y': "x"}}

	if err := Apply(tr, norm); err != nil {
		t.Fatal(err)
	}
	assertPaths(t, tr, "/top", "/y", "/y/x")
	assertContent(t, tr, "/y/x", "child")
}

// A directory normalizing onto an existing file that does not move: the
// conflict group is ordered by original path, so "/a" keeps the name and
// the unmoved file "/b" gets a stable suffix instead of being overwritten.
func TestAncestorCollisionWithUnmovedFile(t *testing.T) {
	tr := NewTree()
	tr.Add("/a/f.txt", false).Content = []byte("inside")
	tr.Add("/b", false).Content = []byte("occupant")
	norm := Normalizer{CharMap: map[rune]string{'a': "b"}}

	if err := Apply(tr, norm); err != nil {
		t.Fatal(err)
	}
	assertPaths(t, tr, "/b", "/b/f.txt", "/b~2")
	assertContent(t, tr, "/b/f.txt", "inside")
	assertContent(t, tr, "/b~2", "occupant")
}

// Three files normalizing to the same lower-cased name form one conflict
// group, ordered by original path with stable suffixes.
func TestCollisionGroupStableSuffixes(t *testing.T) {
	tr := NewTree()
	tr.Add("/REPORT.txt", false).Content = []byte("1")
	tr.Add("/Report.TXT", false).Content = []byte("2")
	tr.Add("/report.txt", false).Content = []byte("3")

	if err := Apply(tr, Normalizer{Case: CaseLower}); err != nil {
		t.Fatal(err)
	}
	assertPaths(t, tr, "/report.txt", "/report~2.txt", "/report~3.txt")
	assertContent(t, tr, "/report.txt", "1")
	assertContent(t, tr, "/report~2.txt", "2")
	assertContent(t, tr, "/report~3.txt", "3")
}

// Renaming a directory rewrites the final paths of all descendants.
func TestDirRenameAffectsDescendants(t *testing.T) {
	tr := NewTree()
	tr.Add("/Dir/Sub/File.TXT", false).Content = []byte("data")

	if err := Apply(tr, Normalizer{Case: CaseLower}); err != nil {
		t.Fatal(err)
	}
	assertPaths(t, tr, "/dir", "/dir/sub", "/dir/sub/file.txt")
	assertContent(t, tr, "/dir/sub/file.txt", "data")
}

// Ancestor-level collision: dir "/a" normalizes onto the existing dir
// "/b", which is suffixed (with its subtree) instead of overwritten.
func TestDescendantCollisionViaAncestor(t *testing.T) {
	tr := NewTree()
	tr.Add("/a/c.txt", false).Content = []byte("from-a")
	tr.Add("/b/c.txt", false).Content = []byte("from-b")
	norm := Normalizer{CharMap: map[rune]string{'a': "b"}}

	if err := Apply(tr, norm); err != nil {
		t.Fatal(err)
	}
	assertPaths(t, tr, "/b", "/b/c.txt", "/b~2", "/b~2/c.txt")
	assertContent(t, tr, "/b/c.txt", "from-a")
	assertContent(t, tr, "/b~2/c.txt", "from-b")
}

// Char mapping plus case rule compose: map first, then case folding.
func TestCharMapAndCaseCompose(t *testing.T) {
	tr := NewTree()
	tr.Add("/My File.txt", false).Content = []byte("x")
	norm := Normalizer{CharMap: map[rune]string{' ': "_"}, Case: CaseLower}

	if err := Apply(tr, norm); err != nil {
		t.Fatal(err)
	}
	assertPaths(t, tr, "/my_file.txt")
	assertContent(t, tr, "/my_file.txt", "x")
}

// Already-normalized trees are untouched and leave no journal behind.
func TestNoOp(t *testing.T) {
	tr := NewTree()
	tr.Add("/ok/file.txt", false).Content = []byte("x")

	if err := Apply(tr, Normalizer{Case: CaseLower}); err != nil {
		t.Fatal(err)
	}
	assertPaths(t, tr, "/ok", "/ok/file.txt")
}

// A normalization producing an empty name is rejected.
func TestInvalidNormalizedName(t *testing.T) {
	tr := NewTree()
	tr.Add("/a", false)
	norm := Normalizer{CharMap: map[rune]string{'a': ""}}

	if err := Apply(tr, norm); err == nil {
		t.Fatal("expected error for empty normalized name")
	}
	assertPaths(t, tr, "/a")
}

// crashFixture builds a tree exercising swaps, dir renames and collisions.
func crashFixture() *Tree {
	tr := NewTree()
	tr.Add("/aaa", false).Content = []byte("A")
	tr.Add("/bbb", false).Content = []byte("B")
	tr.Add("/Dir/Sub/File.TXT", false).Content = []byte("data")
	tr.Add("/x/f.txt", false).Content = []byte("inside")
	tr.Add("/y", false).Content = []byte("occupant")
	return tr
}

func crashNorm() Normalizer {
	return Normalizer{
		CharMap: map[rune]string{'a': "b", 'b': "a", 'x': "y"},
		Case:    CaseLower,
	}
}

// "/aaa"<->"/bbb" swap, "/Dir" lower-cased with descendants ("Sub" ->
// "sua" via the b->a mapping), dir "/x" moves onto unmoved "/y" (suffix).
var crashWant = []string{
	"/aaa", "/bbb", "/dir", "/dir/sua", "/dir/sua/file.txt",
	"/y", "/y/f.tyt", "/y~2",
}

func assertCrashResult(t *testing.T, tr *Tree) {
	t.Helper()
	assertPaths(t, tr, crashWant...)
	assertContent(t, tr, "/aaa", "B")
	assertContent(t, tr, "/bbb", "A")
	assertContent(t, tr, "/dir/sua/file.txt", "data")
	assertContent(t, tr, "/y/f.tyt", "inside")
	assertContent(t, tr, "/y~2", "occupant")
}

func TestCleanRun(t *testing.T) {
	tr := crashFixture()
	if err := Apply(tr, crashNorm()); err != nil {
		t.Fatal(err)
	}
	assertCrashResult(t, tr)
}

// Crash after the journal is written but before any rename happened.
func TestRecoverBeforePhase1(t *testing.T) {
	tr := crashFixture()
	plan, err := ComputePlan(tr, crashNorm())
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.WriteJournal(tr); err != nil {
		t.Fatal(err)
	}
	if err := Recover(tr); err != nil {
		t.Fatal(err)
	}
	assertCrashResult(t, tr)
}

// Crash midway through phase 1: only the first two nodes sit at temporary
// names (one of them a directory whose child has not moved yet).
func TestRecoverMidPhase1(t *testing.T) {
	tr := crashFixture()
	plan, err := ComputePlan(tr, crashNorm())
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.WriteJournal(tr); err != nil {
		t.Fatal(err)
	}
	origToTemp := map[string]string{}
	for _, r := range plan.Renames {
		origToTemp[r.Orig] = r.Temp
	}
	for _, r := range plan.Renames[:2] {
		n := findNode(tr, r.Orig, origToTemp)
		if n == nil {
			t.Fatalf("node %q not found", r.Orig)
		}
		if err := n.Rename(r.Temp); err != nil {
			t.Fatal(err)
		}
	}
	if err := Recover(tr); err != nil {
		t.Fatal(err)
	}
	assertCrashResult(t, tr)
}

// Crash between the phases: every node is at its temporary name.
func TestRecoverBetweenPhases(t *testing.T) {
	tr := crashFixture()
	plan, err := ComputePlan(tr, crashNorm())
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.WriteJournal(tr); err != nil {
		t.Fatal(err)
	}
	j, err := readJournal(tr)
	if err != nil {
		t.Fatal(err)
	}
	if err := runPhase1(tr, j); err != nil {
		t.Fatal(err)
	}
	if err := Recover(tr); err != nil {
		t.Fatal(err)
	}
	assertCrashResult(t, tr)
}

// Crash midway through phase 2: some nodes already carry final names, the
// rest still sit at temporary names. Recovery must skip finished entries.
func TestRecoverMidPhase2(t *testing.T) {
	tr := crashFixture()
	plan, err := ComputePlan(tr, crashNorm())
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.WriteJournal(tr); err != nil {
		t.Fatal(err)
	}
	j, err := readJournal(tr)
	if err != nil {
		t.Fatal(err)
	}
	if err := runPhase1(tr, j); err != nil {
		t.Fatal(err)
	}
	// Finish the first three phase-2 renames by hand, then "crash".
	byOrig := map[string]journalEntry{}
	for _, e := range j.Entries {
		byOrig[e.Orig] = e
	}
	for _, e := range j.Entries[:3] {
		n := findNode(tr, tempPath(e, byOrig), nil)
		if n == nil {
			t.Fatalf("temp node for %q not found", e.Orig)
		}
		if err := n.Rename(e.Final); err != nil {
			t.Fatal(err)
		}
	}
	if err := Recover(tr); err != nil {
		t.Fatal(err)
	}
	assertCrashResult(t, tr)
}

// Recovery is idempotent: a second Recover on a finished tree is a no-op.
func TestRecoverIdempotent(t *testing.T) {
	tr := crashFixture()
	if err := Apply(tr, crashNorm()); err != nil {
		t.Fatal(err)
	}
	if err := Recover(tr); err != nil {
		t.Fatal(err)
	}
	assertCrashResult(t, tr)
}
