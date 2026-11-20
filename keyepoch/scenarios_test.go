package keyepoch

import "testing"

// TestWriteSuccessThenDisconnect commits a write, then the transport dies:
// committed frames stay recorded while queued frames are purged and no further
// write attempts occur.
func TestWriteSuccessThenDisconnect(t *testing.T) {
	p := newPair(t, 4)

	_, err := p.a.SendApp([]byte("committed"))
	must(t, err)
	firstID := p.a.writeQueue[0].frame.MsgID
	_, err = p.a.SetWrite(firstID, WriteCommitted)
	must(t, err)
	if _, err := p.a.Pump(); err != nil && !p.a.dead() {
		t.Fatal(err)
	}
	if p.wire.Count() != 1 {
		t.Fatalf("wire after commit: %d", p.wire.Count())
	}

	_, err = p.a.SendApp([]byte("queued-then-dead"))
	must(t, err)
	secondID := p.a.writeQueue[0].frame.MsgID
	_, err = p.a.SetWrite(secondID, WriteUnknown)
	must(t, err)
	if _, err := p.a.Pump(); err != nil && !p.a.dead() {
		t.Fatal(err)
	}
	if _, err := p.a.TransportDead(); err != nil {
		t.Fatal(err)
	}

	if p.a.Status() != statusFailed {
		t.Fatalf("status: %s", p.a.Status())
	}
	if p.wire.Count() != 1 {
		t.Fatalf("wire changed after disconnect: %d", p.wire.Count())
	}
	if len(p.a.SendAttempts()) != 2 {
		t.Fatalf("attempts after dead: %+v", p.a.SendAttempts())
	}
	// A late lookup callback for the ambiguous frame must be ignored.
	_, err = p.a.SetLookup(secondID, WriteCommitted)
	must(t, err)
	if _, err := p.a.ResolveWrite(secondID); err != nil {
		t.Fatal(err)
	}
	if p.wire.Count() != 1 {
		t.Fatalf("late callback altered wire: %d", p.wire.Count())
	}
}

// TestLateCallbacksAfterClose closes the session and then replays old inbound
// frames and write callbacks: nothing is delivered, nothing is sent and the
// closed epochs are fixed.
func TestLateCallbacksAfterClose(t *testing.T) {
	p := newPair(t, 4)
	p.scripts["A"].setAllCommitted()

	_, err := p.a.SendApp([]byte("before-close"))
	must(t, err)
	appID := p.a.writeQueue[0].frame.MsgID
	if _, err := p.a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.a.Pump(); err != nil && !p.a.dead() {
		t.Fatal(err)
	}
	if p.a.Status() != statusClosed {
		t.Fatalf("status: %s", p.a.Status())
	}

	// Old write callback arriving after close.
	_, err = p.a.SetWrite(appID, WriteCommitted)
	must(t, err)
	if _, err := p.a.ResolveWrite(appID); err != nil {
		t.Fatal(err)
	}
	// Old inbound frame arriving after close: dropped, never queued.
	old := SignFrame(Frame{Kind: KindApp, Epoch: 0, Seq: 9, MsgID: "late-in-1"}, p.a.cfg.ReadSecret)
	if _, err := p.a.Incoming(old); err != nil {
		t.Fatal(err)
	}
	// New sends are rejected.
	if _, err := p.a.SendApp([]byte("nope")); err == nil {
		t.Fatal("send after close must fail")
	}

	sum := p.a.Summaries()[len(p.a.Summaries())-1]
	if len(sum.ReadQueue) != 0 || len(sum.WriteQueue) != 0 {
		t.Fatalf("queues not empty: r=%v w=%v", sum.ReadQueue, sum.WriteQueue)
	}
	if len(p.a.Delivered()) != 0 {
		t.Fatalf("deliveries after close: %v", p.a.Delivered())
	}
	if p.a.WriteEpoch() != 0 || p.a.ReadEpoch() != 0 {
		t.Fatalf("epochs moved after close: r=%d w=%d", p.a.ReadEpoch(), p.a.WriteEpoch())
	}
	if got := dropReasons(p.a); got["session-closed"] == 0 {
		t.Fatalf("late inbound frame not recorded as drop: %v", got)
	}
}

