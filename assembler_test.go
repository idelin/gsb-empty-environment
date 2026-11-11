package auditjoin

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestConfig(clock *fakeClock) Config {
	return Config{
		PublishTimeout:   10 * time.Second,
		CorrectionWindow: time.Minute,
		Clock:            clock.now,
	}
}

func frag(node NodeID, gen Generation, seq uint64, shard ShardID, fid FragmentID, payload string, expected int) Fragment {
	return Fragment{
		NodeID:            node,
		Generation:        gen,
		Seq:               seq,
		ShardID:           shard,
		FragmentID:        fid,
		ExpectedFragments: expected,
		Payload:           []byte(payload),
	}
}

func keyOf(node NodeID, gen Generation, seq uint64) EventKey {
	return EventKey{NodeID: node, Generation: gen, Seq: seq}
}

func TestDuplicateFragmentsNoDuplicateEvent(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	store := NewMemStore()
	a, err := New(newTestConfig(clock), store, "")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()

	f := frag("node-a", "g1", 1, "s1", "f1", "p1", 2)
	if err := a.Ingest(ctx, f); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if err := a.Ingest(ctx, f); err != nil {
		t.Fatalf("duplicate ingest: %v", err)
	}
	f2 := f
	f2.ShardID = "s2"
	if err := a.Ingest(ctx, f2); err != nil {
		t.Fatalf("cross-shard duplicate: %v", err)
	}

	if recs, _ := store.List(ctx); len(recs) != 0 {
		t.Fatalf("event must not publish before all fragments arrive, got %d", len(recs))
	}

	if err := a.Ingest(ctx, frag("node-a", "g1", 1, "s2", "f2", "p2", 2)); err != nil {
		t.Fatalf("ingest f2: %v", err)
	}
	if err := a.Ingest(ctx, f); err != nil {
		t.Fatalf("replay f1: %v", err)
	}
	if err := a.Ingest(ctx, f2); err != nil {
		t.Fatalf("replay f2: %v", err)
	}

	recs, _ := store.List(ctx)
	if len(recs) != 1 {
		t.Fatalf("expected exactly one event, got %d", len(recs))
	}
	if recs[0].Version != 1 || !recs[0].Complete {
		t.Fatalf("unexpected record: %+v", recs[0])
	}
	if got := a.Stats().DuplicateFragments; got != 4 {
		t.Fatalf("expected 4 duplicate fragments, got %d", got)
	}
}

func TestConflictFragmentReturnsError(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	store := NewMemStore()
	a, _ := New(newTestConfig(clock), store, "")
	ctx := context.Background()

	f1 := frag("n", "g", 1, "s1", "f1", "a", 0)
	f1b := frag("n", "g", 1, "s2", "f1", "different", 0)
	if err := a.Ingest(ctx, f1); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if err := a.Ingest(ctx, f1b); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestTimeoutPublishThenLateCorrection(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	store := NewMemStore()
	a, _ := New(newTestConfig(clock), store, "")
	ctx := context.Background()

	evKey := keyOf("node-a", "g1", 7)
	if err := a.Ingest(ctx, frag("node-a", "g1", 7, "s1", "f1", "part-1", 2)); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	clock.advance(11 * time.Second)
	res, err := a.Sweep(clock.now())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(res.Published) != 1 {
		t.Fatalf("expected one timeout publish, got %d", len(res.Published))
	}
	rec, ok, _ := store.Get(ctx, evKey, 0)
	if !ok || rec.Complete || rec.Version != 1 {
		t.Fatalf("expected incomplete v1 timeout record, got %+v ok=%v", rec, ok)
	}

	if err := a.Ingest(ctx, frag("node-a", "g1", 7, "s2", "f2", "part-2", 2)); err != nil {
		t.Fatalf("late correction: %v", err)
	}
	rec, _, _ = store.Get(ctx, evKey, 0)
	if rec.Version != 2 || !rec.Complete {
		t.Fatalf("expected complete v2 after correction, got %+v", rec)
	}
	if string(rec.Payload) != "part-1\npart-2" {
		t.Fatalf("unexpected merged payload %q", rec.Payload)
	}
	if got := store.VersionHistory(evKey, 0); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("expected versions [1 2], got %v", got)
	}

	clock.advance(2 * time.Minute)
	err = a.Ingest(ctx, frag("node-a", "g1", 7, "s1", "f3", "late", 2))
	if !errors.Is(err, ErrEventSealed) {
		t.Fatalf("expected ErrEventSealed, got %v", err)
	}
	if _, err := a.Sweep(clock.now()); err != nil {
		t.Fatalf("sweep seal: %v", err)
	}
	if recs, _ := store.List(ctx); len(recs) != 1 {
		t.Fatalf("seal must not create records, got %d", len(recs))
	}
}

func TestWatermarkPublishesWithoutTimeout(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	cfg := newTestConfig(clock)
	cfg.RequiredShards = []ShardID{"s1", "s2"}
	store := NewMemStore()
	a, _ := New(cfg, store, "")
	ctx := context.Background()

	evKey := keyOf("node-a", "g1", 5)
	if err := a.Ingest(ctx, frag("node-a", "g1", 5, "s1", "f1", "only", 0)); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if recs, _ := store.List(ctx); len(recs) != 0 {
		t.Fatalf("must wait for watermark, got %d", len(recs))
	}

	if err := a.ObserveWatermark(ctx, "s1", 5); err != nil {
		t.Fatalf("wm s1: %v", err)
	}
	if recs, _ := store.List(ctx); len(recs) != 0 {
		t.Fatalf("single shard watermark must not publish, got %d", len(recs))
	}
	if err := a.ObserveWatermark(ctx, "s2", 5); err != nil {
		t.Fatalf("wm s2: %v", err)
	}
	rec, ok, _ := store.Get(ctx, evKey, 0)
	if !ok || !rec.Complete || rec.Version != 1 {
		t.Fatalf("watermark should publish complete v1, got %+v ok=%v", rec, ok)
	}
	if a.Stats().WatermarkPublished != 1 {
		t.Fatalf("expected 1 watermark publish, stats=%+v", a.Stats())
	}

	if err := a.Ingest(ctx, frag("node-a", "g1", 5, "s2", "f1", "only", 0)); err != nil {
		t.Fatalf("dup after wm: %v", err)
	}
	rec, _, _ = store.Get(ctx, evKey, 0)
	if rec.Version != 1 {
		t.Fatalf("duplicate fragment must not bump version, got v%d", rec.Version)
	}
}

