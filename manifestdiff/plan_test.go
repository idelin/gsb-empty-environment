package manifestdiff

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// spec builds a manifest, its virtual tree, and a content store from a
// compact description: "dir", "f:<content>", or "l:<target>". Ancestor
// directories are added automatically.
func spec(t *testing.T, entries map[string]string) (*Manifest, *VirtualTree, MapStore) {
	t.Helper()
	store := MapStore{}
	all := map[string]Entry{}
	add := func(path, spec string) {
		p, err := NormalizePath(path)
		if err != nil {
			t.Fatal(err)
		}
		e := Entry{Path: p}
		switch {
		case spec == "dir":
			e.Kind = KindDir
		case strings.HasPrefix(spec, "f:"):
			e.Kind = KindFile
			content := []byte(strings.TrimPrefix(spec, "f:"))
			e.Digest = DigestOf(content)
			store[e.Digest] = content
		case strings.HasPrefix(spec, "l:"):
			e.Kind = KindSymlink
			e.Target = strings.TrimPrefix(spec, "l:")
		default:
			t.Fatalf("bad spec %q for %q", spec, path)
		}
		if _, dup := all[p]; dup {
			t.Fatalf("duplicate path %q", p)
		}
		all[p] = e
	}
	for path, s := range entries {
		add(path, s)
	}
	// fill in ancestor dirs
	for p := range all {
		for parent := Parent(p); parent != ""; parent = Parent(parent) {
			if _, ok := all[parent]; !ok {
				all[parent] = Entry{Path: parent, Kind: KindDir}
			}
		}
	}
	list := make([]Entry, 0, len(all))
	for _, e := range all {
		list = append(list, e)
	}
	m, err := NewManifest(list)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := BuildTree(m, store)
	if err != nil {
		t.Fatal(err)
	}
	return m, tree, store
}

// countingExecutor counts mutating calls to verify idempotency.
type countingExecutor struct {
	inner     Executor
	mutations int
}

func (c *countingExecutor) Stat(p string) (Entry, bool) { return c.inner.Stat(p) }
func (c *countingExecutor) Mkdir(p string) error        { c.mutations++; return c.inner.Mkdir(p) }
func (c *countingExecutor) WriteFile(p string, b []byte) error {
	c.mutations++
	return c.inner.WriteFile(p, b)
}
func (c *countingExecutor) Symlink(p, target string) error {
	c.mutations++
	return c.inner.Symlink(p, target)
}
func (c *countingExecutor) Remove(p string) error      { c.mutations++; return c.inner.Remove(p) }
func (c *countingExecutor) Rename(s, d string) error   { c.mutations++; return c.inner.Rename(s, d) }

var oldSpec = map[string]string{
	"a":          "f:old-a",       // unchanged
	"b/c":        "f:keep",        // unchanged, nested
	"b/d":        "f:will-change", // content changes
	"gone":       "f:bye",         // removed file
	"gone2/x":    "f:nested",      // removed subtree
	"d":          "f:becomes-dir", // file -> dir
	"d2/y":       "f:child",       // dir -> file
	"s":          "l:/oldtarget",  // symlink retarget
	"t":          "f:to-symlink",  // file -> symlink
	"u":          "l:/to-file",    // symlink -> file
	"deep":       "f:conflict",    // file -> ancestor dir of new deep file
	"swap1":      "f:swap-a",      // file -> dir
	"swap2/inner": "f:swap-b",     // dir -> file
}

var newSpec = map[string]string{
	"a":              "f:old-a",
	"b/c":            "f:keep",
	"b/d":            "f:changed",
	"d/inner":        "f:new nested",
	"d2":             "f:now-a-file",
	"s":              "l:/newtarget",
	"t":              "l:/target",
	"u":              "f:was-symlink",
	"deep/child/grand": "f:deep new",
	"newdir/f":       "f:brand new",
	"swap1/inner":    "f:swap-a2",
	"swap2":          "f:swap-b2",
}

func diffFixture(t *testing.T) (Plan, *VirtualTree, *Manifest, MapStore) {
	t.Helper()
	oldM, oldTree, store1 := spec(t, oldSpec)
	newM, _, store2 := spec(t, newSpec)
	store := MapStore{}
	for k, v := range store1 {
		store[k] = v
	}
	for k, v := range store2 {
		store[k] = v
	}
	return Diff(oldM, newM), oldTree, newM, store
}

func TestApplyMatchesTargetExactly(t *testing.T) {
	plan, tree, newM, store := diffFixture(t)
	if err := Apply(plan, tree, store); err != nil {
		t.Fatalf("apply: %v\nplan:\n%s", err, plan)
	}
	if got := tree.Snapshot(); !got.Equal(newM) {
		t.Fatalf("result differs from target\ngot:\n%+v", got.Sorted())
	}
}

