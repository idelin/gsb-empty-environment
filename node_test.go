package swim

import (
	"sort"
	"testing"
	"time"
)

type cluster struct {
	clock  *ManualClock
	net    *Network
	events chan Event
	nodes  map[string]*Node
}

func newCluster(t *testing.T, ids []string, k int) *cluster {
	c := &cluster{
		clock:  NewManualClock(time.Unix(0, 0)),
		net:    NewNetwork(),
		events: make(chan Event, 1024),
		nodes:  map[string]*Node{},
	}
	for i, id := range ids {
		var peers []string
		for _, other := range ids {
			if other != id {
				peers = append(peers, other)
			}
		}
		cfg := Config{
			ID:              id,
			Interval:        time.Second,
			AckTimeout:      200 * time.Millisecond,
			IndirectTimeout: 400 * time.Millisecond,
			K:               k,
			Events:          c.events,
		}
		n := NewNode(cfg, c.clock, c.net, peers, int64(i+1))
		c.net.Attach(id, n.Inbox())
		c.nodes[id] = n
		n.Start()
	}
	settle() // let all Run goroutines arm their interval timers
	return c
}

func (c *cluster) stop() {
	for _, n := range c.nodes {
		n.Stop()
	}
}

// settle lets node goroutines process fired timers and queued messages.
func settle() { time.Sleep(50 * time.Millisecond) }

// advance moves the test clock and then lets goroutines react and arm
// the timers for the next probe phase.
func (c *cluster) advance(d time.Duration) {
	c.clock.Advance(d)
	settle()
}

func (c *cluster) drainEvents() []Event {
	var out []Event
	for {
		select {
		case e := <-c.events:
			out = append(out, e)
		default:
			return out
		}
	}
}

func findEvent(events []Event, node string, typ EventType, target string) *Event {
	for i := range events {
		e := &events[i]
		if e.Node == node && e.Type == typ && e.Target == target {
			return e
		}
	}
	return nil
}

// Acceptance: direct path A<->B is lossy but proxies C/D are healthy.
// A's direct Ping to B times out, K=2 proxies relay B's Ack in time,
// and B must NOT be marked Down (or even Suspect) at A.
func TestIndirectProbePreventsFalsePositive(t *testing.T) {
	c := newCluster(t, []string{"A", "B", "C", "D"}, 2)
	defer c.stop()

	// Deterministic: A always probes B.
	c.nodes["A"].cfg.PickTarget = func(peers []string) string { return "B" }

	// Weak network: direct A<->B drops everything; proxy links are fine.
	c.net.Drop("A", "B")
	c.net.Drop("B", "A")

	// t=1s: A starts probing B; the direct Ping is lost.
	c.advance(time.Second)
	// t=1.2s: direct AckTimeout fires -> A sends PingReq to K=2 proxies.
	// C and D ping B and forward B's Ack back to A well within the
	// 400ms indirect window.
	c.advance(200 * time.Millisecond)
	events := c.drainEvents()

	if got := c.nodes["A"].StateOf("B"); got != Alive {
		t.Fatalf("B must stay Alive at A, got %s", got)
	}

	ind := findEvent(events, "A", EventIndirectStart, "B")
	if ind == nil {
		t.Fatalf("expected indirect probe of B at A, events: %v", events)
	}
	sort.Strings(ind.Proxies)
	if len(ind.Proxies) != 2 || ind.Proxies[0] != "C" || ind.Proxies[1] != "D" {
		t.Fatalf("expected proxies [C D], got %v", ind.Proxies)
	}

	succ := findEvent(events, "A", EventProbeSuccess, "B")
	if succ == nil {
		t.Fatalf("expected probe success for B at A, events: %v", events)
	}
	if succ.Via != "C" && succ.Via != "D" {
		t.Fatalf("expected ack via C or D, got %q", succ.Via)
	}

	if e := findEvent(events, "A", EventStateChange, "B"); e != nil {
		t.Fatalf("B must not change state at A, got %v", e)
	}

	// Let the indirect window expire and several more probe cycles run:
	// B must remain Alive the whole time.
	c.advance(time.Second)
	c.advance(2 * time.Second)
	if got := c.nodes["A"].StateOf("B"); got != Alive {
		t.Fatalf("B must remain Alive at A after extra cycles, got %s", got)
	}
	for _, e := range c.drainEvents() {
		if e.Node == "A" && e.Type == EventStateChange && e.Target == "B" {
			t.Fatalf("B must never be marked down at A, got %v", e)
		}
	}
}

// A fully unreachable peer degrades Alive -> Suspect -> Down.
func TestUnreachableNodeMarkedDown(t *testing.T) {
	c := newCluster(t, []string{"A", "B", "C", "D"}, 2)
	defer c.stop()

	c.nodes["A"].cfg.PickTarget = func(peers []string) string { return "B" }

	// Isolate B completely.
	for _, id := range []string{"A", "C", "D"} {
		c.net.Drop(id, "B")
		c.net.Drop("B", id)
	}

	// First failed probe cycle: Alive -> Suspect.
	c.advance(time.Second)            // probe starts
	c.advance(200 * time.Millisecond) // direct timeout -> PingReq
	c.advance(400 * time.Millisecond) // indirect timeout -> fail
	if got := c.nodes["A"].StateOf("B"); got != Suspect {
		t.Fatalf("expected B Suspect at A after first failure, got %s", got)
	}

	// Second failed probe cycle: Suspect -> Down.
	c.advance(time.Second)
	c.advance(200 * time.Millisecond)
	c.advance(400 * time.Millisecond)
	if got := c.nodes["A"].StateOf("B"); got != Down {
		t.Fatalf("expected B Down at A after second failure, got %s", got)
	}

	events := c.drainEvents()
	if e := findEvent(events, "A", EventProbeFailed, "B"); e == nil {
		t.Fatalf("expected probe-failed events for B at A, events: %v", events)
	}
}

// Sanity: a healthy direct path completes with a direct Ack.
func TestDirectPingSuccess(t *testing.T) {
	c := newCluster(t, []string{"A", "B"}, 2)
	defer c.stop()

	c.nodes["A"].cfg.PickTarget = func(peers []string) string { return "B" }

	c.advance(time.Second) // probe starts; B acks directly
	events := c.drainEvents()

	succ := findEvent(events, "A", EventProbeSuccess, "B")
	if succ == nil {
		t.Fatalf("expected direct probe success, events: %v", events)
	}
	if succ.Via != "" {
		t.Fatalf("expected direct ack (Via empty), got via %q", succ.Via)
	}
	if got := c.nodes["A"].StateOf("B"); got != Alive {
		t.Fatalf("expected B Alive at A, got %s", got)
	}
}
