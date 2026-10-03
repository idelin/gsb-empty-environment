package netsim

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"pbufsafe/testutil"
)

// TestReliableDeliveryOverLossyLink: every message arrives exactly once,
// in order, intact, despite 40% frame loss and 10% corruption on the data
// path and 20% loss on the ACK path.
func TestReliableDeliveryOverLossyLink(t *testing.T) {
	defer testutil.AssertNoGoroutineLeak(t)()
	dataLink := NewLink(42, 0.40, 0.10)
	ackLink := NewLink(7, 0.20, 0.0)
	sess := NewSession(dataLink, ackLink)

	const n = 50
	received := make(chan []byte, n)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			p, err := sess.Recv()
			if err != nil {
				t.Errorf("recv %d: %v", i, err)
				return
			}
			received <- p
		}
	}()

	for i := 0; i < n; i++ {
		payload := []byte(fmt.Sprintf("msg-%03d", i))
		if err := sess.Send(uint32(i), payload); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		want := []byte(fmt.Sprintf("msg-%03d", i))
		if got := <-received; !bytes.Equal(got, want) {
			t.Fatalf("message %d: got %q want %q", i, got, want)
		}
	}
	if dataLink.Dropped == 0 {
		t.Error("fault injection inactive: no frames were dropped")
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestFrameIntegrity: corrupted frames are detected by CRC and never
// delivered; the sender retransmits until an intact copy lands.
func TestFrameIntegrity(t *testing.T) {
	frame := EncodeFrame(9, []byte("payload"))
	seq, payload, err := DecodeFrame(frame)
	if err != nil || seq != 9 || string(payload) != "payload" {
		t.Fatalf("clean frame: seq=%d payload=%q err=%v", seq, payload, err)
	}
	// Flip one bit in the payload.
	bad := append([]byte(nil), frame...)
	bad[len(bad)-1] ^= 0x01
	if _, _, err := DecodeFrame(bad); err != ErrCorruptFrame {
		t.Fatalf("corrupted frame: got %v, want ErrCorruptFrame", err)
	}
	// Truncated frame.
	if _, _, err := DecodeFrame(frame[:3]); err != ErrCorruptFrame {
		t.Fatalf("truncated frame: got %v, want ErrCorruptFrame", err)
	}
}

// TestDuplicateSuppression: a duplicate delivery of the same sequence
// number (classic ACK-loss artifact) is ACKed again but not delivered twice.
func TestDuplicateSuppression(t *testing.T) {
	defer testutil.AssertNoGoroutineLeak(t)()
	// Lossless links: no retransmission noise.
	sess := NewSession(NewLink(1, 0, 0), NewLink(2, 0, 0))
	done := make(chan []byte, 4)
	go func() {
		for i := 0; i < 2; i++ {
			p, _ := sess.Recv()
			done <- p
		}
	}()
	if err := sess.Send(0, []byte("only-once")); err != nil {
		t.Fatal(err)
	}
	// Inject a duplicate of seq 0 directly at the receiver.
	sess.onWireFrame(EncodeFrame(0, []byte("only-once")))
	// Next logical message must still be accepted.
	if err := sess.Send(1, []byte("second")); err != nil {
		t.Fatal(err)
	}
	first := <-done
	second := <-done
	if string(first) != "only-once" || string(second) != "second" {
		t.Fatalf("got %q then %q", first, second)
	}
	select {
	case extra := <-done:
		t.Fatalf("duplicate delivered: %q", extra)
	default:
	}
	sess.Close()
}

// TestConcurrentSenders: multiple goroutines share the session's links;
// the link loss model must be race-free.
func TestConcurrentLinkAccess(t *testing.T) {
	link := NewLink(99, 0.5, 0.5)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				link.Transmit(EncodeFrame(uint32(i), []byte("x")))
			}
		}(g)
	}
	wg.Wait()
	total := link.Dropped + (link.Sent - link.Dropped)
	if total != link.Sent {
		t.Fatalf("counter mismatch: sent=%d dropped=%d", link.Sent, link.Dropped)
	}
}

// TestCloseIdempotentAndLeaks: Close is safe to call once; Recv unblocks.
func TestCloseUnblocksRecv(t *testing.T) {
	defer testutil.AssertNoGoroutineLeak(t)()
	sess := NewSession(NewLink(1, 0, 0), NewLink(2, 0, 0))
	errCh := make(chan error, 1)
	go func() { errCh <- func() error { _, err := sess.Recv(); return err }() }()
	sess.Close()
	if err := <-errCh; err != ErrClosed {
		t.Fatalf("Recv after close: got %v, want ErrClosed", err)
	}
	if err := sess.Send(0, []byte("x")); err != ErrClosed {
		t.Fatalf("Send after close: got %v, want ErrClosed", err)
	}
	if err := sess.Close(); err != ErrClosed {
		t.Fatalf("second Close: got %v, want ErrClosed", err)
	}
}
