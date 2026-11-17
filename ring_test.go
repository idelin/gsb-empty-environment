package netsec

import (
	"testing"
)

type releaseEvent struct {
	tenant Tenant
	lease  Lease
}

type recorder struct {
	events []releaseEvent
}

func (rec *recorder) release(tenant Tenant, lease Lease) {
	rec.events = append(rec.events, releaseEvent{tenant, lease})
}

func (rec *recorder) count(lease Lease) int {
	n := 0
	for _, e := range rec.events {
		if e.lease == lease {
			n++
		}
	}
	return n
}

func newTestRing(t *testing.T, cfg Config) (*Ring, *recorder) {
	t.Helper()
	rec := &recorder{}
	cfg.OnRelease = rec.release
	r, err := NewRing(cfg)
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}
	return r, rec
}

func mustSubmit(t *testing.T, r *Ring, tenant Tenant, lease Lease) int {
	t.Helper()
	slot, err := r.Submit(tenant, lease)
	if err != nil {
		t.Fatalf("Submit(%d, %d): %v", tenant, lease, err)
	}
	return slot
}

func mustComplete(t *testing.T, r *Ring, slot int, tag uint64) {
	t.Helper()
	if err := r.Complete(slot, tag); err != nil {
		t.Fatalf("Complete(%d, %d): %v", slot, tag, err)
	}
}

// TestOutOfOrderCompletions submits a full ring and completes it in every
// possible order; each lease must be released exactly once.
func TestOutOfOrderCompletions(t *testing.T) {
	cfg := Config{Slots: 4, TruncBits: 3, SeqBits: 6}
	for _, perm := range permutations(4) {
		r, rec := newTestRing(t, cfg)
		for i := 0; i < 4; i++ {
			mustSubmit(t, r, Tenant(10+i), Lease(100+i))
		}
		for _, slot := range perm {
			mustComplete(t, r, slot, uint64(slot)) // seq == slot for the first cycle
		}
		for i := 0; i < 4; i++ {
			if got := rec.count(Lease(100 + i)); got != 1 {
				t.Fatalf("perm %v: lease %d released %d times", perm, 100+i, got)
			}
		}
		if r.InFlight() != 0 || r.Isolated() {
			t.Fatalf("perm %v: inFlight=%d isolated=%v", perm, r.InFlight(), r.Isolated())
		}
	}
}

// TestDuplicateCompletion delivers the same completion twice; the second
// delivery must be ignored.
func TestDuplicateCompletion(t *testing.T) {
	r, rec := newTestRing(t, Config{Slots: 4, TruncBits: 3, SeqBits: 6})
	for i := 0; i < 4; i++ {
		mustSubmit(t, r, 1, Lease(100+i))
	}
	mustComplete(t, r, 2, 2)
	mustComplete(t, r, 2, 2) // duplicate
	if got := rec.count(102); got != 1 {
		t.Fatalf("lease 102 released %d times", got)
	}
	for slot := 0; slot < 4; slot++ {
		if slot != 2 {
			mustComplete(t, r, slot, uint64(slot))
		}
	}
	for i := 0; i < 4; i++ {
		if got := rec.count(Lease(100 + i)); got != 1 {
			t.Fatalf("lease %d released %d times", 100+i, got)
		}
	}
	if st := r.Stats(); st.Stale != 1 {
		t.Fatalf("Stale = %d, want 1", st.Stale)
	}
}

// TestLateCompletionAfterSlotReuse reuses a slot for a new tenant, then
// replays the previous occupant's completion. The stale entry must not
// release the new tenant's lease.
func TestLateCompletionAfterSlotReuse(t *testing.T) {
	r, rec := newTestRing(t, Config{Slots: 4, TruncBits: 3, SeqBits: 6})
	for i := 0; i < 4; i++ {
		mustSubmit(t, r, 1, Lease(100+i))
	}
	mustComplete(t, r, 0, 0) // releases lease 100, frees slot 0
	mustSubmit(t, r, 2, 200) // slot 0, seq 4, tag 4

	mustComplete(t, r, 0, 0) // late replay of the old completion
	if got := rec.count(200); got != 0 {
		t.Fatalf("stale completion released new tenant's lease %d times", got)
	}
	mustComplete(t, r, 0, 4) // the real completion for seq 4
	if got := rec.count(200); got != 1 {
		t.Fatalf("lease 200 released %d times, want 1", got)
	}
	if got := rec.count(100); got != 1 {
		t.Fatalf("lease 100 released %d times, want 1", got)
	}
}

