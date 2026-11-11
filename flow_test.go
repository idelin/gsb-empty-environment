package netsec

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(time.Millisecond)
	}
}

// virtualSender is a scripted FrameWriter: it records every frame and can be
// told to fail (optionally after a partial payload write) once a counter
// reaches zero.
type virtualSender struct {
	mu     sync.Mutex
	frames []Frame

	failAfter int   // -1 disables injection; otherwise frames left before failure
	partial   int   // payload bytes to deliver on the failing frame; -1 = all
	failErr   error // error returned on the failing frame
}

func newVirtualSender() *virtualSender {
	return &virtualSender{failAfter: -1, partial: -1, failErr: errors.New("virtual transport boom")}
}

func (v *virtualSender) WriteFrame(f Frame) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Record the frame as the transport would see it.
	cp := Frame{ChannelID: f.ChannelID, Type: f.Type, N: f.N}
	cp.Payload = append([]byte(nil), f.Payload...)

	if v.failAfter >= 0 {
		if v.failAfter == 0 {
			n := len(f.Payload)
			if v.partial >= 0 && v.partial < n {
				n = v.partial
			}
			cp.Payload = cp.Payload[:n]
			v.frames = append(v.frames, cp)
			return n, v.failErr
		}
		v.failAfter--
	}

	v.frames = append(v.frames, cp)
	return len(f.Payload), nil
}

func (v *virtualSender) snapshot() []Frame {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]Frame, len(v.frames))
	copy(out, v.frames)
	return out
}

func (v *virtualSender) dataBytes(id uint32) (total int) {
	for _, f := range v.snapshot() {
		if f.ChannelID == id && f.Type == FrameData {
			total += len(f.Payload)
		}
	}
	return total
}

func (v *virtualSender) count(id uint32, t FrameType) (n int) {
	for _, f := range v.snapshot() {
		if f.ChannelID == id && f.Type == t {
			n++
		}
	}
	return n
}

func (v *virtualSender) adjustSum(id uint32) (total uint64) {
	for _, f := range v.snapshot() {
		if f.ChannelID == id && f.Type == FrameWindowAdjust {
			total += uint64(f.N)
		}
	}
	return total
}

// conservation tracks the adjustments a test legitimately applied so the
// send-side invariant can be checked:
//
//	acked == Σ data frame payloads
//	sendWindow == initialWindow + Σadjusts − acked + cancelled
type conservation struct {
	initial uint64
	adjusts uint64
	cancel  uint64
}

func (c *conservation) checkSend(t *testing.T, ch *Channel, vs *virtualSender, acked int) {
	t.Helper()
	wire := vs.dataBytes(ch.ID())
	if wire != acked {
		t.Fatalf("ch%d: wire bytes %d != acked %d", ch.ID(), wire, acked)
	}
	st := ch.Snapshot()
	want := c.initial + c.adjusts - uint64(acked) + c.cancel
	if uint64(st.SendWindow) != want {
		t.Fatalf("ch%d: sendWindow %d != expected %d (init %d + adjusts %d - acked %d + cancel %d)",
			ch.ID(), st.SendWindow, want, c.initial, c.adjusts, acked, c.cancel)
	}
	if st.QueuedSend != 0 {
		t.Fatalf("ch%d: %d bytes still queued after drain", ch.ID(), st.QueuedSend)
	}
}

// TestWindowExhaustion: a writer blocks once the window is spent, resumes on
// WINDOW_ADJUST, and every frame stays within the max packet size.
func TestWindowExhaustion(t *testing.T) {
	vs := newVirtualSender()
	conn := NewFlowConn(vs)
	ch, err := conn.OpenChannel(1, 100, 1024, 32)
	if err != nil {
		t.Fatal(err)
	}
	cons := &conservation{initial: 100}

	payload := make([]byte, 250)
	for i := range payload {
		payload[i] = byte(i)
	}

	done := make(chan struct{})
	var wrote int
	var werr error
	go func() {
		defer close(done)
		wrote, werr = ch.Write(payload)
	}()

	// Wait until the initial window is exhausted and the writer blocks.
	waitFor(t, func() bool { return ch.Snapshot().SendWindow == 0 })
	if got := vs.dataBytes(1); got != 100 {
		t.Fatalf("sent %d bytes with a 100-byte window", got)
	}

	// Frames must never exceed the max packet size.
	for _, f := range vs.snapshot() {
		if f.Type == FrameData && len(f.Payload) > 32 {
			t.Fatalf("frame of %d bytes exceeds max packet 32", len(f.Payload))
		}
	}

	// Grant more credit in two rounds; the writer must finish.
	if err := ch.HandleWindowAdjust(100); err != nil {
		t.Fatal(err)
	}
	cons.adjusts += 100
	if err := ch.HandleWindowAdjust(50); err != nil {
		t.Fatal(err)
	}
	cons.adjusts += 50

	<-done
	if werr != nil {
		t.Fatalf("write failed: %v", werr)
	}
	if wrote != len(payload) {
		t.Fatalf("wrote %d of %d bytes", wrote, len(payload))
	}
	cons.checkSend(t, ch, vs, wrote)
}

