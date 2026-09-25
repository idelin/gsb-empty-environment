package lease

import (
	"testing"
	"time"

	"leasefencing/sim"
)

type sink struct {
	writes []WriteResponse
}

func (s *sink) Receive(msg interface{}) {
	if resp, ok := msg.(WriteResponse); ok {
		s.writes = append(s.writes, resp)
	}
}

func TestOldLeaderStaleWriteCannotOverwriteNewLeader(t *testing.T) {
	sys := NewSystem([]string{"A", "B"}, 4*time.Second, 250*time.Millisecond, map[string]time.Duration{
		"A": 200 * time.Millisecond,
		"B": -200 * time.Millisecond,
	})
	sys.Sim.SetDelay("A", ResourceID, 5*time.Second)
	a := sys.Nodes["A"]
	b := sys.Nodes["B"]

	a.Campaign()
	sys.Sim.Advance(100 * time.Millisecond)
	if a.Role() != Leader {
		t.Fatalf("A should be leader, got %s", a.Role())
	}
	if err := a.RequestWrite("v1-from-A"); err != nil {
		t.Fatalf("A write: %v", err)
	}
	sys.Sim.Advance(50 * time.Millisecond)

	sys.Sim.Pause("A")
	sys.Sim.Advance(4200 * time.Millisecond)

	b.Campaign()
	sys.Sim.Advance(100 * time.Millisecond)
	if b.Role() != Leader {
		t.Fatalf("B should be leader after A lease expired, got %s", b.Role())
	}
	if err := b.RequestWrite("v2-from-B"); err != nil {
		t.Fatalf("B write: %v", err)
	}
	sys.Sim.Advance(200 * time.Millisecond)
	if got := sys.Resource.Value(); got != "v2-from-B" {
		t.Fatalf("resource should hold v2-from-B, got %q", got)
	}
	if sys.Resource.LastFence() != 2 {
		t.Fatalf("resource fence should be 2, got %d", sys.Resource.LastFence())
	}

	sys.Sim.Resume("A")
	sys.Sim.Advance(1500 * time.Millisecond)

	if got := sys.Resource.Value(); got != "v2-from-B" {
		t.Fatalf("stale write overwrote resource: %q", got)
	}
	if sys.Resource.LastFence() != 2 {
		t.Fatalf("fence regressed to %d", sys.Resource.LastFence())
	}
	if sys.Resource.Rejected == 0 {
		t.Fatalf("resource should have rejected A stale write")
	}
	if a.LastFence() != 1 {
		t.Fatalf("A stale fence should be 1, got %d", a.LastFence())
	}
	if a.Role() == Leader {
		t.Fatalf("A should step down after resume")
	}
	if err := a.RequestWrite("v1-late"); err == nil {
		t.Fatalf("A write after resume should fail locally")
	}
}

func TestResourceRejectsDuplicateAndRollback(t *testing.T) {
	s := sim.NewSimulator(time.Unix(0, 0).UTC())
	s.SetDefaultDelay(time.Millisecond)
	res := NewResource(ResourceID, s)
	client := &sink{}
	s.Register(ResourceID, res)
	s.Register("client", client)

	send := func(fence uint64, value string) {
		s.Send("client", ResourceID, WriteRequest{From: "client", Term: 1, Fence: fence, Value: value})
		s.Advance(5 * time.Millisecond)
	}

	send(2, "v2")
	send(2, "duplicate")
	send(1, "rollback")
	send(0, "zero")

	if res.Value() != "v2" || res.LastFence() != 2 {
		t.Fatalf("resource state = (%q, %d), want (v2, 2)", res.Value(), res.LastFence())
	}
	if res.Rejected != 3 {
		t.Fatalf("rejected = %d, want 3", res.Rejected)
	}
	if len(client.writes) != 4 {
		t.Fatalf("responses = %d, want 4", len(client.writes))
	}
	want := []bool{true, false, false, false}
	for i, resp := range client.writes {
		if resp.Accepted != want[i] {
			t.Fatalf("response %d accepted = %v, want %v (%s)", i, resp.Accepted, want[i], resp.Reason)
		}
	}
}

func TestClockSkewModelForcesEarlyStepDown(t *testing.T) {
	sys := NewSystem([]string{"A"}, 6*time.Second, 500*time.Millisecond, map[string]time.Duration{
		"A": 400 * time.Millisecond,
	})
	a := sys.Nodes["A"]

	a.Campaign()
	sys.Sim.Advance(100 * time.Millisecond)
	if a.Role() != Leader {
		t.Fatalf("A should be leader, got %s", a.Role())
	}
	expiry := sys.Coordinator.ExpiresAt()
	localDeadline := a.LeaseExpiresLocal()
	if got := expiry.Sub(localDeadline); got != time.Second {
		t.Fatalf("local safety margin = %v, want 1s (2*maxClockError)", got)
	}

	sys.Sim.Drop("A", CoordinatorID)
	sys.Sim.Advance(5 * time.Second)

	if a.Role() == Leader {
		t.Fatalf("A must step down before reference lease expiry")
	}
	if !sys.Sim.Now().Before(expiry) {
		t.Fatalf("step down happened after reference expiry")
	}
}

func TestNewLeaderAcquiresLeaseAndWritesAfterExpiry(t *testing.T) {
	sys := NewSystem([]string{"A", "B"}, 3*time.Second, 250*time.Millisecond, nil)
	a := sys.Nodes["A"]
	b := sys.Nodes["B"]

	a.Campaign()
	sys.Sim.Advance(100 * time.Millisecond)
	if a.Role() != Leader {
		t.Fatalf("A should be leader, got %s", a.Role())
	}

	sys.Sim.Crash("A")
	sys.Sim.Advance(3200 * time.Millisecond)

	sys.Sim.Drop("B", CoordinatorID)
	b.Campaign()
	sys.Sim.Advance(100 * time.Millisecond)
	if b.Role() == Leader {
		t.Fatalf("B campaign request should have been dropped")
	}
	sys.Sim.ClearDrop("B", CoordinatorID)
	sys.Sim.Advance(1500 * time.Millisecond)
	if b.Role() != Leader {
		t.Fatalf("B should become leader after retry, got %s", b.Role())
	}

	if err := b.RequestWrite("v2-from-B"); err != nil {
		t.Fatalf("B write: %v", err)
	}
	sys.Sim.Advance(200 * time.Millisecond)
	if got := sys.Resource.Value(); got != "v2-from-B" {
		t.Fatalf("resource should hold v2-from-B, got %q", got)
	}
}
