package leasefence

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// blockingCommitter models a host preparing a transaction against an external
// resource. Submit blocks on gate until the test releases it, recording the
// token that entered the prepare phase so tests can prove which job got far
// enough to apply and which one had to roll back.
type blockingCommitter struct {
	gate     chan struct{}
	started  chan Token
	prepared []Token
	applied  []Token
	mu       sync.Mutex
}

func newBlockingCommitter() *blockingCommitter {
	return &blockingCommitter{
		gate:    make(chan struct{}),
		started: make(chan Token, 1),
	}
}

// submit is the prepare callback handed to Fence.Commit.
func (c *blockingCommitter) submit(tok Token) error {
	c.mu.Lock()
	c.prepared = append(c.prepared, tok)
	c.mu.Unlock()

	select {
	case c.started <- tok:
	default:
	}
	<-c.gate
	return nil
}

// release lets every blocked submit return.
func (c *blockingCommitter) release() {
	close(c.gate)
}

func (c *blockingCommitter) preparedTokens() []Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Token, len(c.prepared))
	copy(out, c.prepared)
	return out
}

func (c *blockingCommitter) appliedTokens() []Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Token, len(c.applied))
	copy(out, c.applied)
	return out
}

func (c *blockingCommitter) markApplied(tok Token) {
	c.mu.Lock()
	c.applied = append(c.applied, tok)
	c.mu.Unlock()
}

// runJob drives the full lifecycle of one safety job: register an in-flight
// ticket, block inside the committer, then perform the mandatory final
// validation and only mutate the external resource on success.
func runJob(f *Fence, h Handle, c *blockingCommitter) error {
	ticket, err := f.Begin(h)
	if err != nil {
		return err
	}
	if err := f.Commit(ticket, c.submit); err != nil {
		// Commit failed the final fencing check: the prepared transaction
		// must be rolled back and must never touch the shared resource.
		return err
	}
	// Commit succeeded: its internal final validation is the authoritative
	// fence check, so applying the prepared work is now safe.
	c.markApplied(ticket.Token())
	return nil
}

func checkErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func newTestFence(t *testing.T, ttl time.Duration) (*Fence, *ManualClock) {
	t.Helper()
	clock := NewManualClock(time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC))
	return NewFence(clock, ttl), clock
}

// waitFor polls cond against Snapshot until it is true. Tests use it instead
// of sleeping when a background goroutine must finish processing a tick.
func waitFor(t *testing.T, f *Fence, cond func(Snapshot) bool, msg string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := f.Snapshot()
		if cond(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for condition: %s (last snapshot %+v)", msg, s)
		}
		time.Sleep(time.Millisecond)
	}
}
