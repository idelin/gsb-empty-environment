package auditjoin

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNodeNameReuseIsolatedByGeneration(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	store := NewMemStore()
	a, _ := New(newTestConfig(clock), store, "")
	ctx := context.Background()

	if err := a.Ingest(ctx, frag("node", "gen-old", 1, "s1", "f1", "old", 1)); err != nil {
		t.Fatalf("old: %v", err)
	}
	if err := a.Ingest(ctx, frag("node", "gen-new", 1, "s1", "f1", "new", 1)); err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := a.Ingest(ctx, frag("node", "gen-old", 1, "s2", "f1", "old", 1)); err != nil {
		t.Fatalf("old replay: %v", err)
	}

	recs, _ := store.List(ctx)
	if len(recs) != 2 {
		t.Fatalf("expected two isolated events, got %d", len(recs))
	}
	for _, rec := range recs {
		if rec.Version != 1 || !rec.Complete {
			t.Fatalf("each generation must publish v1 complete: %+v", rec)
		}
	}
	oldRec, ok, _ := store.Get(ctx, keyOf("node", "gen-old", 1), 0)
	if !ok || string(oldRec.Payload) != "old" {
		t.Fatalf("old generation payload corrupted: %+v", oldRec)
	}
	newRec, ok, _ := store.Get(ctx, keyOf("node", "gen-new", 1), 0)
	if !ok || string(newRec.Payload) != "new" {
		t.Fatalf("new generation payload corrupted: %+v", newRec)
	}
}

func TestSequenceWraparoundNoDuplicates(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	cfg := newTestConfig(clock)
	cfg.WrapModulus = 8
	store := NewMemStore()
	a, _ := New(cfg, store, "")
	ctx := context.Background()

	// 第一个回绕周期：序号 5、6、7 单调推进到高半区，随后跨 0 回绕。
	for _, seq := range []uint64{5, 6, 7} {
		if err := a.Ingest(ctx, frag("node-a", "g1", seq, "s1", "f1", "first", 1)); err != nil {
			t.Fatalf("cycle0 seq %d: %v", seq, err)
		}
	}
	// 序号回绕到 0、1：必须识别为新周期，而不是重复事件。
	for _, seq := range []uint64{0, 1} {
		if err := a.Ingest(ctx, frag("node-a", "g1", seq, "s1", "f1", "wrapped", 1)); err != nil {
			t.Fatalf("cycle1 seq %d: %v", seq, err)
		}
	}

	// 回绕后的重复投递必须幂等。
	if err := a.Ingest(ctx, frag("node-a", "g1", 1, "s2", "f1", "wrapped", 1)); err != nil {
		t.Fatalf("wrapped duplicate: %v", err)
	}

	recs, _ := store.List(ctx)
	if len(recs) != 5 {
		t.Fatalf("expected four distinct events across cycles, got %d", len(recs))
	}
	cycle0, ok0, _ := store.Get(ctx, keyOf("node-a", "g1", 7), 0)
	cycle1, ok1, _ := store.Get(ctx, keyOf("node-a", "g1", 1), 1)
	if !ok0 || !ok1 {
		t.Fatalf("expected both cycles present: ok0=%v ok1=%v", ok0, ok1)
	}
	if cycle0.Cycle != 0 || cycle1.Cycle != 1 {
		t.Fatalf("cycle disambiguation failed: %+v %+v", cycle0, cycle1)
	}
	if string(cycle1.Payload) != "wrapped" || cycle1.Version != 1 {
		t.Fatalf("wrapped event corrupted: %+v", cycle1)
	}

	// 回绕后新周期事件的迟到补正仍按原键递增版本。
	late := frag("node-a", "g1", 1, "s2", "f2", "extra", 2)
	late.ExpectedFragments = 2
	first := frag("node-a", "g1", 1, "s1", "f1", "wrapped", 2)
	if err := a.Ingest(ctx, first); err != nil {
		t.Fatalf("re-open wrapped event: %v", err)
	}
	if err := a.Ingest(ctx, late); err != nil {
		t.Fatalf("wrapped correction: %v", err)
	}
	cycle1, _, _ = store.Get(ctx, keyOf("node-a", "g1", 1), 1)
	if cycle1.Version != 2 || !cycle1.Complete {
		t.Fatalf("expected v2 complete wrapped correction, got %+v", cycle1)
	}
	if recs, _ := store.List(ctx); len(recs) != 5 {
		t.Fatalf("correction must not add events, got %d", len(recs))
	}
}

