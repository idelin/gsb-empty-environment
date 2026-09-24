package treeindex

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mustOpen(t *testing.T, dir string, opts ...Option) *Index {
	t.Helper()
	ix, err := Open(dir, opts...)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return ix
}

func mustCommit(t *testing.T, ix *Index, ops ...Op) {
	t.Helper()
	if err := ix.Commit(ops); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func mustPath(t *testing.T, ix *Index, id string) string {
	t.Helper()
	p, err := ix.Path(id)
	if err != nil {
		t.Fatalf("path(%s): %v", id, err)
	}
	return p
}

// buildSample 构造 /a, /a/b, /a/b/c, /dst。
func buildSample(t *testing.T, ix *Index) {
	t.Helper()
	mustCommit(t, ix,
		Create("a", RootID, "a"),
		Create("b", "a", "b"),
		Create("c", "b", "c"),
		Create("dst", RootID, "dst"),
	)
}

func TestBasicPaths(t *testing.T) {
	ix := mustOpen(t, t.TempDir())
	buildSample(t, ix)
	for id, want := range map[string]string{
		"a": "/a", "b": "/a/b", "c": "/a/b/c", "dst": "/dst",
	} {
		if got := mustPath(t, ix, id); got != want {
			t.Errorf("path(%s) = %q, want %q", id, got, want)
		}
	}
	if _, err := ix.Path("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: got %v, want ErrNotFound", err)
	}
}

func TestMoveSubtreeUpdatesAllDescendants(t *testing.T) {
	ix := mustOpen(t, t.TempDir())
	buildSample(t, ix)
	mustCommit(t, ix, Move("a", "dst"))
	for id, want := range map[string]string{
		"a": "/dst/a", "b": "/dst/a/b", "c": "/dst/a/b/c",
	} {
		if got := mustPath(t, ix, id); got != want {
			t.Errorf("after move path(%s) = %q, want %q", id, got, want)
		}
	}
	// 移动后 ID 不变
	if _, ok := ix.Node("c"); !ok {
		t.Error("node c lost after move")
	}
}

func TestMoveIntoOwnSubtreeRejected(t *testing.T) {
	ix := mustOpen(t, t.TempDir())
	buildSample(t, ix)
	before := map[string]string{"a": mustPath(t, ix, "a"), "b": mustPath(t, ix, "b")}

	// A 移到自己的子目录 B 下：必须拒绝
	if err := ix.Commit([]Op{Move("a", "b")}); !errors.Is(err, ErrCycle) {
		t.Fatalf("move a under b: got %v, want ErrCycle", err)
	}
	// A 移到自己下面：同样拒绝
	if err := ix.Commit([]Op{Move("a", "a")}); !errors.Is(err, ErrCycle) {
		t.Fatalf("move a under a: got %v, want ErrCycle", err)
	}
	// 移到更深层后代也拒绝
	if err := ix.Commit([]Op{Move("a", "c")}); !errors.Is(err, ErrCycle) {
		t.Fatalf("move a under c: got %v, want ErrCycle", err)
	}
	// 失败后一切保持原样
	for id, want := range before {
		if got := mustPath(t, ix, id); got != want {
			t.Errorf("after rejected move path(%s) = %q, want %q", id, got, want)
		}
	}
}

func TestRenameKeepsID(t *testing.T) {
	ix := mustOpen(t, t.TempDir())
	buildSample(t, ix)
	mustCommit(t, ix, Rename("a", "renamed"))
	if got := mustPath(t, ix, "a"); got != "/renamed" {
		t.Errorf("path after rename = %q, want /renamed", got)
	}
	if got := mustPath(t, ix, "c"); got != "/renamed/b/c" {
		t.Errorf("descendant path after rename = %q, want /renamed/b/c", got)
	}
	n, ok := ix.Node("a")
	if !ok || n.Name != "renamed" {
		t.Errorf("id a should survive rename, got %+v ok=%v", n, ok)
	}
}

