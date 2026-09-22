package causalslice

import (
	"strings"
	"testing"
)

// controller runs a fixed script at fixed logical times, keeping every
// experiment deterministic.
type controller struct {
	sim *Sim
	id  string
	at  []int
	fn  map[int]func()
}

func newController(sim *Sim) *controller {
	c := &controller{sim: sim, id: "ctrl", fn: map[int]func(){}}
	sim.Register(c)
	return c
}

func (c *controller) at_(t int, f func()) {
	c.fn[t] = f
	c.at = append(c.at, t)
	c.sim.Schedule(c.id, t, t)
}

func (c *controller) ID() string { return c.id }
func (c *controller) Receive(sim *Sim, env Envelope) {
	if t, ok := env.Msg.(Timer); ok {
		if when, ok := t.Tag.(int); ok {
			c.fn[when]()
		}
	}
}

type testHarness struct {
	sim    *Sim
	net    *Network
	logs   []string
	r1     *Replica
	r2     *Replica
	r3     *Replica
	client *Client
	writer *Client
	ctrl   *controller
	closed map[int]Result
}

func newHarness(t *testing.T, seed int64) *testHarness {
	t.Helper()
	h := &testHarness{closed: map[int]Result{}}
	h.net = NewNetwork().Seed(seed).Delay(1, 3)
	h.sim = NewSim(h.net, func(time int, text string) {
		h.logs = append(h.logs, itoa(time)+" "+text)
	})
	h.r1 = NewReplica(h.sim, "r1", 10)
	h.r2 = NewReplica(h.sim, "r2", 10)
	h.r3 = NewReplica(h.sim, "r3", 10)
	h.client = NewClient(h.sim, "c", func(id int, res Result) { h.closed[id] = res })
	h.writer = NewClient(h.sim, "cw", func(int, Result) {})
	h.ctrl = newController(h.sim)
	return h
}

// seedWrites produces A=1@r1 then causally dependent B=2@r2.
func (h *testHarness) seedWrites(writeBAt int) {
	h.ctrl.at_(2, func() { h.writer.Put("r1", "A", "1", nil) })
	h.ctrl.at_(20, func() {
		a := h.r1.latest["A"].ID
		h.writer.Put("r2", "B", "2", []VersionID{a})
	})
}

