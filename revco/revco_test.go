package revco

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func own(p string) Owner { return Owner{Process: p} }

func mustObserve(t *testing.T, g *Graph, evs ...Event) {
	t.Helper()
	for _, ev := range evs {
		if err := g.Observe(ev); err != nil {
			t.Fatalf("Observe %v: %v", ev, err)
		}
	}
}

func mustAdvance(t *testing.T, g *Graph, wm map[ChanID]Seq) {
	t.Helper()
	chans := make([]ChanID, 0, len(wm))
	for ch := range wm {
		chans = append(chans, ch)
	}
	sort.Slice(chans, func(i, j int) bool { return chans[i] < chans[j] })
	for _, ch := range chans {
		if err := g.Advance(ch, wm[ch]); err != nil {
			t.Fatalf("Advance %s: %v", ch, err)
		}
	}
}

type hostErr string

func (e hostErr) Error() string { return string(e) }

const errInterrupt = hostErr("simulated host interruption")

type recordingHost struct {
	calls  []HoldKey
	failOn map[HoldKey]int
	failed map[HoldKey]int
}

func (h *recordingHost) Revoke(k HoldKey) error {
	h.calls = append(h.calls, k)
	if h.failOn[k] > h.failed[k] {
		if h.failed == nil {
			h.failed = map[HoldKey]int{}
		}
		h.failed[k]++
		return errInterrupt
	}
	return nil
}

func rangesEqual(a, b []Range) bool {
	return reflect.DeepEqual(unionRanges(a), unionRanges(b))
}

func keySet(keys []HoldKey) map[HoldKey]bool {
	out := map[HoldKey]bool{}
	for _, k := range keys {
		out[k] = true
	}
	return out
}

