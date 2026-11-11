package filtermerge

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func testParams() Params {
	return Params{TenantID: "tenant-a", SaltGen: 7, NumCounters: 1024, NumHashes: 4, CounterBits: 8}
}

// collidingHash maps every key to the same value, forcing all positions to
// counter 0 regardless of the key. Used to construct deliberate collisions.
func collidingHash([]byte, uint64) uint64 { return 0 }

func collidingParams() Params {
	return Params{TenantID: "tenant-a", SaltGen: 1, NumCounters: 8, NumHashes: 3, CounterBits: 8}
}

func mustShard(t *testing.T, id string, p Params, keys []string, opts ...Option) *Shard {
	t.Helper()
	s, err := NewShard(id, p, opts...)
	if err != nil {
		t.Fatalf("NewShard(%q): %v", id, err)
	}
	for _, k := range keys {
		s.Add([]byte(k))
	}
	return s
}

func mustSummary(t *testing.T, p Params, opts ...Option) *Summary {
	t.Helper()
	m, err := NewSummary(p, opts...)
	if err != nil {
		t.Fatalf("NewSummary: %v", err)
	}
	return m
}

func TestMergeRequiresMatchingParams(t *testing.T) {
	base := testParams()
	m := mustSummary(t, base)

	cases := map[string]Params{
		"different tenant":      {TenantID: "tenant-b", SaltGen: 7, NumCounters: 1024, NumHashes: 4, CounterBits: 8},
		"different salt gen":    {TenantID: "tenant-a", SaltGen: 8, NumCounters: 1024, NumHashes: 4, CounterBits: 8},
		"different counter num": {TenantID: "tenant-a", SaltGen: 7, NumCounters: 2048, NumHashes: 4, CounterBits: 8},
		"different hash num":    {TenantID: "tenant-a", SaltGen: 7, NumCounters: 1024, NumHashes: 5, CounterBits: 8},
		"different width":       {TenantID: "tenant-a", SaltGen: 7, NumCounters: 1024, NumHashes: 4, CounterBits: 4},
	}
	for name, p := range cases {
		s := mustShard(t, "shard-"+name, p, []string{"k"})
		if err := m.Merge(s); !errors.Is(err, ErrIncompatible) {
			t.Errorf("%s: got %v, want ErrIncompatible", name, err)
		}
	}
	if m.Len() != 0 {
		t.Fatalf("rejected merges changed summary: Len()=%d", m.Len())
	}

	ok := mustShard(t, "shard-ok", base, []string{"k"})
	if err := m.Merge(ok); err != nil {
		t.Fatalf("matching params rejected: %v", err)
	}
}

func TestSnapshotQueryAndImmutability(t *testing.T) {
	p := testParams()
	m := mustSummary(t, p)
	must := func(s *Shard) {
		t.Helper()
		if err := m.Merge(s); err != nil {
			t.Fatalf("Merge(%q): %v", s.ID(), err)
		}
	}
	must(mustShard(t, "h1", p, []string{"alpha", "beta"}))
	must(mustShard(t, "h2", p, []string{"gamma"}))

	snap := m.Snapshot()
	for _, k := range []string{"alpha", "beta", "gamma"} {
		if !snap.Contains([]byte(k)) {
			t.Errorf("snapshot missing %q", k)
		}
	}
	// Find a deterministically absent key.
	absent := ""
	for i := 0; ; i++ {
		k := fmt.Sprintf("absent-%d", i)
		if !snap.Contains([]byte(k)) {
			absent = k
			break
		}
	}

	// Later merges and revocations must not mutate the taken snapshot.
	must(mustShard(t, "h3", p, []string{"epsilon"}))
	m.Revoke("h1")
	if snap.Contains([]byte("epsilon")) {
		t.Error("snapshot reflects a merge that happened after it was taken")
	}
	if !snap.Contains([]byte("alpha")) {
		t.Error("snapshot reflects a revocation that happened after it was taken")
	}
	if snap.Contains([]byte(absent)) {
		t.Errorf("snapshot reports %q present", absent)
	}

	live := m.Snapshot()
	if live.Contains([]byte("alpha")) {
		t.Error("revoked shard key still present in fresh snapshot")
	}
	if !live.Contains([]byte("epsilon")) || !live.Contains([]byte("gamma")) {
		t.Error("fresh snapshot missing live keys")
	}
}

