package groupcalc

import (
	"fmt"
	"testing"
)

func payloads(g Group) []string {
	out := make([]string, 0, len(g.Entries))
	for _, e := range g.Entries {
		out = append(out, e.Payload)
	}
	return out
}

func findGroup(t *testing.T, gs []Group, id string) Group {
	t.Helper()
	for _, g := range gs {
		if g.ID == id {
			return g
		}
	}
	t.Fatalf("group %q not found in %v", id, gs)
	return Group{}
}

// Interleaved same-id, different-payload input across tenants must not
// deduplicate across tenants and must not leak payloads between tenants.
func TestInterleavedSameIDNoCrossTenantDedup(t *testing.T) {
	eng := NewEngine(nil)
	tenants := []string{"tenant-a", "tenant-b", "tenant-c"}

	// Interleave: every tenant sends the same ids with tenant-unique
	// payloads, plus one record key ("shared-key") that is byte-identical
	// across all tenants and must still be applied once per tenant.
	seq := map[string]uint64{}
	ingest := func(tn, key, id, payload string) {
		t.Helper()
		seq[tn]++
		applied, err := eng.Ingest(tn, Record{Key: key, ID: id, Payload: payload, Seq: seq[tn]})
		if err != nil {
			t.Fatalf("ingest %s: %v", tn, err)
		}
		if !applied {
			t.Fatalf("cross-tenant dedup: %s key=%s id=%s dropped", tn, key, id)
		}
	}
	for round := 0; round < 3; round++ {
		for _, tn := range tenants {
			for _, id := range []string{"id-1", "id-2"} {
				key := fmt.Sprintf("key-%s-r%d-%s", tn, round, id)
				ingest(tn, key, id, fmt.Sprintf("%s-payload-r%d-%s", tn, round, id))
			}
		}
	}
	// One record key that is byte-identical across all tenants: it must be
	// applied exactly once per tenant, never deduplicated across tenants.
	for _, tn := range tenants {
		ingest(tn, "shared-key", "id-shared", tn+"-shared-payload")
	}

	for _, tn := range tenants {
		gs := eng.Groups(tn)
		if len(gs) != 3 {
			t.Fatalf("%s: want 3 groups, got %d (%+v)", tn, len(gs), gs)
		}
		for _, id := range []string{"id-1", "id-2"} {
			g := findGroup(t, gs, id)
			if len(g.Entries) != 3 {
				t.Fatalf("%s id=%s: want 3 entries, got %d", tn, id, len(g.Entries))
			}
			for _, e := range g.Entries {
				if e.Origin != tn {
					t.Fatalf("leak: %s group %s contains entry from %s", tn, id, e.Origin)
				}
				want := tn + "-payload"
				if got := e.Payload[:len(want)]; got != want {
					t.Fatalf("leak: %s group %s has foreign payload %q", tn, id, e.Payload)
				}
			}
		}
		g := findGroup(t, gs, "id-shared")
		if ps := payloads(g); len(ps) != 1 || ps[0] != tn+"-shared-payload" {
			t.Fatalf("%s id-shared: want own payload only, got %v", tn, ps)
		}
	}
}

// Dedup must still work *within* a tenant: the same record key re-sent is
// dropped, while a different key for the same id groups normally.
func TestIntraTenantDedupStillWorks(t *testing.T) {
	eng := NewEngine(nil)
	if ok, _ := eng.Ingest("t1", Record{Key: "r1", ID: "x", Payload: "p1", Seq: 1}); !ok {
		t.Fatal("first ingest should apply")
	}
	ok, err := eng.Ingest("t1", Record{Key: "r1", ID: "x", Payload: "p2", Seq: 2})
	if err != nil || ok {
		t.Fatalf("intra-tenant duplicate should be dropped, got applied=%v err=%v", ok, err)
	}
	if ok, _ := eng.Ingest("t1", Record{Key: "r2", ID: "x", Payload: "p3", Seq: 3}); !ok {
		t.Fatal("new key for same id should group, not dedup")
	}
	g := findGroup(t, eng.Groups("t1"), "x")
	if ps := payloads(g); len(ps) != 2 || ps[0] != "p1" || ps[1] != "p3" {
		t.Fatalf("dedup cache polluted group: %v", ps)
	}
}