// TestCrossProcessTransfer verifies copy/send/receive across channels and that
// closing the sender's source handle does not revoke the delivered receive
// right; isolation still revokes every provenance-descended hold.
func TestCrossProcessTransfer(t *testing.T) {
	g := NewGraph()
	const obj ObjID = "mapA"
	root := HoldKey{Gen: 1, ID: "h0"}
	mustObserve(t, g,
		Event{Type: EvIssue, Channel: "cA", Seq: 1, Gen: 1, Object: obj, NewID: "h0",
			Owner: own("A"), Range: Range{Off: 0, Len: 128}, Rights: []Right{Read, Write}},
		Event{Type: EvCopy, Channel: "cA", Seq: 2, Gen: 1, Object: obj, NewID: "h1",
			Owner: own("A"), Source: "h0", Range: Range{Off: 0, Len: 128}, Rights: []Right{Read}},
		Event{Type: EvSend, Channel: "cA", Seq: 3, Gen: 1, Object: obj, NewID: "m1",
			Owner: own("A"), Receiver: own("B"), Source: "h1", DestChan: "cB",
			Range: Range{Off: 0, Len: 128}, Rights: []Right{Read}},
		Event{Type: EvClose, Channel: "cA", Seq: 4, Gen: 1, Object: obj, Source: "h0", Owner: own("A")},
		Event{Type: EvReceive, Channel: "cB", Seq: 1, Gen: 1, Object: obj, NewID: "h2",
			Owner: own("B"), Source: "m1", SrcGen: 1, DestChan: "cB"},
	)
	mustAdvance(t, g, map[ChanID]Seq{"cA": 4, "cB": 1})

	snap := g.Snapshot()
	if !snap.Holds[root].Dead {
		t.Fatalf("root h0 should be dead after close")
	}
	delivered := snap.Holds[HoldKey{Gen: 1, ID: "h2"}]
	if delivered == nil || delivered.Dead || delivered.Owner.Process != "B" {
		t.Fatalf("delivered receive hold h2 must survive source close: %+v", delivered)
	}

	host := &recordingHost{}
	co, err := NewCoordinator(g, host, &MemoryStore{})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := co.Isolate(IsolateRequest{
		ID: "req-1", Object: obj, Root: root,
		Range: Range{Off: 0, Len: 128}, Rights: []Right{Read, Write},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Complete {
		t.Fatalf("expected complete isolation, got blocked=%+v unknown=%+v", rec.Blocked, rec.Unknown)
	}
	got := keySet(rec.RevokedKeys)
	for _, want := range []HoldKey{
		{Gen: 1, ID: "h1"},
		{Gen: 1, ID: "m1"},
		{Gen: 1, ID: "h2"},
	} {
		if !got[want] {
			t.Fatalf("expected revoke of descended hold %v, calls=%v", want, rec.RevokedKeys)
		}
	}
	if got[root] {
		t.Fatalf("dead root must not be re-revoked at the host")
	}
	if rec.Watermarks["cA"] != 4 || rec.Watermarks["cB"] != 1 {
		t.Fatalf("receipt must bind all participating channel watermarks: %+v", rec.Watermarks)
	}
}

// TestPendingMessage checks a sent-but-uncollected message: it is an actionable
// live hold, and until the receiver channel advances, the blocked channel and
// unknown bytes are reported precisely.
func TestPendingMessage(t *testing.T) {
	g := NewGraph()
	const obj ObjID = "mapB"
	root := HoldKey{Gen: 1, ID: "h0"}
	mustObserve(t, g,
		Event{Type: EvIssue, Channel: "cA", Seq: 1, Gen: 1, Object: obj, NewID: "h0",
			Owner: own("A"), Range: Range{Off: 0, Len: 64}, Rights: []Right{Read, Write}},
		Event{Type: EvSend, Channel: "cA", Seq: 2, Gen: 1, Object: obj, NewID: "m9",
			Owner: own("A"), Receiver: own("B"), Source: "h0", DestChan: "cB",
			Range: Range{Off: 8, Len: 16}, Rights: []Right{Read}},
	)
	mustAdvance(t, g, map[ChanID]Seq{"cA": 2})

	host := &recordingHost{}
	co, _ := NewCoordinator(g, host, &MemoryStore{})
	rec, err := co.Isolate(IsolateRequest{
		ID: "req-2", Object: obj, Root: root,
		Range: Range{Off: 0, Len: 64}, Rights: []Right{Read, Write},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Complete {
		t.Fatalf("pending message must keep the request incomplete")
	}
	var blocked *BlockedChannel
	for i := range rec.Blocked {
		if rec.Blocked[i].Channel == "cB" {
			blocked = &rec.Blocked[i]
		}
	}
	if blocked == nil {
		t.Fatalf("expected blocked receiver channel cB, got %+v", rec.Blocked)
	}
	if !rangesEqual(rec.Unknown, []Range{{Off: 8, Len: 16}}) {
		t.Fatalf("unknown bytes must match the in-flight slice, got %+v", rec.Unknown)
	}

	// The late receive is observed first, then its channel watermark advances.
	mustObserve(t, g, Event{Type: EvReceive, Channel: "cB", Seq: 1, Gen: 1, Object: obj,
		NewID: "h9", Owner: own("B"), Source: "m9", SrcGen: 1, DestChan: "cB"})
	mustAdvance(t, g, map[ChanID]Seq{"cB": 1})
	fresh, err := co.Isolate(IsolateRequest{
		ID: "req-2b", Object: obj, Root: root,
		Range: Range{Off: 0, Len: 64}, Rights: []Right{Read, Write},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.Complete || len(fresh.Blocked) != 0 {
		t.Fatalf("after receiver advances, isolation must complete: %+v", fresh)
	}
	if !rangesEqual(fresh.Revoked, []Range{{Off: 0, Len: 64}}) {
		t.Fatalf("full requested range should be bound, got %+v", fresh.Revoked)
	}
}

// TestSliceAndParentResidual verifies partial byte-slice grants, preservation
// of unrelated same-object authorizations, and that narrowing a child interval
// never masks the parent's residual write right.
func TestSliceAndParentResidual(t *testing.T) {
	g := NewGraph()
	const obj ObjID = "mapC"
	root := HoldKey{Gen: 1, ID: "h0"}
	mustObserve(t, g,
		Event{Type: EvIssue, Channel: "cA", Seq: 1, Gen: 1, Object: obj, NewID: "h0",
			Owner: own("A"), Range: Range{Off: 0, Len: 256}, Rights: []Right{Read, Write}},
		Event{Type: EvIssue, Channel: "cC", Seq: 1, Gen: 1, Object: obj, NewID: "u0",
			Owner: own("C"), Range: Range{Off: 0, Len: 256}, Rights: []Right{Read}},
		Event{Type: EvSlice, Channel: "cA", Seq: 2, Gen: 1, Object: obj, NewID: "s1",
			Owner: own("B"), Source: "h0", Range: Range{Off: 32, Len: 32}, Rights: []Right{Read}},
	)
	mustAdvance(t, g, map[ChanID]Seq{"cA": 2, "cC": 1})

	host := &recordingHost{}
	co, _ := NewCoordinator(g, host, &MemoryStore{})
	rec, err := co.Isolate(IsolateRequest{
		ID: "req-3", Object: obj, Root: root,
		Range: Range{Off: 0, Len: 256}, Rights: []Right{Read, Write},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Complete {
		t.Fatalf("expected complete: %+v", rec.Blocked)
	}
	keys := keySet(rec.RevokedKeys)
	if !keys[root] || !keys[HoldKey{Gen: 1, ID: "s1"}] {
		t.Fatalf("parent h0 (residual write) and child s1 must both revoke: %v", rec.RevokedKeys)
	}
	if keys[HoldKey{Gen: 1, ID: "u0"}] {
		t.Fatalf("unrelated same-object authorization u0 must be preserved")
	}
	if !rangesEqual(rec.Revoked, []Range{{Off: 0, Len: 256}}) {
		t.Fatalf("parent residual write keeps whole range in scope: %+v", rec.Revoked)
	}
}

// TestRecoveryIdempotency simulates an interruption mid-host-round, rebuilds
// the coordinator from a durable journal, and proves the same external request
// id converges without double-revoking already journaled holds.
func TestRecoveryIdempotency(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "journal.json")
	const obj ObjID = "mapD"
	root := HoldKey{Gen: 1, ID: "h0"}

	build := func() *Graph {
		g := NewGraph()
		mustObserve(t, g,
			Event{Type: EvIssue, Channel: "cA", Seq: 1, Gen: 1, Object: obj, NewID: "h0",
				Owner: own("A"), Range: Range{Off: 0, Len: 16}, Rights: []Right{Read}},
			Event{Type: EvCopy, Channel: "cA", Seq: 2, Gen: 1, Object: obj, NewID: "h1",
				Owner: own("B"), Source: "h0", Range: Range{Off: 0, Len: 16}, Rights: []Right{Read}},
			Event{Type: EvCopy, Channel: "cA", Seq: 3, Gen: 1, Object: obj, NewID: "h2",
				Owner: own("C"), Source: "h1", Range: Range{Off: 0, Len: 16}, Rights: []Right{Read}},
		)
		mustAdvance(t, g, map[ChanID]Seq{"cA": 3})
		return g
	}

	store := &FileStore{Path: path}
	host1 := &recordingHost{failOn: map[HoldKey]int{{Gen: 1, ID: "h2"}: 1}}
	co1, err := NewCoordinator(build(), host1, store)
	if err != nil {
		t.Fatal(err)
	}
	rec1, err := co1.Isolate(IsolateRequest{
		ID: "req-4", Object: obj, Root: root,
		Range: Range{Off: 0, Len: 16}, Rights: []Right{Read},
	})
	if err != errInterrupt || rec1 == nil || !rec1.Resumable {
		t.Fatalf("expected resumable interrupted receipt, err=%v rec=%+v", err, rec1)
	}

	// Rebuild everything as a new process would after the crash.
	host2 := &recordingHost{}
	co2, err := NewCoordinator(build(), host2, store)
	if err != nil {
		t.Fatal(err)
	}
	rec2, err := co2.Isolate(IsolateRequest{
		ID: "req-4", Object: obj, Root: root,
		Range: Range{Off: 0, Len: 16}, Rights: []Right{Read},
	})
	if err != nil {
		t.Fatalf("resume must not fail: %v", err)
	}
	if !rec2.Complete {
		t.Fatalf("resumed request should complete: %+v", rec2)
	}
	// h0 and h1 succeeded before the interruption and are journaled; the new
	// process must not re-attempt them. Only the failed h2 may be retried, and
	// the host contract treats that retry as idempotent.
	if len(host2.calls) != 1 || host2.calls[0] != (HoldKey{Gen: 1, ID: "h2"}) {
		t.Fatalf("resume must retry only the unjournaled hold, got %v", host2.calls)
	}
	journal, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[HoldKey]int{}
	for _, k := range journal.Revoked["req-4"] {
		seen[k]++
	}
	for k, n := range seen {
		if n != 1 {
			t.Fatalf("journaled hold %v recorded %d times", k, n)
		}
	}

	// Re-issuing the identical request returns the bound receipt and does no work.
	host3 := &recordingHost{}
	co3, _ := NewCoordinator(build(), host3, store)
	rec3, err := co3.Isolate(IsolateRequest{
		ID: "req-4", Object: obj, Root: root,
		Range: Range{Off: 0, Len: 16}, Rights: []Right{Read},
	})
	if err != nil || len(host3.calls) != 0 {
		t.Fatalf("idempotent replay must be a no-op, err=%v calls=%v", err, host3.calls)
	}
	if rec3.RequestID != rec2.RequestID || !rangesEqual(rec3.Revoked, rec2.Revoked) {
		t.Fatalf("replay receipt must stay bound to the same actual range")
	}

	// The same id with a different body is rejected.
	if _, err := co3.Isolate(IsolateRequest{
		ID: "req-4", Object: obj, Root: root,
		Range: Range{Off: 0, Len: 8}, Rights: []Right{Read},
	}); err != ErrRequestMismatch {
		t.Fatalf("expected ErrRequestMismatch, got %v", err)
	}
}

// TestStaggeredWatermarkEmptyScan reproduces a scan that looks empty under
// staggered watermarks: the sender's send event is committed while the
// receiver channel has no progress at all. The materialized hold set has no
// receiver-side hold, yet the in-flight message forces an unknown fragment and
// names the missing channel rather than reporting success.
func TestStaggeredWatermarkEmptyScan(t *testing.T) {
	g := NewGraph()
	const obj ObjID = "mapE"
	root := HoldKey{Gen: 1, ID: "h0"}
	mustObserve(t, g,
		Event{Type: EvIssue, Channel: "cA", Seq: 1, Gen: 1, Object: obj, NewID: "h0",
			Owner: own("A"), Range: Range{Off: 0, Len: 100}, Rights: []Right{Read, Write}},
		Event{Type: EvSend, Channel: "cA", Seq: 2, Gen: 1, Object: obj, NewID: "m1",
			Owner: own("A"), Receiver: own("B"), Source: "h0", DestChan: "cB",
			Range: Range{Off: 40, Len: 20}, Rights: []Right{Write}},
	)
	// Only the sender channel advances; cB is never mentioned to the kernel feed.
	mustAdvance(t, g, map[ChanID]Seq{"cA": 2})

	snap := g.Snapshot()
	var receivedByB int
	for _, h := range snap.Holds {
		if h.Owner.Process == "B" && h.Msg == nil {
			receivedByB++
		}
	}
	if receivedByB != 0 {
		t.Fatalf("no receive event has committed: B must hold no delivered right, got %d", receivedByB)
	}

	co, _ := NewCoordinator(g, &recordingHost{}, &MemoryStore{})
	rec, err := co.Isolate(IsolateRequest{
		ID: "req-5", Object: obj, Root: root,
		Range: Range{Off: 0, Len: 100}, Rights: []Right{Read, Write},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Complete {
		t.Fatalf("empty-looking scan must not be reported complete")
	}
	found := false
	for _, b := range rec.Blocked {
		if b.Channel == "cB" && b.CurrentWater == 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("must name channel cB missing progress at watermark 0: %+v", rec.Blocked)
	}
	if !rangesEqual(rec.Unknown, []Range{{Off: 40, Len: 20}}) {
		t.Fatalf("unknown fragment must equal the in-flight write slice: %+v", rec.Unknown)
	}
	if !rangesEqual(rec.Revoked, []Range{{Off: 0, Len: 40}, {Off: 60, Len: 40}}) {
		t.Fatalf("only evidenced bytes may be claimed revoked: %+v", rec.Revoked)
	}

	// Once cB advances past the receive, the same lineage becomes fully provable.
	mustObserve(t, g, Event{Type: EvReceive, Channel: "cB", Seq: 1, Gen: 1, Object: obj,
		NewID: "hB", Owner: own("B"), Source: "m1", SrcGen: 1, DestChan: "cB"})
	mustAdvance(t, g, map[ChanID]Seq{"cB": 1})
	fresh, err := co.Isolate(IsolateRequest{
		ID: "req-5b", Object: obj, Root: root,
		Range: Range{Off: 0, Len: 100}, Rights: []Right{Read, Write},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.Complete || !rangesEqual(fresh.Revoked, []Range{{Off: 0, Len: 100}}) {
		t.Fatalf("after cB catches up the full range must be proven: %+v", fresh)
	}
}

// TestDuplicateAndGenerationalReuse verifies identity semantics: duplicate
// deliveries are no-ops, conflicting same-seq events are rejected, and a HoldID
// reused in a later generation names an unrelated hold.
func TestDuplicateAndGenerationalReuse(t *testing.T) {
	g := NewGraph()
	const obj ObjID = "mapF"
	issue := Event{Type: EvIssue, Channel: "cA", Seq: 1, Gen: 1, Object: obj, NewID: "h0",
		Owner: own("A"), Range: Range{Off: 0, Len: 10}, Rights: []Right{Read}}
	mustObserve(t, g, issue)
	if err := g.Observe(issue); err != nil {
		t.Fatalf("duplicate delivery must be idempotent: %v", err)
	}
	bogus := issue
	bogus.Range = Range{Off: 0, Len: 99}
	if err := g.Observe(bogus); err == nil {
		t.Fatalf("conflicting payload at same seq must error")
	}

	// A later generation reuses the same HoldID; it must not alias the first.
	mustObserve(t, g,
		Event{Type: EvIssue, Channel: "cA", Seq: 2, Gen: 2, Object: obj, NewID: "h0",
			Owner: own("A2"), Range: Range{Off: 0, Len: 5}, Rights: []Right{Write}},
		Event{Type: EvClose, Channel: "cA", Seq: 3, Gen: 1, Object: obj, Source: "h0", SrcGen: 1, Owner: own("A")},
	)
	mustAdvance(t, g, map[ChanID]Seq{"cA": 3})
	snap := g.Snapshot()
	gen1 := snap.Holds[HoldKey{Gen: 1, ID: "h0"}]
	gen2 := snap.Holds[HoldKey{Gen: 2, ID: "h0"}]
	if gen1 == nil || !gen1.Dead {
		t.Fatalf("generation-1 hold should be closed: %+v", gen1)
	}
	if gen2 == nil || gen2.Dead || !gen2.Rights[Write] {
		t.Fatalf("generation-2 reuse must stay independent and alive: %+v", gen2)
	}

	co, _ := NewCoordinator(g, &recordingHost{}, &MemoryStore{})
	rec, err := co.Isolate(IsolateRequest{
		ID: "req-6", Object: obj, Root: HoldKey{Gen: 1, ID: "h0"},
		Range: Range{Off: 0, Len: 10}, Rights: []Right{Read},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Complete {
		t.Fatalf("single-generation lineage should complete: %+v", rec.Blocked)
	}
	if keySet(rec.RevokedKeys)[HoldKey{Gen: 2, ID: "h0"}] {
		t.Fatalf("later-generation reuse must not be tainted")
	}
}

// TestEarlyEventDoesNotCrossWatermark ensures an observed event above its
// channel watermark contributes no evidence and is reported as missing
// progress until the watermark catches up.
func TestEarlyEventDoesNotCrossWatermark(t *testing.T) {
	g := NewGraph()
	const obj ObjID = "mapG"
	root := HoldKey{Gen: 1, ID: "h0"}
	mustObserve(t, g,
		Event{Type: EvIssue, Channel: "cA", Seq: 1, Gen: 1, Object: obj, NewID: "h0",
			Owner: own("A"), Range: Range{Off: 0, Len: 10}, Rights: []Right{Read}},
		// seq 2 observed early while watermark is still 1.
		Event{Type: EvCopy, Channel: "cA", Seq: 2, Gen: 1, Object: obj, NewID: "h1",
			Owner: own("B"), Source: "h0", Range: Range{Off: 0, Len: 10}, Rights: []Right{Read}},
	)
	mustAdvance(t, g, map[ChanID]Seq{"cA": 1})
	if _, ok := g.Snapshot().Holds[HoldKey{Gen: 1, ID: "h1"}]; ok {
		t.Fatalf("early event must not materialize a hold below the watermark")
	}
	co, _ := NewCoordinator(g, &recordingHost{}, &MemoryStore{})
	rec, err := co.Isolate(IsolateRequest{
		ID: "req-7", Object: obj, Root: root,
		Range: Range{Off: 0, Len: 10}, Rights: []Right{Read},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Complete {
		t.Fatalf("early copy must keep part of the range unprovable: %+v", rec)
	}
	mustAdvance(t, g, map[ChanID]Seq{"cA": 2})
	fresh, err := co.Isolate(IsolateRequest{
		ID: "req-7b", Object: obj, Root: root,
		Range: Range{Off: 0, Len: 10}, Rights: []Right{Read},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.Complete {
		t.Fatalf("after watermark catches up, evidence becomes complete: %+v", fresh)
	}
}
