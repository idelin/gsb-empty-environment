package netsec

import (
	"context"
	"sync"
	"testing"
)

// TestConcurrentSubmissions hammers the state with parallel submissions and
// replays to ensure the mutex-protected bookkeeping stays consistent and
// unsealing happens exactly once.
func TestConcurrentSubmissions(t *testing.T) {
	state, _, _, _ := newTestState(t)
	shares := testShares(t, state)

	var wg sync.WaitGroup
	for round := 0; round < 3; round++ {
		for _, id := range []string{"alice", "bob", "carol", "dave"} {
			wg.Add(1)
			go func(holderID string) {
				defer wg.Done()
				_, _ = state.SubmitShare(context.Background(), shares[holderID])
			}(id)
		}
	}
	wg.Wait()

	if !state.IsUnsealed() {
		t.Fatal("state should unseal under concurrent submissions")
	}
	events := state.AuditEvents()
	if countByType(events, EventUnsealed) != 1 {
		t.Fatalf("unsealed events = %d, want exactly 1", countByType(events, EventUnsealed))
	}
	if state.PendingCount() != state.Threshold() {
		t.Fatalf("pending count = %d, want threshold %d", state.PendingCount(), state.Threshold())
	}
	if _, err := state.UnsealKey(); err != nil {
		t.Fatalf("unseal key unavailable: %v", err)
	}
}