// Watermarks are per-tenant: a high watermark in one tenant must not reject
// another tenant's lower sequence numbers, and replays within a tenant are
// rejected against that tenant's own watermark only.
func TestWatermarkIsolation(t *testing.T) {
	eng := NewEngine(nil)
	if _, err := eng.Ingest("hot", Record{ID: "a", Payload: "p", Seq: 1000}); err != nil {
		t.Fatal(err)
	}
	// Cold tenant with low seq must not be affected by hot tenant's watermark.
	if ok, err := eng.Ingest("cold", Record{ID: "a", Payload: "p", Seq: 1}); err != nil || !ok {
		t.Fatalf("cross-tenant watermark interference: applied=%v err=%v", ok, err)
	}
	// Replay within the same tenant is rejected.
	if _, err := eng.Ingest("hot", Record{ID: "b", Payload: "p", Seq: 1000}); err != ErrStaleSeq {
		t.Fatalf("want ErrStaleSeq, got %v", err)
	}
	if _, err := eng.Ingest("hot", Record{ID: "b", Payload: "p", Seq: 999}); err != ErrStaleSeq {
		t.Fatalf("want ErrStaleSeq, got %v", err)
	}
	wm, ok := eng.Watermark("hot")
	if !ok || wm != 1000 {
		t.Fatalf("hot watermark = %d,%v want 1000,true", wm, ok)
	}
	wm, ok = eng.Watermark("cold")
	if !ok || wm != 1 {
		t.Fatalf("cold watermark = %d,%v want 1,true", wm, ok)
	}
}

// Restoring one tenant from its checkpoint must not disturb any other
// tenant's groups, dedup cache or watermark.
func TestRecoveryIsolation(t *testing.T) {
	eng := NewEngine(nil)

	mustApply := func(tn, key, id, payload string, seq uint64) {
		t.Helper()
		if ok, err := eng.Ingest(tn, Record{Key: key, ID: id, Payload: payload, Seq: seq}); err != nil || !ok {
			t.Fatalf("ingest %s/%s: applied=%v err=%v", tn, key, ok, err)
		}
	}

	mustApply("a", "a1", "k", "a-before-1", 1)
	mustApply("b", "b1", "k", "b-1", 1)
	if err := eng.Checkpoint("a", "cp1"); err != nil {
		t.Fatal(err)
	}
	// After the checkpoint both tenants keep ingesting, same ids interleaved.
	mustApply("a", "a2", "k2", "a-after-1", 2)
	// Replay of b's own key is deduped inside b only.
	if ok, err := eng.Ingest("b", Record{Key: "b1", ID: "k", Payload: "b-replay", Seq: 2}); err != nil || ok {
		t.Fatalf("b replay should dedup: applied=%v err=%v", ok, err)
	}
	mustApply("b", "b2", "k2", "b-2", 3)

	if err := eng.Restore("a", "cp1"); err != nil {
		t.Fatal(err)
	}

	// Tenant a rolled back: only pre-checkpoint state remains.
	ga := eng.Groups("a")
	if len(ga) != 1 || ga[0].ID != "k" || len(ga[0].Entries) != 1 {
		t.Fatalf("tenant a not restored to checkpoint: %+v", ga)
	}
	if wm, _ := eng.Watermark("a"); wm != 1 {
		t.Fatalf("tenant a watermark = %d, want 1", wm)
	}
	// Restored dedup cache must be the checkpoint's: key a2 (seen only after
	// the checkpoint) applies again; key a1 is still deduped.
	if ok, err := eng.Ingest("a", Record{Key: "a2", ID: "k2", Payload: "a-after-1b", Seq: 2}); err != nil || !ok {
		t.Fatalf("post-restore a2 should apply: applied=%v err=%v", ok, err)
	}
	if ok, err := eng.Ingest("a", Record{Key: "a1", ID: "k", Payload: "a-dup", Seq: 3}); err != nil || ok {
		t.Fatalf("post-restore a1 should dedup: applied=%v err=%v", ok, err)
	}

	// Tenant b completely untouched by a's checkpoint/restore cycle.
	gb := eng.Groups("b")
	if len(gb) != 2 {
		t.Fatalf("tenant b disturbed: %+v", gb)
	}
	if ps := payloads(findGroup(t, gb, "k")); len(ps) != 1 || ps[0] != "b-1" {
		t.Fatalf("tenant b group k leaked/lost entries: %v", ps)
	}
	if ps := payloads(findGroup(t, gb, "k2")); len(ps) != 1 || ps[0] != "b-2" {
		t.Fatalf("tenant b group k2 leaked/lost entries: %v", ps)
	}
	if wm, _ := eng.Watermark("b"); wm != 3 {
		t.Fatalf("tenant b watermark = %d, want 3", wm)
	}
}

// Checkpoints are namespaced per tenant: same checkpoint name in different
// tenants holds independent snapshots.
func TestCheckpointNamespaceIsolation(t *testing.T) {
	eng := NewEngine(nil)
	eng.Ingest("a", Record{ID: "x", Payload: "from-a", Seq: 1})
	eng.Ingest("b", Record{ID: "x", Payload: "from-b", Seq: 1})
	if err := eng.Checkpoint("a", "snap"); err != nil {
		t.Fatal(err)
	}
	if err := eng.Checkpoint("b", "snap"); err != nil {
		t.Fatal(err)
	}
	eng.Ingest("a", Record{ID: "y", Payload: "a-extra", Seq: 2})
	eng.Ingest("b", Record{ID: "z", Payload: "b-extra", Seq: 2})

	if err := eng.Restore("a", "snap"); err != nil {
		t.Fatal(err)
	}
	if len(eng.Groups("a")) != 1 {
		t.Fatalf("a restore wrong: %+v", eng.Groups("a"))
	}
	if len(eng.Groups("b")) != 2 {
		t.Fatalf("b must be unaffected by a's restore: %+v", eng.Groups("b"))
	}
	// Restoring b from a nonexistent checkpoint name fails; restoring a's
	// checkpoint into b is impossible by construction (keyed by tenant).
	if err := eng.Restore("b", "nope"); err != ErrNoCheckpoint {
		t.Fatalf("want ErrNoCheckpoint, got %v", err)
	}
}

