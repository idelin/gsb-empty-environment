package lease

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const (
	keyA KeyVersion = "ecdsa-p256-v1"
	keyB KeyVersion = "ecdsa-p256-v2"
)

func open(t *testing.T, dir, instance string) *Manager {
	t.Helper()
	m, err := Open(dir, instance)
	if err != nil {
		t.Fatalf("Open(%s): %v", instance, err)
	}
	return m
}

// TestConcurrentAcquireNoDuplicateIssuance runs many instances against one
// slot directory and asserts every lease hands out a distinct slot.
func TestConcurrentAcquireNoDuplicateIssuance(t *testing.T) {
	dir := t.TempDir()
	const instances = 6
	const slots = 120

	first := open(t, dir, "provisioner")
	if err := first.Provision(keyA, slots); err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	type got struct {
		slot uint64
		id   string
	}
	results := make(chan got, slots)
	var wg sync.WaitGroup
	for i := 0; i < instances; i++ {
		m := open(t, dir, fmt.Sprintf("inst-%d", i))
		defer m.Close()
		wg.Add(1)
		go func(m *Manager) {
			defer wg.Done()
			for {
				l, err := m.Acquire(keyA)
				if errors.Is(err, ErrNoSlots) {
					return
				}
				if err != nil {
					t.Errorf("Acquire: %v", err)
					return
				}
				results <- got{slot: l.Slot, id: l.ID}
			}
		}(m)
	}
	wg.Wait()
	close(results)

	seenSlots := make(map[uint64]string)
	seenIDs := make(map[string]bool)
	count := 0
	for g := range results {
		count++
		if prev, dup := seenSlots[g.slot]; dup {
			t.Fatalf("slot %d issued twice (leases %q and %q)", g.slot, prev, g.id)
		}
		seenSlots[g.slot] = g.id
		if seenIDs[g.id] {
			t.Fatalf("lease ID %q issued twice", g.id)
		}
		seenIDs[g.id] = true
	}
	if count != slots {
		t.Fatalf("issued %d leases, want %d", count, slots)
	}
}

// TestReleaseOnlyBeforeHandoff checks the happy-path return and that a
// released slot is reissued.
func TestReleaseOnlyBeforeHandoff(t *testing.T) {
	dir := t.TempDir()
	m := open(t, dir, "inst")
	defer m.Close()
	if err := m.Provision(keyA, 1); err != nil {
		t.Fatal(err)
	}

	l, err := m.Acquire(keyA)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Release(l); err != nil {
		t.Fatalf("Release before handoff: %v", err)
	}
	l2, err := m.Acquire(keyA)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	if l2.Slot != l.Slot {
		t.Fatalf("released slot not reused: got %d, want %d", l2.Slot, l.Slot)
	}
	// Double release of the old token must fail.
	if err := m.Release(l); !errors.Is(err, ErrLeaseSettled) && !errors.Is(err, ErrLeaseBindingMismatch) {
		t.Fatalf("double release: got %v", err)
	}
}

// TestHandoffIsPointOfNoReturn simulates an HSM timeout: after handoff the
// slot is consumed forever and can never be released or reissued.
func TestHandoffIsPointOfNoReturn(t *testing.T) {
	dir := t.TempDir()
	m := open(t, dir, "inst")
	defer m.Close()
	if err := m.Provision(keyA, 2); err != nil {
		t.Fatal(err)
	}

	l, err := m.Acquire(keyA)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Handoff(l); err != nil {
		t.Fatal(err)
	}
	// Hardware call times out: release must be refused.
	if err := m.Release(l); !errors.Is(err, ErrLeaseHandedOff) {
		t.Fatalf("Release after handoff: got %v, want ErrLeaseHandedOff", err)
	}
	// Timeout path permanently consumes the slot.
	if err := m.Consume(l); err != nil {
		t.Fatalf("Consume after timeout: %v", err)
	}
	if err := m.Consume(l); err != nil {
		t.Fatalf("Consume must be idempotent: %v", err)
	}

	// Only the second slot may ever be issued again.
	l2, err := m.Acquire(keyA)
	if err != nil {
		t.Fatal(err)
	}
	if l2.Slot == l.Slot {
		t.Fatalf("consumed slot %d reissued", l.Slot)
	}
	if _, err := m.Acquire(keyA); !errors.Is(err, ErrNoSlots) {
		t.Fatalf("pool should be exhausted, got %v", err)
	}
	st := m.Stats(keyA)
	if st.Consumed != 1 || st.Leased != 1 {
		t.Fatalf("stats = %+v, want 1 consumed + 1 leased", st)
	}
}