// TestMaxPacketBound: with ample credit, frames are still capped by maxPacket.
func TestMaxPacketBound(t *testing.T) {
	vs := newVirtualSender()
	conn := NewFlowConn(vs)
	ch, err := conn.OpenChannel(7, 1<<20, 1024, 100)
	if err != nil {
		t.Fatal(err)
	}
	cons := &conservation{initial: 1 << 20}

	n, err := ch.Write(make([]byte, 1000))
	if err != nil || n != 1000 {
		t.Fatalf("write = %d, %v", n, err)
	}
	frames := vs.snapshot()
	if len(frames) != 10 {
		t.Fatalf("expected 10 frames of 100 bytes, got %d", len(frames))
	}
	for _, f := range frames {
		if len(f.Payload) != 100 {
			t.Fatalf("frame size %d, want 100", len(f.Payload))
		}
	}
	cons.checkSend(t, ch, vs, n)
}

// TestPartialWriteFailure: the transport dies mid-frame. Only the delivered
// prefix counts as acknowledged; the undelivered remainder keeps its local
// reservation and the channel terminates without leaking remote credit.
func TestPartialWriteFailure(t *testing.T) {
	vs := newVirtualSender()
	conn := NewFlowConn(vs)
	ch, err := conn.OpenChannel(3, 1000, 1024, 100)
	if err != nil {
		t.Fatal(err)
	}

	// Let two full frames through, then fail the third after 40 of 100 bytes.
	vs.failAfter = 2
	vs.partial = 40

	n, err := ch.Write(make([]byte, 500))
	if err == nil {
		t.Fatal("expected transport error")
	}
	if n != 240 {
		t.Fatalf("acked %d bytes, want 240 (2 full frames + 40 partial)", n)
	}
	if got := vs.dataBytes(3); got != 240 {
		t.Fatalf("wire carries %d bytes, want 240", got)
	}

	// The channel is dead: further writes and adjusts report the failure.
	if _, err := ch.Write([]byte("x")); err == nil {
		t.Fatal("write after failure must fail")
	}
	if err := ch.HandleWindowAdjust(10); err == nil {
		t.Fatal("adjust after failure must fail")
	}

	// Conservation over the acknowledged prefix: the failed channel's
	// remaining credit is exactly initial - acked; nothing was invented.
	st := ch.Snapshot()
	if want := uint64(1000 - 240); uint64(st.SendWindow) != want {
		t.Fatalf("sendWindow %d, want %d", st.SendWindow, want)
	}
}

