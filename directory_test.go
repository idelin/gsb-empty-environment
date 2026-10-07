package dirdb

import (
	"fmt"
	"sync"
	"testing"
)

// checkConsistent 用 byID 单字典重建索引，与目录内部索引逐一比对。
func checkConsistent(t *testing.T, d *Directory) {
	t.Helper()
	d.mu.RLock()
	defer d.mu.RUnlock()
	rebuilt := rebuildIndexes(d.ix.byID)
	if !equalIndexes(rebuilt, d.ix) {
		t.Fatalf("index mismatch: rebuilt=%+v live=%+v", rebuilt, d.ix)
	}
}

// checkSnapshot 核对快照自身跨索引一致，且与期望对象集完全吻合。
func checkSnapshot(t *testing.T, s *Snapshot, want map[string]Object) {
	t.Helper()
	if !equalIndexes(rebuildIndexes(s.ix.byID), s.ix) {
		t.Fatal("snapshot indexes diverged from its byID dictionary")
	}
	if s.Len() != len(want) {
		t.Fatalf("snapshot len=%d want %d", s.Len(), len(want))
	}
	for id, w := range want {
		got, ok := s.Get(id)
		if !ok || got.Name != w.Name || !equalStrings(got.Tags, w.Tags) {
			t.Fatalf("Get(%q)=%+v,%v want %+v", id, got, ok, w)
		}
		if byName, ok := s.Lookup(w.Name); !ok || byName.ID != id {
			t.Fatalf("Lookup(%q) did not resolve to %q", w.Name, id)
		}
		for _, tag := range w.Tags {
			found := false
			for _, o := range s.Tagged(tag) {
				if o.ID == id {
					found = true
				}
			}
			if !found {
				t.Fatalf("Tagged(%q) missing %q", tag, id)
			}
		}
	}
}