func dropReasons(e *Engine) map[string]int {
	out := map[string]int{}
	for _, d := range e.Summaries()[len(e.Summaries())-1].Drops {
		out[d.Reason]++
	}
	return out
}

// TestSameSeqAcrossEpochs sends the same per-epoch sequence number under two
// generations. Both authenticate and deliver independently, while a genuine
// duplicate inside one epoch is dropped by the sequence window.
func TestSameSeqAcrossEpochs(t *testing.T) {
	p := newPair(t, 4)
	p.scripts["A"].setAllCommitted()
	p.scripts["B"].setAllCommitted()
	p.scripts["A"].setAllAccepted()
	p.scripts["B"].setAllAccepted()

	// Enqueue and send both epoch-0 application records before any rotation,
	// so the epoch binding is fixed at enqueue time.
	_, err := p.a.SendApp([]byte("A epoch0"))
	must(t, err)
	if _, err := p.a.Pump(); err != nil && !p.a.dead() {
		t.Fatal(err)
	}
	_, err = p.b.SendApp([]byte("B epoch0"))
	must(t, err)
	if _, err := p.b.Pump(); err != nil && !p.b.dead() {
		t.Fatal(err)
	}
	// Deliver both epoch-0 records before rotating either read generation.
	p.relay(t)

	// Now B rotates so it can emit epoch-1 records with seq 0 again.
	_, err = p.b.RequestUpdate()
	must(t, err)
	if _, err := p.b.Pump(); err != nil && !p.b.dead() {
		t.Fatal(err)
	}
	p.relay(t)

	if p.a.ReadEpoch() != 1 {
		t.Fatalf("A read epoch: %d", p.a.ReadEpoch())
	}

	// B emits another seq-0 app frame, now protected by epoch 1.
	_, err = p.b.SendApp([]byte("B epoch1"))
	must(t, err)
	if _, err := p.b.Pump(); err != nil && !p.b.dead() {
		t.Fatal(err)
	}
	p.relay(t)

	got := p.a.Delivered()
	if len(got) != 2 {
		t.Fatalf("deliveries: %+v", got)
	}
	if got[0].Epoch != 0 || got[0].Seq != 0 || got[1].Epoch != 1 || got[1].Seq != 0 {
		t.Fatalf("cross-generation seq binding wrong: %+v", got)
	}

	// Replay the epoch-1 frame verbatim: same epoch+seq must be rejected.
	var replay Frame
	for _, f := range p.wire.Frames() {
		if f.Kind == KindApp && f.Epoch == 1 && f.Seq == 0 {
			replay = f
		}
	}
	if replay.MsgID == "" {
		t.Fatal("epoch-1 app frame missing on wire")
	}
	before := len(p.a.Delivered())
	if _, err := p.a.Incoming(replay); err != nil {
		t.Fatal(err)
	}
	if _, err := p.a.Pump(); err != nil && !p.a.dead() {
		t.Fatal(err)
	}
	if len(p.a.Delivered()) != before {
		t.Fatalf("duplicate frame was delivered")
	}
	if dropReasons(p.a)["seq-window"] == 0 {
		t.Fatalf("duplicate not rejected by sequence window")
	}
}

// TestBudgetExhaustionFailsCleanly drives updates up to and beyond the public
// budget. The over-budget attempt must fail the session without leaving a
// half-advanced write epoch or a queued update.
func TestBudgetExhaustionFailsCleanly(t *testing.T) {
	p := newPair(t, 1)
	p.scripts["A"].setAllCommitted()

	// First update takes the write epoch from 0 to 1, within budget.
	_, err := p.a.RequestUpdate()
	must(t, err)
	if _, err := p.a.Pump(); err != nil && !p.a.dead() {
		t.Fatal(err)
	}
	if p.a.WriteEpoch() != 1 {
		t.Fatalf("write epoch: %d", p.a.WriteEpoch())
	}

	// Second update would reach epoch 2 and must fail the whole session.
	if _, err := p.a.RequestUpdate(); err == nil {
		t.Fatal("over-budget update must return an error")
	}
	if p.a.Status() != statusFailed {
		t.Fatalf("status: %s", p.a.Status())
	}
	if p.a.WriteEpoch() != 1 {
		t.Fatalf("half-updated write epoch: %d", p.a.WriteEpoch())
	}
	sum := p.a.Summaries()[len(p.a.Summaries())-1]
	if len(sum.WriteQueue) != 0 || sum.LocalUpdatePending {
		t.Fatalf("half-update left queued state: %+v", sum)
	}
}

