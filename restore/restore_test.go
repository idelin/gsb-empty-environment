package restore

import (
	"strings"
	"testing"
)

func fileEntry(p, content string) Entry {
	return Entry{Path: p, Type: FileEntry, Content: []byte(content)}
}

func mustApply(t *testing.T, r *Restorer, entries ...Entry) {
	t.Helper()
	if err := r.Apply(entries); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
}

// A hard link may cite a target that appears later in the same batch; both
// paths must end up sharing one inode.
func TestForwardReferenceResolvesToSharedInode(t *testing.T) {
	r := NewRestorer()
	mustApply(t, r,
		Entry{Path: "bin/link", Type: HardlinkEntry, Target: "bin/target"},
		fileEntry("bin/target", "payload"),
	)
	tree := r.Tree()
	link, target := tree.Node("bin/link"), tree.Node("bin/target")
	if link == nil || target == nil {
		t.Fatalf("missing nodes: link=%v target=%v", link, target)
	}
	if link.Kind != FileNode || target.Kind != FileNode {
		t.Fatalf("expected file nodes, got %v and %v", link.Kind, target.Kind)
	}
	if link.Inode != target.Inode {
		t.Fatal("forward-referenced hard link did not resolve to the shared inode")
	}
	if got := tree.LinkCount("bin/target"); got != 2 {
		t.Fatalf("link count = %d, want 2", got)
	}
	if string(link.Inode.Content) != "payload" {
		t.Fatalf("inode content = %q", link.Inode.Content)
	}
}

// Chained forward references (link -> link -> file) collapse onto one inode.
func TestChainedForwardReferences(t *testing.T) {
	r := NewRestorer()
	mustApply(t, r,
		Entry{Path: "a", Type: HardlinkEntry, Target: "b"},
		Entry{Path: "b", Type: HardlinkEntry, Target: "c"},
		fileEntry("c", "x"),
	)
	tree := r.Tree()
	if tree.Node("a").Inode != tree.Node("c").Inode || tree.Node("b").Inode != tree.Node("c").Inode {
		t.Fatal("chained links did not converge on one inode")
	}
	if got := tree.LinkCount("c"); got != 3 {
		t.Fatalf("link count = %d, want 3", got)
	}
}

// A hard link to a symbolic link yields an independent symbolic link with
// the same text, never a shared inode.
func TestSymlinkRemainsIndependentContent(t *testing.T) {
	r := NewRestorer()
	mustApply(t, r,
		Entry{Path: "real", Type: SymlinkEntry, Target: "/etc/hostname"},
		Entry{Path: "alias", Type: HardlinkEntry, Target: "real"},
	)
	tree := r.Tree()
	alias, real := tree.Node("alias"), tree.Node("real")
	if alias.Kind != SymlinkNode || real.Kind != SymlinkNode {
		t.Fatalf("expected symlink nodes, got %v and %v", alias.Kind, real.Kind)
	}
	if alias == real {
		t.Fatal("symlink content was shared instead of copied")
	}
	if alias.Target != "/etc/hostname" {
		t.Fatalf("alias target = %q", alias.Target)
	}
	if len(tree.inodes) != 0 {
		t.Fatal("symlinks must not allocate inodes")
	}
}

func TestHardlinkCycleRejected(t *testing.T) {
	r := NewRestorer()
	err := r.Apply([]Entry{
		{Path: "a", Type: HardlinkEntry, Target: "b"},
		{Path: "b", Type: HardlinkEntry, Target: "a"},
	})
	if err == nil || !strings.Contains(err.Error(), ErrLinkCycle.Error()) {
		t.Fatalf("expected cycle error, got %v", err)
	}
	if len(r.Tree().Paths()) != 0 {
		t.Fatal("rejected cycle still published nodes")
	}
}

