package logrec

import "testing"

func TestRecyclablePrefixIsMinPersisted(t *testing.T) {
	c := NewCluster("A", "B", "C")
	for i := 0; i < 10; i++ {
		c.Append("x")
	}
	c.OnPersistedAck("A", 8)
	c.OnPersistedAck("B", 10)
	c.OnPersistedAck("C", 6)
	// quorum of 3 members is 2; 2nd-largest persisted of {8,10,6} is 8.
	if c.Confirmed != 8 {
		t.Fatalf("confirmed = %d, want 8", c.Confirmed)
	}
	// recyclable = min(confirmed=8, min persisted=6) = 6.
	if got := c.RecyclablePrefix(); got != 6 {
		t.Fatalf("recyclable prefix = %d, want 6", got)
	}
	if c.Log.Base() != 6 || c.Log.FirstAvailable() != 7 {
		t.Fatalf("log base = %d first = %d, want 6/7", c.Log.Base(), c.Log.FirstAvailable())
	}
}

func TestConfirmRequiresQuorum(t *testing.T) {
	c := NewCluster("A", "B", "C")
	for i := 0; i < 5; i++ {
		c.Append("x")
	}
	c.OnPersistedAck("A", 5)
	if c.Confirmed != 0 {
		t.Fatalf("single replica must not confirm; confirmed = %d", c.Confirmed)
	}
	c.OnPersistedAck("B", 5)
	if c.Confirmed != 5 {
		t.Fatalf("quorum must confirm; confirmed = %d", c.Confirmed)
	}
}

func TestStaleAckDoesNotRegress(t *testing.T) {
	c := NewCluster("A", "B", "C")
	for i := 0; i < 5; i++ {
		c.Append("x")
	}
	c.OnPersistedAck("A", 5)
	c.OnPersistedAck("A", 3) // stale, delayed ack
	if got := c.Replica("A").Persisted; got != 5 {
		t.Fatalf("persisted regressed to %d", got)
	}
}

func TestRemoveOnlineReplicaRejected(t *testing.T) {
	c := NewCluster("A", "B", "C")
	if err := c.RemoveReplica("A"); err == nil {
		t.Fatal("removing an online replica must be rejected")
	}
}