// TestDeviceReset releases every in-flight lease once, then ignores late
// completions from before the reset.
func TestDeviceReset(t *testing.T) {
	r, rec := newTestRing(t, Config{Slots: 4, TruncBits: 3, SeqBits: 6})
	for i := 0; i < 3; i++ {
		mustSubmit(t, r, 1, Lease(100+i))
	}
	r.Reset()
	for i := 0; i < 3; i++ {
		if got := rec.count(Lease(100 + i)); got != 1 {
			t.Fatalf("lease %d released %d times by reset", 100+i, got)
		}
	}
	// Late completions for pre-reset requests: ignored, no double release.
	for slot := 0; slot < 3; slot++ {
		mustComplete(t, r, slot, uint64(slot))
	}
	for i := 0; i < 3; i++ {
		if got := rec.count(Lease(100 + i)); got != 1 {
			t.Fatalf("lease %d released %d times after late completion", 100+i, got)
		}
	}
	// The queue is usable again.
	mustSubmit(t, r, 2, 200)
	mustComplete(t, r, 0, 0)
	if got := rec.count(200); got != 1 {
		t.Fatalf("lease 200 released %d times", got)
	}
	if r.Epoch() != 1 {
		t.Fatalf("epoch = %d, want 1", r.Epoch())
	}
}

// TestAmbiguousCompletionIsolates checks that a completion whose tag names
// a live request on a different slot isolates the queue instead of guessing,
// and that isolation releases nothing until Reset.
func TestAmbiguousCompletionIsolates(t *testing.T) {
	r, rec := newTestRing(t, Config{Slots: 4, TruncBits: 3, SeqBits: 6})
	for i := 0; i < 4; i++ {
		mustSubmit(t, r, 1, Lease(100+i))
	}
	// Tag 0 names live seq 0 on slot 0, but the completion claims slot 1.
	if err := r.Complete(1, 0); err != ErrQueueIsolated {
		t.Fatalf("Complete(1, 0) = %v, want ErrQueueIsolated", err)
	}
	if !r.Isolated() {
		t.Fatal("queue not isolated")
	}
	if len(rec.events) != 0 {
		t.Fatalf("isolation released leases: %v", rec.events)
	}
	if _, err := r.Submit(1, 200); err != ErrQueueIsolated {
		t.Fatalf("Submit on isolated queue = %v", err)
	}
	if err := r.Complete(0, 0); err != ErrQueueIsolated {
		t.Fatalf("Complete on isolated queue = %v", err)
	}
	if len(rec.events) != 0 {
		t.Fatalf("isolated queue released leases: %v", rec.events)
	}
	// Reset recovers and retires each in-flight lease exactly once.
	r.Reset()
	for i := 0; i < 4; i++ {
		if got := rec.count(Lease(100 + i)); got != 1 {
			t.Fatalf("lease %d released %d times", 100+i, got)
		}
	}
	if r.Isolated() {
		t.Fatal("reset did not clear isolation")
	}
}

// TestProtocolViolationsIsolate covers out-of-range tags and slot indices.
func TestProtocolViolationsIsolate(t *testing.T) {
	for _, tc := range []struct {
		name string
		slot int
		tag  uint64
	}{
		{"tag out of range", 0, 8}, // mask is 7
		{"slot out of range", 4, 0},
		{"negative slot", -1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, rec := newTestRing(t, Config{Slots: 4, TruncBits: 3, SeqBits: 6})
			mustSubmit(t, r, 1, 100)
			if err := r.Complete(tc.slot, tc.tag); err != ErrQueueIsolated {
				t.Fatalf("Complete(%d, %d) = %v, want ErrQueueIsolated", tc.slot, tc.tag, err)
			}
			if len(rec.events) != 0 {
				t.Fatalf("released leases: %v", rec.events)
			}
		})
	}
}