func TestReplayIsNoOp(t *testing.T) {
	plan, tree, newM, store := diffFixture(t)
	if err := Apply(plan, tree, store); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	counter := &countingExecutor{inner: tree}
	if err := Apply(plan, counter, store); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if counter.mutations != 0 {
		t.Fatalf("replay performed %d mutations, want 0", counter.mutations)
	}
	if got := tree.Snapshot(); !got.Equal(newM) {
		t.Fatal("tree changed after replay")
	}
}

// failAtExecutor fails the n-th mutating call.
type failAtExecutor struct {
	inner Executor
	fail  int
	n     int
}

var errInjected = errors.New("injected failure")

func (f *failAtExecutor) maybe() error {
	f.n++
	if f.n-1 == f.fail {
		return errInjected
	}
	return nil
}

func (f *failAtExecutor) Stat(p string) (Entry, bool) { return f.inner.Stat(p) }
func (f *failAtExecutor) Mkdir(p string) error {
	if err := f.maybe(); err != nil {
		return err
	}
	return f.inner.Mkdir(p)
}
func (f *failAtExecutor) WriteFile(p string, b []byte) error {
	if err := f.maybe(); err != nil {
		return err
	}
	return f.inner.WriteFile(p, b)
}
func (f *failAtExecutor) Symlink(p, target string) error {
	if err := f.maybe(); err != nil {
		return err
	}
	return f.inner.Symlink(p, target)
}
func (f *failAtExecutor) Remove(p string) error {
	if err := f.maybe(); err != nil {
		return err
	}
	return f.inner.Remove(p)
}
func (f *failAtExecutor) Rename(s, d string) error {
	if err := f.maybe(); err != nil {
		return err
	}
	return f.inner.Rename(s, d)
}

func TestFailureAtAnyPointThenRecover(t *testing.T) {
	plan, baseTree, newM, store := diffFixture(t)
	failAt := 0
	for ; ; failAt++ {
		tree := baseTree.Clone()
		failing := &failAtExecutor{inner: tree, fail: failAt}
		err := Apply(plan, failing, store)
		if err == nil {
			break // ran out of mutation points
		}
		if !errors.Is(err, errInjected) {
			t.Fatalf("failAt=%d: unexpected error: %v", failAt, err)
		}
		// Recover by re-running the same plan on the partially applied tree.
		if err := Apply(plan, tree, store); err != nil {
			t.Fatalf("failAt=%d: recovery apply: %v", failAt, err)
		}
		if got := tree.Snapshot(); !got.Equal(newM) {
			t.Fatalf("failAt=%d: recovered tree differs from target", failAt)
		}
	}
	if failAt == 0 {
		t.Fatal("plan had no mutations to interrupt")
	}
	t.Logf("verified recovery from %d distinct failure points", failAt)
}