// TestEOFCloseInterleaving: EOF and CLOSE frames interleave with data on both
// directions without losing or duplicating a single acknowledged byte.
func TestEOFCloseInterleaving(t *testing.T) {
	vs := newVirtualSender()
	conn := NewFlowConn(vs)
	ch, err := conn.OpenChannel(9, 4096, 4096, 64)
	if err != nil {
		t.Fatal(err)
	}
	cons := &conservation{initial: 4096}

	// Send data, half-close, then fully close.
	n1, err := ch.Write([]byte("first chunk"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.SendEOF(); err != nil {
		t.Fatal(err)
	}
	if vs.count(9, FrameEOF) != 1 {
		t.Fatal("expected exactly one EOF frame")
	}
	// A second EOF must be rejected.
	if err := ch.SendEOF(); err == nil {
		t.Fatal("duplicate SendEOF must fail")
	}

	// Inbound: data then EOF; the buffer drains before EOF surfaces.
	if err := ch.HandleData([]byte("reply")); err != nil {
		t.Fatal(err)
	}
	if err := ch.HandleEOF(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if n, err := ch.Read(buf); n != 5 || err != nil {
		t.Fatalf("read = %d, %v", n, err)
	}
	if string(buf) != "reply" {
		t.Fatalf("read %q, want %q", buf, "reply")
	}
	if _, err := ch.Read(buf); err != io.EOF {
		t.Fatalf("read after drain = %v, want EOF", err)
	}

	// A second channel rejects data that arrives after EOF.
	late, err := conn.OpenChannel(10, 1024, 1024, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := late.HandleData([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := late.HandleEOF(); err != nil {
		t.Fatal(err)
	}
	if err := late.HandleData([]byte("late")); err != ErrDataAfterEOF {
		t.Fatalf("data after EOF: got %v", err)
	}

	// Close interleaves with the half-closed state.
	if err := ch.Close(); err != nil {
		t.Fatal(err)
	}
	if vs.count(9, FrameClose) != 1 {
		t.Fatal("expected exactly one CLOSE frame")
	}
	if err := ch.Close(); err != nil {
		t.Fatal("Close must be idempotent")
	}
	if conn.Channel(9) != nil {
		t.Fatal("channel still registered after Close")
	}
	if _, err := ch.Write([]byte("x")); err != ErrChannelClosed {
		t.Fatalf("write after close = %v", err)
	}

	cons.checkSend(t, ch, vs, n1)
}

// TestMaliciousChannelIsolation: a channel that sends a window update
// overflowing uint32 is terminated, but its sibling channels on the same
// connection are completely unaffected.
func TestMaliciousChannelIsolation(t *testing.T) {
	vs := newVirtualSender()
	conn := NewFlowConn(vs)

	good, err := conn.OpenChannel(1, 500, 1024, 64)
	if err != nil {
		t.Fatal(err)
	}
	evil, err := conn.OpenChannel(2, 10, 8, 64)
	if err != nil {
		t.Fatal(err)
	}
	goodCons := &conservation{initial: 500}

	// The malicious channel pushes its window past the uint32 ceiling.
	if err := evil.HandleWindowAdjust(^uint32(0)); err != ErrWindowOverflow {
		t.Fatalf("overflow adjust = %v, want ErrWindowOverflow", err)
	}
	// The evil channel is dead; the error is sticky.
	if _, err := evil.Write([]byte("x")); err != ErrWindowOverflow {
		t.Fatalf("evil write after overflow = %v", err)
	}
	if err := evil.HandleWindowAdjust(1); err != ErrWindowOverflow {
		t.Fatalf("evil adjust after overflow = %v", err)
	}

	// Inbound flooding on the receive side also kills only the offender.
	flood, err := conn.OpenChannel(4, 10, 8, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := flood.HandleData(make([]byte, 9)); err != ErrRecvWindowOverflow {
		t.Fatalf("recv overflow = %v, want ErrRecvWindowOverflow", err)
	}
	if err := flood.HandleData(make([]byte, 1)); err != ErrRecvWindowOverflow {
		t.Fatalf("recv overflow must be sticky, got %v", err)
	}

	// The good channel keeps flowing with exact accounting.
	n, err := good.Write(make([]byte, 500))
	if err != nil || n != 500 {
		t.Fatalf("good write = %d, %v", n, err)
	}
	if err := good.HandleWindowAdjust(200); err != nil {
		t.Fatalf("good adjust: %v", err)
	}
	goodCons.adjusts += 200
	n2, err := good.Write(make([]byte, 200))
	if err != nil || n2 != 200 {
		t.Fatalf("good second write = %d, %v", n2, err)
	}
	goodCons.checkSend(t, good, vs, n+n2)

	// No frame from the evil channel ever hit the wire.
	if got := vs.dataBytes(2); got != 0 {
		t.Fatalf("evil channel leaked %d bytes", got)
	}
}

// TestCancelPendingReturnsLocalOnly: cancelling unsent data returns the local
// reservation to the send window but emits no WINDOW_ADJUST, so the remote's
// credit accounting is never inflated.
func TestCancelPendingReturnsLocalOnly(t *testing.T) {
	vs := newVirtualSender()
	conn := NewFlowConn(vs)
	ch, err := conn.OpenChannel(5, 100, 1024, 64)
	if err != nil {
		t.Fatal(err)
	}

	// Exhaust the window, then queue 40 more bytes in a blocked writer.
	if _, err := ch.Write(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	blocked := make(chan struct{})
	go func() {
		defer close(blocked)
		ch.Write(make([]byte, 40))
	}()
	waitFor(t, func() bool { return ch.Snapshot().QueuedSend == 40 })

	// Cancel the 40 queued bytes: the reservation returns locally...
	if n := ch.CancelPending(); n != 40 {
		t.Fatalf("cancelled %d bytes, want 40", n)
	}
	st := ch.Snapshot()
	if st.SendWindow != 40 {
		t.Fatalf("sendWindow %d, want 40 after cancel", st.SendWindow)
	}
	// ...but no WINDOW_ADJUST frame may leave for this channel.
	if got := vs.count(5, FrameWindowAdjust); got != 0 {
		t.Fatalf("cancel emitted %d WINDOW_ADJUST frames", got)
	}
	if got := vs.adjustSum(5); got != 0 {
		t.Fatalf("remote credit inflated by %d", got)
	}

	<-blocked
	// The returned reservation is real: 40 more bytes can now be sent.
	n, err := ch.Write(make([]byte, 40))
	if err != nil || n != 40 {
		t.Fatalf("write after cancel = %d, %v", n, err)
	}
	if got := vs.dataBytes(5); got != 140 {
		t.Fatalf("wire bytes %d, want 140", got)
	}
}

// TestRecvWindowAdjust: consuming inbound data returns credit to the sender
// via WINDOW_ADJUST, and the receive-side invariant holds:
//
//	recvWindow + buffered + (bytesRead − ΣadjustsSent) == initialRecvWindow
func TestRecvWindowAdjust(t *testing.T) {
	vs := newVirtualSender()
	conn := NewFlowConn(vs)
	ch, err := conn.OpenChannel(11, 1024, 100, 64)
	if err != nil {
		t.Fatal(err)
	}

	var readTotal uint64
	checkRecv := func() {
		t.Helper()
		st := ch.Snapshot()
		pending := readTotal - vs.adjustSum(11)
		total := uint64(st.RecvWindow) + uint64(st.BufferedRecv) + pending
		if total != 100 {
			t.Fatalf("recv conservation broken: window %d + buffered %d + pending %d = %d, want 100",
				st.RecvWindow, st.BufferedRecv, pending, total)
		}
	}

	if err := ch.HandleData(make([]byte, 80)); err != nil {
		t.Fatal(err)
	}
	checkRecv()

	// Read 60 bytes: consumed credit crosses the half-window threshold and
	// is returned to the sender.
	buf := make([]byte, 60)
	if n, err := ch.Read(buf); n != 60 || err != nil {
		t.Fatalf("read = %d, %v", n, err)
	}
	readTotal += 60
	if got := vs.adjustSum(11); got != 60 {
		t.Fatalf("adjust sum %d, want 60", got)
	}
	checkRecv()

	// Refill within the returned credit, then drain everything.
	if err := ch.HandleData(make([]byte, 60)); err != nil {
		t.Fatal(err)
	}
	if n, err := ch.Read(make([]byte, 80)); n != 80 || err != nil {
		t.Fatalf("read = %d, %v", n, err)
	}
	readTotal += 80
	if got := vs.adjustSum(11); got != 140 {
		t.Fatalf("adjust sum %d, want 140", got)
	}
	checkRecv()

	st := ch.Snapshot()
	if st.RecvWindow != 100 || st.BufferedRecv != 0 {
		t.Fatalf("final recv state: window %d buffered %d, want 100/0",
			st.RecvWindow, st.BufferedRecv)
	}
}

// TestConcurrentConservation: many channels pump concurrently over one
// connection; every acknowledged byte is conserved per channel.
func TestConcurrentConservation(t *testing.T) {
	vs := newVirtualSender()
	conn := NewFlowConn(vs)

	const channels = 8
	const perChannel = 4096
	const window = 512

	type result struct {
		ch   *Channel
		cons *conservation
		acks int
	}
	results := make([]result, channels)

	var wg sync.WaitGroup
	for i := 0; i < channels; i++ {
		id := uint32(100 + i)
		ch, err := conn.OpenChannel(id, window, 4096, 96)
		if err != nil {
			t.Fatal(err)
		}
		cons := &conservation{initial: window}
		results[i] = result{ch: ch, cons: cons}

		wg.Add(2)
		// Writer: streams perChannel bytes, failing only on fatal error.
		go func(ch *Channel, r *result) {
			defer wg.Done()
			remaining := perChannel
			for remaining > 0 {
				chunk := min(remaining, 700)
				n, err := ch.Write(make([]byte, chunk))
				r.acks += n
				if err != nil {
					t.Errorf("ch%d write: %v", ch.ID(), err)
					return
				}
				remaining -= n
			}
		}(ch, &results[i])

		// Adjuster: dribbles credit back as the wire consumes it.
		go func(ch *Channel, r *result) {
			defer wg.Done()
			granted := 0
			for granted < perChannel {
				sent := vs.dataBytes(ch.ID())
				if sent > granted {
					inc := uint32(sent - granted)
					if err := ch.HandleWindowAdjust(inc); err != nil {
						t.Errorf("ch%d adjust: %v", ch.ID(), err)
						return
					}
					r.cons.adjusts += uint64(inc)
					granted = sent
				} else {
					time.Sleep(time.Millisecond)
				}
			}
		}(ch, &results[i])
	}
	wg.Wait()

	for i := range results {
		r := &results[i]
		if r.acks != perChannel {
			t.Fatalf("ch%d acked %d, want %d", r.ch.ID(), r.acks, perChannel)
		}
		r.cons.checkSend(t, r.ch, vs, r.acks)
	}
}
