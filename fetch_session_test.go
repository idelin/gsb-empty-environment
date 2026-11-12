package netsec

import (
	"errors"
	"testing"
)

var (
	tpA0 = TopicPartition{Topic: "orders", Partition: 0}
	tpA1 = TopicPartition{Topic: "orders", Partition: 1}
	tpB0 = TopicPartition{Topic: "payments", Partition: 0}
)

// allowAll authorizes every partition.
func allowAll() Authorizer {
	return AuthorizerFunc(func(string, TopicPartition) bool { return true })
}

func TestStolenSessionID(t *testing.T) {
	table := NewTable(allowAll())
	victim := table.Open("alice", []TopicPartition{tpA0, tpA1})

	// Attacker replays the victim's session ID under their own principal.
	fetched, denied, err := table.Fetch(IncrementalRequest{
		SessionID:  victim.ID(),
		Epoch:      1,
		Principal:  "mallory",
		Partitions: []TopicPartition{tpA0, tpA1},
	})
	if !errors.Is(err, ErrPrincipalMismatch) {
		t.Fatalf("expected ErrPrincipalMismatch, got %v", err)
	}
	if fetched != nil || denied != nil {
		t.Fatalf("attacker must get no data, got fetched=%v denied=%v", fetched, denied)
	}

	// The failed attempt must not consume the epoch: the victim can still
	// continue with epoch 1.
	fetched, _, err = table.Fetch(IncrementalRequest{
		SessionID:  victim.ID(),
		Epoch:      1,
		Principal:  "alice",
		Partitions: []TopicPartition{tpA0},
	})
	if err != nil || len(fetched) != 1 {
		t.Fatalf("victim continuation failed: fetched=%v err=%v", fetched, err)
	}
}

func TestUnknownSessionID(t *testing.T) {
	table := NewTable(allowAll())
	_, _, err := table.Fetch(IncrementalRequest{
		SessionID:  4242,
		Epoch:      1,
		Principal:  "alice",
		Partitions: []TopicPartition{tpA0},
	})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestEpochJump(t *testing.T) {
	table := NewTable(allowAll())
	s := table.Open("alice", []TopicPartition{tpA0})

	// Epoch jump: expected 1, got 7.
	_, _, err := table.Fetch(IncrementalRequest{
		SessionID:  s.ID(),
		Epoch:      7,
		Principal:  "alice",
		Partitions: []TopicPartition{tpA0},
	})
	if !errors.Is(err, ErrInvalidEpoch) {
		t.Fatalf("expected ErrInvalidEpoch on jump, got %v", err)
	}

	// The rejected request must not advance the epoch.
	if _, _, err := table.Fetch(IncrementalRequest{
		SessionID:  s.ID(),
		Epoch:      1,
		Principal:  "alice",
		Partitions: []TopicPartition{tpA0},
	}); err != nil {
		t.Fatalf("epoch 1 should still be accepted, got %v", err)
	}

	// Replay of an already-consumed epoch is also rejected.
	if _, _, err := table.Fetch(IncrementalRequest{
		SessionID:  s.ID(),
		Epoch:      1,
		Principal:  "alice",
		Partitions: []TopicPartition{tpA0},
	}); !errors.Is(err, ErrInvalidEpoch) {
		t.Fatalf("expected ErrInvalidEpoch on replay, got %v", err)
	}
}

func TestRevokedPartitionDroppedOnNextIncremental(t *testing.T) {
	grants := map[TopicPartition]bool{tpA0: true, tpA1: true}
	table := NewTable(AuthorizerFunc(func(_ string, tp TopicPartition) bool {
		return grants[tp]
	}))
	s := table.Open("alice", []TopicPartition{tpA0, tpA1})

	// Revoke tpA1 after the session was created.
	grants[tpA1] = false

	fetched, denied, err := table.Fetch(IncrementalRequest{
		SessionID:  s.ID(),
		Epoch:      1,
		Principal:  "alice",
		Partitions: []TopicPartition{tpA0, tpA1},
	})
	if err != nil {
		t.Fatalf("request should succeed with per-partition error, got %v", err)
	}
	if len(fetched) != 1 || fetched[0] != tpA0 {
		t.Fatalf("expected only tpA0 fetched, got %v", fetched)
	}
	if len(denied) != 1 || denied[0].Partition != tpA1 || !errors.Is(denied[0].Err, ErrNotAuthorized) {
		t.Fatalf("expected tpA1 authorization failure, got %v", denied)
	}

	// The revoked partition is evicted from the session: asking for it again
	// still fails, and re-granting does not resurrect it.
	grants[tpA1] = true
	fetched, denied, err = table.Fetch(IncrementalRequest{
		SessionID:  s.ID(),
		Epoch:      2,
		Principal:  "alice",
		Partitions: []TopicPartition{tpA1},
	})
	if err != nil {
		t.Fatalf("unexpected session error: %v", err)
	}
	if len(fetched) != 0 || len(denied) != 1 || !errors.Is(denied[0].Err, ErrNotAuthorized) {
		t.Fatalf("evicted partition must stay denied, fetched=%v denied=%v", fetched, denied)
	}
}

func TestNeverAuthorizedPartitionRejected(t *testing.T) {
	table := NewTable(allowAll())
	s := table.Open("alice", []TopicPartition{tpA0})

	// tpB0 was never in the session's creation-time authorized set.
	fetched, denied, err := table.Fetch(IncrementalRequest{
		SessionID:  s.ID(),
		Epoch:      1,
		Principal:  "alice",
		Partitions: []TopicPartition{tpA0, tpB0},
	})
	if err != nil {
		t.Fatalf("unexpected session error: %v", err)
	}
	if len(fetched) != 1 || fetched[0] != tpA0 {
		t.Fatalf("expected tpA0 fetched, got %v", fetched)
	}
	if len(denied) != 1 || denied[0].Partition != tpB0 || !errors.Is(denied[0].Err, ErrNotAuthorized) {
		t.Fatalf("expected tpB0 authorization failure, got %v", denied)
	}
}

func TestLegitimateContinuation(t *testing.T) {
	table := NewTable(allowAll())
	s := table.Open("alice", []TopicPartition{tpA0, tpA1})

	for epoch := int32(1); epoch <= 3; epoch++ {
		fetched, denied, err := table.Fetch(IncrementalRequest{
			SessionID:  s.ID(),
			Epoch:      epoch,
			Principal:  "alice",
			Partitions: []TopicPartition{tpA0, tpA1},
		})
		if err != nil {
			t.Fatalf("epoch %d: unexpected error %v", epoch, err)
		}
		if len(denied) != 0 {
			t.Fatalf("epoch %d: unexpected denials %v", epoch, denied)
		}
		if len(fetched) != 2 {
			t.Fatalf("epoch %d: expected 2 partitions, got %v", epoch, fetched)
		}
	}
}

func TestClosedSessionRejected(t *testing.T) {
	table := NewTable(allowAll())
	s := table.Open("alice", []TopicPartition{tpA0})
	table.Close(s.ID())

	_, _, err := table.Fetch(IncrementalRequest{
		SessionID:  s.ID(),
		Epoch:      1,
		Principal:  "alice",
		Partitions: []TopicPartition{tpA0},
	})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound after close, got %v", err)
	}
}