func TestStoreConcurrentIdempotentPuts(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	k := keyOf("node-a", "g1", 9)
	base := time.Unix(1_700_000_000, 0)
	rec := Record{Key: k, Cycle: 0, Version: 1, Complete: true, Payload: []byte("x"), PublishedAt: base, UpdatedAt: base}

	const n = 64
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := store.Put(ctx, k, 0, rec)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent put: %v", err)
		}
	}

	recs, _ := store.List(ctx)
	if len(recs) != 1 {
		t.Fatalf("expected one record under concurrent puts, got %d", len(recs))
	}
	st := store.Stats()
	if st.Changes != 1 || st.Idempotent != n-1 || st.DistinctKeys != 1 {
		t.Fatalf("unexpected stats: %+v", st)
	}

	// 同版本冲突内容必须报错，旧版本必须被拒绝。
	conflict := rec
	conflict.Payload = []byte("y")
	if _, _, err := store.Put(ctx, k, 0, conflict); err == nil {
		t.Fatal("expected conflict error")
	}
	old := rec
	old.Version = 0
	if _, changed, err := store.Put(ctx, k, 0, old); err != nil || changed {
		t.Fatalf("old version must be rejected without error, changed=%v err=%v", changed, err)
	}
}

func TestBackgroundSweepPublishesTimeout(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	cfg := newTestConfig(clock)
	cfg.SweepInterval = 5 * time.Millisecond
	store := NewMemStore()
	a, err := New(cfg, store, "")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()

	if err := a.Ingest(ctx, frag("node-a", "g1", 2, "s1", "f1", "p", 0)); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	clock.advance(11 * time.Second)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if recs, _ := store.List(ctx); len(recs) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	recs, _ := store.List(ctx)
	if len(recs) != 1 || recs[0].Complete {
		t.Fatalf("expected one timeout-published incomplete record, got %+v", recs)
	}
}

func TestFileStoreRestoresAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/store.json"
	ctx := context.Background()
	k := keyOf("node-a", "g1", 4)
	base := time.Unix(1_700_000_000, 0)

	s1, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, _, err := s1.Put(ctx, k, 0, Record{Key: k, Version: 1, Complete: false, Payload: []byte("v1"), PublishedAt: base, UpdatedAt: base}); err != nil {
		t.Fatalf("put v1: %v", err)
	}
	if _, _, err := s1.Put(ctx, k, 0, Record{Key: k, Version: 2, Complete: true, Payload: []byte("v2"), PublishedAt: base, UpdatedAt: base}); err != nil {
		t.Fatalf("put v2: %v", err)
	}

	s2, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	rec, ok, _ := s2.Get(ctx, k, 0)
	if !ok || rec.Version != 2 || !rec.Complete {
		t.Fatalf("restored record mismatch: %+v ok=%v", rec, ok)
	}
	// 重放旧版本与同版本内容都不得制造重复或回退。
	if _, changed, _ := s2.Put(ctx, k, 0, Record{Key: k, Version: 1, Payload: []byte("v1"), PublishedAt: base, UpdatedAt: base}); changed {
		t.Fatal("old version changed store after restart")
	}
	if _, changed, _ := s2.Put(ctx, k, 0, rec); changed {
		t.Fatal("equal re-put should be idempotent")
	}
	if recs, _ := s2.List(ctx); len(recs) != 1 {
		t.Fatalf("restart replay created duplicates: %d", len(recs))
	}
}

func TestFileStorePersistFailureRollsBack(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenFileStore(dir + "/store.json")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// 把存储路径改成一个目录，后续持久化创建临时文件必然失败，触发回滚。
	store.path = dir
	ctx := context.Background()
	k := keyOf("node-a", "g1", 1)
	_, changed, err := store.Put(ctx, k, 0, Record{Key: k, Version: 1, Payload: []byte("x")})
	if err == nil || changed {
		t.Fatalf("expected persist failure without a change, changed=%v err=%v", changed, err)
	}
	if recs, _ := store.List(ctx); len(recs) != 0 {
		t.Fatalf("failed put must roll back in-memory state, got %d", len(recs))
	}
	if st := store.Stats(); st.Changes != 0 || st.DistinctKeys != 0 {
		t.Fatalf("stats must roll back, got %+v", st)
	}
}
