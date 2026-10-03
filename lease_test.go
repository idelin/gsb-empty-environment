package lease

import "testing"

func testConfig() Config {
	return Config{
		RoundTrip:       5,
		LeaseDuration:   50,
		HeartbeatEvery:  10,
		StepDownTimeout: 15,
		ElectionTimeout: 20,
		MaxClockDrift:   2,
	}
}

// runScenario drives the acceptance scenario up to tick 165 while checking
// the no-split-brain invariant on every tick.
func runScenario(t *testing.T) *Cluster {
	t.Helper()
	c := NewCluster(3, testConfig())
	c.Boot(99)
	for c.Tick < 165 {
		c.Step()
		leaders := 0
		for _, n := range c.Nodes {
			if n.Role == Leader {
				leaders++
			}
		}
		if leaders > 1 {
			t.Fatalf("tick %d: split brain, %d simultaneous leaders", c.Tick, leaders)
		}
		if c.Tick >= 150 && c.CanWrite(0) {
			t.Fatalf("tick %d: old primary node 0 can still write after demotion", c.Tick)
		}
		switch c.Tick {
		case 100:
			c.AcquireLease(0)
		case 139:
			c.Isolate(0)
		}
	}
	return c
}

func findEvent(c *Cluster, tick int, kind string, node int) *Event {
	for i := range c.Events {
		e := &c.Events[i]
		if e.Tick == tick && e.Kind == kind && e.Node == node {
			return e
		}
	}
	return nil
}

func TestLeaseConfirmedByMajorityAt105(t *testing.T) {
	c := runScenario(t)
	if findEvent(c, 100, "lease-request", 0) == nil {
		t.Error("missing lease-request from node 0 at tick 100")
	}
	e := findEvent(c, 105, "lease-confirmed", 0)
	if e == nil {
		t.Fatal("missing lease-confirmed by majority at tick 105")
	}
	t.Logf("event: %v", e)
}

func TestLeaderStepsDownAt150(t *testing.T) {
	c := runScenario(t)
	e := findEvent(c, 150, "step-down", 0)
	if e == nil {
		t.Fatal("node 0 did not demote itself at tick 150")
	}
	if c.Nodes[0].Role == Leader {
		t.Fatal("node 0 is still leader after step-down")
	}
	t.Logf("event: %v", e)
}

func TestNewLeaderElectedByMajorityAt155(t *testing.T) {
	c := runScenario(t)
	e := findEvent(c, 155, "elected", 1)
	if e == nil {
		t.Fatal("node 1 was not elected leader at tick 155")
	}
	l := c.Leader()
	if l == nil || l.ID != 1 {
		t.Fatalf("expected node 1 as final leader, got %+v", l)
	}
	if l.Term != 2 {
		t.Fatalf("expected term 2 for new leader, got %d", l.Term)
	}
	t.Logf("event: %v", e)
}

func TestOldPrimaryRequestsRejected(t *testing.T) {
	c := runScenario(t)
	if c.SendWrite(0, "set x=1") {
		t.Fatal("stale primary's write was accepted")
	}
	if c.CanWrite(0) {
		t.Fatal("stale primary can still write locally")
	}
	rejected := false
	for _, e := range c.Events {
		if e.Kind == "write-rejected" {
			rejected = true
		}
	}
	if !rejected {
		t.Fatal("no write-rejected event recorded")
	}
}

func TestNoMajorityNoLease(t *testing.T) {
	c := NewCluster(3, testConfig())
	c.Boot(99)
	c.Step()
	c.AcquireLease(0)
	c.Isolate(1)
	c.Isolate(2)
	c.Run(10)
	for _, e := range c.Events {
		if e.Kind == "lease-confirmed" {
			t.Fatal("lease confirmed without a majority")
		}
	}
	if c.Nodes[0].LeaseConfirmed {
		t.Fatal("node 0 marked lease confirmed without majority acks")
	}
}

func TestBoundedClockDriftFencing(t *testing.T) {
	c := NewCluster(3, testConfig())
	c.Boot(99)
	c.Step()
	c.AcquireLease(0)
	c.Run(5) // lease confirmed at tick 105, expiry = 155
	n := c.Nodes[0]
	if !n.LeaseConfirmed {
		t.Fatal("lease should be confirmed")
	}
	// Within MaxClockDrift of expiry the leader must refuse to write.
	c.Tick = n.LeaseExpiry - c.Config.MaxClockDrift
	if c.CanWrite(0) {
		t.Fatal("write allowed inside the clock-drift safety margin")
	}
	c.Tick = n.LeaseExpiry
	if c.CanWrite(0) {
		t.Fatal("write allowed after lease expiry")
	}
}