// TestPowerLossRecovery kills an instance without cleanup and verifies the
// survivor treats every in-flight slot (leased or handed off) as consumed,
// while cleanly released slots stay reusable.
func TestPowerLossRecovery(t *testing.T) {
	dir := t.TempDir()

	dead := open(t, dir, "dead-instance")
	if err := dead.Provision(keyA, 5); err != nil {
		t.Fatal(err)
	}
	handed, err := dead.Acquire(keyA) // slot 1
	if err != nil {
		t.Fatal(err)
	}
	if err := dead.Handoff(handed); err != nil {
		t.Fatal(err)
	}
	if _, err := dead.Acquire(keyA); err != nil { // slot 2: leased, never handed off
		t.Fatal(err)
	}
	released, err := dead.Acquire(keyA) // slot 3
	if err != nil {
		t.Fatal(err)
	}
	if err := dead.Release(released); err != nil {
		t.Fatal(err)
	}
	// Power loss: no Close, no defer; the OS would reap the fds.
	_ = dead

	survivor := open(t, dir, "survivor")
	defer survivor.Close()
	st := survivor.Stats(keyA)
	if st.Consumed != 2 || st.Free != 3 {
		t.Fatalf("after recovery stats = %+v, want 2 consumed + 3 free", st)
	}

	// The two in-flight slots must never be issued again.
	issued := make(map[uint64]bool)
	for {
		l, err := survivor.Acquire(keyA)
		if errors.Is(err, ErrNoSlots) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		issued[l.Slot] = true
	}
	if issued[handed.Slot] {
		t.Fatalf("handed-off slot %d reissued after crash", handed.Slot)
	}
	if len(issued) != 3 {
		t.Fatalf("issued %d slots after recovery, want 3", len(issued))
	}
	if !issued[released.Slot] {
		t.Fatalf("cleanly released slot %d not reusable", released.Slot)
	}
}