func TestSiblingConflictRegardlessOfBatchOrder(t *testing.T) {
	// 同一批次内两个同名兄弟：两种顺序都必须被拒绝
	for _, tc := range []struct{ first, second Op }{
		{Create("x", RootID, "same"), Create("y", RootID, "same")},
		{Create("y", RootID, "same"), Create("x", RootID, "same")},
	} {
		ix := mustOpen(t, t.TempDir())
		if err := ix.Commit([]Op{tc.first, tc.second}); !errors.Is(err, ErrNameConflict) {
			t.Errorf("batch %v: got %v, want ErrNameConflict", tc, err)
		}
		if _, ok := ix.Node("x"); ok {
			t.Error("x should not exist after rejected batch")
		}
		if _, ok := ix.Node("y"); ok {
			t.Error("y should not exist after rejected batch")
		}
	}
}

func TestConflictAgainstExistingAndViaRename(t *testing.T) {
	ix := mustOpen(t, t.TempDir())
	buildSample(t, ix)
	// 与已有兄弟重名
	if err := ix.Commit([]Op{Create("z", RootID, "a")}); !errors.Is(err, ErrNameConflict) {
		t.Errorf("create duplicate: got %v, want ErrNameConflict", err)
	}
	// 重命名撞已有兄弟
	if err := ix.Commit([]Op{Rename("dst", "a")}); !errors.Is(err, ErrNameConflict) {
		t.Errorf("rename into conflict: got %v, want ErrNameConflict", err)
	}
	// 移动到已有同名节点的父目录
	mustCommit(t, ix, Create("a2", "dst", "x"))
	if err := ix.Commit([]Op{Rename("a2", "y"), Move("a2", RootID), Rename("a2", "a")}); !errors.Is(err, ErrNameConflict) {
		t.Errorf("move+rename into conflict: got %v, want ErrNameConflict", err)
	}
}

func TestBatchAtomicOnFailure(t *testing.T) {
	dir := t.TempDir()
	ix := mustOpen(t, dir)
	buildSample(t, ix)
	ver := ix.Version()
	snapshot := map[string]string{}
	for _, id := range ix.IDs() {
		snapshot[id] = mustPath(t, ix, id)
	}

	// 批次中前两个操作合法、第三个非法：整批必须拒绝
	err := ix.Commit([]Op{
		Create("n1", RootID, "n1"),
		Move("a", "dst"),
		Create("n2", RootID, "dst"), // 与已有 /dst 重名
	})
	if !errors.Is(err, ErrNameConflict) {
		t.Fatalf("got %v, want ErrNameConflict", err)
	}
	// 所有路径保持原样，合法的前两个操作也不生效
	for id, want := range snapshot {
		if got := mustPath(t, ix, id); got != want {
			t.Errorf("path(%s) = %q, want %q (unchanged)", id, got, want)
		}
	}
	if _, ok := ix.Node("n1"); ok {
		t.Error("n1 leaked from rejected batch")
	}
	if ix.Version() != ver {
		t.Error("version bumped by rejected batch")
	}
	// 磁盘上也没有留下任何痕迹
	ix2 := mustOpen(t, dir)
	for id, want := range snapshot {
		if got := mustPath(t, ix2, id); got != want {
			t.Errorf("reopened path(%s) = %q, want %q", id, got, want)
		}
	}
}

func TestPathCacheInvalidatedByVersion(t *testing.T) {
	ix := mustOpen(t, t.TempDir())
	mustCommit(t, ix, Create("a", RootID, "a"))

	if got := mustPath(t, ix, "a"); got != "/a" {
		t.Fatalf("path = %q, want /a", got)
	}
	c := ix.cache["a"]
	if c.version != ix.Version() {
		t.Fatalf("cache entry version %d != index version %d", c.version, ix.Version())
	}

	mustCommit(t, ix, Rename("a", "b"))
	// 旧版本缓存必须失效，重新计算得到新路径
	if got := mustPath(t, ix, "a"); got != "/b" {
		t.Errorf("path after rename = %q, want /b", got)
	}
	if ix.cache["a"].version != ix.Version() {
		t.Error("cache entry not refreshed to new version")
	}
}