func TestSelfLinkRejected(t *testing.T) {
	r := NewRestorer()
	err := r.Apply([]Entry{{Path: "a", Type: HardlinkEntry, Target: "a"}})
	if err == nil || !strings.Contains(err.Error(), ErrLinkCycle.Error()) {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestHardlinkToDirectoryRejected(t *testing.T) {
	r := NewRestorer()
	err := r.Apply([]Entry{
		{Path: "dir", Type: DirEntry},
		{Path: "lnk", Type: HardlinkEntry, Target: "dir"},
	})
	if err == nil || !strings.Contains(err.Error(), ErrLinkToDir.Error()) {
		t.Fatalf("expected link-to-dir error, got %v", err)
	}
	if len(r.Tree().Paths()) != 0 {
		t.Fatal("rejected batch still published nodes")
	}
}

// A hard link into an implicitly created directory is also rejected.
func TestHardlinkToImplicitDirRejected(t *testing.T) {
	r := NewRestorer()
	err := r.Apply([]Entry{
		fileEntry("d/f", "x"),
		{Path: "lnk", Type: HardlinkEntry, Target: "d"},
	})
	if err == nil || !strings.Contains(err.Error(), ErrLinkToDir.Error()) {
		t.Fatalf("expected link-to-dir error, got %v", err)
	}
}

func TestDanglingHardlinkRejected(t *testing.T) {
	r := NewRestorer()
	err := r.Apply([]Entry{{Path: "lnk", Type: HardlinkEntry, Target: "ghost"}})
	if err == nil || !strings.Contains(err.Error(), ErrDanglingLink.Error()) {
		t.Fatalf("expected dangling-link error, got %v", err)
	}
}

func TestDuplicatePathConflict(t *testing.T) {
	r := NewRestorer()
	err := r.Apply([]Entry{
		fileEntry("same", "one"),
		fileEntry("same", "two"),
	})
	if err == nil || !strings.Contains(err.Error(), ErrDuplicatePath.Error()) {
		t.Fatalf("expected duplicate-path error, got %v", err)
	}
	if len(r.Tree().Paths()) != 0 {
		t.Fatal("conflicting batch still published nodes")
	}
}

// A path that duplicates an already-committed entry conflicts as well.
func TestDuplicateAgainstCommittedTree(t *testing.T) {
	r := NewRestorer()
	mustApply(t, r, fileEntry("kept", "v1"))
	err := r.Apply([]Entry{fileEntry("kept", "v2")})
	if err == nil || !strings.Contains(err.Error(), ErrDuplicatePath.Error()) {
		t.Fatalf("expected duplicate-path error, got %v", err)
	}
	if got := string(r.Tree().Node("kept").Inode.Content); got != "v1" {
		t.Fatalf("committed content changed to %q", got)
	}
}

// A file used as a path prefix conflicts with entries beneath it.
func TestParentNotDirConflict(t *testing.T) {
	r := NewRestorer()
	err := r.Apply([]Entry{
		fileEntry("a", "file"),
		fileEntry("a/b", "child"),
	})
	if err == nil || !strings.Contains(err.Error(), ErrParentNotDir.Error()) {
		t.Fatalf("expected parent-not-dir error, got %v", err)
	}
}

// A failing batch must roll back completely: the previously committed tree
// stays byte-for-byte intact and no half-built state leaks out.
func TestFailureRollbackPublishesNoHalfTree(t *testing.T) {
	r := NewRestorer()
	mustApply(t, r, fileEntry("committed", "stable"))
	before := r.Tree()

	err := r.Apply([]Entry{
		fileEntry("newdir/ok1", "a"),
		fileEntry("newdir/ok2", "b"),
		{Path: "newdir/boom", Type: HardlinkEntry, Target: "newdir"}, // link to dir
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	after := r.Tree()
	if after != before {
		t.Fatal("committed tree pointer changed after failed Apply")
	}
	for _, p := range []string{"newdir", "newdir/ok1", "newdir/ok2", "newdir/boom"} {
		if after.Node(p) != nil {
			t.Fatalf("rolled-back path %q is visible", p)
		}
	}
	if got := string(after.Node("committed").Inode.Content); got != "stable" {
		t.Fatalf("committed content = %q", got)
	}
	if got := after.LinkCount("committed"); got != 1 {
		t.Fatalf("committed link count = %d, want 1", got)
	}
}

// Link counts on committed inodes must not be perturbed by a rolled-back
// batch that briefly shared them.
func TestRollbackRestoresLinkCounts(t *testing.T) {
	r := NewRestorer()
	mustApply(t, r, fileEntry("base", "data"))
	err := r.Apply([]Entry{
		{Path: "extra", Type: HardlinkEntry, Target: "base"},
		fileEntry("dup", "x"),
		fileEntry("dup", "y"), // duplicate -> whole batch rolls back
	})
	if err == nil {
		t.Fatal("expected failure")
	}
	if got := r.Tree().LinkCount("base"); got != 1 {
		t.Fatalf("link count after rollback = %d, want 1", got)
	}
	if r.Tree().Node("extra") != nil {
		t.Fatal("rolled-back hard link is visible")
	}
}

// Successful batches layer onto the committed tree and share inodes across
// batch boundaries.
func TestIncrementalApplySharesInodeAcrossBatches(t *testing.T) {
	r := NewRestorer()
	mustApply(t, r, fileEntry("orig", "blob"))
	mustApply(t, r, Entry{Path: "later", Type: HardlinkEntry, Target: "orig"})
	tree := r.Tree()
	if tree.Node("later").Inode != tree.Node("orig").Inode {
		t.Fatal("cross-batch hard link did not share the inode")
	}
	if got := tree.LinkCount("orig"); got != 2 {
		t.Fatalf("link count = %d, want 2", got)
	}
}

func TestInvalidPathsRejected(t *testing.T) {
	for _, p := range []string{"", "/abs", "../escape", "a/../../escape", "./"} {
		r := NewRestorer()
		if err := r.Apply([]Entry{fileEntry(p, "x")}); err == nil {
			t.Fatalf("path %q accepted", p)
		}
	}
}

func TestPathsSortedAndImplicitDirs(t *testing.T) {
	r := NewRestorer()
	mustApply(t, r, fileEntry("z/deep/file", "x"), fileEntry("a", "y"))
	got := strings.Join(r.Tree().Paths(), ",")
	want := "a,z,z/deep,z/deep/file"
	if got != want {
		t.Fatalf("paths = %q, want %q", got, want)
	}
	if r.Tree().Node("z/deep").Kind != DirNode {
		t.Fatal("implicit parent is not a directory")
	}
}
