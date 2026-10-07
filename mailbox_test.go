package mailbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type manualClock struct{ t time.Time }

func (c *manualClock) now() time.Time          { return c.t }
func (c *manualClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// faultStore fails all Appends while err is non-nil, delegating otherwise.
type faultStore struct {
	inner Store
	err   error
}

func (s *faultStore) Append(op Op) error {
	if s.err != nil {
		return s.err
	}
	return s.inner.Append(op)
}

func (s *faultStore) Load() ([]Op, error) { return s.inner.Load() }

var errInjected = errors.New("injected persistence failure")

func newTestQueue(t *testing.T, store Store, clock *manualClock) *Queue {
	t.Helper()
	q, err := New(store, Options{
		VisibilityTimeout: 10 * time.Second,
		Now:               clock.now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return q
}

func mustSend(t *testing.T, q *Queue, bodies ...string) {
	t.Helper()
	for _, b := range bodies {
		if _, err := q.Send([]byte(b)); err != nil {
			t.Fatalf("Send(%q): %v", b, err)
		}
	}
}

func mustReceive(t *testing.T, q *Queue) (Message, ClaimToken) {
	t.Helper()
	msg, tok, err := q.Receive()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	return msg, tok
}

func expectEmpty(t *testing.T, q *Queue) {
	t.Helper()
	if _, _, err := q.Receive(); err != ErrEmpty {
		t.Fatalf("Receive: got %v, want ErrEmpty", err)
	}
}

func TestFIFOOrder(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	q := newTestQueue(t, &MemoryStore{}, clock)
	mustSend(t, q, "a", "b", "c")

	for _, want := range []string{"a", "b", "c"} {
		msg, tok, err := q.Receive()
		if err != nil {
			t.Fatalf("Receive: %v", err)
		}
		if string(msg.Body) != want {
			t.Fatalf("got %q, want %q", msg.Body, want)
		}
		if err := q.Ack(tok); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}
	expectEmpty(t, q)
}

func TestReceiveDoesNotDeleteAndTimeoutRedelivers(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	q := newTestQueue(t, &MemoryStore{}, clock)
	mustSend(t, q, "a")

	msg1, tok1, err := q.Receive()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	// Claimed but not acked: invisible to other receivers, still in queue.
	expectEmpty(t, q)
	if q.Len() != 1 {
		t.Fatalf("Len = %d, want 1", q.Len())
	}

	// After the visibility timeout the message is redelivered with a
	// bumped generation.
	clock.advance(11 * time.Second)
	msg2, tok2, err := q.Receive()
	if err != nil {
		t.Fatalf("Receive after timeout: %v", err)
	}
	if msg2.ID != msg1.ID || string(msg2.Body) != "a" {
		t.Fatalf("redelivery = %+v, want same message %q", msg2, msg1.ID)
	}
	if tok2.Generation != tok1.Generation+1 {
		t.Fatalf("generation = %d, want %d", tok2.Generation, tok1.Generation+1)
	}
}

func TestStaleTokenRejectedAfterRedelivery(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	q := newTestQueue(t, &MemoryStore{}, clock)
	mustSend(t, q, "a")

	_, tok1, _ := q.Receive()
	clock.advance(11 * time.Second)
	_, tok2, _ := q.Receive()

	if err := q.Ack(tok1); err != ErrStaleToken {
		t.Fatalf("Ack(old token) = %v, want ErrStaleToken", err)
	}
	if err := q.Nack(tok1); err != ErrStaleToken {
		t.Fatalf("Nack(old token) = %v, want ErrStaleToken", err)
	}
	if err := q.Ack(tok2); err != nil {
		t.Fatalf("Ack(current token): %v", err)
	}
	expectEmpty(t, q)
	if q.Len() != 0 {
		t.Fatalf("Len = %d, want 0", q.Len())
	}
}

func TestNackRequeuesToTail(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	q := newTestQueue(t, &MemoryStore{}, clock)
	mustSend(t, q, "a", "b")

	_, tokA, _ := q.Receive()
	if err := q.Nack(tokA); err != nil {
		t.Fatalf("Nack: %v", err)
	}

	// "a" went to the tail: "b" is delivered first, then "a" again.
	msg, tokB, _ := q.Receive()
	if string(msg.Body) != "b" {
		t.Fatalf("got %q, want %q", msg.Body, "b")
	}
	msg, tokA2, _ := q.Receive()
	if string(msg.Body) != "a" {
		t.Fatalf("got %q, want %q", msg.Body, "a")
	}
	if tokA2.Generation != tokA.Generation+1 {
		t.Fatalf("generation = %d, want %d", tokA2.Generation, tokA.Generation+1)
	}
	// The settled token stays invalid after the nack.
	if err := q.Ack(tokA); err != ErrStaleToken {
		t.Fatalf("Ack(nacked token) = %v, want ErrStaleToken", err)
	}
	if err := q.Ack(tokB); err != nil {
		t.Fatalf("Ack(b): %v", err)
	}
	if err := q.Ack(tokA2); err != nil {
		t.Fatalf("Ack(a): %v", err)
	}
	expectEmpty(t, q)
}

func TestDuplicateAckIsIdempotent(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	q := newTestQueue(t, &MemoryStore{}, clock)
	mustSend(t, q, "a")

	_, tok, _ := q.Receive()
	if err := q.Ack(tok); err != nil {
		t.Fatalf("first Ack: %v", err)
	}
	if err := q.Ack(tok); err != nil {
		t.Fatalf("duplicate Ack: %v", err)
	}
	expectEmpty(t, q)
}

func TestSendBatchAtomicOnPersistenceFailure(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	mem := &MemoryStore{}
	store := &faultStore{inner: mem}
	q := newTestQueue(t, store, clock)

	store.err = errInjected
	if _, err := q.SendBatch([]byte("a"), []byte("b"), []byte("c")); !errors.Is(err, errInjected) {
		t.Fatalf("SendBatch = %v, want injected error", err)
	}
	// Nothing became visible and nothing was persisted.
	if q.Len() != 0 {
		t.Fatalf("Len = %d, want 0", q.Len())
	}
	expectEmpty(t, q)
	if len(mem.Ops) != 0 {
		t.Fatalf("store holds %d ops, want 0", len(mem.Ops))
	}

	// Once persistence recovers, the same call succeeds as a unit.
	store.err = nil
	ids, err := q.SendBatch([]byte("a"), []byte("b"), []byte("c"))
	if err != nil {
		t.Fatalf("SendBatch: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("got %d ids, want 3", len(ids))
	}
	for _, want := range []string{"a", "b", "c"} {
		msg, tok := mustReceive(t, q)
		if string(msg.Body) != want {
			t.Fatalf("got %q, want %q", msg.Body, want)
		}
		if err := q.Ack(tok); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}
}

func TestPersistenceFailureLeavesStateUntouched(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	mem := &MemoryStore{}
	store := &faultStore{inner: mem}
	q := newTestQueue(t, store, clock)
	mustSend(t, q, "a")

	_, tok, _ := q.Receive()

	// A failed Ack must not delete the message; the claim stays valid.
	store.err = errInjected
	if err := q.Ack(tok); !errors.Is(err, errInjected) {
		t.Fatalf("Ack = %v, want injected error", err)
	}
	store.err = nil
	expectEmpty(t, q) // still claimed, still invisible
	if q.Len() != 1 {
		t.Fatalf("Len = %d, want 1", q.Len())
	}
	if err := q.Ack(tok); err != nil {
		t.Fatalf("Ack after recovery: %v", err)
	}

	// A failed Nack must not release the claim either.
	mustSend(t, q, "b")
	_, tokB, _ := q.Receive()
	store.err = errInjected
	if err := q.Nack(tokB); !errors.Is(err, errInjected) {
		t.Fatalf("Nack = %v, want injected error", err)
	}
	store.err = nil
	expectEmpty(t, q)
	if err := q.Ack(tokB); err != nil {
		t.Fatalf("Ack(b) after failed Nack: %v", err)
	}
}

func TestRecoveryAcrossReopen(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	dir := t.TempDir()
	path := filepath.Join(dir, "queue.wal")

	fs, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	q := newTestQueue(t, fs, clock)
	mustSend(t, q, "a", "b", "c")

	_, tokA, _ := q.Receive()
	if err := q.Ack(tokA); err != nil {
		t.Fatalf("Ack(a): %v", err)
	}
	_, tokB, _ := q.Receive() // "b" claimed, not settled
	if err := fs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen: "a" is gone, "b" is still claimed (invisible until its
	// recorded deadline passes), "c" waits behind it.
	fs2, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	defer fs2.Close()
	q2 := newTestQueue(t, fs2, clock)
	if q2.Len() != 2 {
		t.Fatalf("Len = %d, want 2", q2.Len())
	}
	msg, tokC := mustReceive(t, q2)
	if string(msg.Body) != "c" {
		t.Fatalf("got %q, want %q", msg.Body, "c")
	}
	expectEmpty(t, q2)

	// "b"'s claim survives the restart with its original deadline.
	clock.advance(11 * time.Second)
	msg, tokB2 := mustReceive(t, q2)
	if string(msg.Body) != "b" {
		t.Fatalf("got %q, want %q", msg.Body, "b")
	}
	if tokB2.Generation != tokB.Generation+1 {
		t.Fatalf("generation = %d, want %d", tokB2.Generation, tokB.Generation+1)
	}
	if err := q2.Ack(tokB); err != ErrStaleToken {
		t.Fatalf("Ack(pre-restart token) = %v, want ErrStaleToken", err)
	}
	if err := q2.Ack(tokB2); err != nil {
		t.Fatalf("Ack(b): %v", err)
	}
	if err := q2.Ack(tokC); err != nil {
		t.Fatalf("Ack(c): %v", err)
	}
	expectEmpty(t, q2)
}

func TestTornTailIsTruncatedOnOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queue.wal")

	fs, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("OpenFileStore: %v", err)
	}
	q := newTestQueue(t, fs, &manualClock{t: time.Unix(0, 0)})
	mustSend(t, q, "a")
	goodOffset, err := fs.f.Seek(0, os.SEEK_CUR)
	if err != nil {
		t.Fatalf("Seek: %v", err)
	}
	// Simulate a crash in the middle of an append.
	if _, err := fs.f.Write([]byte(`{"kind":"enqueue","mess`)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := fs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	fs2, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("OpenFileStore after torn write: %v", err)
	}
	defer fs2.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != goodOffset {
		t.Fatalf("wal size = %d, want truncated to %d", info.Size(), goodOffset)
	}
	q2 := newTestQueue(t, fs2, &manualClock{t: time.Unix(0, 0)})
	msg, _, err := q2.Receive()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(msg.Body) != "a" {
		t.Fatalf("got %q, want %q", msg.Body, "a")
	}
}