// TestSeqExhaustionAndEpochSwitch fills the sequence space, requires a
// drain before the epoch switch, and ignores late completions from the old
// epoch.
func TestSeqExhaustionAndEpochSwitch(t *testing.T) {
	r, rec := newTestRing(t, Config{Slots: 4, TruncBits: 3, SeqBits: 4}) // 16 seqs per epoch
	var lease Lease
	completeSeq := func(seq uint64) {
		mustComplete(t, r, int(seq)&3, seq&7)
	}
	// Drain 8 requests in two ring cycles.
	for seq := uint64(0); seq < 8; seq++ {
		mustSubmit(t, r, 1, lease)
		lease++
		completeSeq(seq)
	}
	// 8 submitted, 8 remain. Fill the ring without draining.
	for i := 0; i < 4; i++ {
		mustSubmit(t, r, 1, lease)
		lease++
	}
	if err := r.SwitchEpoch(); err != ErrNotDrained {
		t.Fatalf("SwitchEpoch with in-flight = %v, want ErrNotDrained", err)
	}
	for seq := uint64(8); seq < 12; seq++ {
		completeSeq(seq)
	}
	for seq := uint64(12); seq < 16; seq++ {
		mustSubmit(t, r, 1, lease)
		lease++
		completeSeq(seq)
	}
	// 16 total: sequence space exhausted.
	if _, err := r.Submit(1, lease); err != ErrSeqExhausted {
		t.Fatalf("Submit after exhaustion = %v, want ErrSeqExhausted", err)
	}
	if err := r.SwitchEpoch(); err != nil {
		t.Fatalf("SwitchEpoch after drain: %v", err)
	}
	if r.Epoch() != 1 {
		t.Fatalf("epoch = %d, want 1", r.Epoch())
	}
	// Late completion from the old epoch: slot 3 is free, tag 3 maps
	// outside the new window.
	mustComplete(t, r, 3, 3)
	// Submissions resume in the new epoch.
	mustSubmit(t, r, 2, 900)
	mustComplete(t, r, 0, 0)
	if got := rec.count(900); got != 1 {
		t.Fatalf("lease 900 released %d times", got)
	}
	for l := Lease(0); l < 16; l++ {
		if got := rec.count(l); got != 1 {
			t.Fatalf("lease %d released %d times", l, got)
		}
	}
}

// TestNewRingValidation rejects configurations that break the half-tag-space
// safety contract.
func TestNewRingValidation(t *testing.T) {
	for _, cfg := range []Config{
		{Slots: 3, TruncBits: 3, SeqBits: 6},  // not a power of two
		{Slots: 1, TruncBits: 3, SeqBits: 6},  // too small
		{Slots: 4, TruncBits: 0, SeqBits: 6},  // no tag bits
		{Slots: 4, TruncBits: 2, SeqBits: 6},  // tag space 4 < 2*4
		{Slots: 4, TruncBits: 3, SeqBits: 3},  // SeqBits <= TruncBits
		{Slots: 4, TruncBits: 3, SeqBits: 64}, // too large
	} {
		if _, err := NewRing(cfg); err == nil {
			t.Fatalf("NewRing(%+v) succeeded, want error", cfg)
		}
	}
}

func permutations(n int) [][]int {
	if n == 1 {
		return [][]int{{0}}
	}
	var out [][]int
	for _, p := range permutations(n - 1) {
		for i := 0; i <= len(p); i++ {
			q := make([]int, 0, n)
			q = append(q, p[:i]...)
			q = append(q, n-1)
			q = append(q, p[i:]...)
			out = append(out, q)
		}
	}
	return out
}
