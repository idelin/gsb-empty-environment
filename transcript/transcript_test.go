package transcript_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"

	"example.org/netsec/q0163/transcript"
)

// referenceSum recomputes the expected transcript digest with the
// standard library only: SHA-256 over seq (big-endian uint64) || body
// for each message, in sequence order.
func referenceSum(msgs ...transcript.Message) []byte {
	h := sha256.New()
	var hdr [8]byte
	for _, m := range msgs {
		binary.BigEndian.PutUint64(hdr[:], m.Seq)
		h.Write(hdr[:])
		h.Write(m.Body)
	}
	return h.Sum(nil)
}

func msg(epoch, seq uint64, body string) transcript.Message {
	return transcript.Message{Epoch: epoch, Seq: seq, Body: []byte(body)}
}

func mustRecord(t *testing.T, c *transcript.Context, m transcript.Message) transcript.Outcome {
	t.Helper()
	out, err := c.Record(m)
	if err != nil {
		t.Fatalf("Record(seq=%d) unexpected error: %v", m.Seq, err)
	}
	return out
}

func TestRecordInOrder(t *testing.T) {
	c := transcript.NewHost().Context()
	msgs := []transcript.Message{msg(0, 0, "ClientHello"), msg(0, 1, "ServerHello"), msg(0, 2, "Finished")}
	for _, m := range msgs {
		if out := mustRecord(t, c, m); out != transcript.Appended {
			t.Fatalf("Record(seq=%d) = %v, want Appended", m.Seq, out)
		}
	}
	if c.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", c.Len())
	}
	if want := referenceSum(msgs...); !bytes.Equal(c.Sum(), want) {
		t.Fatalf("Sum() = %x, want %x", c.Sum(), want)
	}
}

func TestRetransmitIsDeduplicated(t *testing.T) {
	c := transcript.NewHost().Context()
	m := msg(0, 0, "ClientHello")
	if out := mustRecord(t, c, m); out != transcript.Appended {
		t.Fatalf("first Record = %v, want Appended", out)
	}
	before := c.Sum()
	// Replay the exact same message several times, as a retransmitting
	// proxy would: the transcript must not extend again.
	for i := 0; i < 3; i++ {
		if out := mustRecord(t, c, m); out != transcript.Duplicate {
			t.Fatalf("replay %d = %v, want Duplicate", i, out)
		}
	}
	if c.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", c.Len())
	}
	if !bytes.Equal(c.Sum(), before) {
		t.Fatalf("transcript mutated by retransmission: %x -> %x", before, c.Sum())
	}
}

func TestConflictSameSeqDifferentBody(t *testing.T) {
	c := transcript.NewHost().Context()
	mustRecord(t, c, msg(0, 0, "ClientHello"))
	before := c.Sum()

	_, err := c.Record(msg(0, 0, "ClientHello-forged"))
	var conflict *transcript.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Record conflicting seq 0: err = %v, want *ConflictError", err)
	}
	if conflict.Seq != 0 || conflict.Epoch != 0 {
		t.Fatalf("conflict refers to epoch %d seq %d, want epoch 0 seq 0", conflict.Epoch, conflict.Seq)
	}
	wantFirst := sha256.Sum256([]byte("ClientHello"))
	if conflict.First != wantFirst {
		t.Fatalf("conflict.First = %x, want digest of original body %x", conflict.First, wantFirst)
	}
	// The conflicting message must not have extended the transcript.
	if c.Len() != 1 || !bytes.Equal(c.Sum(), before) {
		t.Fatalf("transcript mutated by conflicting message")
	}
}

func TestReorderBufferedThenFlushed(t *testing.T) {
	c := transcript.NewHost().Context()
	m0, m1, m2 := msg(0, 0, "ClientHello"), msg(0, 1, "ServerHello"), msg(0, 2, "Finished")

	// Deliver completely out of order: 2, 1, then the missing 0.
	if out := mustRecord(t, c, m2); out != transcript.Buffered {
		t.Fatalf("Record(seq=2) = %v, want Buffered", out)
	}
	if out := mustRecord(t, c, m1); out != transcript.Buffered {
		t.Fatalf("Record(seq=1) = %v, want Buffered", out)
	}
	if c.Len() != 0 {
		t.Fatalf("Len() = %d before gap filled, want 0", c.Len())
	}
	if out := mustRecord(t, c, m0); out != transcript.Appended {
		t.Fatalf("Record(seq=0) = %v, want Appended", out)
	}
	// Once the gap is filled the buffered messages flush in order and the
	// transcript equals the in-order recording.
	if c.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", c.Len())
	}
	if want := referenceSum(m0, m1, m2); !bytes.Equal(c.Sum(), want) {
		t.Fatalf("Sum() = %x, want in-order %x", c.Sum(), want)
	}
}