func (h *testHarness) logContains(sub string) bool {
	for _, l := range h.logs {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// TestSafetyNewBRequiresA: r3 receives B but not A. A snapshot touching B must
// stay open and wait for A; it may never return B without its cause. Once the
// partition heals the very same snapshot closes and contains both.
func TestSafetyNewBRequiresA(t *testing.T) {
	h := newHarness(t, 7)
	h.seedWrites(20)

	partition := true
	h.net.Drop(func(env Envelope) bool {
		if !partition {
			return false
		}
		if env.To == "r3" {
			switch m := env.Msg.(type) {
			case Replicate:
				if m.Ver.ID.Origin == "r1" {
					return true
				}
			case GossipResp, GossipReq:
				return true
			}
		}
		// isolate client fetches from other replicas until healing;
		// seeding writes and their acks are allowed explicitly.
		if _, ok := env.Msg.(PutReq); ok {
			return false
		}
		if _, ok := env.Msg.(PutAck); ok {
			return false
		}
		// The read client may only talk to r3 until the partition heals.
		if _, ok := env.Msg.(FetchReq); ok && env.From == "c" && env.To != "r3" {
			return true
		}
		if _, ok := env.Msg.(FetchResp); ok && env.To == "c" && env.From != "r3" {
			return true
		}
		return false
	})

	var snap int
	var mid Peek
	h.ctrl.at_(25, func() { snap = h.client.Read("r3", []string{"A", "B"}, 8) })
	h.ctrl.at_(42, func() { mid = h.client.Peek(snap) })
	h.ctrl.at_(45, func() { partition = false })

	h.sim.Run(120)

	if mid.Closed {
		t.Fatalf("snapshot closed while A was unavailable: %+v", mid)
	}
	seen := map[VersionID]bool{}
	for _, b := range mid.Blocked {
		seen[b.Missing] = true
	}
	if !seen[VersionID{Origin: "r1", Seq: 1}] {
		t.Fatalf("blocking dependency r1#1 not reported: %+v", mid.Blocked)
	}

	res, ok := h.closed[snap]
	if !ok {
		t.Fatalf("snapshot never closed after partition healed")
	}
	if res.Selected["B"] != (VersionID{Origin: "r2", Seq: 1}) {
		t.Fatalf("bad B selection: %v", res.Selected)
	}
	if res.Selected["A"] != (VersionID{Origin: "r1", Seq: 1}) {
		t.Fatalf("slice with new B lacks corresponding A: %v", res.Selected)
	}
	if res.Vector["r1"] != 1 || res.Vector["r2"] != 1 {
		t.Fatalf("unexpected version vector: %v", res.Vector)
	}
}

// TestPutRefusedWithoutCause: an effect cannot be created at a replica that has
// not seen its cause.
func TestPutRefusedWithoutCause(t *testing.T) {
	h := newHarness(t, 3)
	h.net.Drop(func(env Envelope) bool {
		// r2 must never learn A, not even via the r3 anti-entropy relay.
		if env.To == "r2" {
			switch m := env.Msg.(type) {
			case Replicate:
				if m.Ver.Key == "A" {
					return true
				}
			case GossipResp:
				for _, v := range m.Vers {
					if v.Key == "A" {
						return true
					}
				}
			}
		}
		return false
	})
	h.ctrl.at_(2, func() { h.client.Put("r1", "A", "1", nil) })
	h.ctrl.at_(20, func() {
		h.client.Put("r2", "B", "2", []VersionID{{Origin: "r1", Seq: 1}})
	})
	h.sim.Run(60)
	if !h.logContains("refused") && !h.logContains("missing causes") {
		t.Fatalf("expected write refusal, logs:\n%s", strings.Join(h.logs, "\n"))
	}
	if h.r2.has(VersionID{Origin: "r2", Seq: 1}) {
		t.Fatal("B was created without its cause A")
	}
}

// TestLivenessBoundedDelays: with bounded delays and no failures every read
// eventually closes, and the selected slice is closed under causality.
func TestLivenessBoundedDelays(t *testing.T) {
	h := newHarness(t, 11)
	h.seedWrites(20)
	var snap int
	h.ctrl.at_(25, func() { snap = h.client.Read("r3", []string{"A", "B"}, 8) })
	h.sim.Run(120)
	res, ok := h.closed[snap]
	if !ok {
		t.Fatalf("snapshot did not close under bounded delays; blocked=%+v", h.client.Peek(snap).Blocked)
	}
	verifyCausalClosure(t, h, res)
}

// TestCrashRecovery: the replica serving the read crashes; retries rotate and
// gossip after recovery still yields a closed, causal slice.
func TestCrashRecovery(t *testing.T) {
	h := newHarness(t, 21)
	h.seedWrites(20)
	var snap int
	h.ctrl.at_(25, func() { snap = h.client.Read("r2", []string{"A", "B"}, 6) })
	h.ctrl.at_(27, func() { h.r2.Crash() })
	h.ctrl.at_(70, func() { h.r2.Recover() })
	h.sim.Run(160)
	res, ok := h.closed[snap]
	if !ok {
		t.Fatalf("snapshot did not close after recovery: %+v", h.client.Peek(snap))
	}
	verifyCausalClosure(t, h, res)
}

// TestPermanentUnavailabilityBlocksForever: if no reachable replica can ever
// supply the cause, liveness cannot be promised: the snapshot must remain open
// and keep reporting the blocking dependency instead of returning an
// inconsistent slice.
func TestPermanentUnavailabilityBlocksForever(t *testing.T) {
	h := newHarness(t, 5)
	h.seedWrites(20)
	h.net.Drop(func(env Envelope) bool {
		// r3 may learn B via direct replication from r2, but must never
		// learn A: block its anti-entropy pulls and every r1-origin push.
		if env.To == "r3" {
			switch m := env.Msg.(type) {
			case Replicate:
				if m.Ver.ID.Origin == "r1" {
					return true
				}
			case GossipResp, GossipReq:
				return true
			}
		}
		if _, ok := env.Msg.(PutReq); ok {
			return false
		}
		if _, ok := env.Msg.(PutAck); ok {
			return false
		}
		if _, ok := env.Msg.(FetchReq); ok && env.From == "c" && env.To != "r3" {
			return true
		}
		if _, ok := env.Msg.(FetchResp); ok && env.To == "c" && env.From != "r3" {
			return true
		}
		return false
	})
	var snap int
	h.ctrl.at_(25, func() { snap = h.client.Read("r3", []string{"B"}, 6) })
	h.sim.Run(200)
	if _, ok := h.closed[snap]; ok {
		t.Fatal("snapshot closed despite permanently unavailable cause")
	}
	peek := h.client.Peek(snap)
	found := false
	for _, b := range peek.Blocked {
		if b.Missing == (VersionID{Origin: "r1", Seq: 1}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing cause not reported: %+v", peek.Blocked)
	}
}

// TestSnapshotIsolation: concurrent requests carry independent mutable state,
// and observations are defensive copies.
func TestSnapshotIsolation(t *testing.T) {
	h := newHarness(t, 31)
	h.seedWrites(20)
	otherClosed := map[int]Result{}
	other := NewClient(h.sim, "c2", func(id int, r Result) { otherClosed[id] = r })
	var sA, sB int
	h.ctrl.at_(25, func() {
		sA = h.client.Read("r1", []string{"A"}, 8)
		sB = other.Read("r2", []string{"B"}, 8)
	})
	h.sim.Run(120)

	ra, ok1 := h.closed[sA]
	rb, ok2 := otherClosed[sB]
	if !ok1 || !ok2 {
		t.Fatalf("both snapshots should close: %v %v", ok1, ok2)
	}
	if _, hasB := ra.Selected["B"]; hasB {
		t.Fatal("snapshot A observed state belonging to another request")
	}
	if _, hasA := rb.Selected["A"]; hasA {
		t.Fatal("snapshot B returned key A it never requested")
	}
	// B's cause A is fetched as closure but stays out of the selected slice.
	if rb.Vector["r1"] != 1 {
		t.Fatalf("snapshot B closed without causal past of B: %v", rb.Vector)
	}
	peek := h.client.Peek(sA)
	peek.Selected["A"] = VersionID{Origin: "x", Seq: 99}
	peek.Vector["r1"] = 99
	again := h.client.Peek(sA)
	if again.Selected["A"].Origin == "x" || again.Vector["r1"] == 99 {
		t.Fatal("Peek aliases internal snapshot state")
	}
}

// TestDeterministic: identical inputs reproduce the identical execution.
func TestDeterministic(t *testing.T) {
	run := func() []string {
		hh := newHarness(t, 99)
		hh.seedWrites(20)
		hh.ctrl.at_(25, func() { hh.client.Read("r3", []string{"A", "B"}, 8) })
		hh.sim.Run(120)
		return append([]string(nil), hh.logs...)
	}
	a := run()
	b := run()
	if len(a) != len(b) {
		t.Fatalf("log length differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("nondeterministic at line %d:\n%s\n%s", i, a[i], b[i])
		}
	}
}

func verifyCausalClosure(t *testing.T, h *testHarness, res Result) {
	t.Helper()
	// Every dependency of every selected version must itself be selected when
	// its key is among requested keys; here both A and B are requested.
	have := map[VersionID]bool{}
	for _, id := range res.Selected {
		have[id] = true
	}
	if !have[VersionID{Origin: "r1", Seq: 1}] {
		t.Fatalf("cause A missing from closed slice: %v", res.Selected)
	}
	if !have[VersionID{Origin: "r2", Seq: 1}] {
		t.Fatalf("effect B missing from closed slice: %v", res.Selected)
	}
}