// TestTornJournalTail simulates a crash in the middle of a journal write:
// replay must stop at the torn record and stay consistent.
func TestTornJournalTail(t *testing.T) {
	dir := t.TempDir()
	m := open(t, dir, "inst")
	if err := m.Provision(keyA, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Acquire(keyA); err != nil {
		t.Fatal(err)
	}
	m.Close()

	// Torn tail: half-written record from the dying flush.
	f, err := os.OpenFile(filepath.Join(dir, "lease.journal"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"op":"release","key":"ecdsa-p256-v1","slot":1,"lease`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	m2 := open(t, dir, "inst-2")
	defer m2.Close()
	st := m2.Stats(keyA)
	// The leased slot is conservatively consumed; the torn release is ignored.
	if st.Consumed != 1 || st.Free != 1 {
		t.Fatalf("stats = %+v, want 1 consumed + 1 free", st)
	}
}

// TestOutOfOrderCompletion hands several leases to the hardware and lets
// them complete in arbitrary order; every slot ends consumed exactly once.
func TestOutOfOrderCompletion(t *testing.T) {
	dir := t.TempDir()
	m := open(t, dir, "inst")
	defer m.Close()
	const n = 8
	if err := m.Provision(keyA, n); err != nil {
		t.Fatal(err)
	}

	leases := make([]*Lease, 0, n)
	for i := 0; i < n; i++ {
		l, err := m.Acquire(keyA)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Handoff(l); err != nil {
			t.Fatal(err)
		}
		leases = append(leases, l)
	}

	// Hardware completions arrive out of order and concurrently.
	order := []int{5, 0, 7, 2, 3, 6, 1, 4}
	var wg sync.WaitGroup
	for _, idx := range order {
		wg.Add(1)
		go func(l *Lease) {
			defer wg.Done()
			if err := m.Consume(l); err != nil {
				t.Errorf("Consume slot %d: %v", l.Slot, err)
			}
		}(leases[idx])
	}
	wg.Wait()

	st := m.Stats(keyA)
	if st.Consumed != n {
		t.Fatalf("consumed = %d, want %d", st.Consumed, n)
	}
	if _, err := m.Acquire(keyA); !errors.Is(err, ErrNoSlots) {
		t.Fatalf("all slots consumed, got %v", err)
	}
}

// TestCrossKeyReturnRejected verifies a lease issued for one key version can
// never be returned (or consumed) against another key's pool, even with a
// tampered token or from a different instance.
func TestCrossKeyReturnRejected(t *testing.T) {
	dir := t.TempDir()
	m1 := open(t, dir, "inst-1")
	defer m1.Close()
	m2 := open(t, dir, "inst-2")
	defer m2.Close()
	if err := m1.Provision(keyA, 1); err != nil {
		t.Fatal(err)
	}
	if err := m1.Provision(keyB, 1); err != nil {
		t.Fatal(err)
	}

	l, err := m1.Acquire(keyA)
	if err != nil {
		t.Fatal(err)
	}

	// Tampered token: same lease ID and slot, but relabeled to key B.
	forged := &Lease{ID: l.ID, Key: keyB, Slot: l.Slot}
	if err := m1.Release(forged); !errors.Is(err, ErrLeaseBindingMismatch) {
		t.Fatalf("cross-key release: got %v, want ErrLeaseBindingMismatch", err)
	}
	if err := m1.Consume(forged); !errors.Is(err, ErrLeaseBindingMismatch) {
		t.Fatalf("cross-key consume: got %v, want ErrLeaseBindingMismatch", err)
	}
	// Cross-instance attempt with the forged token.
	if err := m2.Release(forged); !errors.Is(err, ErrLeaseBindingMismatch) {
		t.Fatalf("cross-instance cross-key release: got %v", err)
	}
	// Cross-instance attempt with a slot number that exists under key B.
	forgedSlot := &Lease{ID: l.ID, Key: keyB, Slot: 1}
	if err := m2.Release(forgedSlot); !errors.Is(err, ErrLeaseBindingMismatch) {
		t.Fatalf("cross-key slot reuse: got %v", err)
	}

	// Key B's pool is untouched; key A's lease is still live and valid.
	if st := m1.Stats(keyB); st.Free != 1 {
		t.Fatalf("key B pool disturbed: %+v", st)
	}
	if err := m1.Release(l); err != nil {
		t.Fatalf("legitimate release after forged attempts: %v", err)
	}
	if st := m1.Stats(keyA); st.Free != 1 {
		t.Fatalf("key A slot not returned: %+v", st)
	}
}

// TestCrossInstanceHandoffVisibility ensures a lease handed off by one
// instance cannot be released through another instance sharing the store.
func TestCrossInstanceHandoffVisibility(t *testing.T) {
	dir := t.TempDir()
	m1 := open(t, dir, "inst-1")
	defer m1.Close()
	m2 := open(t, dir, "inst-2")
	defer m2.Close()
	if err := m1.Provision(keyA, 1); err != nil {
		t.Fatal(err)
	}

	l, err := m1.Acquire(keyA)
	if err != nil {
		t.Fatal(err)
	}
	if err := m1.Handoff(l); err != nil {
		t.Fatal(err)
	}
	// Another instance sees the handoff and refuses the return.
	if err := m2.Release(l); !errors.Is(err, ErrLeaseHandedOff) {
		t.Fatalf("cross-instance release after handoff: got %v", err)
	}
	if err := m2.Consume(l); err != nil {
		t.Fatalf("cross-instance consume: %v", err)
	}
	if st := m1.Stats(keyA); st.Consumed != 1 {
		t.Fatalf("stats = %+v, want 1 consumed", st)
	}
}