// Cross-tenant merge requires explicit authorization and yields a new
// namespace; sources stay intact and same-id groups remain distinct.
func TestMergeAuthorizationAndIsolation(t *testing.T) {
	allow := func(op string, sources []string, target string) bool {
		return op == "merge" && target == "merged-ns" && len(sources) == 2
	}
	eng := NewEngine(allow)

	eng.Ingest("a", Record{ID: "k", Payload: "a-payload", Seq: 5})
	eng.Ingest("b", Record{ID: "k", Payload: "b-payload", Seq: 7})

	// Unauthorized target is denied.
	if err := eng.Merge("sneaky", "a", "b"); err != ErrUnauthorized {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
	// Nil authorizer denies everything.
	if err := NewEngine(nil).Merge("m", "a"); err != ErrUnauthorized {
		t.Fatalf("nil authorizer must deny, got %v", err)
	}
	// Target must be new and not a source.
	if err := eng.Merge("a", "a", "b"); err != ErrTargetIsSource {
		t.Fatalf("want ErrTargetIsSource, got %v", err)
	}
	eng.Ingest("existing", Record{ID: "q", Payload: "p", Seq: 1})
	if err := eng.Merge("existing", "a", "b"); err != ErrTargetExists {
		t.Fatalf("want ErrTargetExists, got %v", err)
	}

	// Authorized merge into a fresh namespace.
	if err := eng.Merge("merged-ns", "a", "b"); err != nil {
		t.Fatal(err)
	}
	gm := eng.Groups("merged-ns")
	if len(gm) != 2 {
		t.Fatalf("same id from two tenants must stay distinct, got %+v", gm)
	}
	ga := findGroup(t, gm, "a/k")
	gb := findGroup(t, gm, "b/k")
	if ps := payloads(ga); len(ps) != 1 || ps[0] != "a-payload" {
		t.Fatalf("merged a/k wrong: %v", ps)
	}
	if ps := payloads(gb); len(ps) != 1 || ps[0] != "b-payload" {
		t.Fatalf("merged b/k wrong: %v", ps)
	}
	if ga.Entries[0].Origin != "a" || gb.Entries[0].Origin != "b" {
		t.Fatalf("origin tags lost: %+v %+v", ga, gb)
	}
	// Merged watermark is the max of sources.
	if wm, ok := eng.Watermark("merged-ns"); !ok || wm != 7 {
		t.Fatalf("merged watermark = %d,%v want 7,true", wm, ok)
	}

	// Sources untouched by the merge.
	if ps := payloads(findGroup(t, eng.Groups("a"), "k")); len(ps) != 1 || ps[0] != "a-payload" {
		t.Fatalf("source a mutated by merge: %v", ps)
	}
	if ps := payloads(findGroup(t, eng.Groups("b"), "k")); len(ps) != 1 || ps[0] != "b-payload" {
		t.Fatalf("source b mutated by merge: %v", ps)
	}

	// The merged namespace is independent: mutating it must not leak back,
	// and its dedup cache is its own.
	if ok, err := eng.Ingest("merged-ns", Record{ID: "a/k", Payload: "dup", Seq: 8}); err != nil || ok {
		t.Fatalf("merged dedup cache missing key: applied=%v err=%v", ok, err)
	}
	if ok, err := eng.Ingest("merged-ns", Record{ID: "new", Payload: "fresh", Seq: 9}); err != nil || !ok {
		t.Fatalf("merged namespace should accept new ids: applied=%v err=%v", ok, err)
	}
	if len(eng.Groups("a")) != 1 || len(eng.Groups("b")) != 1 {
		t.Fatal("write to merged namespace leaked into sources")
	}
}

// Snapshot copies must be deep: mutating a returned group cannot corrupt
// engine state.
func TestGroupsSnapshotIsDeepCopy(t *testing.T) {
	eng := NewEngine(nil)
	eng.Ingest("a", Record{ID: "k", Payload: "orig", Seq: 1})
	gs := eng.Groups("a")
	gs[0].Entries[0].Payload = "tampered"
	if got := eng.Groups("a")[0].Entries[0].Payload; got != "orig" {
		t.Fatalf("engine state mutated via snapshot: %q", got)
	}
}