func TestRestartReplaysWithoutDuplicates(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	storePath := filepath.Join(dir, "store.json")
	ctx := context.Background()

	store, err := OpenFileStore(storePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	a, err := New(newTestConfig(clock), store, statePath)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	f1 := frag("node-a", "g1", 3, "s1", "f1", "a", 2)
	f2 := frag("node-a", "g1", 3, "s2", "f2", "b", 2)
	if err := a.Ingest(ctx, f1); err != nil {
		t.Fatalf("ingest f1: %v", err)
	}
	clock.advance(11 * time.Second)
	if _, err := a.Sweep(clock.now()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	store2, err := OpenFileStore(storePath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	a2, err := New(newTestConfig(clock), store2, statePath)
	if err != nil {
		t.Fatalf("restart new: %v", err)
	}
	defer a2.Close()

	if err := a2.Ingest(ctx, f1); err != nil {
		t.Fatalf("replay f1: %v", err)
	}
	if err := a2.Ingest(ctx, f2); err != nil {
		t.Fatalf("correction f2: %v", err)
	}

	evKey := keyOf("node-a", "g1", 3)
	recs, _ := store2.List(ctx)
	if len(recs) != 1 {
		t.Fatalf("restart must keep one logical event, got %d", len(recs))
	}
	rec := recs[0]
	if rec.Key != evKey || rec.Version != 2 || !rec.Complete {
		t.Fatalf("unexpected post-restart record: %+v", rec)
	}
	st := store2.Stats()
	if st.DistinctKeys != 1 || st.VersionBumps != 1 {
		t.Fatalf("unexpected store stats: %+v", st)
	}
	if st.Changes != 2 {
		t.Fatalf("only the initial timeout publish and one correction may write, stats=%+v", st)
	}
	// 重放的旧片段在组装层被去重，不得产生第二个逻辑事件。
	if a2.Stats().DuplicateFragments != 1 {
		t.Fatalf("expected replay to be deduped at assembler layer, stats=%+v", a2.Stats())
	}
}

func TestTimeoutThenWatermarkCompletesCorrection(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	cfg := newTestConfig(clock)
	cfg.RequiredShards = []ShardID{"s1", "s2"}
	store := NewMemStore()
	a, _ := New(cfg, store, "")
	ctx := context.Background()

	evKey := keyOf("node-a", "g1", 8)
	if err := a.Ingest(ctx, frag("node-a", "g1", 8, "s1", "f1", "a", 0)); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	clock.advance(11 * time.Second)
	if _, err := a.Sweep(clock.now()); err != nil {
		t.Fatalf("timeout sweep: %v", err)
	}
	rec, _, _ := store.Get(ctx, evKey, 0)
	if rec.Complete || rec.Version != 1 {
		t.Fatalf("expected timeout incomplete v1, got %+v", rec)
	}

	// 水位线越过该事件：半成品按原键补正为完整、版本递增。
	if err := a.ObserveWatermark(ctx, "s1", 8); err != nil {
		t.Fatalf("wm s1: %v", err)
	}
	// s2 先上报落后水位线（不触发发布），再推进到 8。
	if err := a.ObserveWatermark(ctx, "s2", 7); err != nil {
		t.Fatalf("wm s2 lag: %v", err)
	}
	if err := a.ObserveWatermark(ctx, "s2", 8); err != nil {
		t.Fatalf("wm s2: %v", err)
	}
	rec, _, _ = store.Get(ctx, evKey, 0)
	if rec.Version != 2 || !rec.Complete {
		t.Fatalf("watermark completion should bump to complete v2, got %+v", rec)
	}

	// 再次 Sweep 不应重复发布或继续递增版本。
	res, err := a.Sweep(clock.now())
	if err != nil {
		t.Fatalf("idle sweep: %v", err)
	}
	if len(res.Published) != 0 || len(res.Corrected) != 0 {
		t.Fatalf("idle sweep must not republish, got %+v", res)
	}
	rec, _, _ = store.Get(ctx, evKey, 0)
	if rec.Version != 2 {
		t.Fatalf("sweep must not bump version, got v%d", rec.Version)
	}
	if got := store.VersionHistory(evKey, 0); len(got) != 2 {
		t.Fatalf("expected versions [1 2] after watermark completion, got %v", got)
	}

	// 真正的迟到片段到达时才沿用原键、版本递增做补正。
	if err := a.Ingest(ctx, frag("node-a", "g1", 8, "s2", "f2", "b", 0)); err != nil {
		t.Fatalf("late fragment: %v", err)
	}
	rec, _, _ = store.Get(ctx, evKey, 0)
	if rec.Version != 3 || !rec.Complete {
		t.Fatalf("late fragment should produce v3 correction, got %+v", rec)
	}
	if a.Stats().Corrections != 2 {
		t.Fatalf("expected watermark + payload corrections, stats=%+v", a.Stats())
	}
}
