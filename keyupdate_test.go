package netsec

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestUpdateAdvancesGeneration(t *testing.T) {
	m := NewStateMachine(4)

	if got := m.Generation(); got != 0 {
		t.Fatalf("initial generation = %d, want 0", got)
	}
	if err := m.Update(); err != nil {
		t.Fatalf("first update: %v", err)
	}
	if got := m.Generation(); got != 1 {
		t.Fatalf("generation after update = %d, want 1", got)
	}
	if !m.Pending() {
		t.Fatal("update is not pending")
	}
	if got := m.Consecutive(); got != 1 {
		t.Fatalf("consecutive = %d, want 1", got)
	}
}

func TestSecondUpdateWithoutAckFails(t *testing.T) {
	m := NewStateMachine(4)

	if err := m.Update(); err != nil {
		t.Fatalf("first update: %v", err)
	}
	if err := m.Update(); !errors.Is(err, ErrKeyUpdatePending) {
		t.Fatalf("second update error = %v, want ErrKeyUpdatePending", err)
	}
	if got := m.Generation(); got != 1 {
		t.Fatalf("generation after rejected update = %d, want 1", got)
	}
	if m.Failed() {
		t.Fatal("pending error must not make the machine terminal")
	}
}

func TestAckAllowsAnotherUpdate(t *testing.T) {
	m := NewStateMachine(4)

	for i := 1; i <= 2; i++ {
		if err := m.Update(); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
		if err := m.Acknowledge(); err != nil {
			t.Fatalf("ack %d: %v", i, err)
		}
		if m.Pending() {
			t.Fatalf("update %d still pending after ack", i)
		}
	}

	if got := m.Generation(); got != 2 {
		t.Fatalf("generation = %d, want 2", got)
	}
	if got := m.Consecutive(); got != 2 {
		t.Fatalf("consecutive = %d, want 2", got)
	}
}

func TestConsecutiveLimitIsTerminal(t *testing.T) {
	m := NewStateMachine(3)

	for i := 1; i <= 3; i++ {
		if err := m.Update(); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
		if err := m.Ack(); err != nil {
			t.Fatalf("ack %d: %v", i, err)
		}
	}

	if err := m.Update(); !errors.Is(err, ErrTooManyKeyUpdates) {
		t.Fatalf("update beyond limit error = %v, want ErrTooManyKeyUpdates", err)
	}
	if got := m.Generation(); got != 3 {
		t.Fatalf("generation after rejected update = %d, want 3", got)
	}
	if !m.Failed() {
		t.Fatal("limit violation did not make the machine terminal")
	}
	if err := m.Ack(); !errors.Is(err, ErrTooManyKeyUpdates) {
		t.Fatalf("ack after failure = %v, want ErrTooManyKeyUpdates", err)
	}
}

func TestProgressResetsConsecutiveRun(t *testing.T) {
	m := NewStateMachine(2)

	for i := 0; i < 2; i++ {
		if err := m.Update(); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
		if err := m.Ack(); err != nil {
			t.Fatalf("ack %d: %v", i, err)
		}
	}
	if err := m.ApplicationData(); err != nil {
		t.Fatalf("application data: %v", err)
	}
	if got := m.Consecutive(); got != 0 {
		t.Fatalf("consecutive after progress = %d, want 0", got)
	}
	if err := m.Update(); err != nil {
		t.Fatalf("update after progress: %v", err)
	}
}

func TestAckWithoutPendingIsIdempotent(t *testing.T) {
	m := NewStateMachine(2)

	if err := m.Acknowledge(); err != nil {
		t.Fatalf("ack with no pending update: %v", err)
	}
	if err := m.Update(); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := m.Acknowledge(); err != nil {
		t.Fatalf("first ack: %v", err)
	}
	if err := m.Acknowledge(); err != nil {
		t.Fatalf("duplicate ack: %v", err)
	}
	if m.Pending() {
		t.Fatal("duplicate ack left update pending")
	}
}

