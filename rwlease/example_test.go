package rwlease_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"rwlease/rwlease"
)

// Example shows the complete lifecycle: concurrent readers, a queued writer
// that gates new readers, a failing upgrade that keeps the read right, a
// successful upgrade, double-release / stale-release guards, and revocation.
func Example() {
	m := rwlease.NewManager()
	ctx := context.Background()

	// Two concurrent readers.
	r1, _ := m.Acquire(ctx, rwlease.Read)
	r2, _ := m.Acquire(ctx, rwlease.Read)

	// A writer queues behind the readers; new readers arriving now wait too,
	// so the writer cannot be starved by a continuous read stream.
	writerDone := make(chan struct{})
	go func() {
		w, err := m.Acquire(ctx, rwlease.Write)
		if err != nil {
			return
		}
		fmt.Println("writer acquired")
		m.Release(w)
		close(writerDone)
	}()
	time.Sleep(10 * time.Millisecond)

	// r1 tries to jump ahead of the queued writer: rejected immediately and
	// its read right is preserved.
	if _, err := m.Upgrade(ctx, r1); errors.Is(err, rwlease.ErrUpgradeImpossible) {
		fmt.Println("upgrade blocked, read kept")
	}

	m.Release(r1)
	m.Release(r2)
	<-writerDone

	// A clean reader upgrades to writer once it is the last reader.
	r3, _ := m.Acquire(ctx, rwlease.Read)
	w3, err := m.Upgrade(ctx, r3)
	if err != nil {
		fmt.Println("unexpected:", err)
		return
	}
	fmt.Println("upgraded to", w3.Mode())

	// Releasing the old read token after a successful upgrade is safe.
	if err := m.Release(r3); errors.Is(err, rwlease.ErrTokenReplaced) {
		fmt.Println("old token replaced")
	}
	m.Release(w3)

	// Double release is rejected.
	tok, _ := m.Acquire(ctx, rwlease.Read)
	m.Release(tok)
	if err := m.Release(tok); errors.Is(err, rwlease.ErrAlreadyReleased) {
		fmt.Println("double release rejected")
	}

	// Cross-generation release: Revoke invalidates the live generation.
	live, _ := m.Acquire(ctx, rwlease.Read)
	m.Revoke()
	if err := m.Release(live); errors.Is(err, rwlease.ErrLeaseRevoked) {
		fmt.Println("stale release rejected, gen", live.Generation())
	}
	fresh, _ := m.Acquire(ctx, rwlease.Read)
	fmt.Println("fresh generation", fresh.Generation())
	m.Release(fresh)

	// Output:
	// upgrade blocked, read kept
	// writer acquired
	// upgraded to write
	// old token replaced
	// double release rejected
	// stale release rejected, gen 0
	// fresh generation 1
}
