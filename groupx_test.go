package groupx

import (
	"fmt"
	"testing"
)

// interleave feeds n rounds of records with identical IDs and groups but
// tenant-distinct payloads/values, alternating tenants A and B.
func interleave(t *testing.T, e *Engine, a, b string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("id-%d", i)
		recA := Record{Tenant: a, Group: "g", ID: id, Payload: "A-" + id, Value: 1, Ts: int64(2*i + 1), Seq: uint64(2*i + 1)}
		recB := Record{Tenant: b, Group: "g", ID: id, Payload: "B-" + id, Value: 100, Ts: int64(2*i + 2), Seq: uint64(2*i + 2)}
		if ok, err := e.Ingest(recA); err != nil || !ok {
			t.Fatalf("ingest %s/%s: applied=%v err=%v", a, id, ok, err)
		}
		if ok, err := e.Ingest(recB); err != nil || !ok {
			t.Fatalf("ingest %s/%s: applied=%v err=%v (cross-tenant dedup leak?)", b, id, ok, err)
		}
	}
}

// TestInterleavedSameIDNoLeak is the core isolation check: same IDs with
// different payloads interleaved across tenants must never dedup against
// each other, and every piece of state must stay tenant-private.
func TestInterleavedSameIDNoLeak(t *testing.T) {
	e := New(1000, "admin")
	interleave(t, e, "tenantA", "tenantB", 50)

	aggA, ok := e.Aggregate("tenantA", "g")
	if !ok || aggA.Count != 50 || aggA.Sum != 50 {
		t.Fatalf("tenantA aggregate = %+v, want Count=50 Sum=50", aggA)
	}
	aggB, ok := e.Aggregate("tenantB", "g")
	if !ok || aggB.Count != 50 || aggB.Sum != 5000 {
		t.Fatalf("tenantB aggregate = %+v, want Count=50 Sum=5000", aggB)
	}

	// Cache isolation: each tenant sees only its own payload for the same ID.
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("id-%d", i)
		if p, ok := e.CacheGet("tenantA", id); !ok || p != "A-"+id {
			t.Fatalf("tenantA cache[%s] = %q,%v (leak from B?)", id, p, ok)
		}
		if p, ok := e.CacheGet("tenantB", id); !ok || p != "B-"+id {
			t.Fatalf("tenantB cache[%s] = %q,%v (leak from A?)", id, p, ok)
		}
	}

	// Watermark isolation: B always had the higher event times.
	if wm := e.Watermark("tenantA"); wm != 99 {
		t.Fatalf("tenantA watermark = %d, want 99", wm)
	}
	if wm := e.Watermark("tenantB"); wm != 100 {
		t.Fatalf("tenantB watermark = %d, want 100", wm)
	}
	if e.HighSeq("tenantA") != 99 || e.HighSeq("tenantB") != 100 {
		t.Fatalf("highSeq leak: A=%d B=%d", e.HighSeq("tenantA"), e.HighSeq("tenantB"))
	}
}

// TestDedupIsPerTenant: duplicates drop only inside their own tenant.
func TestDedupIsPerTenant(t *testing.T) {
	e := New(10, "admin")
	rec := Record{Tenant: "A", Group: "g", ID: "x", Payload: "p1", Value: 5, Ts: 1, Seq: 1}
	if ok, _ := e.Ingest(rec); !ok {
		t.Fatal("first ingest dropped")
	}
	if ok, _ := e.Ingest(rec); ok {
		t.Fatal("duplicate within tenant A was applied")
	}
	// Same ID, same payload, different tenant: must apply.
	rec.Tenant = "B"
	if ok, _ := e.Ingest(rec); !ok {
		t.Fatal("same ID in tenant B wrongly deduplicated by tenant A")
	}
	aggA, _ := e.Aggregate("A", "g")
	aggB, _ := e.Aggregate("B", "g")
	if aggA.Count != 1 || aggB.Count != 1 {
		t.Fatalf("counts A=%d B=%d, want 1/1", aggA.Count, aggB.Count)
	}
}