func TestCrashAfterJournalCommitRecovers(t *testing.T) {
	dir := t.TempDir()
	ix := mustOpen(t, dir)
	mustCommit(t, ix, Create("a", RootID, "a"))

	// 模拟“日志已提交、随后立刻崩溃”：注入点在提交点之后
	boom := errors.New("simulated crash")
	ix2 := mustOpen(t, dir, WithHook(HookAfterJournalCommit, func() error { return boom }))
	if err := ix2.Commit([]Op{Create("b", "a", "b")}); !errors.Is(err, boom) {
		t.Fatalf("got %v, want injected crash", err)
	}

	// 进程重启：Open 重放 WAL，已提交的批次必须生效
	ix3 := mustOpen(t, dir)
	if got := mustPath(t, ix3, "b"); got != "/a/b" {
		t.Errorf("recovered path(b) = %q, want /a/b", got)
	}
	// 恢复后日志已被 checkpoint 清理
	if _, err := os.Stat(filepath.Join(dir, journalFile)); !os.IsNotExist(err) {
		t.Error("journal should be removed after recovery checkpoint")
	}
}

func TestCrashBeforeJournalCommitLosesBatch(t *testing.T) {
	dir := t.TempDir()
	ix := mustOpen(t, dir)
	mustCommit(t, ix, Create("a", RootID, "a"))

	boom := errors.New("simulated power loss")
	ix2 := mustOpen(t, dir, WithHook(HookBeforeJournalRename, func() error { return boom }))
	if err := ix2.Commit([]Op{Create("b", "a", "b")}); !errors.Is(err, boom) {
		t.Fatalf("got %v, want injected crash", err)
	}

	// 批次未到达提交点：重启后必须不存在，且临时文件被清理
	ix3 := mustOpen(t, dir)
	if _, ok := ix3.Node("b"); ok {
		t.Error("uncommitted batch must not survive crash")
	}
	if _, err := os.Stat(filepath.Join(dir, journalTmp)); !os.IsNotExist(err) {
		t.Error("stale journal.tmp should be discarded on open")
	}
	if got := mustPath(t, ix3, "a"); got != "/a" {
		t.Errorf("path(a) = %q, want /a", got)
	}
}

func TestCrashDuringSnapshotStillRecovers(t *testing.T) {
	dir := t.TempDir()
	ix := mustOpen(t, dir)
	mustCommit(t, ix, Create("a", RootID, "a"))

	// 快照写入前崩溃：日志仍在，重启后重放恢复
	boom := errors.New("simulated crash during checkpoint")
	ix2 := mustOpen(t, dir, WithHook(HookBeforeSnapshotWrite, func() error { return boom }))
	if err := ix2.Commit([]Op{Create("b", "a", "b")}); !errors.Is(err, boom) {
		t.Fatalf("got %v, want injected crash", err)
	}
	ix3 := mustOpen(t, dir)
	if got := mustPath(t, ix3, "b"); got != "/a/b" {
		t.Errorf("recovered path(b) = %q, want /a/b", got)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	ix := mustOpen(t, dir)
	buildSample(t, ix)
	mustCommit(t, ix, Move("a", "dst"), Rename("dst", "archive"))

	ix2 := mustOpen(t, dir)
	for id, want := range map[string]string{
		"a": "/archive/a", "b": "/archive/a/b", "c": "/archive/a/b/c",
	} {
		if got := mustPath(t, ix2, id); got != want {
			t.Errorf("reopened path(%s) = %q, want %q", id, got, want)
		}
	}
}