// TestBatchNameSwap 批内两条 upsert 互换名称必须成功。
func TestBatchNameSwap(t *testing.T) {
	d := New()
	if err := d.Apply([]Op{
		Upsert(Object{ID: "a", Name: "foo", Tags: []string{"x"}}),
		Upsert(Object{ID: "b", Name: "bar", Tags: []string{"y"}}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.Apply([]Op{
		Upsert(Object{ID: "a", Name: "bar"}),
		Upsert(Object{ID: "b", Name: "foo"}),
	}); err != nil {
		t.Fatalf("name swap rejected: %v", err)
	}
	s := d.Snapshot()
	if o, _ := s.Lookup("foo"); o.ID != "b" {
		t.Fatalf("foo -> %q, want b", o.ID)
	}
	if o, _ := s.Lookup("bar"); o.ID != "a" {
		t.Fatalf("bar -> %q, want a", o.ID)
	}
	checkConsistent(t, d)
}

// TestBatchConflictFailsAtomically 批内任何冲突使整批失败，目录保持原状。
func TestBatchConflictFailsAtomically(t *testing.T) {
	d := New()
	seed := []Op{
		Upsert(Object{ID: "a", Name: "alpha", Tags: []string{"t1"}}),
		Upsert(Object{ID: "b", Name: "beta", Tags: []string{"t2"}}),
	}
	if err := d.Apply(seed); err != nil {
		t.Fatal(err)
	}
	before := d.Snapshot()

	cases := map[string][]Op{
		// 与现有对象撞名（批内没有释放该名）。
		"name taken by other": {
			Upsert(Object{ID: "c", Name: "gamma"}),
			Upsert(Object{ID: "d", Name: "alpha"}),
		},
		// 批内两条操作撞名。
		"name clash inside batch": {
			Upsert(Object{ID: "c", Name: "gamma"}),
			Upsert(Object{ID: "d", Name: "gamma"}),
		},
		// 批内同 id 重复操作。
		"duplicate id in batch": {
			Upsert(Object{ID: "c", Name: "gamma"}),
			Upsert(Object{ID: "c", Name: "delta"}),
		},
		// 删除不存在的对象。
		"delete missing": {
			Upsert(Object{ID: "c", Name: "gamma"}),
			Delete("nope"),
		},
	}
	for name, ops := range cases {
		if err := d.Apply(ops); err == nil {
			t.Fatalf("%s: batch unexpectedly succeeded", name)
		}
		if err := d.Verify(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		after := d.Snapshot()
		if !equalIndexes(before.ix, after.ix) {
			t.Fatalf("%s: failed batch mutated the directory", name)
		}
	}
	checkConsistent(t, d)
}

// TestDeleteClearsReverseTagRefs 删除对象后标签索引不得残留其引用。
func TestDeleteClearsReverseTagRefs(t *testing.T) {
	d := New()
	if err := d.Apply([]Op{
		Upsert(Object{ID: "a", Name: "alpha", Tags: []string{"shared", "solo"}}),
		Upsert(Object{ID: "b", Name: "beta", Tags: []string{"shared"}}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.Apply([]Op{Delete("a")}); err != nil {
		t.Fatal(err)
	}
	s := d.Snapshot()
	if got := s.Tagged("solo"); len(got) != 0 {
		t.Fatalf("tag solo still has %d objects", len(got))
	}
	got := s.Tagged("shared")
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("tag shared = %+v, want only b", got)
	}
	d.mu.RLock()
	if _, ok := d.ix.byTag["solo"]; ok {
		t.Fatal("empty tag set not pruned")
	}
	d.mu.RUnlock()
	checkConsistent(t, d)
}

// TestUpsertReplaceRetiresOldIndexes 替换对象时旧名称与旧标签引用必须失效。
func TestUpsertReplaceRetiresOldIndexes(t *testing.T) {
	d := New()
	if err := d.Apply([]Op{
		Upsert(Object{ID: "a", Name: "old", Tags: []string{"stale"}}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.Apply([]Op{
		Upsert(Object{ID: "a", Name: "new", Tags: []string{"fresh"}}),
	}); err != nil {
		t.Fatal(err)
	}
	s := d.Snapshot()
	if _, ok := s.Lookup("old"); ok {
		t.Fatal("old name still resolves")
	}
	if got := s.Tagged("stale"); len(got) != 0 {
		t.Fatal("stale tag still references object")
	}
	if o, ok := s.Lookup("new"); !ok || o.ID != "a" {
		t.Fatal("new name does not resolve")
	}
	checkConsistent(t, d)
}

// TestSnapshotIsolation 快照不受后续写入影响。
func TestSnapshotIsolation(t *testing.T) {
	d := New()
	if err := d.Apply([]Op{
		Upsert(Object{ID: "a", Name: "alpha", Tags: []string{"t"}}),
	}); err != nil {
		t.Fatal(err)
	}
	snap := d.Snapshot()
	if err := d.Apply([]Op{
		Delete("a"),
		Upsert(Object{ID: "b", Name: "alpha", Tags: []string{"t"}}),
	}); err != nil {
		t.Fatal(err)
	}
	checkSnapshot(t, snap, map[string]Object{
		"a": {ID: "a", Name: "alpha", Tags: []string{"t"}},
	})
}

// TestConcurrentSnapshots 并发批量写入与快照读取：
// 每个快照都必须通过单字典重建核对，且读写竞态由 -race 兜底。
func TestConcurrentSnapshots(t *testing.T) {
	d := New()
	const writers = 4
	const readers = 4
	const rounds = 200

	var writeWG sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < writers; w++ {
		writeWG.Add(1)
		go func(w int) {
			defer writeWG.Done()
			for i := 0; i < rounds; i++ {
				idA := fmt.Sprintf("w%d-a", w)
				idB := fmt.Sprintf("w%d-b", w)
				nameA := fmt.Sprintf("w%d-alpha-%d", w, i%2)
				nameB := fmt.Sprintf("w%d-beta-%d", w, i%2)
				tag := fmt.Sprintf("w%d-tag", w)
				// 每轮交替互换两个对象的名称。
				_ = d.Apply([]Op{
					Upsert(Object{ID: idA, Name: nameA, Tags: []string{tag}}),
					Upsert(Object{ID: idB, Name: nameB, Tags: []string{tag}}),
				})
				if i%3 == 0 {
					_ = d.Apply([]Op{Delete(idB)})
				}
			}
		}(w)
	}

	errCh := make(chan string, readers)
	var readWG sync.WaitGroup
	for r := 0; r < readers; r++ {
		readWG.Add(1)
		go func() {
			defer readWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := d.Snapshot()
				// 核心不变量：快照的名称/标签索引与 byID 单字典重建结果一致。
				if !equalIndexes(rebuildIndexes(snap.ix.byID), snap.ix) {
					errCh <- "snapshot indexes inconsistent"
					return
				}
				// 名称索引与 id 索引必须互相印证。
				for id, o := range snap.ix.byID {
					if snap.ix.byName[o.Name] != id {
						errCh <- "name index disagrees with byID"
						return
					}
				}
			}
		}()
	}

	writeWG.Wait()
	close(stop)
	readWG.Wait()

	select {
	case msg := <-errCh:
		t.Fatal(msg)
	default:
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}
	checkConsistent(t, d)
}
