package visibility

import (
	"errors"
	"sort"
	"testing"
)

func mustQuery(t *testing.T, ix *Index, r *Root, s *Snapshot, terms ...string) []DocResult {
	t.Helper()
	got, err := ix.Query(r, s, terms)
	if err != nil {
		t.Fatalf("Query(%v) unexpected error: %v", terms, err)
	}
	return got
}

func docIDs(rs []DocResult) []DocID {
	out := make([]DocID, len(rs))
	for i, r := range rs {
		out[i] = r.Doc
	}
	return out
}

func assertDocs(t *testing.T, got []DocResult, want ...DocID) {
	t.Helper()
	gotDocs := docIDs(got)
	if len(gotDocs) != len(want) {
		t.Fatalf("docs = %v, want %v", gotDocs, want)
	}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	for i := range want {
		if gotDocs[i] != want[i] {
			t.Fatalf("docs = %v, want %v", gotDocs, want)
		}
	}
}

func findPosting(ix *Index, term string, doc DocID, gen Gen, tomb bool) (posting, bool) {
	for _, p := range ix.index[term] {
		if p.doc == doc && p.gen == gen && p.tomb == tomb {
			return p, true
		}
	}
	return posting{}, false
}

// 场景一：删除 -> 重建与压缩交错，旧根必须持续复现“已删除”，且墓碑在仍被
// 旧根需要时不得被压缩丢弃。
func TestDeleteRebuildInterleavedCompact(t *testing.T) {
	ix := New()
	authz := NewAuthz().Grant("d1")

	r0 := ix.Head()
	r1 := ix.Put("d1", []string{"a", "b"})
	s1 := authz.Snapshot()

	assertDocs(t, mustQuery(t, ix, r1, s1, "a"), "d1")
	if got := mustQuery(t, ix, r1, s1, "a"); got[0].Gen != 1 {
		t.Fatalf("initial gen = %d, want 1", got[0].Gen)
	}

	// 删除：新根观察不到 d1，旧根 r1 仍能看到旧代。
	r2 := ix.Delete("d1")
	assertDocs(t, mustQuery(t, ix, r2, s1, "a"))
	assertDocs(t, mustQuery(t, ix, r1, s1, "a"), "d1")

	// 压缩：r0/r1/r2 仍被持有，rmin=0，v2 墓碑必须保留。
	ix.Compact()
	if _, ok := findPosting(ix, "a", "d1", 1, true); !ok {
		t.Fatal("tombstone for gen1 was dropped while an old root still needs it")
	}
	assertDocs(t, mustQuery(t, ix, r2, s1, "a"))
	assertDocs(t, mustQuery(t, ix, r1, s1, "a"), "d1")
	assertDocs(t, mustQuery(t, ix, r0, s1, "a"))

	// 重建：全新代次，与旧代共存于同一倒排表。
	r3 := ix.Put("d1", []string{"a", "c"})
	ix.Compact()
	assertDocs(t, mustQuery(t, ix, r3, s1, "a", "c"), "d1")
	if got := mustQuery(t, ix, r3, s1, "a"); got[0].Gen != 2 {
		t.Fatalf("rebuilt gen = %d, want 2", got[0].Gen)
	}
	assertDocs(t, mustQuery(t, ix, r2, s1, "a"))
	assertDocs(t, mustQuery(t, ix, r1, s1, "a"), "d1")

	// 旧根全部释放后再压缩：最旧持有根 r3 已观察到删除，墓碑与旧代可回收。
	if err := r0.Release(); err != nil {
		t.Fatal(err)
	}
	if err := r1.Release(); err != nil {
		t.Fatal(err)
	}
	if err := r2.Release(); err != nil {
		t.Fatal(err)
	}
	ix.Compact()

	if _, ok := findPosting(ix, "a", "d1", 1, true); ok {
		t.Fatal("stale tombstone survived after every root observed the deletion")
	}
	if _, ok := findPosting(ix, "a", "d1", 1, false); ok {
		t.Fatal("stale gen1 posting survived compaction")
	}
	assertDocs(t, mustQuery(t, ix, r3, s1, "a", "c"), "d1")

	// 已释放根不可再用于查询。
	if _, err := ix.Query(r1, s1, []string{"a"}); !errors.Is(err, ErrRootReleased) {
		t.Fatalf("released root query err = %v, want ErrRootReleased", err)
	}
	if err := r1.Release(); !errors.Is(err, ErrRootReleased) {
		t.Fatalf("double release err = %v, want ErrRootReleased", err)
	}
	s1.Release()
	r3.Release()
}