func TestResetClearsTerminalFailure(t *testing.T) {
	m := NewStateMachine(1)

	if err := m.Update(); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := m.Ack(); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if err := m.Update(); !errors.Is(err, ErrTooManyKeyUpdates) {
		t.Fatalf("update error = %v, want ErrTooManyKeyUpdates", err)
	}

	m.Reset()
	if m.Failed() {
		t.Fatal("reset did not clear failure")
	}
	if got := m.Generation(); got != 0 {
		t.Fatalf("generation after reset = %d, want 0", got)
	}
	if err := m.Update(); err != nil {
		t.Fatalf("update after reset: %v", err)
	}
}

func TestApplyEvents(t *testing.T) {
	m := NewStateMachine(2)

	if err := m.Apply(EventKeyUpdate); err != nil {
		t.Fatalf("key update event: %v", err)
	}
	if err := m.Apply(EventAcknowledged); err != nil {
		t.Fatalf("ack event: %v", err)
	}
	if err := m.Apply(EventKeyUpdate); err != nil {
		t.Fatalf("second key update event: %v", err)
	}
	if err := m.Apply(EventProgress); err != nil {
		t.Fatalf("progress event: %v", err)
	}
	if m.Pending() {
		t.Fatal("progress did not confirm pending update")
	}
	if got := m.Consecutive(); got != 0 {
		t.Fatalf("consecutive after progress = %d, want 0", got)
	}
	if err := m.Apply(Event(255)); !errors.Is(err, ErrUnknownEvent) {
		t.Fatalf("unknown event error = %v, want ErrUnknownEvent", err)
	}
}

func TestZeroLimitUsesSecureDefault(t *testing.T) {
	m := NewStateMachine(0)
	if got := m.Limit(); got != DefaultMaxConsecutiveUpdates {
		t.Fatalf("limit = %d, want %d", got, DefaultMaxConsecutiveUpdates)
	}
}

func TestZeroValueUsesSecureDefault(t *testing.T) {
	var m StateMachine
	if got := m.Limit(); got != DefaultMaxConsecutiveUpdates {
		t.Fatalf("limit = %d, want %d", got, DefaultMaxConsecutiveUpdates)
	}
}

func TestUnlimited(t *testing.T) {
	m := NewStateMachine(Unlimited)
	if got := m.Limit(); got != 0 {
		t.Fatalf("limit = %d, want 0 (unlimited)", got)
	}
	for i := 0; i < DefaultMaxConsecutiveUpdates+1; i++ {
		if err := m.Update(); err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
		if err := m.Ack(); err != nil {
			t.Fatalf("ack %d: %v", i, err)
		}
	}
}

func TestSnapshotDoesNotChangeState(t *testing.T) {
	m := NewStateMachine(2)
	if err := m.Update(); err != nil {
		t.Fatalf("update: %v", err)
	}

	got := m.Snapshot()
	if got.Generation != 1 || !got.Pending || got.PendingGeneration != 1 || got.Consecutive != 1 || got.Limit != 2 || got.Err != nil {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
}

func TestConcurrentUpdatesRespectPendingInvariant(t *testing.T) {
	m := NewStateMachine(DefaultMaxConsecutiveUpdates)

	const goroutines = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if err := m.Update(); err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if accepted != 1 {
		t.Fatalf("accepted updates = %d, want 1", accepted)
	}
	if got := m.Generation(); got != 1 {
		t.Fatalf("generation = %d, want 1", got)
	}
}

func ExampleStateMachine() {
	m := NewStateMachine(3)

	_ = m.Update()      // advance to generation 1
	_ = m.Update()      // rejected: generation 1 is not acknowledged
	_ = m.Acknowledge() // peer confirms generation 1
	_ = m.Update()      // advance to generation 2

	_ = m.ApplicationData() // non-KeyUpdate traffic resets the run
	_ = m.Update()          // advance to generation 3

	fmt.Println(m.Generation())
	// Output:
	// 3
}