func TestRetransmitWhileBuffered(t *testing.T) {
	c := transcript.NewHost().Context()
	m1 := msg(0, 1, "ServerHello")
	if out := mustRecord(t, c, m1); out != transcript.Buffered {
		t.Fatalf("Record(seq=1) = %v, want Buffered", out)
	}
	// A retransmission of a still-buffered message is a duplicate, not a
	// second buffered copy.
	if out := mustRecord(t, c, m1); out != transcript.Duplicate {
		t.Fatalf("buffered replay = %v, want Duplicate", out)
	}
	mustRecord(t, c, msg(0, 0, "ClientHello"))
	if c.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", c.Len())
	}
	if want := referenceSum(msg(0, 0, "ClientHello"), m1); !bytes.Equal(c.Sum(), want) {
		t.Fatalf("Sum() = %x, want %x", c.Sum(), want)
	}
}

func TestRestartOpensFreshContext(t *testing.T) {
	h := transcript.NewHost()
	old := h.Context()
	mustRecord(t, old, msg(0, 0, "ClientHello"))
	mustRecord(t, old, msg(0, 1, "ServerHello"))
	oldSum := old.Sum()

	h.Restart()
	fresh := h.Context()
	if fresh == old {
		t.Fatal("Restart did not install a new context")
	}
	if !old.Sealed() {
		t.Fatal("old context not sealed after Restart")
	}
	if fresh.Epoch() != 1 || fresh.Len() != 0 {
		t.Fatalf("fresh context: epoch %d len %d, want epoch 1 len 0", fresh.Epoch(), fresh.Len())
	}

	// Sequence numbers restart from zero in the new epoch.
	if out := mustRecord(t, fresh, msg(1, 0, "ClientHello'")); out != transcript.Appended {
		t.Fatalf("Record in new epoch = %v, want Appended", out)
	}
	// The new transcript contains only the new message; nothing carried
	// over from the old epoch.
	if want := referenceSum(msg(1, 0, "ClientHello'")); !bytes.Equal(fresh.Sum(), want) {
		t.Fatalf("fresh Sum() = %x, want %x", fresh.Sum(), want)
	}
	// The old context is untouched by the restart and the new recording.
	if old.Len() != 2 || !bytes.Equal(old.Sum(), oldSum) {
		t.Fatal("old context mutated after Restart")
	}
}

func TestRestartBoundaryRejectsOldMessages(t *testing.T) {
	h := transcript.NewHost()
	old := h.Context()
	m0 := msg(0, 0, "ClientHello")
	mustRecord(t, old, m0)

	h.Restart()

	// A retransmission of an old-epoch message arriving after the restart
	// must not leak into the new transcript.
	if _, err := h.Record(m0); !errors.Is(err, transcript.ErrStaleEpoch) {
		t.Fatalf("replayed old-epoch message: err = %v, want ErrStaleEpoch", err)
	}
	if h.Context().Len() != 0 {
		t.Fatalf("stale message leaked into new context: Len() = %d", h.Context().Len())
	}
	// Recording directly into the sealed old context is refused too.
	if _, err := old.Record(msg(0, 1, "ServerHello")); !errors.Is(err, transcript.ErrSealed) {
		t.Fatalf("record into sealed context: err = %v, want ErrSealed", err)
	}
	// A message claiming a future epoch is rejected as well.
	if _, err := h.Record(msg(2, 0, "early")); !errors.Is(err, transcript.ErrStaleEpoch) {
		t.Fatalf("future-epoch message: err = %v, want ErrStaleEpoch", err)
	}
}

func TestConflictDetectedPerEpoch(t *testing.T) {
	h := transcript.NewHost()
	mustRecord(t, h.Context(), msg(0, 0, "ClientHello"))
	h.Restart()
	// The same sequence number may legitimately carry a different body in
	// the new epoch: it is a fresh transcript, not a conflict.
	if out := mustRecord(t, h.Context(), msg(1, 0, "ClientHello-renewed")); out != transcript.Appended {
		t.Fatalf("Record in new epoch = %v, want Appended", out)
	}
}