// TestCheckpointRestoreIsolation: checkpointing and restoring tenant A must
// leave tenant B's dedup set, cache, aggregates and watermark untouched.
func TestCheckpointRestoreIsolation(t *testing.T) {
	e := New(1000, "admin")
	interleave(t, e, "A", "B", 10)

	if err := e.Checkpoint("A"); err != nil {
		t.Fatal(err)
	}

	// More interleaved traffic after the checkpoint.
	interleave2 := func(base int) {
		for i := base; i < base+5; i++ {
			id := fmt.Sprintf("id-%d", i)
			e.Ingest(Record{Tenant: "A", Group: "g", ID: id, Payload: "A2-" + id, Value: 1, Ts: int64(100 + i), Seq: uint64(100 + i)})
			e.Ingest(Record{Tenant: "B", Group: "g", ID: id, Payload: "B2-" + id, Value: 100, Ts: int64(200 + i), Seq: uint64(200 + i)})
		}
	}
	interleave2(10)

	// Snapshot B's expected state before A is restored.
	aggBBefore, _ := e.Aggregate("B", "g")
	wmBBefore := e.Watermark("B")
	seqBBefore := e.HighSeq("B")

	if err := e.Restore("A"); err != nil {
		t.Fatal(err)
	}

	// A rolled back to checkpoint: 10 records, original payloads, old watermark.
	aggA, _ := e.Aggregate("A", "g")
	if aggA.Count != 10 || aggA.Sum != 10 {
		t.Fatalf("restored A aggregate = %+v, want Count=10 Sum=10", aggA)
	}
	if wm := e.Watermark("A"); wm != 19 {
		t.Fatalf("restored A watermark = %d, want 19", wm)
	}
	if p, ok := e.CacheGet("A", "id-9"); !ok || p != "A-id-9" {
		t.Fatalf("restored A cache[id-9] = %q,%v", p, ok)
	}
	if _, ok := e.CacheGet("A", "id-10"); ok {
		t.Fatal("restored A cache still holds post-checkpoint entry id-10")
	}
	// Restored dedup set: re-ingesting a pre-checkpoint ID drops, a
	// post-checkpoint ID applies again.
	if ok, _ := e.Ingest(Record{Tenant: "A", Group: "g", ID: "id-3", Payload: "x", Value: 1, Ts: 300, Seq: 300}); ok {
		t.Fatal("restored A lost pre-checkpoint dedup state")
	}
	if ok, _ := e.Ingest(Record{Tenant: "A", Group: "g", ID: "id-10", Payload: "x", Value: 1, Ts: 300, Seq: 300}); !ok {
		t.Fatal("restored A should accept post-checkpoint ID id-10 again")
	}

	// B completely unaffected by A's checkpoint+restore cycle.
	aggBAfter, _ := e.Aggregate("B", "g")
	if aggBAfter != aggBBefore {
		t.Fatalf("B aggregate changed by A restore: before=%+v after=%+v", aggBBefore, aggBAfter)
	}
	if e.Watermark("B") != wmBBefore || e.HighSeq("B") != seqBBefore {
		t.Fatalf("B watermark/seq changed by A restore")
	}
	for i := 0; i < 15; i++ {
		id := fmt.Sprintf("id-%d", i)
		want := "B-" + id
		if i >= 10 {
			want = "B2-" + id
		}
		if p, ok := e.CacheGet("B", id); !ok || p != want {
			t.Fatalf("B cache[%s] = %q,%v after A restore, want %q", id, p, ok, want)
		}
	}
}

// TestCacheEvictionPerTenant: one tenant's cache pressure never evicts
// another tenant's entries.
func TestCacheEvictionPerTenant(t *testing.T) {
	e := New(3, "admin")
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("k%d", i)
		e.Ingest(Record{Tenant: "A", Group: "g", ID: id, Payload: "pa" + id, Value: 1, Ts: int64(i), Seq: uint64(i)})
	}
	e.Ingest(Record{Tenant: "B", Group: "g", ID: "k0", Payload: "pb0", Value: 1, Ts: 1, Seq: 1})
	if e.CacheLen("A") != 3 || e.CacheLen("B") != 1 {
		t.Fatalf("cache lens A=%d B=%d, want 3/1", e.CacheLen("A"), e.CacheLen("B"))
	}
	if _, ok := e.CacheGet("A", "k0"); ok {
		t.Fatal("A:k0 should have been evicted from A's small cache")
	}
	if p, ok := e.CacheGet("B", "k0"); !ok || p != "pb0" {
		t.Fatalf("B:k0 = %q,%v; A's eviction must not touch B", p, ok)
	}
}

// TestMergeRequiresAuthorization: no token / wrong token / mismatched
// source set all fail and change nothing.
func TestMergeRequiresAuthorization(t *testing.T) {
	e := New(100, "admin")
	interleave(t, e, "A", "B", 5)

	if err := e.Merge("bogus-token", []string{"A", "B"}, "C"); err != ErrUnauthorizedMerge {
		t.Fatalf("merge with bogus token: err=%v, want ErrUnauthorizedMerge", err)
	}
	if err := e.Merge("", []string{"A", "B"}, "C"); err != ErrUnauthorizedMerge {
		t.Fatalf("merge with empty token: err=%v, want ErrUnauthorizedMerge", err)
	}

	tok, err := e.IssueMergeToken("admin", []string{"A", "B"}, "C")
	if err != nil {
		t.Fatal(err)
	}
	// Token bound to exact source set and target.
	if err := e.Merge(tok, []string{"A", "B"}, "D"); err != ErrUnauthorizedMerge {
		t.Fatalf("merge to wrong target: err=%v", err)
	}
	if err := e.Merge(tok, []string{"A"}, "C"); err != ErrInvalidMerge {
		t.Fatalf("merge with single source: err=%v", err)
	}
	// Wrong admin key cannot mint tokens.
	if _, err := e.IssueMergeToken("wrong", []string{"A", "B"}, "C"); err != ErrBadAdminKey {
		t.Fatalf("issue with bad key: err=%v", err)
	}
	// Nothing was created or modified.
	for _, name := range e.Tenants() {
		if name != "A" && name != "B" {
			t.Fatalf("unexpected namespace %q created by unauthorized merge", name)
		}
	}
	aggA, _ := e.Aggregate("A", "g")
	aggB, _ := e.Aggregate("B", "g")
	if aggA.Count != 5 || aggB.Count != 5 {
		t.Fatalf("sources modified by failed merges: A=%+v B=%+v", aggA, aggB)
	}
}

