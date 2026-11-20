package keyepoch

import "testing"

// pair bundles two engines talking over one in-memory wire with swapped
// directional secrets, plus per-role scripts for deterministic callbacks.
type pair struct {
	wire      *Wire
	scripts   map[string]*Scripts
	a, b      *Engine
	delivered map[string]bool
}

func newPair(t *testing.T, budget uint64) *pair {
	t.Helper()
	secretAB := []byte("read-secret-A-to-B direction")
	secretBA := []byte("read-secret-B-to-A direction")
	wire := NewWire()
	scripts := map[string]*Scripts{"A": NewScripts(), "B": NewScripts()}

	cfgA := Config{Role: "A", ReadSecret: secretBA, WriteSecret: secretAB, Budget: budget, Wire: wire, Scripts: scripts["A"]}
	cfgB := Config{Role: "B", ReadSecret: secretAB, WriteSecret: secretBA, Budget: budget, Wire: wire, Scripts: scripts["B"]}
	a, err := NewEngine(cfgA)
	if err != nil {
		t.Fatalf("new A: %v", err)
	}
	b, err := NewEngine(cfgB)
	if err != nil {
		t.Fatalf("new B: %v", err)
	}
	return &pair{wire: wire, scripts: scripts, a: a, b: b, delivered: map[string]bool{}}
}

// setAllCommitted makes every future write and ambiguity lookup report
// success. Authentication still requires a valid HMAC tag, and per-MsgID
// entries still override the default.
func (s *Scripts) setAllCommitted() {
	s.DefaultWrite = WriteCommitted
	s.DefaultLookup = WriteCommitted
}

// setAllAccepted authenticates every well-tagged inbound frame by default.
func (s *Scripts) setAllAccepted() {
	s.DefaultAuth = AuthAccepted
}

// relay moves every newly committed wire frame into the peer's read queue and
// pumps both sides until a fixpoint, returning the number of newly relayed
// frames.
func (p *pair) relay(t *testing.T) int {
	t.Helper()
	relayed := 0
	for progress := true; progress; {
		progress = false
		for _, f := range p.wire.Frames() {
			target := p.b
			if len(f.MsgID) > 0 && f.MsgID[0] == 'B' {
				target = p.a
			}
			if p.delivered[f.MsgID] {
				continue
			}
			if _, err := target.Incoming(f); err != nil {
				t.Fatalf("relay %s: %v", f.MsgID, err)
			}
			p.delivered[f.MsgID] = true
			relayed++
			progress = true
		}
		before := p.wire.Count()
		if _, err := p.a.Pump(); err != nil && !p.a.dead() {
			t.Fatalf("pump A: %v", err)
		}
		if _, err := p.b.Pump(); err != nil && !p.b.dead() {
			t.Fatalf("pump B: %v", err)
		}
		if p.wire.Count() > before {
			progress = true
		}
	}
	return relayed
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertSendCount(t *testing.T, e *Engine, kind FrameKind, want int) {
	t.Helper()
	got := 0
	for _, a := range e.SendAttempts() {
		if a.Kind == kind.String() {
			got++
		}
	}
	if got != want {
		t.Fatalf("%s send attempts for %s: got %d want %d (%+v)", e.cfg.Role, kind, got, want, e.SendAttempts())
	}
}

func assertWireKindCount(t *testing.T, p *pair, kind FrameKind, want int) {
	t.Helper()
	got := 0
	for _, f := range p.wire.Frames() {
		if f.Kind == kind {
			got++
		}
	}
	if got != want {
		t.Fatalf("wire %s frames: got %d want %d", kind, got, want)
	}
}

// TestSimultaneousUpdates drives both endpoints requesting an update at the
// same time. Requested replies must not form an update loop: each side emits
// exactly one reply and epochs settle at fixed points.
func TestSimultaneousUpdates(t *testing.T) {
	p := newPair(t, 4)
	p.scripts["A"].setAllCommitted()
	p.scripts["B"].setAllCommitted()
	p.scripts["A"].setAllAccepted()
	p.scripts["B"].setAllAccepted()

	_, err := p.a.RequestUpdate()
	must(t, err)
	_, err = p.b.RequestUpdate()
	must(t, err)
	if _, err := p.a.Pump(); err != nil && !p.a.dead() {
		t.Fatal(err)
	}
	if _, err := p.b.Pump(); err != nil && !p.b.dead() {
		t.Fatal(err)
	}
	p.relay(t)

	if p.a.WriteEpoch() != 2 || p.b.WriteEpoch() != 2 {
		t.Fatalf("write epochs: A=%d B=%d want 2,2", p.a.WriteEpoch(), p.b.WriteEpoch())
	}
	// Each side receives the peer's request and reply: two authenticated
	// KeyUpdates, hence two read-epoch advances per side.
	if p.a.ReadEpoch() != 2 || p.b.ReadEpoch() != 2 {
		t.Fatalf("read epochs: A=%d B=%d want 2,2", p.a.ReadEpoch(), p.b.ReadEpoch())
	}
	// Each side sent its own request plus exactly one reply.
	assertSendCount(t, p.a, KindKeyUpdate, 2)
	assertSendCount(t, p.b, KindKeyUpdate, 2)
	assertWireKindCount(t, p, KindKeyUpdate, 4)
	if p.a.Status() != statusRunning || p.b.Status() != statusRunning {
		t.Fatalf("statuses: A=%s B=%s", p.a.Status(), p.b.Status())
	}
	// A second relay must add nothing: no reply loop.
	if n := p.relay(t); n != 0 {
		t.Fatalf("reply loop: %d extra relayed frames", n)
	}
}

// TestCancelThenCompleteAuth enqueues a KeyUpdate with an ambiguous
// authentication, cancels it, then a later scripted acceptance must not
// resurrect the canceled update or advance the read epoch.
func TestCancelThenCompleteAuth(t *testing.T) {
	p := newPair(t, 4)
	p.scripts["B"].setAllCommitted()

	_, err := p.b.RequestUpdate()
	must(t, err)
	if _, err := p.b.Pump(); err != nil && !p.b.dead() {
		t.Fatal(err)
	}
	var ku Frame
	for _, f := range p.wire.Frames() {
		if f.Kind == KindKeyUpdate {
			ku = f
		}
	}
	if ku.MsgID == "" {
		t.Fatal("B did not emit a KeyUpdate")
	}

	if _, err := p.a.Incoming(ku); err != nil {
		t.Fatal(err)
	}
	if _, err := p.a.Pump(); err != nil && !p.a.dead() {
		t.Fatal(err)
	}
	if got := len(p.a.Summaries()[len(p.a.Summaries())-1].PendingAuth); got != 1 {
		t.Fatalf("pending auth: got %d want 1", got)
	}
	if _, err := p.a.CancelAuth(ku.MsgID); err != nil {
		t.Fatal(err)
	}
	// Late positive answer for an identity that was explicitly canceled.
	if _, err := p.a.SetAuth(ku.MsgID, AuthAccepted); err != nil {
		t.Fatal(err)
	}
	if _, err := p.a.Pump(); err != nil && !p.a.dead() {
		t.Fatal(err)
	}
	if p.a.ReadEpoch() != 0 {
		t.Fatalf("read epoch advanced after cancel: %d", p.a.ReadEpoch())
	}
	if p.a.Status() != statusRunning {
		t.Fatalf("status after cancel: %s", p.a.Status())
	}
	if got := len(p.a.Summaries()[len(p.a.Summaries())-1].Drops); got != 1 {
		t.Fatalf("drops: got %d want 1", got)
	}
}