// TestUnknownWriteReconciledByIdentity makes the KeyUpdate write ambiguous.
// The queue freezes, no second update is ever produced, and a later identity
// lookup commits the original frame exactly once before the epoch advances.
func TestUnknownWriteReconciledByIdentity(t *testing.T) {
	p := newPair(t, 4)

	_, err := p.a.RequestUpdate()
	must(t, err)
	id := p.a.writeQueue[0].frame.MsgID
	_, err = p.a.SetWrite(id, WriteUnknown)
	must(t, err)
	if _, err := p.a.Pump(); err != nil && !p.a.dead() {
		t.Fatal(err)
	}
	if p.a.WriteEpoch() != 0 {
		t.Fatalf("epoch advanced before ambiguity resolved: %d", p.a.WriteEpoch())
	}
	if p.a.Summaries()[len(p.a.Summaries())-1].AmbiguousWrite != id {
		t.Fatal("ambiguous head not reported in summary")
	}

	// Repeated pumps must not create or send another update.
	for i := 0; i < 3; i++ {
		if _, err := p.a.Pump(); err != nil && !p.a.dead() {
			t.Fatal(err)
		}
	}
	assertSendCount(t, p.a, KindKeyUpdate, 1)
	if p.wire.Count() != 0 {
		t.Fatalf("ambiguous frame leaked onto wire: %d", p.wire.Count())
	}

	// Identity lookup confirms the single frame committed.
	_, err = p.a.SetLookup(id, WriteCommitted)
	must(t, err)
	if _, err := p.a.ResolveWrite(id); err != nil {
		t.Fatal(err)
	}
	if p.a.WriteEpoch() != 1 {
		t.Fatalf("write epoch after resolve: %d", p.a.WriteEpoch())
	}
	assertSendCount(t, p.a, KindKeyUpdate, 1)
	assertWireKindCount(t, p, KindKeyUpdate, 1)
}

// TestReplayCanonicalSummaries verifies independent replay byte-for-byte.
func TestReplayCanonicalSummaries(t *testing.T) {
	cfg := Config{Role: "X", ReadSecret: []byte("read-secret"), WriteSecret: []byte("write-secret"), Budget: 2, WindowSize: 8}
	e, err := NewEngine(cfg)
	must(t, err)

	_, err = e.SendApp([]byte("hello"))
	must(t, err)
	id := e.writeQueue[0].frame.MsgID
	_, err = e.SetWrite(id, WriteCommitted)
	must(t, err)
	_, err = e.Pump()
	must(t, err)
	in := SignFrame(Frame{Kind: KindKeyUpdate, Epoch: 0, Seq: 0, MsgID: "peer-ku-1", Request: true}, cfg.ReadSecret)
	_, err = e.Incoming(in)
	must(t, err)
	// The authentication answer is itself an event, so replay reproduces it.
	_, err = e.SetAuth("peer-ku-1", AuthAccepted)
	must(t, err)
	_, err = e.Pump()
	must(t, err)
	// The pump enqueues a reply KeyUpdate (X-m2); script its write too so
	// every effect is an explicit event in the recorded history.
	_, err = e.SetWrite("X-m2", WriteCommitted)
	must(t, err)
	_, err = e.Pump()
	must(t, err)

	_, replayed, err := Replay(cfg, e.Events())
	must(t, err)
	original := e.Summaries()
	if len(replayed) != len(original) {
		t.Fatalf("summary count: got %d want %d", len(replayed), len(original))
	}
	for i := range original {
		if replayed[i].Canonical() != original[i].Canonical() {
			t.Fatalf("summary %d diverges:\n%s\n---\n%s", i, original[i].Canonical(), replayed[i].Canonical())
		}
	}
}