// 场景二：授权撤回与压缩交错；旧快照保持固定视图，撤回只影响新快照。
func TestAuthzRevokeInterleavedCompact(t *testing.T) {
	ix := New()
	authz := NewAuthz().Grant("d1", "d2")

	r1 := ix.Put("d1", []string{"x"})
	r2 := ix.Put("d2", []string{"x"})

	sAll := authz.Snapshot()
	assertDocs(t, mustQuery(t, ix, r2, sAll, "x"), "d1", "d2")

	authz.Revoke("d1")
	sRestricted := authz.Snapshot()

	// 同一索引根、不同授权快照，输出不同；旧快照视图不被撤回影响。
	assertDocs(t, mustQuery(t, ix, r2, sRestricted, "x"), "d2")
	assertDocs(t, mustQuery(t, ix, r2, sAll, "x"), "d1", "d2")

	ix.Compact()
	assertDocs(t, mustQuery(t, ix, r2, sRestricted, "x"), "d2")
	assertDocs(t, mustQuery(t, ix, r2, sAll, "x"), "d1", "d2")

	// 删除 d2 后：授权仍在也不应返回（索引根与授权快照取交集）。
	r3 := ix.Delete("d2")
	assertDocs(t, mustQuery(t, ix, r3, sAll, "x"), "d1")
	assertDocs(t, mustQuery(t, ix, r3, sRestricted, "x"))
	// 旧根 r2 配合两个快照的历史视图保持稳定。
	assertDocs(t, mustQuery(t, ix, r2, sAll, "x"), "d1", "d2")
	assertDocs(t, mustQuery(t, ix, r2, sRestricted, "x"), "d2")

	ix.Compact()
	assertDocs(t, mustQuery(t, ix, r2, sAll, "x"), "d1", "d2")
	assertDocs(t, mustQuery(t, ix, r2, sRestricted, "x"), "d2")
	assertDocs(t, mustQuery(t, ix, r3, sAll, "x"), "d1")

	// 撤回后重新授权并发放新快照，文档重新可见。
	authz.Grant("d1")
	sAgain := authz.Snapshot()
	// r2 早于 d2 的删除，且新快照仍授权 d2，因此两个文档都可见。
	assertDocs(t, mustQuery(t, ix, r2, sAgain, "x"), "d1", "d2")
	assertDocs(t, mustQuery(t, ix, r3, sAgain, "x"), "d1")

	if _, err := ix.Query(r1, sAll, nil); err != nil {
		t.Fatalf("empty terms query errored: %v", err)
	}
	sAll.Release()
	sRestricted.Release()
	sAgain.Release()
	r1.Release()
	r2.Release()
	r3.Release()
}

// 删除残留不得泄漏历史存在性：已删除文档与从未存在的词项，查询结果形态一致。
func TestNoExistenceLeakage(t *testing.T) {
	ix := New()
	authz := NewAuthz().Grant("secret")
	snap := authz.Snapshot()
	root := ix.Put("secret", []string{"forensics"})
	next := ix.Delete("secret")

	if got, err := ix.Query(next, snap, []string{"forensics"}); err != nil || got != nil {
		t.Fatalf("deleted doc query = (%v, %v), want (nil, nil)", got, err)
	}
	if got, err := ix.Query(next, snap, []string{"never-indexed"}); err != nil || got != nil {
		t.Fatalf("unknown term query = (%v, %v), want (nil, nil)", got, err)
	}

	// 旧根仍能看到历史文档是预期功能，而非泄漏：它是被显式持有的只读视图。
	assertDocs(t, mustQuery(t, ix, root, snap, "forensics"), "secret")

	root.Release()
	next.Release()
	snap.Release()
}

func TestReleasedSnapshot(t *testing.T) {
	ix := New()
	authz := NewAuthz().Grant("d1")
	root := ix.Put("d1", []string{"q"})
	snap := authz.Snapshot()
	if err := snap.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Query(root, snap, []string{"q"}); !errors.Is(err, ErrSnapshotReleased) {
		t.Fatalf("err = %v, want ErrSnapshotReleased", err)
	}
	if err := snap.Release(); !errors.Is(err, ErrSnapshotReleased) {
		t.Fatalf("double release err = %v, want ErrSnapshotReleased", err)
	}
	root.Release()
}

// 存活文档替换（原地更新）也要为旧代写墓碑，旧根看到旧代、新根看到新代。
func TestReplaceWritesTombstone(t *testing.T) {
	ix := New()
	authz := NewAuthz().Grant("d1")
	snap := authz.Snapshot()

	old := ix.Put("d1", []string{"alpha"})
	new := ix.Put("d1", []string{"beta"})

	assertDocs(t, mustQuery(t, ix, old, snap, "alpha"), "d1")
	assertDocs(t, mustQuery(t, ix, new, snap, "beta"), "d1")
	assertDocs(t, mustQuery(t, ix, new, snap, "alpha"))

	ix.Compact()
	assertDocs(t, mustQuery(t, ix, old, snap, "alpha"), "d1")
	assertDocs(t, mustQuery(t, ix, new, snap, "beta"), "d1")
	if _, ok := findPosting(ix, "alpha", "d1", 1, true); !ok {
		t.Fatal("replacement tombstone missing while old root is held")
	}

	old.Release()
	new.Release()
	snap.Release()
}
