package chunkedtx

import (
	"strings"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock for expiry tests.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func chunks(txID string, parts ...string) []Chunk {
	full := strings.Join(parts, "")
	digest := DigestOf([]byte(full))
	out := make([]Chunk, len(parts))
	for i, p := range parts {
		out[i] = Chunk{
			TxID:    txID,
			Seq:     uint32(i),
			Final:   i == len(parts)-1,
			Digest:  digest,
			Payload: []byte(p),
		}
	}
	return out
}

func addNoCommit(t *testing.T, s *Stager, c Chunk) {
	t.Helper()
	payload, committed, err := s.Add(c)
	if err != nil {
		t.Fatalf("Add(tx=%q seq=%d): %v", c.TxID, c.Seq, err)
	}
	if committed {
		t.Fatalf("committed too early with %q", payload)
	}
}

func TestInOrderCommit(t *testing.T) {
	s := New(1<<20, time.Minute)
	cs := chunks("tx1", "hello, ", "chunked ", "world")
	for _, c := range cs[:2] {
		addNoCommit(t, s, c)
	}
	payload, committed, err := s.Add(cs[2])
	if err != nil || !committed {
		t.Fatalf("got committed=%v err=%v", committed, err)
	}
	if string(payload) != "hello, chunked world" {
		t.Fatalf("payload = %q", payload)
	}
	if used, pending := s.Stats(); used != 0 || pending != 0 {
		t.Fatalf("budget not released after commit: used=%d pending=%d", used, pending)
	}
}

func TestOutOfOrderCommit(t *testing.T) {
	s := New(1<<20, time.Minute)
	cs := chunks("tx1", "a", "b", "c", "d")
	// Final chunk first, then the rest out of order: nothing may commit
	// until the last gap is filled.
	for _, i := range []int{3, 1, 0} {
		addNoCommit(t, s, cs[i])
	}
	payload, committed, err := s.Add(cs[2])
	if err != nil || !committed {
		t.Fatalf("got committed=%v err=%v", committed, err)
	}
	if string(payload) != "abcd" {
		t.Fatalf("payload = %q", payload)
	}
}

func TestGapDoesNotCommit(t *testing.T) {
	s := New(1<<20, time.Minute)
	cs := chunks("tx1", "a", "b", "c")
	// Seq 0 and final seq 2 staged, seq 1 missing: must not commit.
	addNoCommit(t, s, cs[0])
	addNoCommit(t, s, cs[2])
	if _, pending := s.Stats(); pending != 1 {
		t.Fatalf("pending = %d", pending)
	}
}

func TestDuplicateRedeliveryIsIdempotent(t *testing.T) {
	s := New(1<<20, time.Minute)
	cs := chunks("tx1", "a", "b")
	addNoCommit(t, s, cs[0])
	addNoCommit(t, s, cs[0]) // exact redelivery
	if used, _ := s.Stats(); used != 1 {
		t.Fatalf("duplicate double-counted budget: used=%d", used)
	}
	if _, committed, err := s.Add(cs[1]); err != nil || !committed {
		t.Fatalf("got committed=%v err=%v", committed, err)
	}
}

func TestDuplicateConflictFailsPermanently(t *testing.T) {
	s := New(1<<20, time.Minute)
	cs := chunks("tx1", "a", "b")
	addNoCommit(t, s, cs[0])

	evil := cs[0]
	evil.Payload = []byte("X")
	if _, _, err := s.Add(evil); err != ErrTxFailed {
		t.Fatalf("conflict err = %v", err)
	}
	// The transaction is permanently failed: even the valid final chunk
	// can never complete it.
	if _, committed, err := s.Add(cs[1]); err != ErrTxFailed || committed {
		t.Fatalf("after failure: committed=%v err=%v", committed, err)
	}
	if used, pending := s.Stats(); used != 0 || pending != 0 {
		t.Fatalf("failed tx still holds budget: used=%d pending=%d", used, pending)
	}
}

func TestConflictingFinalSeqFails(t *testing.T) {
	s := New(1<<20, time.Minute)
	cs := chunks("tx1", "a", "b")
	addNoCommit(t, s, cs[1]) // final at seq 1
	other := cs[0]
	other.Final = true // a second, conflicting final at seq 0
	if _, _, err := s.Add(other); err != ErrTxFailed {
		t.Fatalf("err = %v", err)
	}
}

func TestDigestMismatchFailsAndDoesNotCommit(t *testing.T) {
	s := New(1<<20, time.Minute)
	// All chunks agree on the same wrong digest, so the inconsistency
	// only surfaces when the assembled payload is verified at completion.
	cs := chunks("tx1", "a", "b")
	wrong := DigestOf([]byte("something else"))
	cs[0].Digest = wrong
	cs[1].Digest = wrong
	addNoCommit(t, s, cs[0])
	if _, committed, err := s.Add(cs[1]); err != ErrDigestMismatch || committed {
		t.Fatalf("committed=%v err=%v", committed, err)
	}
	if _, _, err := s.Add(cs[0]); err != ErrTxFailed {
		t.Fatalf("after digest failure err = %v", err)
	}
}

func TestBudgetIsGlobalAndBounded(t *testing.T) {
	s := New(10, time.Minute)
	a := chunks("txA", "aaaa", "aa")
	b := chunks("txB", "bbbb", "bbbb")
	addNoCommit(t, s, a[0]) // used=4
	addNoCommit(t, s, b[0]) // used=8
	if _, _, err := s.Add(Chunk{TxID: "txC", Seq: 0, Payload: []byte("ccc")}); err != ErrBudgetExceeded {
		t.Fatalf("err = %v", err)
	}
	// Completing txA releases its budget, making room for txC.
	if _, committed, err := s.Add(a[1]); err != nil || !committed {
		t.Fatalf("txA: committed=%v err=%v", committed, err)
	}
	addNoCommit(t, s, Chunk{TxID: "txC", Seq: 0, Payload: []byte("ccc")})
}

func TestExpiredTransactionDoesNotCommit(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	s := New(1<<20, time.Minute, WithClock(clock.Now))
	cs := chunks("tx1", "a", "b")
	addNoCommit(t, s, cs[0])

	clock.Advance(2 * time.Minute)
	// The completing final chunk arrives after the deadline: the
	// transaction must expire instead of committing.
	if _, committed, err := s.Add(cs[1]); err != ErrExpired || committed {
		t.Fatalf("late final: committed=%v err=%v", committed, err)
	}
	if used, pending := s.Stats(); used != 0 || pending != 0 {
		t.Fatalf("expired tx still holds budget: used=%d pending=%d", used, pending)
	}
	// Late chunks for the expired transaction keep being rejected while
	// the tombstone lives, and can never resurrect it.
	if _, committed, err := s.Add(cs[0]); err != ErrExpired || committed {
		t.Fatalf("late replay: committed=%v err=%v", committed, err)
	}
}

func TestSweepExpiresAndReleasesBudget(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	s := New(4, time.Minute, WithClock(clock.Now))
	addNoCommit(t, s, chunks("tx1", "aaaa", "bbbb")[0])

	clock.Advance(2 * time.Minute)
	s.Sweep()
	if used, pending := s.Stats(); used != 0 || pending != 0 {
		t.Fatalf("after sweep: used=%d pending=%d", used, pending)
	}
	// Budget is available again for a fresh transaction.
	addNoCommit(t, s, chunks("tx2", "cccc", "dddd")[0])
}

func TestCommittedTransactionDoesNotRepublish(t *testing.T) {
	s := New(1<<20, time.Minute)
	c := chunks("tx1", "one-shot")[0]
	if _, committed, err := s.Add(c); err != nil || !committed {
		t.Fatalf("committed=%v err=%v", committed, err)
	}
	// Redelivery after commit must not publish a second time.
	if _, committed, err := s.Add(c); err != ErrAlreadyCommitted || committed {
		t.Fatalf("redelivery: committed=%v err=%v", committed, err)
	}
}

func TestInvalidChunk(t *testing.T) {
	s := New(1<<20, time.Minute)
	if _, _, err := s.Add(Chunk{Seq: 0, Payload: []byte("x")}); err != ErrInvalidChunk {
		t.Fatalf("empty TxID err = %v", err)
	}
	if _, _, err := s.Add(Chunk{TxID: "t", Seq: 0, Final: true, Payload: []byte("x")}); err != ErrInvalidChunk {
		t.Fatalf("final without digest err = %v", err)
	}
}