func TestDeliberateCollisionRevokeKeepsOtherShards(t *testing.T) {
	p := collidingParams()
	opt := WithHashFunc(collidingHash)
	m := mustSummary(t, p, opt)

	// Every key hashes to the same counters: key-a and key-b fully collide.
	a := mustShard(t, "a", p, []string{"key-a"}, opt)
	b := mustShard(t, "b", p, []string{"key-b"}, opt)
	if err := m.Merge(a); err != nil {
		t.Fatal(err)
	}
	if err := m.Merge(b); err != nil {
		t.Fatal(err)
	}

	if !m.Revoke("a") {
		t.Fatal("Revoke(a) = false, want true")
	}
	snap := m.Snapshot()
	if !snap.Contains([]byte("key-b")) {
		t.Error("false negative: key-b lost after revoking colliding shard")
	}
	if snap.Contains([]byte("key-a")) {
		// key-a collides with key-b, so a positive is an acceptable false
		// positive, but with counting semantics the counter should reflect
		// only key-b's contribution and key-a is indistinguishable. This is
		// expected; document rather than fail.
		t.Log("key-a indistinguishable from key-b under forced collision (expected false positive)")
	}
}

func TestSaturationNeverCausesFalseNegative(t *testing.T) {
	// 2-bit counters saturate at 3; five colliding shards overflow them.
	p := Params{TenantID: "tenant-a", SaltGen: 1, NumCounters: 8, NumHashes: 3, CounterBits: 2}
	opt := WithHashFunc(collidingHash)
	m := mustSummary(t, p, opt)

	ids := []string{"s1", "s2", "s3", "s4", "s5"}
	for _, id := range ids {
		if err := m.Merge(mustShard(t, id, p, []string{"key-" + id}, opt)); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.Snapshot().Saturated(); got == 0 {
		t.Fatal("expected saturated counters after merging 5 colliding shards")
	}

	// Revoke three shards; the sticky saturated counter must keep the
	// remaining two keys queryable.
	for _, id := range ids[:3] {
		if !m.Revoke(id) {
			t.Fatalf("Revoke(%q) = false", id)
		}
	}
	snap := m.Snapshot()
	for _, id := range ids[3:] {
		if !snap.Contains([]byte("key-" + id)) {
			t.Errorf("false negative for %q after revoking saturated shards", "key-"+id)
		}
	}
	if got := snap.Saturated(); got == 0 {
		t.Error("saturated counter was decremented during revocation")
	}
}

func TestDuplicateDeliveryIsIdempotent(t *testing.T) {
	p := collidingParams()
	opt := WithHashFunc(collidingHash)
	m := mustSummary(t, p, opt)

	s := mustShard(t, "hour-2026-11-11-09", p, []string{"event-1"}, opt)
	for i := 0; i < 3; i++ {
		if err := m.Merge(s); err != nil {
			t.Fatalf("merge attempt %d: %v", i, err)
		}
	}
	if m.Len() != 1 {
		t.Fatalf("Len()=%d after duplicate deliveries, want 1", m.Len())
	}

	// A single revoke must fully undo the (single) logical delivery.
	if !m.Revoke("hour-2026-11-11-09") {
		t.Fatal("revoke of delivered shard failed")
	}
	if m.Snapshot().Contains([]byte("event-1")) {
		t.Error("duplicate delivery left residual counts after one revoke")
	}
}

func TestRevokeIsIdempotent(t *testing.T) {
	p := collidingParams()
	opt := WithHashFunc(collidingHash)
	m := mustSummary(t, p, opt)

	m.Merge(mustShard(t, "a", p, []string{"key-a"}, opt))
	m.Merge(mustShard(t, "b", p, []string{"key-b"}, opt))

	if !m.Revoke("a") {
		t.Fatal("first Revoke(a) = false")
	}
	if m.Revoke("a") {
		t.Fatal("second Revoke(a) = true, want idempotent false")
	}
	if m.Revoke("never-merged") {
		t.Fatal("Revoke(unknown) = true, want false")
	}
	if !m.Snapshot().Contains([]byte("key-b")) {
		t.Error("false negative: repeated revoke damaged surviving shard")
	}
	if m.Len() != 1 {
		t.Fatalf("Len()=%d, want 1", m.Len())
	}
}

func TestMemoryLimit(t *testing.T) {
	// 100 counters: aggregate costs 100 bytes, each retained shard 100 more.
	p := Params{TenantID: "tenant-a", SaltGen: 1, NumCounters: 100, NumHashes: 2, CounterBits: 8}
	m := mustSummary(t, p, WithMaxBytes(250))

	if err := m.Merge(mustShard(t, "s1", p, []string{"alpha"})); err != nil {
		t.Fatalf("first merge within budget: %v", err)
	}
	if got, want := m.Bytes(), 200; got != want {
		t.Fatalf("Bytes()=%d, want %d", got, want)
	}

	err := m.Merge(mustShard(t, "s2", p, []string{"beta"}))
	if !errors.Is(err, ErrMemoryLimit) {
		t.Fatalf("got %v, want ErrMemoryLimit", err)
	}
	// Failed merge must leave the summary untouched.
	if m.Len() != 1 || m.Bytes() != 200 {
		t.Fatalf("rejected merge mutated summary: Len()=%d Bytes()=%d", m.Len(), m.Bytes())
	}
	if m.Snapshot().Contains([]byte("beta")) {
		t.Error("key from rejected shard is visible")
	}

	// Revoking frees budget, so the shard can be merged afterwards.
	m.Revoke("s1")
	if err := m.Merge(mustShard(t, "s2", p, []string{"beta"})); err != nil {
		t.Fatalf("merge after freeing budget: %v", err)
	}
}

func TestSaltGenerationChangesPositions(t *testing.T) {
	p1 := testParams()
	p2 := p1
	p2.SaltGen = 8
	s1 := mustShard(t, "a", p1, []string{"same-key"})
	s2 := mustShard(t, "b", p2, []string{"same-key"})
	if fmt.Sprint(s1.counters) == fmt.Sprint(s2.counters) {
		t.Error("salt generation rotation did not change counter layout")
	}
}

func TestInvalidParams(t *testing.T) {
	bad := []Params{
		{TenantID: "", SaltGen: 1, NumCounters: 8, NumHashes: 1, CounterBits: 8},
		{TenantID: "t", SaltGen: 1, NumCounters: 0, NumHashes: 1, CounterBits: 8},
		{TenantID: "t", SaltGen: 1, NumCounters: 8, NumHashes: 0, CounterBits: 8},
		{TenantID: "t", SaltGen: 1, NumCounters: 8, NumHashes: 1, CounterBits: 0},
		{TenantID: "t", SaltGen: 1, NumCounters: 8, NumHashes: 1, CounterBits: 9},
	}
	for i, p := range bad {
		if _, err := NewSummary(p); err == nil {
			t.Errorf("case %d: NewSummary accepted invalid params", i)
		}
		if _, err := NewShard("s", p); err == nil {
			t.Errorf("case %d: NewShard accepted invalid params", i)
		}
	}
}

func TestConcurrentMergeSnapshotRevoke(t *testing.T) {
	p := testParams()
	m := mustSummary(t, p)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("shard-%d", w)
			key := fmt.Sprintf("key-%d", w)
			for i := 0; i < 50; i++ {
				s, err := NewShard(id, p)
				if err != nil {
					t.Errorf("NewShard: %v", err)
					return
				}
				s.Add([]byte(key))
				m.Merge(s)
				snap := m.Snapshot()
				if m.Len() > 0 && !snap.Contains([]byte(key)) {
					t.Errorf("false negative for %q under concurrency", key)
				}
				if i%3 == 0 {
					m.Revoke(id)
				}
			}
		}(w)
	}
	wg.Wait()
}