func TestRemoveGuardPreservesNewContent(t *testing.T) {
	oldM, oldTree, store := spec(t, map[string]string{"a": "f:old-content"})
	newM, _, _ := spec(t, map[string]string{})
	plan := Diff(oldM, newM)

	// Simulate new content landing at the path after planning.
	if err := oldTree.WriteFile("a", []byte("brand-new-content")); err != nil {
		t.Fatal(err)
	}
	if err := Apply(plan, oldTree, store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	e, ok := oldTree.Stat("a")
	if !ok {
		t.Fatal("new content was deleted by guarded remove")
	}
	if e.Digest != DigestOf([]byte("brand-new-content")) {
		t.Fatal("content at path changed unexpectedly")
	}
}

func TestMoveAsideGuardPreservesNewContent(t *testing.T) {
	oldM, oldTree, store := spec(t, map[string]string{"a": "f:old"})
	newM, _, store2 := spec(t, map[string]string{"a/b": "f:new"})
	for k, v := range store2 {
		store[k] = v
	}
	plan := Diff(oldM, newM)

	// The file is replaced by something the plan does not know about.
	if err := oldTree.WriteFile("a", []byte("unrelated")); err != nil {
		t.Fatal(err)
	}
	err := Apply(plan, oldTree, store)
	if err == nil {
		t.Fatal("expected apply to fail when mkdir hits preserved content")
	}
	e, ok := oldTree.Stat("a")
	if !ok || e.Digest != DigestOf([]byte("unrelated")) {
		t.Fatal("unfamiliar content was moved aside or deleted")
	}
}

func TestCleanupBackupGuardPreservesModifiedBackup(t *testing.T) {
	oldM, oldTree, store := spec(t, map[string]string{"a": "f:old"})
	newM, _, store2 := spec(t, map[string]string{"a/b": "f:new"})
	for k, v := range store2 {
		store[k] = v
	}
	plan := Diff(oldM, newM)
	if err := Apply(plan, oldTree, store); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Find the backup path used by the plan and plant modified content there.
	var backup string
	for _, op := range plan.Ops {
		if op.Kind == OpMoveAside {
			backup = op.Backup
		}
	}
	if backup == "" {
		t.Fatal("plan had no move-aside")
	}
	if err := oldTree.WriteFile(backup, []byte("edited-after-backup")); err != nil {
		t.Fatal(err)
	}
	// Replaying the plan must not delete the modified backup.
	if err := Apply(plan, oldTree, store); err != nil {
		t.Fatalf("replay: %v", err)
	}
	e, ok := oldTree.Stat(backup)
	if !ok || e.Digest != DigestOf([]byte("edited-after-backup")) {
		t.Fatal("modified backup was deleted during cleanup")
	}
}

func TestDirToFileRemovesDescendantsFirst(t *testing.T) {
	oldM, _, _ := spec(t, map[string]string{"x/y/z": "f:deep", "x/w": "f:shallow"})
	newM, _, _ := spec(t, map[string]string{"x": "f:file"})
	plan := Diff(oldM, newM)
	removeIdx, writeIdx := -1, -1
	var removeDepths []int
	for i, op := range plan.Ops {
		switch op.Kind {
		case OpRemove:
			removeIdx = i
			removeDepths = append(removeDepths, depth(op.Path))
		case OpWriteFile:
			if op.Path == "x" {
				writeIdx = i
			}
		}
	}
	for i := 1; i < len(removeDepths); i++ {
		if removeDepths[i] > removeDepths[i-1] {
			t.Fatalf("removes not deepest-first: depths %v\n%s", removeDepths, plan)
		}
	}
	if removeIdx < 0 || writeIdx < 0 || removeIdx > writeIdx {
		t.Fatalf("expected removes before write of x:\n%s", plan)
	}
}

func TestFileToDirMovesAncestorAsideBeforeCreate(t *testing.T) {
	oldM, _, _ := spec(t, map[string]string{"a": "f:file"})
	newM, _, _ := spec(t, map[string]string{"a/b/c": "f:deep"})
	plan := Diff(oldM, newM)
	moveIdx, mkdirIdx := -1, -1
	for i, op := range plan.Ops {
		if op.Kind == OpMoveAside && op.Path == "a" {
			moveIdx = i
		}
		if op.Kind == OpMkdir && op.Path == "a" {
			mkdirIdx = i
		}
	}
	if moveIdx < 0 || mkdirIdx < 0 || moveIdx > mkdirIdx {
		t.Fatalf("expected move-aside of a before mkdir a:\n%s", plan)
	}
}

func TestEmptyDiff(t *testing.T) {
	m, tree, store := spec(t, map[string]string{"a/b": "f:x", "c": "l:/t"})
	plan := Diff(m, m)
	if len(plan.Ops) != 0 {
		t.Fatalf("expected empty plan, got:\n%s", plan)
	}
	if err := Apply(plan, tree, store); err != nil {
		t.Fatal(err)
	}
	if !tree.Snapshot().Equal(m) {
		t.Fatal("tree changed")
	}
}

func TestDiffDeterministic(t *testing.T) {
	oldM, _, _ := spec(t, oldSpec)
	newM, _, _ := spec(t, newSpec)
	p1, p2 := Diff(oldM, newM), Diff(oldM, newM)
	if !reflect.DeepEqual(p1, p2) {
		t.Fatalf("non-deterministic diff:\n%s\nvs\n%s", p1, p2)
	}
}

func TestManifestValidation(t *testing.T) {
	if _, err := NewManifest([]Entry{{Path: "a/b", Kind: KindFile, Digest: "x"}}); err == nil {
		t.Fatal("expected error for missing ancestor dir")
	}
	if _, err := NewManifest([]Entry{
		{Path: "a", Kind: KindFile, Digest: "x"},
		{Path: "a/b", Kind: KindFile, Digest: "y"},
	}); err == nil {
		t.Fatal("expected error for non-dir ancestor")
	}
	if _, err := NewManifest([]Entry{
		{Path: "a", Kind: KindDir},
		{Path: "a", Kind: KindDir},
	}); err == nil {
		t.Fatal("expected error for duplicate path")
	}
}

func ExampleDiff() {
	oldM := mustManifest(
		Entry{Path: "conf", Kind: KindFile, Digest: "aaa"},
		Entry{Path: "data", Kind: KindDir},
		Entry{Path: "data/x", Kind: KindFile, Digest: "bbb"},
	)
	newM := mustManifest(
		Entry{Path: "conf", Kind: KindFile, Digest: "ccc"},
		Entry{Path: "data", Kind: KindFile, Digest: "ddd"},
	)
	fmt.Print(Diff(oldM, newM))
	// Output:
	//   0: remove data/x (file)
	//   1: remove data (dir)
	//   2: write-file conf digest=ccc
	//   3: write-file data digest=ddd
}

func mustManifest(entries ...Entry) *Manifest {
	m, err := NewManifest(entries)
	if err != nil {
		panic(err)
	}
	return m
}