// TestAuthorizedMergeCreatesNewNamespace: the only cross-tenant path.
func TestAuthorizedMergeCreatesNewNamespace(t *testing.T) {
	e := New(100, "admin")
	interleave(t, e, "A", "B", 5)

	tok, err := e.IssueMergeToken("admin", []string{"A", "B"}, "M")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Merge(tok, []string{"B", "A"}, "M"); err != nil { // order-insensitive
		t.Fatal(err)
	}

	// New namespace holds the union.
	agg, ok := e.Aggregate("M", "g")
	if !ok || agg.Count != 10 || agg.Sum != 505 {
		t.Fatalf("merged aggregate = %+v, want Count=10 Sum=505", agg)
	}
	if wm := e.Watermark("M"); wm != 10 {
		t.Fatalf("merged watermark = %d, want 10", wm)
	}
	// Merged dedup set: IDs seen in either source dedup inside M.
	if ok, _ := e.Ingest(Record{Tenant: "M", Group: "g", ID: "id-0", Payload: "z", Value: 1, Ts: 99, Seq: 99}); ok {
		t.Fatal("merged namespace failed to dedup ID known from sources")
	}
	// Merged cache has entries; payload winner is deterministic by event time.
	if p, ok := e.CacheGet("M", "id-0"); !ok || (p != "A-id-0" && p != "B-id-0") {
		t.Fatalf("merged cache[id-0] = %q,%v", p, ok)
	}

	// Sources untouched.
	aggA, _ := e.Aggregate("A", "g")
	aggB, _ := e.Aggregate("B", "g")
	if aggA.Count != 5 || aggA.Sum != 5 || aggB.Count != 5 || aggB.Sum != 500 {
		t.Fatalf("sources mutated by merge: A=%+v B=%+v", aggA, aggB)
	}

	// Token is one-time.
	if err := e.Merge(tok, []string{"A", "B"}, "M2"); err != ErrUnauthorizedMerge {
		t.Fatalf("token reuse: err=%v, want ErrUnauthorizedMerge", err)
	}
	// Target must be a fresh namespace.
	tok2, _ := e.IssueMergeToken("admin", []string{"A", "B"}, "M")
	if err := e.Merge(tok2, []string{"A", "B"}, "M"); err != ErrTargetExists {
		t.Fatalf("merge into existing namespace: err=%v, want ErrTargetExists", err)
	}
	// Target cannot be a source.
	if _, err := e.IssueMergeToken("admin", []string{"A", "B"}, "A"); err != ErrTargetIsSource {
		t.Fatalf("target==source: err=%v, want ErrTargetIsSource", err)
	}
	// Merged namespace is isolated afterwards: its own dedup/cache/watermark.
	if ok, _ := e.Ingest(Record{Tenant: "M", Group: "g", ID: "new", Payload: "pm", Value: 7, Ts: 500, Seq: 500}); !ok {
		t.Fatal("post-merge ingest into M dropped")
	}
	if wm := e.Watermark("M"); wm != 500 {
		t.Fatalf("M watermark after own ingest = %d, want 500", wm)
	}
	if wm := e.Watermark("A"); wm != 9 {
		t.Fatalf("A watermark leaked from M: %d", wm)
	}
}

// TestMergeDisabledWithoutAdminKey.
func TestMergeDisabledWithoutAdminKey(t *testing.T) {
	e := New(10, "")
	if _, err := e.IssueMergeToken("anything", []string{"A", "B"}, "C"); err != ErrMergeDisabled {
		t.Fatalf("err=%v, want ErrMergeDisabled", err)
	}
}

// TestRestoreWithoutCheckpoint.
func TestRestoreWithoutCheckpoint(t *testing.T) {
	e := New(10, "admin")
	if err := e.Restore("ghost"); err != ErrNoCheckpoint {
		t.Fatalf("err=%v, want ErrNoCheckpoint", err)
	}
	e.Ingest(Record{Tenant: "A", Group: "g", ID: "x", Value: 1, Ts: 1, Seq: 1})
	if err := e.Restore("A"); err != ErrNoCheckpoint {
		t.Fatalf("err=%v, want ErrNoCheckpoint", err)
	}
}
